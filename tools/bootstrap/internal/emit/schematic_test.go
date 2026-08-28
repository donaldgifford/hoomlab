package emit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/donaldgifford/booty/catalog"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// baseExtensions is the drill's base profile in miniature.
var baseExtensions = []string{"siderolabs/iscsi-tools", "siderolabs/qemu-guest-agent"}

// profiledCluster is testCluster with a base profile on every node —
// the drill shape: one class per role, non-vanilla schematic.
func profiledCluster() *config.Cluster {
	c := testCluster()
	c.Talos.Profiles = []config.TalosProfile{
		{Name: "base", Extensions: []string{"siderolabs/qemu-guest-agent", "siderolabs/iscsi-tools"}},
	}
	for i := range c.Talos.Nodes {
		c.Talos.Nodes[i].Profiles = []string{"base"}
	}
	return c
}

// fakeResolver resolves extension sets to deterministic fake IDs and
// counts calls, so tests can assert one resolution per unique set.
type fakeResolver struct {
	calls atomic.Int32
}

func (f *fakeResolver) Resolve(_ context.Context, extensions []string) (string, error) {
	f.calls.Add(1)
	sum := sha256.Sum256([]byte(strings.Join(extensions, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// TestSchematicSingleClassKeepsPlainNames pins the drill shape: every
// node shares one profile set, so the catalog keeps talos-control and
// talos-worker while the boot paths and installer image move to the
// resolved schematic.
func TestSchematicSingleClassKeepsPlainNames(t *testing.T) {
	resolver := &fakeResolver{}
	e := testEmitter(t)
	e.Cluster = profiledCluster()
	e.Factory = resolver

	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Errorf("resolver called %d times, want 1 (one unique extension set)", got)
	}

	id, err := resolver.Resolve(context.Background(), baseExtensions)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	resolver.calls.Store(1)

	profiles := string(tree["catalog/10-profiles.hcl"].Data)
	for _, want := range []string{
		`profile "talos-control" {`,
		`profile "talos-worker" {`,
		"/" + id + "/vmlinuz",
		"factory.talos.dev/installer/" + id + ":" + talosVersion,
	} {
		if !strings.Contains(profiles, want) {
			t.Errorf("profiles catalog is missing %q:\n%s", want, profiles)
		}
	}
	if strings.Contains(profiles, vanillaSchematic) {
		t.Error("profiles catalog still references the vanilla schematic")
	}
}

// TestSchematicClassesSplitARole covers the general model: two
// workers with different image identities become two booty profiles,
// disambiguated by the schematic's short ID, and booty's own loader
// matches each node to its class.
func TestSchematicClassesSplitARole(t *testing.T) {
	resolver := &fakeResolver{}
	e := testEmitter(t)
	e.Cluster = profiledCluster()
	// worker-01 keeps the base profile; a second worker runs vanilla.
	e.Cluster.Talos.Nodes = append(e.Cluster.Talos.Nodes, config.TalosNode{
		Name: "worker-02", Role: config.RoleWorker, PVENode: "pve-02",
		VMID: 301, MAC: "02:50:99:a2:01:02",
	})
	e.Factory = resolver

	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}

	baseID, err := resolver.Resolve(context.Background(), baseExtensions)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	profiles := string(tree["catalog/10-profiles.hcl"].Data)
	for _, want := range []string{
		`profile "talos-control" {`, // single class: plain name
		fmt.Sprintf("profile %q {", "talos-worker-"+baseID[:8]),
		fmt.Sprintf("profile %q {", "talos-worker-"+vanillaSchematic[:8]),
	} {
		if !strings.Contains(profiles, want) {
			t.Errorf("profiles catalog is missing %q:\n%s", want, profiles)
		}
	}

	cat, err := catalog.DirSource{Root: filepath.Join(e.Root, "catalog")}.Load(context.Background())
	if err != nil {
		t.Fatalf("booty catalog load: %v", err)
	}
	for mac, wantProfile := range map[string]string{
		workerMAC:           "talos-worker-" + baseID[:8],
		"02:50:99:a2:01:02": "talos-worker-" + vanillaSchematic[:8],
		cpMAC:               "talos-control",
	} {
		res, err := cat.Match(catalog.Identity{MAC: mac})
		if err != nil {
			t.Fatalf("match %s: %v", mac, err)
		}
		if res.Profile.Name != wantProfile {
			t.Errorf("%s matched profile %q, want %q", mac, res.Profile.Name, wantProfile)
		}
	}
}

// schematicFactory extends the asset fake with the factory's
// /schematics endpoint and schematic-scoped asset paths, so the whole
// profile flow — resolve, render, download per class — runs against
// one server the way it would against the real factory.
func schematicFactory(t *testing.T) (*factory, string) {
	t.Helper()
	f := newFactory()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/schematics" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read schematic body: %v", err)
			}
			if !strings.Contains(string(body), "officialExtensions") {
				t.Errorf("schematic POST body missing officialExtensions:\n%s", body)
			}
			sum := sha256.Sum256(body)
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(map[string]string{"id": hex.EncodeToString(sum[:])}); err != nil {
				t.Errorf("encode schematic response: %v", err)
			}
			return
		}
		// /image/<schematic>/<version>/<file> for any schematic.
		f.hits.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/kernel-amd64"):
			_, _ = w.Write(f.kernel)
		case strings.HasSuffix(r.URL.Path, "/initramfs-amd64.xz"):
			_, _ = w.Write(f.initrd)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// TestSchematicEndToEnd runs the profiled cluster against a fake
