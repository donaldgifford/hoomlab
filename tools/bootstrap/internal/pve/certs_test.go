package pve_test

import (
	"context"
	"encoding/base64"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/mockpve"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/nodes"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/tasks"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/version"
)

// cfSecret is the test Cloudflare token; TestCertsRedaction asserts it
// never reaches any output path.
const cfSecret = "sekrit-cf-token-bytes"

func certsCluster() *config.Cluster {
	c := testCluster()
	c.ACME = config.ACME{
		Email:  "ops@example.test",
		Domain: "pve.example.test",
		DNS:    "cloudflare",
		Token:  cfSecret,
	}
	return c
}

// newCertifier wires a Certifier against a mock already carrying the
// three cluster nodes, with a fixed 2026 clock (mock certificates
// expire 2100-01-01, so they read as fresh).
func newCertifier(t *testing.T, cfg *config.Cluster) (*pve.Certifier, *strings.Builder) {
	t.Helper()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	var logBuf strings.Builder
	svc := nodes.NewService(client, version.Capabilities{})
	return &pve.Certifier{
		Cluster:  cfg,
		Nodes:    svc,
		Tasks:    tasks.NewService(client),
		DialRoot: sameServiceRoot(svc),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
		Log:      slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, &logBuf
}

// sameServiceRoot is the single-mock tests' DialRoot: the mock does
// not distinguish principals, so the root session is the same service.
// TestCertsAccountRegistersAsRoot is where the two sessions are told
// apart.
func sameServiceRoot(svc *nodes.Service) func(context.Context) (*nodes.Service, error) {
	return func(context.Context) (*nodes.Service, error) { return svc, nil }
}

func runCerts(t *testing.T, c *pve.Certifier) steps.Result {
	t.Helper()
	r := steps.Runner{Log: c.Log}
	res, err := r.Run(context.Background(), c.Steps())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	return res
}

func TestCertsFreshRun(t *testing.T) {
	certifier, logBuf := newCertifier(t, certsCluster())

	res := runCerts(t, certifier)
	// account + plugin + 3×(config + order).
	if res.Applied != 8 {
		t.Errorf("Run() applied = %d, want 8", res.Applied)
	}

	ctx := context.Background()
	accounts, err := certifier.Nodes.ListACMEAccounts(ctx)
	if err != nil {
		t.Fatalf("ListACMEAccounts: %v", err)
	}
	found := false
	for _, a := range accounts {
		if a == "default" {
			found = true
		}
	}
	if !found {
		t.Errorf("accounts = %v, want to contain %q", accounts, "default")
	}

	plugin, err := certifier.Nodes.GetACMEPlugin(ctx, "cloudflare")
	if err != nil {
		t.Fatalf("GetACMEPlugin: %v", err)
	}
	if plugin.API != "cf" {
		t.Errorf("plugin.API = %q, want %q", plugin.API, "cf")
	}
	if plugin.Data == "" {
		t.Error("plugin.Data empty, want the encoded credential payload")
	}

	for _, n := range certifier.Cluster.PVE.Nodes {
		fqdn := n.Name + ".pve.example.test"
		nodeCfg, err := certifier.Nodes.GetNodeConfig(ctx, n.Name)
		if err != nil {
			t.Fatalf("GetNodeConfig(%s): %v", n.Name, err)
		}
		if nodeCfg.ACME == nil || nodeCfg.ACME.Account != "default" {
			t.Errorf("node %s acme account = %+v, want default", n.Name, nodeCfg.ACME)
		}
		domainOK := false
		for _, d := range nodeCfg.ACMEDomains {
			if d.Domain == fqdn && d.Plugin == "cloudflare" {
				domainOK = true
			}
		}
		if !domainOK {
			t.Errorf("node %s acmedomains = %+v, want %s via cloudflare", n.Name, nodeCfg.ACMEDomains, fqdn)
		}

		certs, err := certifier.Nodes.GetNodeCertificates(ctx, n.Name)
		if err != nil {
			t.Fatalf("GetNodeCertificates(%s): %v", n.Name, err)
		}
		certOK := false
		for _, c := range certs {
			for _, san := range c.SAN {
				if san == fqdn {
					certOK = true
				}
			}
		}
		if !certOK {
			t.Errorf("node %s has no certificate covering %s", n.Name, fqdn)
		}
	}

	if strings.Contains(logBuf.String(), cfSecret) {
		t.Errorf("log output leaks the provider token:\n%s", logBuf.String())
	}
}

// TestCertsAccountRegistersAsRoot is the INV-0001 regression
// (2026-08-25, deviation 3): POST /cluster/acme/account carries no
// permissions block, and PVE defaults such endpoints to root@pam only
// — r740a rejected the token-dialed registration with HTTP 403
// "user != root@pam". The account write must go through DialRoot while
// everything else stays on the token session. Two separate mocks play
// the two sessions; the account must land on the root side only —
// against the pre-fix code (registration via the token session) the
// account lands on the token mock and this fails.
func TestCertsAccountRegistersAsRoot(t *testing.T) {
	cfg := certsCluster()

	tokenMock := mockpve.New()
	tokenMock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		tokenMock.AddNode(n.Name)
	}
	tokenClient, cleanup := tokenMock.NewClient()
	t.Cleanup(cleanup)
	tokenNodes := nodes.NewService(tokenClient, version.Capabilities{})

	rootMock := mockpve.New()
	rootMock.SeedVersion("9.2.1", "9.2", "test")
	rootClient, rootCleanup := rootMock.NewClient()
	t.Cleanup(rootCleanup)
	rootNodes := nodes.NewService(rootClient, version.Capabilities{})

	rootDials := 0
	certifier := &pve.Certifier{
		Cluster: cfg,
		Nodes:   tokenNodes,
		// The registration task runs on the root side, so the waiter
		// polls there too.
		Tasks: tasks.NewService(rootClient),
		DialRoot: func(context.Context) (*nodes.Service, error) {
			rootDials++
			return rootNodes, nil
		},
		Now: func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
	}

	var r steps.Runner
	res, err := r.Run(context.Background(), certifier.Steps()[:1])
	if err != nil {
		t.Fatalf("account step Run() error: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("applied = %d, want the account registration", res.Applied)
	}
	if rootDials != 1 {
		t.Errorf("DialRoot called %d times, want exactly 1", rootDials)
	}

	onRoot, err := rootNodes.ListACMEAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListACMEAccounts(root side): %v", err)
	}
	if !slices.Contains(onRoot, "default") {
		t.Errorf("root-side accounts = %v, want the registration to land here", onRoot)
	}
	onToken, err := tokenNodes.ListACMEAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListACMEAccounts(token side): %v", err)
	}
	if slices.Contains(onToken, "default") {
		t.Errorf("token-side accounts = %v — the registration went through the token session, which real PVE rejects with 403", onToken)
	}
}

