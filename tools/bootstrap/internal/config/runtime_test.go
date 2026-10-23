package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kataLabel is the label every Kata-capable node derives.
const kataLabel = "runtime.hoomlab.dev/kata"

// withKata declares a kata profile and gives it to the node on the
// vmid line, so that node's extensions provide the Kata runtime.
func withKata(t *testing.T, src, vmidLine string) string {
	t.Helper()
	if !strings.Contains(src, "    booty {") || !strings.Contains(src, vmidLine) {
		t.Fatal("withKata anchors not in fixture")
	}
	src = strings.Replace(src, "    booty {", `    profile "kata" {
      extensions = ["siderolabs/kata-containers"]
    }
    booty {`, 1)
	return strings.Replace(src, vmidLine, vmidLine+"\n      profiles = [\"kata\"]", 1)
}

func TestLoadRuntimes(t *testing.T) {
	tests := []struct {
		name     string
		src      func(t *testing.T) string
		wantErrs []string
	}{
		{
			name: "kata on a worker",
			src: func(t *testing.T) string {
				t.Helper()
				return withKata(t, validHCL, workerVMID)
			},
		},
		{
			name: "kata on a control plane",
			src: func(t *testing.T) string {
				t.Helper()
				return withKata(t, validHCL, cpVMID)
			},
			wantErrs: []string{"Worker-only runtime on a control plane", `"cp-01"`, "siderolabs/kata-containers"},
		},
		{
			name: "explicit runtime label beside the derivation is an XOR error",
			src: func(t *testing.T) string {
				t.Helper()
				return withLabels(t, withKata(t, validHCL, workerVMID), workerVMID, `        "`+kataLabel+`" = "true"`)
			},
			wantErrs: []string{"Label in the derived runtime namespace", kataLabel},
		},
		{
			name: "explicit runtime label without the extension",
			src: func(t *testing.T) string {
				t.Helper()
				return withLabels(t, validHCL, workerVMID, `        "`+kataLabel+`" = "true"`)
			},
			wantErrs: []string{"Label in the derived runtime namespace"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTestEnv(t)
			_, diags := Load(writeConfig(t, tt.src(t)))
			rendered := renderDiags(t, diags)
			if len(tt.wantErrs) == 0 {
				if diags.HasErrors() {
					t.Fatalf("Load failed:\n%s", rendered)
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

func TestLoadDerivesRuntimeLabel(t *testing.T) {
	setTestEnv(t)
	src := withLabels(t, withKata(t, validHCL, workerVMID), workerVMID, `        "hoomlab.dev/size" = "large"`)
	cluster, diags := Load(writeConfig(t, src))
	if diags.HasErrors() {
		t.Fatalf("Load failed:\n%s", renderDiags(t, diags))
	}
	cp, worker := &cluster.Talos.Nodes[0], &cluster.Talos.Nodes[1]
	if got := cp.ResolvedLabels(); got != nil {
		t.Errorf("control plane without kata ResolvedLabels = %v, want nil", got)
	}
	got := worker.ResolvedLabels()
	if got[kataLabel] != "true" || got["hoomlab.dev/size"] != "large" || len(got) != 2 {
		t.Errorf("worker ResolvedLabels = %v, want the derived kata label beside the explicit size label", got)
	}
	if len(worker.Labels) != 1 {
		t.Errorf("raw Labels = %v, want the explicit label only — derivation must not write the raw form", worker.Labels)
	}
}

func TestLoadKataCiliumValues(t *testing.T) {
	const kataValues = validCiliumValues + "socketLB:\n  hostNamespaceOnly: true\n"
	tests := []struct {
		name     string
		kata     bool
		values   string
		wantErrs []string
	}{
		{name: "kata with explicit hostNamespaceOnly", kata: true, values: kataValues},
		{name: "no kata needs no socketLB setting", values: validCiliumValues},
		{
			name: "no kata allows the bandwidth manager", values: validCiliumValues + "bandwidthManager:\n  enabled: true\n",
		},
		{
			name: "kata with the bandwidth manager off", kata: true,
			values: kataValues + "bandwidthManager:\n  enabled: false\n",
		},
		{
			name: "kata without hostNamespaceOnly", kata: true, values: validCiliumValues,
			wantErrs: []string{"Cilium values miss socketLB.hostNamespaceOnly with Kata"},
		},
		{
			name: "kata with hostNamespaceOnly false", kata: true,
			values:   validCiliumValues + "socketLB:\n  hostNamespaceOnly: false\n",
			wantErrs: []string{"Cilium values miss socketLB.hostNamespaceOnly with Kata"},
		},
		{
			name: "kata with the bandwidth manager", kata: true,
			values:   kataValues + "bandwidthManager:\n  enabled: true\n",
			wantErrs: []string{"Cilium bandwidth manager enabled with Kata", "talos#13745"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTestEnv(t)
			src := ciliumHCL
			if tt.kata {
				src = withKata(t, src, workerVMID)
			}
			path := writeConfig(t, src)
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "cilium-values.yaml"), []byte(tt.values), 0o600); err != nil {
				t.Fatalf("write values: %v", err)
			}
			_, diags := Load(path)
			rendered := renderDiags(t, diags)
			if len(tt.wantErrs) == 0 {
				if diags.HasErrors() {
					t.Fatalf("Load failed:\n%s", rendered)
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
