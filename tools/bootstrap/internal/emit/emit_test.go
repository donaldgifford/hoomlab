package emit_test

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	machcfg "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/donaldgifford/booty/catalog"
	"github.com/donaldgifford/booty/render"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/emit"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

const (
	talosVersion = "v1.13.8"
	bootyURL     = "http://10.0.10.5:8080"
	cpMAC        = "02:50:99:a2:00:01"
	workerMAC    = "02:50:99:a2:01:01"
)

// metalMode mirrors the runtime's bare-metal validation mode — the one
// a PXE-booted node is validated under.
type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

// bootNIC is the single inline-form boot NIC the emit fixtures need.
// The fixtures resolve after construction, the same way Load resolves
// a real config.
func bootNIC(mac string) []config.NetworkInterface {
	dhcp, primary := true, true
	return []config.NetworkInterface{{
		Name: "net0", MAC: mac, Bridge: "vmbr0", DHCP: &dhcp, Primary: &primary,
	}}
}

func testCluster() *config.Cluster {
	c := &config.Cluster{
		Name: "homelab",
		Talos: config.Talos{
			Version:  talosVersion,
			Endpoint: "https://10.0.20.10:6443",
			Booty:    config.Booty{URL: bootyURL},
			Nodes: []config.TalosNode{
				{Name: "cp-01", Role: config.RoleControlPlane, PVENode: "pve-01", VMID: 200, Interfaces: bootNIC(cpMAC)},
				{Name: "cp-02", Role: config.RoleControlPlane, PVENode: "pve-02", VMID: 201, Interfaces: bootNIC("02:50:99:a2:00:02")},
				{Name: "worker-01", Role: config.RoleWorker, PVENode: "pve-01", VMID: 300, Interfaces: bootNIC(workerMAC)},
			},
		},
	}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		panic("testCluster does not resolve: " + diags.Error())
	}
	return c
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

func testEmitter(t *testing.T) *emit.Emitter {
	t.Helper()
	return &emit.Emitter{
		Cluster: testCluster(),
		Bundle:  testBundle(t),
		Root:    t.TempDir(),
	}
}

// TestTreeGolden pins the artifacts that are a pure function of the
// config. The machineconfig templates carry freshly generated secrets
// and cannot be golden — TestEmittedCatalogLoadsInBooty validates those
// through booty and machinery instead.
func TestTreeGolden(t *testing.T) {
	treeGolden(t, testEmitter(t), "golden")
}

// withStorageNIC appends the static jumbo storage NIC to a boot NIC —
// the fartlab shape from IMPL-0003.
func withStorageNIC(nics []config.NetworkInterface, mac, address string) []config.NetworkInterface {
	dhcp, mtu := false, 9000
	return append(nics, config.NetworkInterface{
		Name: "net1", MAC: mac, Bridge: "storbr0", DHCP: &dhcp, Address: address, MTU: &mtu,
	})
}

// multiNICTestCluster is testCluster with the storage plane on every
// node, so the catalog groups carry the per-interface vars.
func multiNICTestCluster() *config.Cluster {
	c := &config.Cluster{
		Name: "homelab",
		Talos: config.Talos{
			Version:  talosVersion,
			Endpoint: "https://10.0.20.10:6443",
			Booty:    config.Booty{URL: bootyURL},
			Nodes: []config.TalosNode{
				{
					Name: "cp-01", Role: config.RoleControlPlane, PVENode: "pve-01", VMID: 200,
					Interfaces: withStorageNIC(bootNIC(cpMAC), "02:50:99:a2:14:01", "10.10.13.51/24"),
				},
				{
					Name: "cp-02", Role: config.RoleControlPlane, PVENode: "pve-02", VMID: 201,
					Interfaces: withStorageNIC(bootNIC("02:50:99:a2:00:02"), "02:50:99:a2:14:02", "10.10.13.52/24"),
				},
				{
					Name: "worker-01", Role: config.RoleWorker, PVENode: "pve-01", VMID: 300,
					Interfaces: withStorageNIC(bootNIC(workerMAC), "02:50:99:a2:14:03", "10.10.13.61/24"),
				},
			},
		},
	}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		panic("multiNICTestCluster does not resolve: " + diags.Error())
	}
	return c
}

