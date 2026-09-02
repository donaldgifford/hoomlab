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

// TestTalosNameFlowsToClusterName pins where each of the two names
// lands: the machineconfig clusterName is the Talos cluster's own
// name when the talos block sets one, and the cluster label only by
// inheritance.
func TestTalosNameFlowsToClusterName(t *testing.T) {
	bundle := testBundle(t)

	inherited, err := talos.RoleTemplates(bundle, testCluster())
	if err != nil {
		t.Fatalf("RoleTemplates (inherited): %v", err)
	}
	if !bytes.Contains(inherited.ControlPlane, []byte("clusterName: homelab")) {
		t.Error("without talos name, clusterName must inherit the cluster label")
	}

	named := testCluster()
	named.Talos.Name = "fartlab"
	tmpl, err := talos.RoleTemplates(bundle, named)
	if err != nil {
		t.Fatalf("RoleTemplates (named): %v", err)
	}
	if !bytes.Contains(tmpl.ControlPlane, []byte("clusterName: fartlab")) {
		t.Error("talos name set: clusterName must carry it")
	}
	if bytes.Contains(tmpl.ControlPlane, []byte("clusterName: homelab")) {
		t.Error("talos name set: the PVE label leaked into clusterName")
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

// multiNIC is the storage-plane interface pair (DESIGN-0004): a dhcp
// primary boot NIC and a static jumbo second NIC.
func multiNIC(bootMAC, storMAC, address string) []config.NetworkInterface {
	dhcp, primary := true, true
	static, mtu := false, 9000
	return []config.NetworkInterface{
		{Name: "net0", MAC: bootMAC, Bridge: "vmbr1", DHCP: &dhcp, Primary: &primary},
		{Name: "net1", MAC: storMAC, Bridge: "storbr0", DHCP: &static, Address: address, MTU: &mtu},
	}
}

// multiNICCluster carries the storage-plane shape on both roles.
func multiNICCluster(t *testing.T) *config.Cluster {
	t.Helper()
	c := testCluster()
	c.Talos.Nodes = []config.TalosNode{
		{
			Name: "ctrl01", Role: config.RoleControlPlane, PVENode: "pve-01", VMID: 201,
			Interfaces: multiNIC("02:50:99:a2:00:c9", "02:50:99:a2:14:c9", "10.10.13.51/24"),
		},
		{
			Name: "work01", Role: config.RoleWorker, PVENode: "pve-01", VMID: 301,
			Interfaces: multiNIC("02:50:99:a2:00:2d", "02:50:99:a2:14:2d", "10.10.13.61/24"),
		},
	}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		t.Fatalf("resolve multi-NIC cluster: %s", diags.Error())
	}
	return c
}

// TestRoleTemplatesInterfaces pins the multi-interface machineconfig
// shape (DESIGN-0004): every slot declared by deviceSelector with a
// per-node MAC expression, the static slot carrying its address
// expression and mtu, no placeholder identity leaked, and no routes —
// a secondary plane must never attract the default route.
func TestRoleTemplatesInterfaces(t *testing.T) {
	tmpl, err := talos.RoleTemplates(testBundle(t), multiNICCluster(t))
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}
	for name, data := range map[string][]byte{
		"controlplane": tmpl.ControlPlane,
		"worker":       tmpl.Worker,
	} {
		s := string(data)
		for _, want := range []string{
			`hardwareAddr: {{ index .Vars "net0_mac" }}`,
			`hardwareAddr: {{ index .Vars "net1_mac" }}`,
			`- {{ index .Vars "net1_address" }}`,
			"mtu: 9000",
			"dhcp: true",
			"dhcp: false",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s template is missing %q", name, want)
			}
		}
		for _, leak := range []string{"-mac-placeholder", "203.0.113."} {
			if strings.Contains(s, leak) {
				t.Errorf("%s template leaked placeholder identity %q", name, leak)
			}
		}
		if strings.Contains(s, "routes:") {
			t.Errorf("%s template carries routes; secondary planes must never attract the default route", name)
		}
	}
}

