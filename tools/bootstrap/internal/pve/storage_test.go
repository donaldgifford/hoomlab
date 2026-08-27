package pve_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/mockpve"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/pverr"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/storage"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/types"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/version"
)

// storageCluster is the drill shape in miniature: one entry over a
// vm dataset restricted to two nodes, plus the stock local-zfs
// restricted to the third — nodes-only, no content opinion.
func storageCluster() *config.Cluster {
	c := testCluster()
	c.PVE.Storage = []config.PVEStorage{
		{
			Name: "fast", Type: "zfspool", Pool: "fast/vm",
			Content: []string{"images"}, Nodes: []string{"pve-01", "pve-02"}, Sparse: true,
		},
		{
			Name: "local-zfs", Type: "zfspool", Pool: "rpool/data",
			Nodes: []string{"pve-03"},
		},
	}
	return c
}

func newDeclarer(t *testing.T, cfg *config.Cluster) (*pve.Declarer, *storage.Service) {
	t.Helper()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	svc := storage.NewService(client, version.Capabilities{})
	return &pve.Declarer{
		Cluster: cfg,
		Storage: svc,
		Log:     slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	}, svc
}

func runStorage(t *testing.T, d *pve.Declarer) steps.Result {
	t.Helper()
	r := steps.Runner{Log: d.Log}
	res, err := r.Run(context.Background(), d.Steps())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	return res
}

// seedEntry creates a pre-existing storage entry through the mock's
// API — the same path a hand-managed pvesm add would have taken.
func seedEntry(t *testing.T, svc *storage.Service, spec *storage.DatastoreSpec) {
	t.Helper()
	if _, err := svc.CreateDatastore(context.Background(), spec); err != nil {
		t.Fatalf("seed storage %q: %v", spec.Storage, err)
	}
}

func TestStorageFreshCreate(t *testing.T) {
	d, svc := newDeclarer(t, storageCluster())

	res := runStorage(t, d)
	if res.Applied != 2 {
		t.Errorf("applied = %d, want 2", res.Applied)
	}

	fast, err := svc.GetDatastore(context.Background(), "fast")
	if err != nil {
		t.Fatalf("read fast: %v", err)
	}
	if fast.Type != "zfspool" || fast.Pool != "fast/vm" {
		t.Errorf("fast = type %q pool %q, want zfspool fast/vm", fast.Type, fast.Pool)
	}
	if got := fast.Nodes; !strings.Contains(got, "pve-01") || !strings.Contains(got, "pve-02") {
		t.Errorf("fast nodes = %q, want pve-01 and pve-02", got)
	}

	lz, err := svc.GetDatastore(context.Background(), "local-zfs")
	if err != nil {
		t.Fatalf("read local-zfs: %v", err)
	}
	if lz.Nodes != "pve-03" {
		t.Errorf("local-zfs nodes = %q, want pve-03", lz.Nodes)
	}
}

// TestStorageConverged is the stage's core claim, and the INV-0001
// deviation 6 class in miniature: the second run must read the
// server's own rendering of every entry — set order, bool shape,
// sparse riding Extra — and apply nothing.
func TestStorageConverged(t *testing.T) {
	d, _ := newDeclarer(t, storageCluster())

	runStorage(t, d)
	if res := runStorage(t, d); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0 (rotation on identical state)", res.Applied)
	}
}

// TestStorageRestrictsExistingEntry is the stock-local-zfs scenario
// from the Phase 2 join-wipe findings: the entry already exists,
// unrestricted, with content types the config expresses no opinion
// about. The stage must add the nodes restriction and touch nothing
// else.
func TestStorageRestrictsExistingEntry(t *testing.T) {
	d, svc := newDeclarer(t, storageCluster())
	seedEntry(t, svc, &storage.DatastoreSpec{
		Storage: "local-zfs", Type: "zfspool", Pool: "rpool/data",
		Content: []string{"images", "rootdir"},
	})

	res := runStorage(t, d)
	if res.Applied != 2 { // fast created + local-zfs restricted
		t.Errorf("applied = %d, want 2", res.Applied)
	}

	lz, err := svc.GetDatastore(context.Background(), "local-zfs")
	if err != nil {
		t.Fatalf("read local-zfs: %v", err)
	}
	if lz.Nodes != "pve-03" {
		t.Errorf("local-zfs nodes = %q, want pve-03", lz.Nodes)
	}
	if got := lz.Content; !strings.Contains(got, "images") || !strings.Contains(got, "rootdir") {
		t.Errorf("local-zfs content = %q — the undeclared content types must survive the update", got)
	}
}

// TestStorageSetOrderTolerated seeds an entry equal to the declared
// state except for list order, which PVE never promises to preserve.
// The step must read done.
func TestStorageSetOrderTolerated(t *testing.T) {
	d, svc := newDeclarer(t, storageCluster())
	seedEntry(t, svc, &storage.DatastoreSpec{
		Storage: "fast", Type: "zfspool", Pool: "fast/vm",
		Content: []string{"images"},
		Nodes:   []string{"pve-02", "pve-01"}, // declared order reversed
		Sparse:  types.PVEBool(true),
	})

	res := runStorage(t, d)
	if res.Applied != 1 { // only local-zfs; fast must skip
		t.Errorf("applied = %d, want 1 (fast rotated on list order)", res.Applied)
	}
}

// TestStorageTypeMismatchErrors pins the identity guard: an existing
// entry with a different type cannot be converged — delete-and-
// recreate could orphan VM disks, so the stage stops with an error
// naming the conflict instead.
func TestStorageTypeMismatchErrors(t *testing.T) {
	d, svc := newDeclarer(t, storageCluster())
	seedEntry(t, svc, &storage.DatastoreSpec{
		Storage: "fast", Type: "dir", Path: "/mnt/fast",
	})

	r := steps.Runner{Log: d.Log}
	_, err := r.Run(context.Background(), d.Steps())
	if err == nil {
		t.Fatal("Run() succeeded over a type mismatch, want error")
	}
	if !strings.Contains(err.Error(), "type is fixed at creation") {
		t.Errorf("error = %v, want the fixed-at-creation explanation", err)
	}
}

func TestStorageDryRun(t *testing.T) {
	d, svc := newDeclarer(t, storageCluster())

	var out strings.Builder
	r := steps.Runner{DryRun: true, Out: &out, Log: d.Log}
	res, err := r.Run(context.Background(), d.Steps())
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Applied != 0 {
		t.Errorf("dry run applied %d steps, want 0", res.Applied)
	}
	if res.Pending != 2 {
		t.Errorf("dry run reported %d pending, want 2", res.Pending)
	}
	if _, err := svc.GetDatastore(context.Background(), "fast"); !errors.Is(err, pverr.ErrNotFound) {
		t.Errorf("dry run created storage fast (read err = %v)", err)
	}
}

// TestStorageNoBlocksIsEmptyStage guards the opt-in property: a
// config without storage blocks produces no steps and no API calls.
func TestStorageNoBlocksIsEmptyStage(t *testing.T) {
	d, _ := newDeclarer(t, testCluster())
	if got := len(d.Steps()); got != 0 {
		t.Errorf("Steps() = %d steps for zero storage blocks, want 0", got)
	}
}
