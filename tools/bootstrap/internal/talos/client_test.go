package talos_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// TestTalosconfigBadEndpoint: the endpoint is the one address the CLI
// dials for bring-up, so a config that yields no host must fail here
// rather than as an opaque dial error later.
func TestTalosconfigBadEndpoint(t *testing.T) {
	for name, endpoint := range map[string]string{
		"empty":    "",
		"no host":  "https://",
		"unparsed": "://not-a-url",
	} {
		t.Run(name, func(t *testing.T) {
			cluster := testCluster()
			cluster.Talos.Endpoint = endpoint
			_, err := talos.Talosconfig(testBundle(t), cluster)
			if err == nil {
				t.Fatalf("Talosconfig accepted endpoint %q", endpoint)
			}
			if !strings.Contains(err.Error(), "endpoint") {
				t.Errorf("error %q does not name the endpoint", err)
			}
		})
	}
}

// TestTalosconfigContents pins what the emitted admin config carries:
// the cluster name as the context, the endpoint host, and credentials
// from the bundle.
func TestTalosconfigContents(t *testing.T) {
	cluster := testCluster()
	cfg, err := talos.Talosconfig(testBundle(t), cluster)
	if err != nil {
		t.Fatalf("Talosconfig: %v", err)
	}
	if cfg.Context != cluster.Name {
		t.Errorf("context = %q, want %q", cfg.Context, cluster.Name)
	}
	ctx, ok := cfg.Contexts[cluster.Name]
	if !ok {
		t.Fatalf("no context named %q", cluster.Name)
	}
	if len(ctx.Endpoints) != 1 || ctx.Endpoints[0] != "10.0.20.10" {
		t.Errorf("endpoints = %v, want [10.0.20.10] (host of %s)",
			ctx.Endpoints, cluster.Talos.Endpoint)
	}
	if ctx.CA == "" || ctx.Crt == "" || ctx.Key == "" {
		t.Error("config is missing CA or client credentials")
	}
}

// TestTalosconfigRotatesCertificate documents why the bootstrap stage
// never rewrites an existing talosconfig: every call mints a fresh
// admin certificate, so re-emitting would hand the operator new
// credentials on every run.
func TestTalosconfigRotatesCertificate(t *testing.T) {
	bundle := testBundle(t)
	cluster := testCluster()

	first, err := talos.Talosconfig(bundle, cluster)
	if err != nil {
		t.Fatalf("first Talosconfig: %v", err)
	}
	second, err := talos.Talosconfig(bundle, cluster)
	if err != nil {
		t.Fatalf("second Talosconfig: %v", err)
	}
	if first.Contexts[cluster.Name].Crt == second.Contexts[cluster.Name].Crt {
		t.Error("expected a freshly minted client certificate per call")
	}
	// The CA is the cluster identity and must NOT rotate.
	if first.Contexts[cluster.Name].CA != second.Contexts[cluster.Name].CA {
		t.Error("the CA changed between calls — it comes from the secrets bundle")
	}
}