// TestTreeGoldenMultiNIC pins the multi-interface artifacts: the
// groups catalog gains each slot's MAC and the static slot's address
// as per-node vars; everything else is byte-identical to the
// single-interface goldens by construction.
func TestTreeGoldenMultiNIC(t *testing.T) {
	e := testEmitter(t)
	e.Cluster = multiNICTestCluster()
	treeGolden(t, e, "golden-multinic")
}

// treeGolden compares the deterministic tree artifacts against one
// golden set; -update rewrites the set.
func treeGolden(t *testing.T, e *emit.Emitter, goldenDir string) {
	t.Helper()
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	for _, path := range []string{
		"catalog/00-variables.hcl",
		"catalog/10-profiles.hcl",
		"catalog/20-groups.hcl",
		"embed.ipxe",
		"booty-run.sh",
	} {
		file, ok := tree[path]
		if !ok {
			t.Fatalf("tree is missing %s", path)
		}
		golden := filepath.Join("testdata", goldenDir, filepath.FromSlash(path))
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatalf("create golden dir: %v", err)
			}
			if err := os.WriteFile(golden, file.Data, 0o600); err != nil {
				t.Fatalf("write golden: %v", err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read golden %s (run: go test ./internal/emit -update): %v", golden, err)
		}
		if !bytes.Equal(file.Data, want) {
			t.Errorf("%s differs from golden:\n--- got ---\n%s\n--- want ---\n%s",
				path, file.Data, want)
		}
	}
}

// TestEmbedScriptConfiguresNICBeforeFetch pins the fix for INV-0001
// deviation 11. The embedded script runs in place of iPXE's autoboot
// sequence — the sequence that would have configured the NIC — so
// net0 has no address until the script itself runs dhcp, and an embed
// without that line dies on its very first fetch with ENETUNREACH
// (exactly how the drill's first boot ended, six VMs at an iPXE
// shell). booty's walkthrough calls the dhcp line load-bearing.
func TestEmbedScriptConfiguresNICBeforeFetch(t *testing.T) {
	tree, err := testEmitter(t).Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	script := string(tree["embed.ipxe"].Data)
	dhcp, chain := -1, -1
	for i, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if dhcp < 0 && strings.HasPrefix(trimmed, "dhcp") {
			dhcp = i
		}
		if chain < 0 && strings.HasPrefix(trimmed, "chain ") {
			chain = i
		}
	}
	if chain < 0 {
		t.Fatalf("embed.ipxe has no chain line:\n%s", script)
	}
	if dhcp < 0 || dhcp > chain {
		t.Fatalf("embed.ipxe never configures the NIC before its first fetch "+
			"(dhcp line %d, chain line %d):\n%s", dhcp, chain, script)
	}
}

// TestTreeDeterministic is the invariant the emit step's Check rests
// on: identical inputs must render identical bytes, or every run would
// report drift and tell the operator to restart booty forever.
func TestTreeDeterministic(t *testing.T) {
	e := testEmitter(t)
	first, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("first Tree: %v", err)
	}
	second, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("second Tree: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("tree size changed: %d then %d", len(first), len(second))
	}
	for _, path := range first.Paths() {
		if !bytes.Equal(first[path].Data, second[path].Data) {
			t.Errorf("%s differs between renders", path)
		}
	}
}

