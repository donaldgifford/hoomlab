package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeValues writes valid cilium values beside a ciliumHCL config.
func writeValues(t *testing.T, configPath string) {
	t.Helper()
	valuesPath := filepath.Join(filepath.Dir(configPath), "cilium-values.yaml")
	if err := os.WriteFile(valuesPath, []byte(validCiliumValues), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
}

// withLabels adds a labels attribute to one fixture node, anchored on
// its unique vmid line.
func withLabels(t *testing.T, src, vmidLine, labels string) string {
	t.Helper()
	if !strings.Contains(src, vmidLine) {
		t.Fatalf("anchor %q not in fixture", vmidLine)
	}
	return strings.Replace(src, vmidLine, vmidLine+"\n      labels   = {\n"+labels+"\n      }", 1)
}

const (
	cpVMID     = "vmid     = 200"
	workerVMID = "vmid     = 300"
)

func TestLoadLabels(t *testing.T) {
	tests := []struct {
		name string
		// base is the fixture; empty means validHCL.
		base     string
		vmid     string
		labels   string
		wantErrs []string
	}{
		{
			name:   "hoomlab size label",
			vmid:   workerVMID,
			labels: `        "hoomlab.io/size" = "large"`,
		},
		{
			name:   "unprefixed key and empty value",
			vmid:   workerVMID,
			labels: `        "pool" = ""`,
		},
		{
			name:   "allow-listed node.kubernetes.io namespace",
			vmid:   workerVMID,
			labels: `        "node.kubernetes.io/pool" = "ci"`,
		},
		{
			name:   "allow-listed kubelet subdomain",
			vmid:   workerVMID,
			labels: `        "sub.kubelet.kubernetes.io/x" = "y"`,
		},
		{
			name:   "topology zone without a cluster block renders nothing to collide with",
			vmid:   workerVMID,
			labels: `        "topology.kubernetes.io/zone" = "rack-a"`,
		},
		{
			name:   "load-balancer exclusion on a worker is the operator's to set",
			vmid:   workerVMID,
			labels: `        "node.kubernetes.io/exclude-from-external-load-balancers" = "true"`,
		},
		{
			name:     "node-role prefix",
			vmid:     workerVMID,
			labels:   `        "node-role.kubernetes.io/ci" = ""`,
			wantErrs: []string{"Label refused by NodeRestriction", `"node-role.kubernetes.io/ci"`, `"hoomlab.io/ci"`},
		},
		{
			name:     "node-restriction prefix",
			vmid:     workerVMID,
			labels:   `        "node-restriction.kubernetes.io/pool" = "ci"`,
			wantErrs: []string{"Label refused by NodeRestriction"},
		},
		{
			name:     "other kubernetes.io key",
			vmid:     workerVMID,
			labels:   `        "kubernetes.io/role" = "ci"`,
			wantErrs: []string{"Label refused by NodeRestriction"},
		},
		{
			name:     "k8s.io subdomain",
			vmid:     workerVMID,
			labels:   `        "example.k8s.io/pool" = "ci"`,
			wantErrs: []string{"Label refused by NodeRestriction"},
		},
		{
			name:     "uppercase prefix",
			vmid:     workerVMID,
			labels:   `        "Hoomlab.io/size" = "large"`,
			wantErrs: []string{"Invalid label key", "not a DNS subdomain"},
		},
		{
			name:     "empty name",
			vmid:     workerVMID,
			labels:   `        "hoomlab.io/" = "large"`,
			wantErrs: []string{"Invalid label key"},
		},
		{
			name:     "name over 63 characters",
			vmid:     workerVMID,
			labels:   `        "hoomlab.io/` + strings.Repeat("a", 64) + `" = "x"`,
			wantErrs: []string{"Invalid label key"},
		},
		{
			name:     "invalid value",
			vmid:     workerVMID,
			labels:   `        "hoomlab.io/size" = "extra large"`,
			wantErrs: []string{"Invalid label value"},
		},
		{
			name:     "load-balancer exclusion on a control plane collides",
			vmid:     cpVMID,
			labels:   `        "node.kubernetes.io/exclude-from-external-load-balancers" = "true"`,
			wantErrs: []string{"Label collides with a rendered label"},
		},
		{
			name:     "topology zone collides with the completion surface",
			base:     ciliumHCL,
			vmid:     workerVMID,
			labels:   `        "topology.kubernetes.io/zone" = "rack-a"`,
			wantErrs: []string{"Label collides with a rendered label"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTestEnv(t)
			base := tt.base
			if base == "" {
				base = validHCL
			}
			path := writeConfig(t, withLabels(t, base, tt.vmid, tt.labels))
			if tt.base == ciliumHCL {
				writeValues(t, path)
			}

			cluster, diags := Load(path)
			rendered := renderDiags(t, diags)
			if len(tt.wantErrs) == 0 {
				if diags.HasErrors() {
					t.Fatalf("Load failed:\n%s", rendered)
				}
				if cluster == nil {
					t.Fatal("Load() = nil cluster without errors")
				}
				return
			}
			if !diags.HasErrors() {
				t.Fatalf("Load succeeded, want errors %q", tt.wantErrs)
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(rendered, want) {
					t.Errorf("diagnostics missing %q:\n%s", want, rendered)
				}
			}
		})
	}
}

func TestLoadResolvesLabels(t *testing.T) {
	setTestEnv(t)
	path := writeConfig(t, withLabels(t, validHCL, workerVMID, `        "hoomlab.io/size" = "large"`))
	cluster, diags := Load(path)
	if diags.HasErrors() {
		t.Fatalf("Load failed:\n%s", renderDiags(t, diags))
	}

	cp, worker := &cluster.Talos.Nodes[0], &cluster.Talos.Nodes[1]
	if got := cp.ResolvedLabels(); got != nil {
		t.Errorf("unlabelled node ResolvedLabels = %v, want nil", got)
	}
	got := worker.ResolvedLabels()
	if len(got) != 1 || got["hoomlab.io/size"] != "large" {
		t.Fatalf("ResolvedLabels = %v, want hoomlab.io/size=large", got)
	}
	got["hoomlab.io/size"] = "small"
	if worker.ResolvedLabels()["hoomlab.io/size"] != "large" {
		t.Error("ResolvedLabels returned the resolver's map, not a copy")
	}
}
