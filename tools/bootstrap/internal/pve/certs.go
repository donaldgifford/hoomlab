package pve

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/nodes"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/pverr"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/tasks"
)

const (
	// acmeAccountName is the local handle the account registers under
	// and every node config references.
	acmeAccountName = "default"
	// defaultRenewBefore is the remaining-validity floor below which
	// the certificate step goes pending again — renewal is the same
	// command re-run.
	defaultRenewBefore = 30 * 24 * time.Hour
	// leProductionDirectory is Let's Encrypt production — PVE's
	// default CA when a registration names no directory, and this
	// config's default when acme.directory is unset.
	leProductionDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	// frontendCertFilename is the pveproxy certificate file PVE
	// serves the UI/API with — the one the ACME order installs and
	// the one whose presence makes a plain reorder refuse.
	frontendCertFilename = "pveproxy-ssl.pem"
)

// Certifier builds the Stage 2 step list: ACME account, the DNS-01
// challenge plugin, and per node the domain wiring plus the
// certificate order. Unlike formation this stage runs against a
// formed, stable cluster, so it holds one service pair instead of
// dialing per call, and read errors are real errors.
type Certifier struct {
	Cluster *config.Cluster
	Nodes   *nodes.Service
	Tasks   *tasks.Service

	// DialRoot opens a session authenticated as root@pam, used for
	// exactly one call: registering the ACME account.
	// POST /cluster/acme/account carries no permissions block, and
	// PVE's default for such endpoints is root@pam only — the token
	// gets HTTP 403 "user != root@pam" regardless of privileges
	// (INV-0001, 2026-08-25). Every other write in this stage is
	// Sys.Modify-gated and stays on the token session. Lazy so a
	// converged re-run (renewals included) never authenticates as
	// root at all.
	DialRoot func(ctx context.Context) (*nodes.Service, error)

	// RenewBefore is the remaining-validity floor; zero means 30 days.
	RenewBefore time.Duration
	// Now is the clock, for expiry checks. Nil means time.Now.
	Now func() time.Time
	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

// Steps returns the certificate steps in apply order: account, plugin,
// then per node config + order (orders are serial — a DNS-01 order
// waits on CA-side propagation and there is no need to hammer the CA).
func (c *Certifier) Steps() []steps.Step {
	pluginID := c.Cluster.ACME.DNS
	list := make([]steps.Step, 0, 2+2*len(c.Cluster.PVE.Nodes))
	list = append(list,
		steps.Step{Name: "acme-account", Check: c.accountCheck, Apply: c.applyAccount},
		steps.Step{Name: "acme-plugin-" + pluginID, Check: c.pluginCheck(pluginID), Apply: c.applyPlugin(pluginID)},
	)
	for _, n := range c.Cluster.PVE.Nodes {
		fqdn := n.Name + "." + c.Cluster.ACME.Domain
		list = append(list,
			steps.Step{
				Name:  "acme-config-" + n.Name,
				Check: c.domainCheck(n.Name, fqdn, pluginID),
				Apply: c.applyDomain(n.Name, fqdn, pluginID),
			},
			steps.Step{
				Name:  "acme-cert-" + n.Name,
				Check: c.certCheck(n.Name, fqdn),
				Apply: c.applyOrder(n.Name, fqdn),
			},
		)
	}
	return list
}

// desiredDirectory is the CA directory the config asks for.
func (c *Certifier) desiredDirectory() string {
	if c.Cluster.ACME.Directory != "" {
		return c.Cluster.ACME.Directory
	}
	return leProductionDirectory
}

// accountCheck is done when the account exists AND is registered
// against the configured CA directory — flipping acme.directory
// (staging → production, OQ-1) reopens this step. INV-0001 deviation
// 5 (2026-08-25): the previous name-only check read the staging
// account as done after the flip and the whole stage no-opped. A
// stored directory the server does not report (empty) counts as
// matching.
func (c *Certifier) accountCheck(ctx context.Context) (bool, error) {
	dir, exists, err := c.accountDirectory(ctx)
	if err != nil || !exists {
		return false, err
	}
	if dir != "" && dir != c.desiredDirectory() {
		c.log().Info("acme account registered against a different CA",
			"account", acmeAccountName, "have", dir, "want", c.desiredDirectory())
		return false, nil
	}
	return true, nil
}

// accountDirectory reports whether the account exists and which CA
// directory it is registered against. Reads are token-first; the
// per-account GET falls back through the root session on a 403 —
// PVE's account endpoints default to root@pam-only and the drill has
// only proven the index readable by a token.
func (c *Certifier) accountDirectory(ctx context.Context) (dir string, exists bool, err error) {
	accounts, err := c.Nodes.ListACMEAccounts(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list acme accounts: %w", err)
	}
	if !slices.Contains(accounts, acmeAccountName) {
		return "", false, nil
	}
	acct, err := c.Nodes.GetACMEAccount(ctx, acmeAccountName)
	if errors.Is(err, pverr.ErrForbidden) {
		c.log().Info("account read denied to the token, retrying as root@pam",
			"account", acmeAccountName)
		rootNodes, rootErr := c.dialRootNodes(ctx)
		if rootErr != nil {
			return "", true, rootErr
		}
		acct, err = rootNodes.GetACMEAccount(ctx, acmeAccountName)
	}
	if err != nil {
		return "", true, fmt.Errorf("get acme account %s: %w", acmeAccountName, err)
	}
	return acct.Directory, true, nil
}

// dialRootNodes opens the root@pam session, guarding the seam.
func (c *Certifier) dialRootNodes(ctx context.Context) (*nodes.Service, error) {
	if c.DialRoot == nil {
		return nil, errors.New("certifier: DialRoot not configured")
	}
	svc, err := c.DialRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial as root@pam: %w", err)
	}
	return svc, nil
}