func TestCertsReRunIsNoOp(t *testing.T) {
	certifier, _ := newCertifier(t, certsCluster())
	runCerts(t, certifier)

	res := runCerts(t, certifier)
	if res.Applied != 0 {
		t.Errorf("second Run() applied = %d, want 0", res.Applied)
	}
}

func TestCertsTokenRotation(t *testing.T) {
	certifier, _ := newCertifier(t, certsCluster())
	runCerts(t, certifier)

	before, err := certifier.Nodes.GetACMEPlugin(context.Background(), "cloudflare")
	if err != nil {
		t.Fatalf("GetACMEPlugin: %v", err)
	}

	certifier.Cluster.ACME.Token = "rotated-" + cfSecret
	res := runCerts(t, certifier)
	if res.Applied != 1 {
		t.Errorf("rotation Run() applied = %d, want exactly the plugin update", res.Applied)
	}

	after, err := certifier.Nodes.GetACMEPlugin(context.Background(), "cloudflare")
	if err != nil {
		t.Fatalf("GetACMEPlugin: %v", err)
	}
	if after.Data == before.Data {
		t.Error("plugin.Data unchanged after token rotation")
	}
}

// TestCertsRenewalNearExpiry moves the clock to within 30 days of the
// mock certificates' 2100-01-01 expiry: exactly the three order steps
// must reopen.
func TestCertsRenewalNearExpiry(t *testing.T) {
	certifier, _ := newCertifier(t, certsCluster())
	runCerts(t, certifier)

	certifier.Now = func() time.Time { return time.Date(2099, 12, 20, 0, 0, 0, 0, time.UTC) }
	res := runCerts(t, certifier)
	if res.Applied != 3 {
		t.Errorf("near-expiry Run() applied = %d, want the 3 order steps", res.Applied)
	}
}