// TestRoleTemplatesSingleNICByteIdentical is the OQ-2 back-compat
// proof at the template layer: nodes with only their primary
// interface produce templates byte-identical to a cluster that
// declares no interfaces at all — no machine.network section, exactly
// the v0.2.0 artifact.
func TestRoleTemplatesSingleNICByteIdentical(t *testing.T) {
	bundle := testBundle(t)
	bare, err := talos.RoleTemplates(bundle, testCluster())
	if err != nil {
		t.Fatalf("RoleTemplates (no nodes): %v", err)
	}

	single := testCluster()
	dhcp, primary := true, true
	single.Talos.Nodes = []config.TalosNode{{
		Name: "cp-01", Role: config.RoleControlPlane, PVENode: "pve-01", VMID: 200,
		Interfaces: []config.NetworkInterface{
			{Name: "net0", MAC: "02:50:99:a2:00:01", Bridge: "vmbr0", DHCP: &dhcp, Primary: &primary},
		},
	}}
	if diags := single.ResolveInterfaces(); diags.HasErrors() {
		t.Fatalf("resolve: %s", diags.Error())
	}
	got, err := talos.RoleTemplates(bundle, single)
	if err != nil {
		t.Fatalf("RoleTemplates (single NIC): %v", err)
	}

	if !bytes.Equal(bare.ControlPlane, got.ControlPlane) {
		t.Error("single-interface controlplane template differs from the v0.2.0 shape")
	}
	if !bytes.Equal(bare.Worker, got.Worker) {
		t.Error("single-interface worker template differs from the v0.2.0 shape")
	}
	if bytes.Contains(got.ControlPlane, []byte("deviceSelector")) {
		t.Error("single-interface template declares interfaces; OQ-2 says it must not")
	}
}

// TestRoleTemplatesShapeDivergenceErrors pins the one-template-per-
// role constraint: two nodes of a role with different interface
// shapes cannot share a template, and the error names both sides
// rather than emitting a template that silently fits only one.
func TestRoleTemplatesShapeDivergenceErrors(t *testing.T) {
	c := multiNICCluster(t)
	dhcp, primary := true, true
	c.Talos.Nodes = append(c.Talos.Nodes, config.TalosNode{
		Name: "work02", Role: config.RoleWorker, PVENode: "pve-01", VMID: 302,
		Interfaces: []config.NetworkInterface{
			{Name: "net0", MAC: "02:50:99:a2:00:2e", Bridge: "vmbr1", DHCP: &dhcp, Primary: &primary},
		},
	})
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		t.Fatalf("resolve: %s", diags.Error())
	}

	_, err := talos.RoleTemplates(testBundle(t), c)
	if err == nil {
		t.Fatal("RoleTemplates accepted divergent shapes within a role")
	}
	for _, want := range []string{"work02", "work01", "shape"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("divergence error %q is missing %q", err, want)
		}
	}
}

// TestRoleTemplatesMultiNICRoundTrip renders the multi-interface
// template the way booty would — real values for every expression —
// and re-validates the result with machinery's own loader in metal
// mode. The rendered interfaces block is the shape the live fleet
// carries (IMPL-0003 Phase 2), by construction.
func TestRoleTemplatesMultiNICRoundTrip(t *testing.T) {
	cluster := multiNICCluster(t)
	tmpl, err := talos.RoleTemplates(testBundle(t), cluster)
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}
	image := talos.InstallImage("", talosVersion)

	rendered := string(tmpl.Worker)
	for expr, value := range map[string]string{
		talos.HostnameVar:                  "work01",
		talos.InstallImageVar:              image,
		`{{ index .Vars "net0_mac" }}`:     "02:50:99:a2:00:2d",
		`{{ index .Vars "net1_mac" }}`:     "02:50:99:a2:14:2d",
		`{{ index .Vars "net1_address" }}`: "10.10.13.61/24",
	} {
		if !strings.Contains(rendered, expr) {
			t.Fatalf("worker template is missing expression %q", expr)
		}
		rendered = strings.ReplaceAll(rendered, expr, value)
	}

	provider, err := configloader.NewFromBytes([]byte(rendered))
	if err != nil {
		t.Fatalf("machinery load of rendered config: %v", err)
	}
	warnings, err := provider.Validate(metalMode{})
	if err != nil {
		t.Errorf("rendered multi-NIC config invalid in metal mode: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("rendered multi-NIC config warnings: %q", warnings)
	}
	for _, want := range []string{
		"hardwareAddr: 02:50:99:a2:00:2d",
		"hardwareAddr: 02:50:99:a2:14:2d",
		"- 10.10.13.61/24",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered config is missing %q", want)
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