// applyAccount converges the ACME account: register when absent,
// deactivate-and-re-register when it exists against a different CA
// (the staging → production flip). Both writes go through the root
// session — every account write is root@pam-reserved. The CA's terms
// of service are read from its directory metadata rather than a
// hardcoded URL; that read stays on the token.
func (c *Certifier) applyAccount(ctx context.Context) error {
	rootNodes, err := c.dialRootNodes(ctx)
	if err != nil {
		return err
	}
	_, exists, err := c.accountDirectory(ctx)
	if err != nil {
		return err
	}
	if exists {
		ref, err := rootNodes.DeactivateACMEAccount(ctx, acmeAccountName)
		if err != nil {
			return fmt.Errorf("deactivate acme account %s: %w", acmeAccountName, err)
		}
		if err := c.waitTask(ctx, ref, "acme account deactivation"); err != nil {
			return err
		}
		c.log().Info("acme account deactivated for CA change", "account", acmeAccountName)
	}

	var metaOpts []nodes.ACMEMetaOption
	if c.Cluster.ACME.Directory != "" {
		metaOpts = append(metaOpts, nodes.WithACMEDirectory(c.Cluster.ACME.Directory))
	}
	meta, err := c.Nodes.GetACMEMeta(ctx, metaOpts...)
	if err != nil {
		return fmt.Errorf("read CA metadata: %w", err)
	}
	ref, err := rootNodes.RegisterACMEAccount(ctx, &nodes.ACMEAccountSpec{
		Name:      acmeAccountName,
		Contact:   []string{c.Cluster.ACME.Email},
		Directory: c.Cluster.ACME.Directory,
		TOSURL:    meta.TermsOfService,
	})
	if err != nil {
		return fmt.Errorf("register acme account: %w", err)
	}
	if err := c.waitTask(ctx, ref, "acme account registration"); err != nil {
		return err
	}
	c.log().Info("acme account registered", "account", acmeAccountName,
		"directory", c.desiredDirectory())
	return nil
}

// findACMEPlugin returns the plugin with the given ID from the
// cluster's plugin index, with found reporting whether it exists.
// Existence goes through the index deliberately: real PVE answers a
// by-ID GET on a missing plugin with HTTP 500 "ACME plugin '<id>' not
// defined" rather than a 404 (INV-0001, 2026-08-25), so a by-ID read
// cannot distinguish "not created yet" from a genuine server error.
func (c *Certifier) findACMEPlugin(ctx context.Context, id string) (plugin *nodes.ACMEPlugin, found bool, err error) {
	plugins, err := c.Nodes.ListACMEPlugins(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("list acme plugins: %w", err)
	}
	for i := range plugins {
		if plugins[i].Plugin == id {
			return &plugins[i], true, nil
		}
	}
	return nil, false, nil
}

// pluginCheck is done when the plugin exists, is a DNS plugin for the
// right provider, is enabled, and stores exactly the credentials the
// config resolves to — a rotated token flips this back to pending.
func (c *Certifier) pluginCheck(id string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		plugin, found, err := c.findACMEPlugin(ctx, id)
		if err != nil || !found {
			return false, err
		}
		data := c.pluginData()
		stored := decodePluginData(plugin.Data)
		want := desiredPluginValues(data)
		if !maps.Equal(stored, want) {
			c.log().Info("plugin credentials differ from config",
				"plugin", id,
				"stored_keys", slices.Sorted(maps.Keys(stored)),
				"config_keys", slices.Sorted(maps.Keys(want)))
			return false, nil
		}
		return plugin.API == data.API() &&
			plugin.Type != nodes.ACMEChallengeTypeStandalone &&
			!plugin.Disable.Bool(), nil
	}
}

