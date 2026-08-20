package talos_test

import (
	"bytes"
	"strings"
	"testing"

	machcfg "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

const vanillaSchematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// metalMode mirrors the package's unexported validation mode so the
// round-trip test can re-validate rendered configs the same way.
type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

func testCluster() *config.Cluster {
	return &config.Cluster{
		Name: "homelab",
		Talos: config.Talos{
			Version:  talosVersion,
			Endpoint: "https://10.0.20.10:6443",
		},
	}
}

func testBundle(t *testing.T) *secrets.Bundle {
	t.Helper()
	contract, err := machcfg.ParseContractFromVersion(talosVersion)
	if err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	bundle, err := secrets.NewBundle(secrets.NewClock(), contract)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}
	return bundle
}

func TestInstallImage(t *testing.T) {
	if got, want := talos.InstallImage("", "v1.13.8"),
		"factory.talos.dev/installer/"+vanillaSchematic+":v1.13.8"; got != want {
		t.Errorf("InstallImage default schematic = %q, want %q", got, want)
	}
	if got, want := talos.InstallImage("abc123", "v1.13.8"),
		"factory.talos.dev/installer/abc123:v1.13.8"; got != want {
		t.Errorf("InstallImage pinned schematic = %q, want %q", got, want)
	}
}

func TestRoleTemplates(t *testing.T) {
	bundle := testBundle(t)
	tmpl, err := talos.RoleTemplates(bundle, testCluster())
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}
	if len(tmpl.Warnings) != 0 {
		t.Errorf("unexpected validation warnings: %q", tmpl.Warnings)
	}

	image := talos.InstallImage("", talosVersion)
	for name, data := range map[string][]byte{
		"controlplane": tmpl.ControlPlane,
		"worker":       tmpl.Worker,
	} {
		if !bytes.Contains(data, []byte(talos.HostnameVar)) {
			t.Errorf("%s template is missing the hostname expression", name)
		}
		if !bytes.Contains(data, []byte(talos.InstallImageVar)) {
			t.Errorf("%s template is missing the install_image expression", name)
		}
		if bytes.Contains(data, []byte("bootstrap-hostname-placeholder")) {
			t.Errorf("%s template leaked the hostname placeholder", name)
		}
		if bytes.Contains(data, []byte(image)) {
			t.Errorf("%s template baked in the real installer image", name)
		}
	}
	if !bytes.Contains(tmpl.ControlPlane, []byte("type: controlplane")) {
		t.Error("controlplane template is missing type: controlplane")
	}
	if !bytes.Contains(tmpl.Worker, []byte("type: worker")) {
		t.Error("worker template is missing type: worker")
	}
}

// TestRoleTemplatesRoundTrip renders each template the way booty
// would — substituting real values for the two expressions — and
// re-validates the result with machinery's own loader in metal mode.
func TestRoleTemplatesRoundTrip(t *testing.T) {
	bundle := testBundle(t)
	cluster := testCluster()
	tmpl, err := talos.RoleTemplates(bundle, cluster)
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}

	image := talos.InstallImage("", talosVersion)
	for name, data := range map[string][]byte{
		"controlplane": tmpl.ControlPlane,
		"worker":       tmpl.Worker,
	} {
		rendered := strings.ReplaceAll(string(data), talos.HostnameVar, "cp-01")
		rendered = strings.ReplaceAll(rendered, talos.InstallImageVar, image)

		provider, err := configloader.NewFromBytes([]byte(rendered))
		if err != nil {
			t.Fatalf("%s: machinery load of rendered config: %v", name, err)
		}
		warnings, err := provider.Validate(metalMode{})
		if err != nil {
			t.Errorf("%s: rendered config invalid in metal mode: %v", name, err)
		}
		if len(warnings) != 0 {
			t.Errorf("%s: rendered config warnings: %q", name, warnings)
		}
		if !strings.Contains(rendered, "hostname: cp-01") {
			t.Errorf("%s: rendered config is missing hostname: cp-01", name)
		}
		if got := provider.Machine().Install().Image(); got != image {
			t.Errorf("%s: rendered install image = %q, want %q", name, got, image)
		}
		if got := provider.Cluster().Endpoint().String(); got != cluster.Talos.Endpoint {
			t.Errorf("%s: cluster endpoint = %q, want %q", name, got, cluster.Talos.Endpoint)
		}
	}
}

// TestRoleTemplatesDeterministic pins the emit stage's byte-stable
// invariant at its source: the same secrets bundle must always yield
// byte-identical templates, or the emit diff-Check would report
// permanent drift.
func TestRoleTemplatesDeterministic(t *testing.T) {
	bundle := testBundle(t)
	cluster := testCluster()

	first, err := talos.RoleTemplates(bundle, cluster)
	if err != nil {
		t.Fatalf("first RoleTemplates: %v", err)
	}
	second, err := talos.RoleTemplates(bundle, cluster)
	if err != nil {
		t.Fatalf("second RoleTemplates: %v", err)
	}
	if !bytes.Equal(first.ControlPlane, second.ControlPlane) {
		t.Error("controlplane template differs between renders")
	}
	if !bytes.Equal(first.Worker, second.Worker) {
		t.Error("worker template differs between renders")
	}
}

func TestRoleTemplatesSchematicPin(t *testing.T) {
	cluster := testCluster()
	cluster.Talos.SchematicID = "abc123"
	tmpl, err := talos.RoleTemplates(testBundle(t), cluster)
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}
	// The pinned image is still swapped for the expression — the pin
	// changes the catalog's install_image var, not the template.
	if bytes.Contains(tmpl.ControlPlane, []byte("abc123")) {
		t.Error("pinned schematic leaked into the template")
	}
	if !bytes.Contains(tmpl.ControlPlane, []byte(talos.InstallImageVar)) {
		t.Error("template is missing the install_image expression")
	}
}