func TestTreeWriteAndDiff(t *testing.T) {
	e := testEmitter(t)
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}

	changed, err := tree.Diff(e.Root)
	if err != nil {
		t.Fatalf("Diff on empty root: %v", err)
	}
	if len(changed) != len(tree) {
		t.Errorf("Diff on empty root reported %d changed, want all %d", len(changed), len(tree))
	}

	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	changed, err = tree.Diff(e.Root)
	if err != nil {
		t.Fatalf("Diff after write: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("Diff after write reported %v, want nothing", changed)
	}

	// The launcher must land executable or the operator's copy-paste
	// step fails on the booty host.
	info, err := os.Stat(filepath.Join(e.Root, "booty-run.sh"))
	if err != nil {
		t.Fatalf("stat booty-run.sh: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("booty-run.sh mode = %04o, want 0755", got)
	}

	// A hand-edited artifact is drift, and drift is what Check exists
	// to catch.
	edited := filepath.Join(e.Root, "catalog", "20-groups.hcl")
	if err := os.WriteFile(edited, []byte("# tampered\n"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	changed, err = tree.Diff(e.Root)
	if err != nil {
		t.Fatalf("Diff after tamper: %v", err)
	}
	if len(changed) != 1 || changed[0] != "catalog/20-groups.hcl" {
		t.Errorf("Diff after tamper = %v, want [catalog/20-groups.hcl]", changed)
	}

	// Rewriting restores the tree — and must restore the mode too,
	// since WriteFile leaves an existing file's mode alone.
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("re-Write: %v", err)
	}
	changed, err = tree.Diff(e.Root)
	if err != nil {
		t.Fatalf("Diff after re-write: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("Diff after re-write reported %v, want nothing", changed)
	}
}

// TestEmittedCatalogLoadsInBooty is the contract test: the emitted
// catalog is loaded by booty's own loader, a synthetic node is matched
// the way a booting machine would be, its machineconfig is rendered
// through booty's renderer with our overlay, and the result is parsed
// and validated by machinery in metal mode. If any link in that chain
// is wrong, a real node fails to boot with no useful error — so the
// whole chain runs in-process here.
func TestEmittedCatalogLoadsInBooty(t *testing.T) {
	e := testEmitter(t)
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}

	cat, err := catalog.DirSource{Root: filepath.Join(e.Root, "catalog")}.Load(context.Background())
	if err != nil {
		t.Fatalf("booty catalog load: %v", err)
	}
	if len(cat.Profiles) != 2 {
		t.Errorf("loaded %d profiles, want 2", len(cat.Profiles))
	}
	if len(cat.Groups) != 3 {
		t.Errorf("loaded %d groups, want 3", len(cat.Groups))
	}

	renderer, err := render.New(render.WithTemplates(os.DirFS(filepath.Join(e.Root, "templates"))))
	if err != nil {
		t.Fatalf("booty renderer with overlay: %v", err)
	}

	for _, tc := range []struct {
		node, mac, profile, wantType string
	}{
		{"cp-01", cpMAC, "talos-control", "controlplane"},
		{"worker-01", workerMAC, "talos-worker", "worker"},
	} {
		t.Run(tc.node, func(t *testing.T) {
			id := catalog.Identity{MAC: tc.mac}
			res, err := cat.Match(id)
			if err != nil {
				t.Fatalf("match %s: %v", tc.mac, err)
			}
			if res.Group != tc.node {
				t.Errorf("matched group %q, want %q", res.Group, tc.node)
			}
			if res.Profile.Name != tc.profile {
				t.Errorf("matched profile %q, want %q", res.Profile.Name, tc.profile)
			}

			// The boot cmdline is what a mis-emitted catalog gets wrong
			// silently: a node without these boots, badly.
			cmdline := res.Profile.Boot.Cmdline
			for _, want := range []string{
				"talos.platform=metal", "init_on_alloc=1", "slab_nomerge", "pti=on",
				"talos.config=" + bootyURL + "/machine-config?mac=${mac}",
			} {
				if !containsString(cmdline, want) {
					t.Errorf("cmdline %v is missing %q", cmdline, want)
				}
			}

			rendered, err := renderer.Config(id, res, bootyURL)
			if err != nil {
				t.Fatalf("booty render config: %v", err)
			}
			provider, err := configloader.NewFromBytes([]byte(rendered))
			if err != nil {
				t.Fatalf("machinery load of booty-rendered config: %v", err)
			}
			warnings, err := provider.Validate(metalMode{})
			if err != nil {
				t.Fatalf("rendered config invalid in metal mode: %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("rendered config warnings: %q", warnings)
			}
			if got := provider.Machine().Type().String(); got != tc.wantType {
				t.Errorf("machine type = %q, want %q", got, tc.wantType)
			}
			// Both overlay substitutions must have resolved to the
			// node's own values.
			if !bytes.Contains([]byte(rendered), []byte("hostname: "+tc.node)) {
				t.Errorf("rendered config is missing hostname: %s", tc.node)
			}
			if got, want := provider.Machine().Install().Image(),
				"factory.talos.dev/installer/"; !bytes.Contains([]byte(got), []byte(want)) {
				t.Errorf("install image = %q, want a %s… reference", got, want)
			}
		})
	}
}

// TestUnknownMACDoesNotMatch guards the identity binding: the catalog
// pins each VM by MAC, so a machine that is not in the config must not
// be handed a Talos machineconfig by accident.
func TestUnknownMACDoesNotMatch(t *testing.T) {
	e := testEmitter(t)
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	cat, err := catalog.DirSource{Root: filepath.Join(e.Root, "catalog")}.Load(context.Background())
	if err != nil {
		t.Fatalf("booty catalog load: %v", err)
	}
	if _, err := cat.Match(catalog.Identity{MAC: "aa:bb:cc:dd:ee:ff"}); err == nil {
		t.Error("an unconfigured MAC matched a profile")
	}
}

// discardLogger keeps step progress out of the test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestIdentityBindingSurvivesMACSpelling covers the far end of the
// identity chain, where a MAC crosses out of our code and into booty's.
// A booting machine reports its MAC however its firmware spells it, and
// booty compares that against the selector we emitted. If the two
// spellings disagree the group simply never matches: the node PXE
// boots, is handed nothing, and no error anywhere says why.
//
// The near end — an operator writing the MAC in any accepted spelling
// and the loader rewriting it to canonical form — is pinned by
// config.TestLoadResolvesAndNormalizes, so this starts from a canonical
// config and asserts the two things that test cannot see: that we emit
// the canonical form, and that booty matches a node against it no
// matter how the node spells itself.
func TestIdentityBindingSurvivesMACSpelling(t *testing.T) {
	cluster := testCluster()
	cluster.Talos.Nodes = cluster.Talos.Nodes[:1]

	e := &emit.Emitter{Cluster: cluster, Bundle: testBundle(t), Root: t.TempDir()}
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Our half of the contract. booty normalizes selectors on its side
	// too, so a successful match alone would pass even if we emitted
	// something odd — this pins the bytes we actually write.
	groups := string(tree["catalog/20-groups.hcl"].Data)
	if want := `mac = "` + cpMAC + `"`; !strings.Contains(groups, want) {
		t.Errorf("emitted selector is not the canonical %s:\n%s", want, groups)
	}

	cat, err := catalog.DirSource{Root: filepath.Join(e.Root, "catalog")}.
		Load(context.Background())
	if err != nil {
		t.Fatalf("booty catalog load: %v", err)
	}

	for _, reported := range []string{
		"02:50:99:a2:00:01",
		"02:50:99:A2:00:01",
		"02-50-99-a2-00-01",
		"02-50-99-A2-00-01",
		"0250.99a2.0001",
	} {
		res, err := cat.Match(catalog.Identity{MAC: reported})
		if err != nil {
			t.Fatalf("a node reporting %q matched no group: %v", reported, err)
		}
		if res.Group != "cp-01" {
			t.Errorf("a node reporting %q matched group %q, want cp-01", reported, res.Group)
		}
	}
}

// TestLauncherImageTagHasNoVPrefix pins a mismatch that bit once and
// would bite again: booty's git tags carry a leading "v" (v0.2.1) but
// its GHCR tags do not (ghcr.io/donaldgifford/booty:0.2.1). Emitting
// the git spelling produces an image reference that does not resolve,
// and it fails in the worst place — on the booty host, after the tree
// has been copied over, with docker reporting only "not found".
//
// Whatever the operator writes in the config, the launcher must carry
// a tag that exists.
func TestLauncherImageTagHasNoVPrefix(t *testing.T) {
	for name, version := range map[string]string{
		"default":    "",
		"v-prefixed": "v0.3.0",
		"bare":       "0.3.0",
		"prerelease": "v0.3.0-rc.1",
	} {
		t.Run(name, func(t *testing.T) {
			cluster := testCluster()
			cluster.Talos.Booty.Version = version

			e := &emit.Emitter{Cluster: cluster, Bundle: testBundle(t), Root: t.TempDir()}
			tree, err := e.Tree(context.Background())
			if err != nil {
				t.Fatalf("Tree: %v", err)
			}

			const prefix = "ghcr.io/donaldgifford/booty:"
			launcher := string(tree["booty-run.sh"].Data)
			_, after, found := strings.Cut(launcher, prefix)
			if !found {
				t.Fatalf("launcher references no booty image:\n%s", launcher)
			}
			tag, _, _ := strings.Cut(after, "}")
			if strings.HasPrefix(tag, "v") {
				t.Errorf("image tag %q carries the git-tag \"v\" prefix; GHCR has no such tag", tag)
			}
			if version != "" && tag != strings.TrimPrefix(version, "v") {
				t.Errorf("image tag = %q, want %q", tag, strings.TrimPrefix(version, "v"))
			}
		})
	}
}