// applyPlugin creates the plugin, or updates its credentials when it
// already exists (the drifted-token path pluginCheck reopens).
func (c *Certifier) applyPlugin(id string) func(context.Context) error {
	return func(ctx context.Context) error {
		data := c.pluginData()
		existing, found, err := c.findACMEPlugin(ctx, id)
		switch {
		case err != nil:
			return err
		case !found:
			if err := c.Nodes.CreateACMEPlugin(ctx, &nodes.ACMEPluginSpec{
				ID:   id,
				Type: nodes.ACMEChallengeTypeDNS,
				Data: data,
			}); err != nil {
				return fmt.Errorf("create acme plugin %s: %w", id, err)
			}
			c.log().Info("acme plugin registered", "plugin", id)
		default:
			if err := c.Nodes.UpdateACMEPlugin(ctx, id, &nodes.ACMEPluginUpdate{
				Data:   data,
				Digest: existing.Digest,
			}); err != nil {
				return fmt.Errorf("update acme plugin %s: %w", id, err)
			}
			c.log().Info("acme plugin credentials rotated", "plugin", id)
		}
		return nil
	}
}

func (c *Certifier) domainCheck(node, fqdn, pluginID string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		cfg, err := c.Nodes.GetNodeConfig(ctx, node)
		if err != nil {
			return false, fmt.Errorf("get node config %s: %w", node, err)
		}
		if cfg.ACME == nil || cfg.ACME.Account != acmeAccountName {
			return false, nil
		}
		return slices.ContainsFunc(cfg.ACMEDomains, func(d nodes.ACMEDomain) bool {
			return d.Domain == fqdn && d.Plugin == pluginID
		}), nil
	}
}

// applyDomain wires one node: the ACME account plus an acmedomain slot
// binding the node FQDN to the challenge plugin. An existing slot for
// the FQDN is rewritten in place; otherwise the lowest free slot is
// used. Other slots are untouched.
func (c *Certifier) applyDomain(node, fqdn, pluginID string) func(context.Context) error {
	return func(ctx context.Context) error {
		cfg, err := c.Nodes.GetNodeConfig(ctx, node)
		if err != nil {
			return fmt.Errorf("get node config %s: %w", node, err)
		}
		idx := slotFor(cfg.ACMEDomains, fqdn)
		update := &nodes.NodeConfigUpdate{
			ACME:        &nodes.NodeACME{Account: acmeAccountName},
			ACMEDomains: []nodes.ACMEDomain{{Index: idx, Domain: fqdn, Plugin: pluginID}},
			Digest:      cfg.Digest,
		}
		if err := c.Nodes.SetNodeConfig(ctx, node, update); err != nil {
			return fmt.Errorf("set node config %s: %w", node, err)
		}
		c.log().Info("acme domain configured", "node", node, "domain", fqdn, "slot", idx)
		return nil
	}
}

// certCheck is done when a certificate covering the FQDN is installed
// with more than RenewBefore validity left, issued by an acceptable
// CA (see issuerMatchesCA).
func (c *Certifier) certCheck(node, fqdn string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		certs, err := c.Nodes.GetNodeCertificates(ctx, node)
		if err != nil {
			return false, fmt.Errorf("get certificates of %s: %w", node, err)
		}
		deadline := c.now().Add(c.renewBefore()).Unix()
		return slices.ContainsFunc(certs, func(cert nodes.Certificate) bool {
			return slices.Contains(cert.SAN, fqdn) &&
				cert.NotAfter > deadline &&
				c.issuerMatchesCA(cert.Issuer)
		}), nil
	}
}

// issuerMatchesCA rejects a staging-issued certificate when the
// desired CA is Let's Encrypt production — the staging → production
// flip must reopen the order (INV-0001 deviation 5, 2026-08-25:
// without this the installed staging certificate satisfied the
// SAN + validity check and the flip no-opped). Staging and custom
// CAs express no issuer opinion; the heuristic exists for the one
// documented transition (OQ-1).
func (c *Certifier) issuerMatchesCA(issuer string) bool {
	if c.desiredDirectory() != leProductionDirectory {
		return true
	}
	return !strings.Contains(issuer, "(STAGING)")
}

// certAction is how applyOrder converges a pending certificate step.
type certAction int

const (
	certActionOrder   certAction = iota // no frontend certificate — plain order
	certActionRenew                     // right certificate, merely expiring — renew in place
	certActionReplace                   // wrong certificate (CA flip, SAN change) — delete, then order
)