// factory serving both endpoints: the real resolver POSTs the
// schematic, the catalog references the answered ID, and the assets
// land under its schematic-scoped directory.
func TestSchematicEndToEnd(t *testing.T) {
	f, url := schematicFactory(t)
	e := testEmitter(t)
	e.Cluster = profiledCluster()
	e.FactoryURL = url

	if res := runSteps(t, e); res.Applied != 2 {
		t.Errorf("first run applied %d steps, want 2", res.Applied)
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("downloaded %d assets, want 2 (one class)", got)
	}

	// The catalog and the staged assets must agree on the directory.
	profiles, err := os.ReadFile(filepath.Join(e.Root, "catalog", "10-profiles.hcl"))
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	versionDir := filepath.Join(e.Root, "boot", "talos", talosVersion)
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		t.Fatalf("read %s: %v", versionDir, err)
	}
	var schematicDirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			schematicDirs = append(schematicDirs, entry.Name())
		}
	}
	if len(schematicDirs) != 1 {
		t.Fatalf("staged schematic dirs = %v, want exactly one", schematicDirs)
	}
	if id := schematicDirs[0]; !strings.Contains(string(profiles), "/"+id+"/vmlinuz") {
		t.Errorf("catalog does not reference the staged schematic %s:\n%s", id, profiles)
	}
	if _, err := os.Stat(filepath.Join(versionDir, schematicDirs[0], "vmlinuz")); err != nil {
		t.Errorf("staged kernel missing: %v", err)
	}

	// Idempotence: same config, same content-addressed IDs, no work.
	if res := runSteps(t, e); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0", res.Applied)
	}
}

// TestSchematicFactoryError pins the failure shape: a factory that
// cannot answer fails the emit with the node named, rather than
// rendering a catalog with a missing image identity.
func TestSchematicFactoryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	e := testEmitter(t)
	e.Cluster = profiledCluster()
	e.FactoryURL = srv.URL

	_, err := e.Tree(context.Background())
	if err == nil {
		t.Fatal("Tree succeeded against a broken factory")
	}
	if !strings.Contains(err.Error(), "resolve schematic for node") {
		t.Errorf("error = %v, want it to name the node", err)
	}
}
