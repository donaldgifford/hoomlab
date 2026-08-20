package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/hclkit/pkg/hclkit"
)

// validHCL is the minimal valid config the mutation table below edits.
// Every mutable token is unique within the string so a single
// strings.Replace hits exactly the intended spot.
const validHCL = `
cluster "test" {
  pve {
    token_id      = env("HOOMLAB_PVE_TOKEN_ID")
    token_secret  = env("HOOMLAB_PVE_TOKEN_SECRET")
    root_password = env("HOOMLAB_PVE_ROOT_PASSWORD")

    node "pve-01" {
      endpoint = "https://10.0.10.11:8006"
      address  = "10.0.10.11"
      primary  = true
    }
    node "pve-02" {
      endpoint = "https://10.0.10.12:8006"
    }
  }

  acme {
    email  = "ops@example.test"
    domain = "pve.example.test"
    dns    = "cloudflare"
    token  = env("HOOMLAB_CLOUDFLARE_API_TOKEN")
  }

  talos {
    version  = "v1.13.8"
    endpoint = "https://10.0.20.10:6443"

    booty {
      url = "http://10.0.10.5:8080"
    }

    node "cp-01" {
      role     = "controlplane"
      pve_node = "pve-01"
      vmid     = 200
      mac      = "02:50:99:a2:00:01"
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"
      bridge   = "vmbr0"
    }
    node "worker-01" {
      role     = "worker"
      pve_node = "pve-02"
      vmid     = 300
      mac      = "02:50:99:a2:01:01"
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"
      bridge   = "vmbr0"
    }
  }
}
`

// secretMarker is embedded in every test secret value; no diagnostic
// output may ever contain it.
const secretMarker = "sekrit"

func setTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOOMLAB_PVE_TOKEN_ID", secretMarker+"-token-id")
	t.Setenv("HOOMLAB_PVE_TOKEN_SECRET", secretMarker+"-token-secret")
	t.Setenv("HOOMLAB_PVE_ROOT_PASSWORD", secretMarker+"-root-pw")
	t.Setenv("HOOMLAB_CLOUDFLARE_API_TOKEN", secretMarker+"-cf-token")
	t.Setenv("HOOMLAB_BOOTSTRAP_TEST_EMPTY", "")
}

func writeConfig(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap.hcl")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func renderDiags(t *testing.T, diags hclkit.Diagnostics) string {
	t.Helper()
	var sb strings.Builder
	if _, err := diags.WriteTo(&sb); err != nil {
		t.Fatalf("render diagnostics: %v", err)
	}
	return sb.String()
}

