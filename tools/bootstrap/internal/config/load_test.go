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
// strings.Replace hits exactly the intended spot; where alignment
// makes two lines identical, the mutation anchors carry their leading
// indentation (interface attrs sit two spaces deeper than plane
// attrs). cp-01's interface takes the referenced form and worker-01's
// the inline form, so the base fixture exercises both (DESIGN-0004).
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

    network "servers" {
      dhcp    = true
      primary = true
    }

    booty {
      url = "http://10.0.10.5:8080"
    }

    node "cp-01" {
      role     = "controlplane"
      pve_node = "pve-01"
      vmid     = 200
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"

      network_interface "net0" {
        network = "servers"
        mac     = "02:50:99:a2:00:01"
        bridge  = "vmbr0"
      }
    }
    node "worker-01" {
      role     = "worker"
      pve_node = "pve-02"
      vmid     = 300
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"

      network_interface "net0" {
        mac     = "02:50:99:a2:01:01"
        bridge  = "vmbr1"
        dhcp    = true
        primary = true
      }
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
			mutate: replace(`mac     = "02:50:99:a2:00:01"`, `mac     = "02-50-99-A2-00-01"`),
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
			mutate:   replace(`mac     = "02:50:99:a2:01:01"`, `mac     = "02:50:99:a2:00:01"`),
			wantErrs: []string{"Duplicate network_interface mac"},
		},
		{
			name:     "duplicate mac case variant",
			mutate:   replace(`mac     = "02:50:99:a2:01:01"`, `mac     = "02:50:99:A2:00:01"`),
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
			mutate:   replace(`mac     = "02:50:99:a2:00:01"`, `mac     = "not-a-mac"`),
			wantErrs: []string{"Invalid network_interface mac", "not-a-mac"},
		},
		{
			name:     "vmid below pve floor",
			mutate:   replace("vmid     = 200", "vmid     = 42"),
			wantErrs: []string{"vmid 42 is below 100"},
		},
		{
			// The inline-form anchors carry their 8-space indentation:
			// worker-01's interface attrs would otherwise collide with
			// the identically aligned plane attrs at 6 spaces.
			name: "inline vlan tag accepted",
			mutate: replace(`        primary = true`, `        primary = true
        vlan    = 11`),
		},
		{
			name: "storage block accepted",
			mutate: replace(`node "pve-01" {`, `storage "local-zfs" {
      type  = "zfspool"
      pool  = "rpool/data"
      nodes = ["pve-01"]
    }
    node "pve-01" {`),
		},
		{
			name: "duplicate storage name",
			mutate: replace(`node "pve-01" {`, `storage "local-zfs" {
      type = "zfspool"
      pool = "rpool/data"
    }
    storage "local-zfs" {
      type = "zfspool"
      pool = "rpool/data"
    }
    node "pve-01" {`),
			wantErrs: []string{"Duplicate storage name"},
		},
		{
			name: "zfspool storage without pool",
			mutate: replace(`node "pve-01" {`, `storage "local-zfs" {
      type = "zfspool"
    }
    node "pve-01" {`),
			wantErrs: []string{"type zfspool requires pool"},
		},
		{
			name: "dir storage without path",
			mutate: replace(`node "pve-01" {`, `storage "local-zfs" {
      type = "dir"
    }
    node "pve-01" {`),
			wantErrs: []string{"type dir requires path"},
		},
		{
			name: "storage restricted to unknown node",
			mutate: replace(`node "pve-01" {`, `storage "local-zfs" {
      type  = "zfspool"
      pool  = "rpool/data"
      nodes = ["pve-99"]
    }
    node "pve-01" {`),
			wantErrs: []string{"Unknown storage node restriction", "pve-99"},
		},
		{
			name: "talos storage reference undeclared",
			mutate: replace(`node "pve-01" {`, `storage "other" {
      type = "zfspool"
      pool = "tank/vm"
    }
    node "pve-01" {`),
			wantErrs: []string{"Undeclared talos node storage", `storage "local-zfs" matches no declared`},
		},
		{
			name: "inline vlan above 802.1q range",
			mutate: replace(`        primary = true`, `        primary = true
        vlan    = 4095`),
			wantErrs: []string{"Invalid network_interface vlan", "vlan 4095 is outside the 802.1Q range 1-4094"},
		},
		{
			name: "negative inline vlan",
			mutate: replace(`        primary = true`, `        primary = true
        vlan    = -1`),
			wantErrs: []string{"Invalid network_interface vlan"},
		},
		{
			name: "invalid cluster cni",
			mutate: replace(`booty {`, `cluster {
      cni = "calico"
    }
    booty {`),
			wantErrs: []string{"Invalid talos cluster cni", `cni "calico"`},
		},
		{
			name: "cilium cni without cilium block",
			mutate: replace(`booty {`, `cluster {
      cni = "cilium"
    }
    booty {`),
			wantErrs: []string{"Missing cilium block"},
		},
		{
			name: "cilium block without cni cilium",
			mutate: replace(`booty {`, `cluster {
      cilium {
        version             = "v1.20.1"
        values              = "cilium-values.yaml"
        gateway_api_version = "v1.6.1"
      }
    }
    booty {`),
			wantErrs: []string{"Cilium block without cni cilium"},
		},
		{
			name: "unprefixed cilium version pin",
			mutate: replace(`booty {`, `cluster {
      cni = "cilium"
      cilium {
        version             = "1.18.5"
        values              = "cilium-values.yaml"
        gateway_api_version = "v1.6.1"
      }
    }
    booty {`),
			wantErrs: []string{"Unprefixed version pin", `version "1.18.5"`},
		},
		{
			name: "gateway api pin below the crd-layout floor",
			mutate: replace(`booty {`, `cluster {
      cni = "cilium"
      cilium {
        version             = "v1.20.1"
        values              = "cilium-values.yaml"
        gateway_api_version = "v1.4.1"
      }
    }
    booty {`),
			wantErrs: []string{"Gateway API pin below the CRD-layout floor", "v1.4.1"},
		},
		{
			name: "profile accepted",
			mutate: func(s string) string {
				s = replace(`booty {`, `profile "base" {
      extensions = ["siderolabs/qemu-guest-agent", "siderolabs/iscsi-tools"]
    }
    booty {`)(s)
				return replace(`vmid     = 200`, `vmid     = 200
      profiles = ["base"]`)(s)
			},
		},
		{
			name: "duplicate profile name",
			mutate: replace(`booty {`, `profile "base" {
      extensions = ["siderolabs/qemu-guest-agent"]
    }
    profile "base" {
      extensions = ["siderolabs/iscsi-tools"]
    }
    booty {`),
			wantErrs: []string{"Duplicate profile name"},
		},
		{
			name: "empty profile",
			mutate: replace(`booty {`, `profile "base" {
      extensions = []
    }
    booty {`),
			wantErrs: []string{"Empty profile"},
		},
		{
			name: "invalid extension name",
			mutate: replace(`booty {`, `profile "base" {
      extensions = ["qemu-guest-agent"]
    }
    booty {`),
			wantErrs: []string{"Invalid extension name", "org/name form"},
		},
		{
			name: "unknown profile reference",
			mutate: replace(`vmid     = 200`, `vmid     = 200
      profiles = ["gpu"]`),
			wantErrs: []string{"Unknown profile reference", `"gpu"`},
		},
		{
			// The drill's own near-miss: a profile block added, the node
			// attributes forgotten — which would silently boot every node
			// on the vanilla image, extensions missing.
			name: "unreferenced profile",
			mutate: replace(`booty {`, `profile "base" {
      extensions = ["siderolabs/qemu-guest-agent"]
    }
    booty {`),
			wantErrs: []string{"Unreferenced profile", `add profiles = ["base"]`},
		},
		{
			name: "schematic_id with profiles",
			mutate: replace(`booty {`, `schematic_id = "deadbeef"
    profile "base" {
      extensions = ["siderolabs/qemu-guest-agent"]
    }
    booty {`),
			wantErrs: []string{"Both schematic_id and profiles declared"},
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
			name: "invalid acme directory",
			mutate: replace(`dns    = "cloudflare"`, `dns       = "cloudflare"
    directory = "not a url"`),
			wantErrs: []string{"Invalid acme directory"},
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
					`mac     = "02:50:99:a2:00:01"`, `mac     = "02:50:99:a2:02:01"`, 1)
				second = strings.Replace(second,
					`mac     = "02:50:99:a2:01:01"`, `mac     = "02:50:99:a2:02:02"`, 1)
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