// TestCertsRedaction drives a full run plus a dry-run and asserts the
// provider token appears in no output path: logs, dry-run report, or
// the stored plugin's String rendering.
func TestCertsRedaction(t *testing.T) {
	certifier, logBuf := newCertifier(t, certsCluster())

	var dryOut strings.Builder
	dry := steps.Runner{DryRun: true, Out: &dryOut, Log: certifier.Log}
	if _, err := dry.Run(context.Background(), certifier.Steps()); err != nil {
		t.Fatalf("dry Run() error: %v", err)
	}
	runCerts(t, certifier)

	plugin, err := certifier.Nodes.GetACMEPlugin(context.Background(), "cloudflare")
	if err != nil {
		t.Fatalf("GetACMEPlugin: %v", err)
	}

	for name, output := range map[string]string{
		"log":           logBuf.String(),
		"dry-run":       dryOut.String(),
		"plugin.String": plugin.String(),
	} {
		if strings.Contains(output, cfSecret) {
			t.Errorf("%s output leaks the provider token:\n%s", name, output)
		}
	}
}

// seededCertifier wires a Certifier against a mock whose ACME plugin
// was seeded before the CLI ever ran — the operator-registered-it-out-
// of-band case, which reaches pluginCheck with an existing record
// rather than one this stage created.
func seededCertifier(t *testing.T, cfg *config.Cluster, pluginData string) *pve.Certifier {
	t.Helper()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	mock.AddACMEPlugin(cfg.ACME.DNS, "dns", "cf", pluginData)

	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	var logBuf strings.Builder
	svc := nodes.NewService(client, version.Capabilities{})
	return &pve.Certifier{
		Cluster:  cfg,
		Nodes:    svc,
		Tasks:    tasks.NewService(client),
		DialRoot: sameServiceRoot(svc),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
		Log:      slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// cloudflarePluginData is what PVE stores for a Cloudflare plugin
// holding token: sorted KEY=value lines, empty values dropped, base64.
// Written out longhand rather than by calling the CLI's own encoder, so
// the test pins the wire format independently of the code under test.
func cloudflarePluginData(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("CF_Token=" + token))
}

// TestCertsPreexistingPluginMatches: a plugin already carrying exactly
// the configured credentials is left alone. Rewriting it would be a
// pointless cluster write on every run.
func TestCertsPreexistingPluginMatches(t *testing.T) {
	cfg := certsCluster()
	certifier := seededCertifier(t, cfg, cloudflarePluginData(cfSecret))

	res := runCerts(t, certifier)
	// Everything but the plugin step still has work to do: the account,
	// three node domains, three certificate orders.
	if res.Applied != 7 {
		t.Errorf("applied = %d, want 7 (the seeded plugin must be skipped)", res.Applied)
	}
	plugin, err := certifier.Nodes.GetACMEPlugin(context.Background(), cfg.ACME.DNS)
	if err != nil {
		t.Fatalf("GetACMEPlugin: %v", err)
	}
	if plugin.Data != cloudflarePluginData(cfSecret) {
		t.Errorf("seeded plugin data was rewritten: %q", plugin.Data)
	}
}

// TestCertsPreexistingPluginDrifted: a plugin holding a stale token is
// updated in place, not recreated — PVE rejects a create for an ID that
// already exists, so getting this branch wrong breaks every re-run
// after a token rotation.
func TestCertsPreexistingPluginDrifted(t *testing.T) {
	cfg := certsCluster()
	certifier := seededCertifier(t, cfg, cloudflarePluginData("stale-token-from-last-year"))

	res := runCerts(t, certifier)
	if res.Applied != 8 {
		t.Errorf("applied = %d, want 8 (the drifted plugin must be updated)", res.Applied)
	}
	plugin, err := certifier.Nodes.GetACMEPlugin(context.Background(), cfg.ACME.DNS)
	if err != nil {
		t.Fatalf("GetACMEPlugin: %v", err)
	}
	if plugin.Data != cloudflarePluginData(cfSecret) {
		t.Errorf("plugin data = %q, want the configured token", plugin.Data)
	}

	// And the whole stage converges from there.
	if again := runCerts(t, certifier); again.Applied != 0 {
		t.Errorf("re-run applied = %d, want 0", again.Applied)
	}
}

// TestCertsReusesExistingDomainSlot covers the slot-allocation branch a
// fresh cluster never reaches: the node already carries the FQDN, but
// in a slot other than 0 and bound to no plugin (a hand-configured
// node, or one wired before the plugin existed). The domain must be
// rewritten *in its existing slot* — allocating a fresh index instead
// would leave PVE holding the same domain twice, and a node with a
// duplicated domain fails its certificate order.
func TestCertsReusesExistingDomainSlot(t *testing.T) {
	cfg := certsCluster()
	node := cfg.PVE.Nodes[0].Name
	fqdn := node + "." + cfg.ACME.Domain

	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	// Slots 0 and 1 are taken by unrelated domains; ours sits in 2.
	mock.SetNodeConfigKey(node, "acmedomain0", "other-a."+cfg.ACME.Domain)
	mock.SetNodeConfigKey(node, "acmedomain1", "other-b."+cfg.ACME.Domain)
	mock.SetNodeConfigKey(node, "acmedomain2", fqdn)

	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	var logBuf strings.Builder
	svc := nodes.NewService(client, version.Capabilities{})
	certifier := &pve.Certifier{
		Cluster:  cfg,
		Nodes:    svc,
		Tasks:    tasks.NewService(client),
		DialRoot: sameServiceRoot(svc),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
		Log:      slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	runCerts(t, certifier)

	got, err := certifier.Nodes.GetNodeConfig(context.Background(), node)
	if err != nil {
		t.Fatalf("GetNodeConfig: %v", err)
	}

	var slots []int
	for _, d := range got.ACMEDomains {
		if d.Domain == fqdn {
			slots = append(slots, d.Index)
		}
	}
	if len(slots) != 1 {
		t.Fatalf("%s appears in %d slots (%v), want exactly 1", fqdn, len(slots), slots)
	}
	if slots[0] != 2 {
		t.Errorf("%s moved to slot %d, want its existing slot 2", fqdn, slots[0])
	}
	// The unrelated domains must survive untouched.
	for _, want := range []string{"other-a." + cfg.ACME.Domain, "other-b." + cfg.ACME.Domain} {
		if !slices.ContainsFunc(got.ACMEDomains, func(d nodes.ACMEDomain) bool {
			return d.Domain == want
		}) {
			t.Errorf("pre-existing domain %s was dropped", want)
		}
	}
}

// TestCertsFillsDomainSlotGap: with slots 0 and 2 taken, the new domain
// takes 1 rather than colliding or running past the six PVE allows.
func TestCertsFillsDomainSlotGap(t *testing.T) {
	cfg := certsCluster()
	node := cfg.PVE.Nodes[0].Name

	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	mock.SetNodeConfigKey(node, "acmedomain0", "taken-a."+cfg.ACME.Domain)
	mock.SetNodeConfigKey(node, "acmedomain2", "taken-c."+cfg.ACME.Domain)

	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	var logBuf strings.Builder
	svc := nodes.NewService(client, version.Capabilities{})
	certifier := &pve.Certifier{
		Cluster:  cfg,
		Nodes:    svc,
		Tasks:    tasks.NewService(client),
		DialRoot: sameServiceRoot(svc),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
		Log:      slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	runCerts(t, certifier)

	got, err := certifier.Nodes.GetNodeConfig(context.Background(), node)
	if err != nil {
		t.Fatalf("GetNodeConfig: %v", err)
	}
	fqdn := node + "." + cfg.ACME.Domain
	for _, d := range got.ACMEDomains {
		if d.Domain == fqdn {
			if d.Index != 1 {
				t.Errorf("%s landed in slot %d, want the free slot 1", fqdn, d.Index)
			}
			return
		}
	}
	t.Errorf("%s was not written to any slot", fqdn)
}