func TestLoad(t *testing.T) {
	replace := func(old, new string) func(string) string {
		return func(s string) string {
			if !strings.Contains(s, old) {
				t.Fatalf("mutation target %q not in validHCL", old)
			}
			return strings.Replace(s, old, new, 1)
		}
	}

	tests := []struct {
		name   string
		mutate func(string) string
		// wantErrs empty means the config must load; otherwise every
		// entry must appear in the rendered diagnostics.
		wantErrs []string
	}{
		{
			name: "valid",
		},
		{
			name:   "mac normalized from dash notation",
			mutate: replace(`mac      = "02:50:99:a2:00:01"`, `mac      = "02-50-99-A2-00-01"`),
		},
		{
			name:     "missing env var",
			mutate:   replace("HOOMLAB_PVE_TOKEN_ID", "HOOMLAB_BOOTSTRAP_TEST_UNSET"),
			wantErrs: []string{"HOOMLAB_BOOTSTRAP_TEST_UNSET is not set"},
		},
		{
			name:     "empty env var",
			mutate:   replace("HOOMLAB_PVE_TOKEN_SECRET", "HOOMLAB_BOOTSTRAP_TEST_EMPTY"),
			wantErrs: []string{"HOOMLAB_BOOTSTRAP_TEST_EMPTY is set but empty"},
		},
		{
			name:     "missing required attribute",
			mutate:   replace(`email  = "ops@example.test"`, ""),
			wantErrs: []string{"email"},
		},
		{
			name:     "duplicate vmid",
			mutate:   replace("vmid     = 300", "vmid     = 200"),
			wantErrs: []string{"Duplicate node vmid"},
		},
		{
			name:     "duplicate mac literal",
			mutate:   replace(`mac      = "02:50:99:a2:01:01"`, `mac      = "02:50:99:a2:00:01"`),
			wantErrs: []string{"Duplicate node mac"},
		},
		{
			name:     "duplicate mac case variant",
			mutate:   replace(`mac      = "02:50:99:a2:01:01"`, `mac      = "02:50:99:A2:00:01"`),
			wantErrs: []string{`mac "02:50:99:a2:00:01" is already used by node "cp-01"`},
		},
		{
			name:     "no primary pve node",
			mutate:   replace("primary  = true", "primary  = false"),
			wantErrs: []string{"Exactly one primary pve node required"},
		},
		{
			name: "two primary pve nodes",
			mutate: replace(`endpoint = "https://10.0.10.12:8006"`,
				`endpoint = "https://10.0.10.12:8006"
      primary  = true`),
			wantErrs: []string{"Exactly one primary pve node required"},
		},
		{
			name:     "unknown pve_node reference",
			mutate:   replace(`pve_node = "pve-02"`, `pve_node = "pve-99"`),
			wantErrs: []string{`pve_node "pve-99" names no declared pve node`},
		},
		{
			name:     "invalid role",
			mutate:   replace(`role     = "worker"`, `role     = "banana"`),
			wantErrs: []string{`role "banana" must be "controlplane" or "worker"`},
		},
		{
			name:     "no controlplane node",
			mutate:   replace(`role     = "controlplane"`, `role     = "worker"`),
			wantErrs: []string{"No controlplane node declared"},
		},
		{
			name:     "invalid mac",
			mutate:   replace(`mac      = "02:50:99:a2:00:01"`, `mac      = "not-a-mac"`),
			wantErrs: []string{"Invalid talos node mac", "not-a-mac"},
		},
		{
			name:     "vmid below pve floor",
			mutate:   replace("vmid     = 200", "vmid     = 42"),
			wantErrs: []string{"vmid 42 is below 100"},
		},
		{
			name:     "non-http pve endpoint",
			mutate:   replace(`endpoint = "https://10.0.10.11:8006"`, `endpoint = "ftp://10.0.10.11"`),
			wantErrs: []string{"Invalid pve node endpoint", "scheme must be http or https"},
		},
		{
			name:     "hostless talos endpoint",
			mutate:   replace(`endpoint = "https://10.0.20.10:6443"`, `endpoint = "https://"`),
			wantErrs: []string{"Invalid talos endpoint", "missing host"},
		},
		{
			name:     "schemeless booty url",
			mutate:   replace(`url = "http://10.0.10.5:8080"`, `url = "10.0.10.5:8080"`),
			wantErrs: []string{"Invalid booty url"},
		},
		{
			name:     "unsupported acme dns provider",
			mutate:   replace(`dns    = "cloudflare"`, `dns    = "route53"`),
			wantErrs: []string{"Unsupported acme dns provider", "route53"},
		},
		{
			name:     "zero cluster blocks",
			mutate:   func(string) string { return "" },
			wantErrs: []string{"Exactly one cluster block required"},
		},
		{
			name: "two cluster blocks",
			// The second cluster gets distinct vmids/macs so the
			// decode-time uniqueness validators (which are global
			// across all node blocks) stay quiet and the count check
			// is what fires.
			mutate: func(s string) string {
				second := strings.ReplaceAll(s, `"test"`, `"second"`)
				second = strings.Replace(second, "vmid     = 200", "vmid     = 400", 1)
				second = strings.Replace(second, "vmid     = 300", "vmid     = 401", 1)
				second = strings.Replace(second,
					`mac      = "02:50:99:a2:00:01"`, `mac      = "02:50:99:a2:02:01"`, 1)
				second = strings.Replace(second,
					`mac      = "02:50:99:a2:01:01"`, `mac      = "02:50:99:a2:02:02"`, 1)
				return s + second
			},
			wantErrs: []string{"Exactly one cluster block required"},
		},
		{
			name:     "syntax error",
			mutate:   func(s string) string { return s + "\n}" },
			wantErrs: []string{"error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTestEnv(t)
			src := validHCL
			if tt.mutate != nil {
				src = tt.mutate(src)
			}
			path := writeConfig(t, src)

			cluster, diags := Load(path)
			rendered := renderDiags(t, diags)

			if strings.Contains(rendered, secretMarker) {
				t.Errorf("diagnostics leak a secret value:\n%s", rendered)
			}

			if len(tt.wantErrs) == 0 {
				if diags.HasErrors() {
					t.Fatalf("Load(%s) failed:\n%s", tt.name, rendered)
				}
				if cluster == nil {
					t.Fatal("Load() = nil cluster without errors")
				}
				return
			}

			if !diags.HasErrors() {
				t.Fatalf("Load(%s) succeeded, want errors %q", tt.name, tt.wantErrs)
			}
			if cluster != nil {
				t.Error("Load() returned a cluster alongside errors, want nil")
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(rendered, want) {
					t.Errorf("diagnostics missing %q:\n%s", want, rendered)
				}
			}
		})
	}
}

func TestLoadResolvesAndNormalizes(t *testing.T) {
	setTestEnv(t)
	path := writeConfig(t,
		strings.Replace(validHCL, `mac      = "02:50:99:a2:00:01"`, `mac      = "02-50-99-A2-00-01"`, 1))

	cluster, diags := Load(path)
	if diags.HasErrors() {
		t.Fatalf("Load() failed:\n%s", renderDiags(t, diags))
	}

	if got, want := cluster.Name, "test"; got != want {
		t.Errorf("cluster.Name = %q, want %q", got, want)
	}
	if got, want := cluster.PVE.TokenID, secretMarker+"-token-id"; got != want {
		t.Errorf("PVE.TokenID = %q, want the resolved env value %q", got, want)
	}
	if got, want := cluster.ACME.Token, secretMarker+"-cf-token"; got != want {
		t.Errorf("ACME.Token = %q, want the resolved env value %q", got, want)
	}
	if got, want := cluster.Talos.Nodes[0].MAC, "02:50:99:a2:00:01"; got != want {
		t.Errorf("Nodes[0].MAC = %q, want canonical %q", got, want)
	}
	if got, want := cluster.Talos.Nodes[0].Role, RoleControlPlane; got != want {
		t.Errorf("Nodes[0].Role = %q, want %q", got, want)
	}
}

// TestLoadExampleConfig pins the shipped example: it must stay valid.
func TestLoadExampleConfig(t *testing.T) {
	setTestEnv(t)

	cluster, diags := Load(filepath.Join("..", "..", "examples", "bootstrap.hcl"))
	if diags.HasErrors() {
		t.Fatalf("example config invalid:\n%s", renderDiags(t, diags))
	}
	if got, want := len(cluster.PVE.Nodes), 3; got != want {
		t.Errorf("example pve nodes = %d, want %d", got, want)
	}
	if got, want := len(cluster.Talos.Nodes), 4; got != want {
		t.Errorf("example talos nodes = %d, want %d", got, want)
	}
}
