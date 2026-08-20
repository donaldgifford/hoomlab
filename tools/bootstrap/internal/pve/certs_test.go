package pve_test

import (
	"context"
	"log/slog"
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
	return &pve.Certifier{
		Cluster: cfg,
		Nodes:   nodes.NewService(client, version.Capabilities{}),
		Tasks:   tasks.NewService(client),
		Now:     func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
		Log:     slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, &logBuf
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
