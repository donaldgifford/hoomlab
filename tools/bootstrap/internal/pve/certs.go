package pve

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
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
				Apply: c.applyOrder(n.Name),
			},
		)
	}
	return list
}

func (c *Certifier) accountCheck(ctx context.Context) (bool, error) {
	accounts, err := c.Nodes.ListACMEAccounts(ctx)
	if err != nil {
		return false, fmt.Errorf("list acme accounts: %w", err)
	}
	return slices.Contains(accounts, acmeAccountName), nil
}

// applyAccount registers the ACME account, accepting the CA's current
// terms of service read from its directory metadata rather than a
// hardcoded URL.
func (c *Certifier) applyAccount(ctx context.Context) error {
	var metaOpts []nodes.ACMEMetaOption
	if c.Cluster.ACME.Directory != "" {
		metaOpts = append(metaOpts, nodes.WithACMEDirectory(c.Cluster.ACME.Directory))
	}
	meta, err := c.Nodes.GetACMEMeta(ctx, metaOpts...)
	if err != nil {
		return fmt.Errorf("read CA metadata: %w", err)
	}
	ref, err := c.Nodes.RegisterACMEAccount(ctx, &nodes.ACMEAccountSpec{
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
	c.log().Info("acme account registered", "account", acmeAccountName)
	return nil
}

// pluginCheck is done when the plugin exists, is a DNS plugin for the
// right provider, is enabled, and stores exactly the credentials the
// config resolves to — a rotated token flips this back to pending.
func (c *Certifier) pluginCheck(id string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		plugin, err := c.Nodes.GetACMEPlugin(ctx, id)
		if errors.Is(err, pverr.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("get acme plugin %s: %w", id, err)
		}
		data := c.pluginData()
		return plugin.API == data.API() &&
			plugin.Type != nodes.ACMEChallengeTypeStandalone &&
			!plugin.Disable.Bool() &&
			plugin.Data == encodePluginData(data), nil
	}
}

// applyPlugin creates the plugin, or updates its credentials when it
// already exists (the drifted-token path pluginCheck reopens).
func (c *Certifier) applyPlugin(id string) func(context.Context) error {
	return func(ctx context.Context) error {
		data := c.pluginData()
		existing, err := c.Nodes.GetACMEPlugin(ctx, id)
		switch {
		case errors.Is(err, pverr.ErrNotFound):
			if err := c.Nodes.CreateACMEPlugin(ctx, &nodes.ACMEPluginSpec{
				ID:   id,
				Type: nodes.ACMEChallengeTypeDNS,
				Data: data,
			}); err != nil {
				return fmt.Errorf("create acme plugin %s: %w", id, err)
			}
			c.log().Info("acme plugin registered", "plugin", id)
		case err != nil:
			return fmt.Errorf("get acme plugin %s: %w", id, err)
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
// with more than RenewBefore validity left.
func (c *Certifier) certCheck(node, fqdn string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		certs, err := c.Nodes.GetNodeCertificates(ctx, node)
		if err != nil {
			return false, fmt.Errorf("get certificates of %s: %w", node, err)
		}
		deadline := c.now().Add(c.renewBefore()).Unix()
		return slices.ContainsFunc(certs, func(cert nodes.Certificate) bool {
			return slices.Contains(cert.SAN, fqdn) && cert.NotAfter > deadline
		}), nil
	}
}

// applyOrder orders (or renews) the node certificate and awaits the
// worker. A DNS-01 order waits on the CA resolving the challenge
// record — a live order runs on the order of minutes, so the wait is
// bounded only by the command's context.
func (c *Certifier) applyOrder(node string) func(context.Context) error {
	return func(ctx context.Context) error {
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

// encodePluginData mirrors the SDK's wire encoding of provider
// credentials (proxmox/nodes.encodePluginData: sorted KEY=value lines,
// empty values omitted, newline-joined, std base64) so pluginCheck can
// compare the stored payload against the config without decoding
// secrets. The round-trip test against mockpve pins the two in sync.
func encodePluginData(d nodes.ACMEPluginData) string {
	values := d.Data()
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+values[k])
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
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