// ciliumHCL is validHCL with the full cilium completion surface: the
// cluster block references cilium-values.yaml relative to the config,
// so each test writes its own values file beside the config.
var ciliumHCL = strings.Replace(validHCL, "    booty {", `    cluster {
      cni = "cilium"
      cilium {
        version             = "v1.20.1"
        values              = "cilium-values.yaml"
        gateway_api_version = "v1.6.1"
      }
    }
    booty {`, 1)

// validCiliumValues is the minimal values content that satisfies the
// load-time checks: kube-proxy replacement and the KubePrism endpoint.
const validCiliumValues = `kubeProxyReplacement: true
k8sServiceHost: localhost
k8sServicePort: 7445
ipam:
  mode: kubernetes
`

// TestLoadCiliumValues covers the values-file validation DESIGN-0002
// requires: the file is operator input sealed into machineconfigs, so
// it is checked at load. The maglev case is a regression seeded with
// the archive's actual bug — a lost indent under "loadBalancer:" that
// shipped "algorithm" as a bogus top-level key for the cluster's
// whole life.
func TestLoadCiliumValues(t *testing.T) {
	tests := []struct {
		name string
		// values is the file content; empty means don't write the file.
		values   string
		wantErrs []string
	}{
		{
			name:   "valid values accepted",
			values: validCiliumValues,
		},
		{
			name:     "missing values file",
			wantErrs: []string{"Unreadable cilium values file"},
		},
		{
			name:     "not yaml",
			values:   "kubeProxyReplacement: true\n\t- what",
			wantErrs: []string{"Invalid cilium values file"},
		},
		{
			name: "maglev indentation regression",
			values: `kubeProxyReplacement: true
k8sServiceHost: localhost
k8sServicePort: 7445
loadBalancer:
algorithm: maglev
`,
			wantErrs: []string{"Null top-level cilium value", `"loadBalancer" has no value`},
		},
		{
			name: "kube-proxy replacement missing",
			values: `k8sServiceHost: localhost
k8sServicePort: 7445
`,
			wantErrs: []string{"Cilium values do not replace kube-proxy"},
		},
		{
			name: "kubeprism host wrong",
			values: `kubeProxyReplacement: true
k8sServiceHost: 10.10.11.51
k8sServicePort: 7445
`,
			wantErrs: []string{"Cilium values miss the KubePrism host"},
		},
		{
			name: "kubeprism port wrong",
			values: `kubeProxyReplacement: true
k8sServiceHost: localhost
k8sServicePort: 6443
`,
			wantErrs: []string{"Cilium values miss the KubePrism port", "7445"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTestEnv(t)
			path := writeConfig(t, ciliumHCL)
			if tt.values != "" {
				valuesPath := filepath.Join(filepath.Dir(path), "cilium-values.yaml")
				if err := os.WriteFile(valuesPath, []byte(tt.values), 0o600); err != nil {
					t.Fatalf("write values: %v", err)
				}
			}

			cluster, diags := Load(path)
			rendered := renderDiags(t, diags)

			if len(tt.wantErrs) == 0 {
				if diags.HasErrors() {
					t.Fatalf("Load() failed:\n%s", rendered)
				}
				// The relative values path must come back absolute, so
				// emit can read it from any working directory.
				got := cluster.Talos.Cluster.Cilium.Values
				if !filepath.IsAbs(got) {
					t.Errorf("Cilium.Values = %q, want an absolute path", got)
				}
				return
			}

			if !diags.HasErrors() {
				t.Fatalf("Load() succeeded, want errors %q", tt.wantErrs)
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
		strings.Replace(validHCL, `mac     = "02:50:99:a2:00:01"`, `mac     = "02-50-99-A2-00-01"`, 1))

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
	if got, want := cluster.Talos.Nodes[0].Role, RoleControlPlane; got != want {
		t.Errorf("Nodes[0].Role = %q, want %q", got, want)
	}
	if got, want := cluster.TalosName(), "test"; got != want {
		t.Errorf("TalosName() without talos name = %q, want the label %q", got, want)
	}

	// Both layers carry the canonical MAC after load: the raw block
	// and the resolved interface.
	cp := &cluster.Talos.Nodes[0]
	if got, want := cp.Interfaces[0].MAC, "02:50:99:a2:00:01"; got != want {
		t.Errorf("Interfaces[0].MAC = %q, want canonical %q", got, want)
	}
	nics := cp.ResolvedInterfaces()
	if len(nics) != 1 {
		t.Fatalf("ResolvedInterfaces() has %d entries, want 1", len(nics))
	}
	if got, want := nics[0].MAC, "02:50:99:a2:00:01"; got != want {
		t.Errorf("resolved MAC = %q, want canonical %q", got, want)
	}
	// cp-01 takes the referenced form: the plane's facts arrive whole.
	if !nics[0].DHCP || !nics[0].Primary {
		t.Errorf("resolved interface = %+v, want the servers plane's dhcp and primary facts", nics[0])
	}
	nic, ok := cp.PrimaryInterface()
	if !ok || nic.Slot != "net0" {
		t.Errorf("PrimaryInterface() = %+v, %t; want net0, true", nic, ok)
	}
}

// TestTalosNameSplitsTheLayers pins the two-name contract: the label
// stays the PVE cluster's name (pve form checks it against the live
// cluster) while talos name renames only the Talos side.
func TestTalosNameSplitsTheLayers(t *testing.T) {
	setTestEnv(t)
	path := writeConfig(t, strings.Replace(validHCL,
		`  talos {`, "  talos {\n    name     = \"fartlab\"", 1))

	cluster, diags := Load(path)
	if diags.HasErrors() {
		t.Fatalf("Load() failed:\n%s", renderDiags(t, diags))
	}
	if got, want := cluster.Name, "test"; got != want {
		t.Errorf("cluster.Name = %q, want the label %q untouched", got, want)
	}
	if got, want := cluster.TalosName(), "fartlab"; got != want {
		t.Errorf("TalosName() = %q, want the talos name %q", got, want)
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