// certActionFor decides the converge path for a node whose
// certificate step is pending. PVE's order endpoint refuses while a
// frontend certificate file exists and the SDK exposes no force
// (INV-0001 deviation 7, 2026-08-25), so the existing file dictates
// the path: absent means a plain order; present and otherwise right
// means the certificate is merely inside the renewal window, which
// PVE's renew verb handles without force; present and wrong means
// delete the frontend certificate and order fresh — a brief
// self-signed window, entered only when the served certificate is
// already wrong.
func (c *Certifier) certActionFor(certs []nodes.Certificate, fqdn string) certAction {
	for i := range certs {
		cert := &certs[i]
		if cert.Filename != frontendCertFilename {
			continue
		}
		if slices.Contains(cert.SAN, fqdn) && c.issuerMatchesCA(cert.Issuer) {
			return certActionRenew
		}
		return certActionReplace
	}
	return certActionOrder
}

// applyOrder converges the node certificate per certActionFor and
// awaits the worker. A DNS-01 order waits on the CA resolving the
// challenge record — a live order runs on the order of minutes, so
// the wait is bounded only by the command's context.
func (c *Certifier) applyOrder(node, fqdn string) func(context.Context) error {
	return func(ctx context.Context) error {
		certs, err := c.Nodes.GetNodeCertificates(ctx, node)
		if err != nil {
			return fmt.Errorf("get certificates of %s: %w", node, err)
		}
		switch c.certActionFor(certs, fqdn) {
		case certActionRenew:
			ref, err := c.Nodes.RenewNodeCertificate(ctx, node)
			if err != nil {
				return fmt.Errorf("renew certificate for %s: %w", node, err)
			}
			if err := c.waitTask(ctx, ref, "certificate renewal for "+node); err != nil {
				return err
			}
			c.log().Info("certificate renewed", "node", node)
			return nil
		case certActionReplace:
			if err := c.Nodes.DeleteCustomCertificate(ctx, node); err != nil {
				return fmt.Errorf("delete stale certificate of %s: %w", node, err)
			}
			c.log().Info("stale certificate removed before reorder", "node", node)
		case certActionOrder:
		}
		ref, err := c.Nodes.OrderNodeCertificate(ctx, node)
		if err != nil {
			return fmt.Errorf("order certificate for %s: %w", node, err)
		}
		if err := c.waitTask(ctx, ref, "certificate order for "+node); err != nil {
			return err
		}
		c.log().Info("certificate ordered", "node", node)
		return nil
	}
}

func (c *Certifier) waitTask(ctx context.Context, ref tasks.Ref, what string) error {
	status, err := c.Tasks.Wait(ctx, ref)
	if err != nil {
		return fmt.Errorf("wait for %s: %w", what, err)
	}
	if !status.OK() {
		return fmt.Errorf("%s failed: %s", what, status.ExitStatus)
	}
	return nil
}

// pluginData resolves the provider credentials from config. Only
// Cloudflare exists today (config validation enforces it).
func (c *Certifier) pluginData() nodes.ACMEPluginData {
	return nodes.ACMECloudflare{Token: c.Cluster.ACME.Token}
}

// slotFor picks the acmedomain slot to write: the slot already
// carrying the FQDN when present, otherwise the lowest unused index.
func slotFor(existing []nodes.ACMEDomain, fqdn string) int {
	used := make(map[int]bool, len(existing))
	for _, d := range existing {
		if d.Domain == fqdn {
			return d.Index
		}
		used[d.Index] = true
	}
	idx := 0
	for used[idx] {
		idx++
	}
	return idx
}

// desiredPluginValues is the provider credential map the config
// resolves to, with empty values dropped — the SDK omits them on the
// wire, so they can never appear in the stored payload.
func desiredPluginValues(d nodes.ACMEPluginData) map[string]string {
	values := d.Data()
	out := make(map[string]string, len(values))
	for k, v := range values {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// decodePluginData parses the provider payload a server returns for
// an ACME plugin into a KEY=value map. Servers disagree on the read
// shape (INV-0001 deviation 6, 2026-08-25): the SDK submits the
// payload base64-encoded and mockpve returns that base64 verbatim,
// but real PVE returns the DECODED lines — observed live as "illegal
// base64 data at input byte 2", the '_' of CF_Token. So: decode when
// the payload is valid base64 (either padding), otherwise parse it as
// the plaintext it already is. The cases stay unambiguous in practice
// because KEY=value lines contain mid-string '=' and '_', which no
// base64 alphabet allows.
func decodePluginData(payload string) map[string]string {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil {
		raw = []byte(payload) // real PVE: already decoded
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		out[k] = v
	}
	return out
}

func (c *Certifier) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

func (c *Certifier) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Certifier) renewBefore() time.Duration {
	if c.RenewBefore > 0 {
		return c.RenewBefore
	}
	return defaultRenewBefore
}
