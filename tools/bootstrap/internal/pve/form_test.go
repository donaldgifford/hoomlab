package pve_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/api"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/cluster"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/mockpve"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/version"
)

func testCluster() *config.Cluster {
	return &config.Cluster{
		Name: "homelab",
		PVE: config.PVE{
			TokenID:      "root@pam!bootstrap",
			TokenSecret:  "test-secret",
			RootPassword: "test-root-pw",
			Nodes: []config.PVENode{
				{Name: "pve-01", Endpoint: "https://10.0.10.11:8006", Address: "10.0.10.11", Primary: true},
				{Name: "pve-02", Endpoint: "https://10.0.10.12:8006", Address: "10.0.10.12"},
				{Name: "pve-03", Endpoint: "https://10.0.10.13:8006", Address: "10.0.10.13"},
			},
		},
	}
}

// writeCounter counts the cluster-mutating calls that pass through the
// test dialer, so tests can assert "zero writes" precisely.
type writeCounter struct {
	writes int
	// joinHosts records the contact address each join was told to
	// reach the primary on — the corosync link0 the cluster forms over.
	joinHosts []string
}

// tokenAuthed reports whether creds is the token flavor. The
// Credentials interface is opaque, so the concrete type is the only
// observable difference between token and user credentials out here.
func tokenAuthed(creds api.Credentials) bool {
	return reflect.TypeOf(creds) == reflect.TypeOf(api.TokenCredentials("id", "secret"))
}

// countingAPI wraps a cluster.API, bumping the counter on every write
// — and enforcing real PVE's auth model on the formation writes:
// POST /cluster/config and /cluster/config/join are reserved for the
// literal root@pam user, so a token-dialed connection gets the exact
// 403 real Proxmox returns (INV-0001 deviation, 2026-08-25). Reads
// pass regardless, matching the server.
type countingAPI struct {
	cluster.API
	c     *writeCounter
	token bool
}

// errRootOnly mirrors the server's rejection verbatim so tests fail
// the way the lab did.
var errRootOnly = errors.New("HTTP 403: Permission check failed (user != root@pam)")

func (a countingAPI) CreateCluster(ctx context.Context, spec *cluster.ClusterCreateSpec) error {
	if a.token {
		return errRootOnly
	}
	a.c.writes++
	return a.API.CreateCluster(ctx, spec)
}

func (a countingAPI) JoinCluster(ctx context.Context, spec *cluster.JoinSpec) error {
	if a.token {
		return errRootOnly
	}
	a.c.writes++
	a.c.joinHosts = append(a.c.joinHosts, spec.Hostname)
	return a.API.JoinCluster(ctx, spec)
}

// newTestFormer wires a Former against one mockpve server playing the
// whole cluster: every endpoint dials the same mock (the Dialer seam),
// joins are seeded in order via QueueClusterJoin by the caller.
func newTestFormer(t *testing.T, mock *mockpve.Server, cfg *config.Cluster) (*pve.Former, *writeCounter) {
	t.Helper()
	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)
	svc := cluster.NewService(client, version.Capabilities{})
	counter := &writeCounter{}
	return &pve.Former{
		Cluster: cfg,
		Dial: func(_ context.Context, _ string, creds api.Credentials) (cluster.API, error) {
			return countingAPI{API: svc, c: counter, token: tokenAuthed(creds)}, nil
		},
		PollInterval:  5 * time.Millisecond,
		JoinCeiling:   250 * time.Millisecond,
		QuorumCeiling: 250 * time.Millisecond,
	}, counter
}

// newFormationMock seeds a mock ready for full formation: the primary
// as self, both joins queued, and cluster status quorate (status
// entries are static seeds in mockpve; membership is dynamic).
func newFormationMock(t *testing.T) *mockpve.Server {
	t.Helper()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	mock.SetClusterSelfNode("pve-01")
	mock.QueueClusterJoin("pve-02")
	mock.QueueClusterJoin("pve-03")
	mock.SetClusterStatusInfo("homelab", 3, true)
	for _, n := range []string{"pve-01", "pve-02", "pve-03"} {
		mock.AddClusterStatusNode(n, true)
	}
	return mock
}

func members(t *testing.T, f *pve.Former) []string {
	t.Helper()
	svc, err := f.Dial(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	nodes, err := svc.ListConfigNodes(context.Background())
	if err != nil {
		t.Fatalf("ListConfigNodes: %v", err)
	}
	names := make([]string, 0, len(nodes))
	for i := range nodes {
		names = append(names, nodes[i].NodeName())
	}
	return names
}

func TestFormFreshCluster(t *testing.T) {
	mock := newFormationMock(t)
	former, counter := newTestFormer(t, mock, testCluster())
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}
	if len(stage) != 4 {
		t.Fatalf("Steps() = %d steps, want create + 2 joins + quorate = 4", len(stage))
	}

	var r steps.Runner
	res, err := r.Run(context.Background(), stage)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	// create + two joins apply; the quorate step checks done off the
	// seeded status.
	if res.Applied != 3 {
		t.Errorf("Run() applied = %d, want 3", res.Applied)
	}
	if counter.writes != 3 {
		t.Errorf("writes = %d, want 3 (create + 2 joins)", counter.writes)
	}
	if got, want := strings.Join(members(t, former), ","), "pve-01,pve-02,pve-03"; got != want {
		t.Errorf("membership = %q, want %q", got, want)
	}
}

// TestFormWritesRejectTokenAuth is the INV-0001 regression
// (2026-08-25): real PVE reserves cluster create/join for the literal
// root@pam user, and the enforcing test dialer mirrors that rejection.
// Formation succeeding under that rule proves both formation writes
// dial with root@pam password credentials — against the pre-fix code
// (create dialed with the API token) this fails exactly the way the
// first hardware contact did.
func TestFormWritesRejectTokenAuth(t *testing.T) {
	mock := newFormationMock(t)
	former, counter := newTestFormer(t, mock, testCluster())
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}

	var r steps.Runner
	if _, err := r.Run(context.Background(), stage); err != nil {
		t.Fatalf("formation failed under the root-only write rule: %v", err)
	}
	if counter.writes != 3 {
		t.Errorf("writes = %d, want 3 (create + 2 joins, all as root@pam)", counter.writes)
	}
}

func TestFormReRunIsNoOp(t *testing.T) {
	mock := newFormationMock(t)
	former, counter := newTestFormer(t, mock, testCluster())
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}

	var r steps.Runner
	if _, err := r.Run(context.Background(), stage); err != nil {
		t.Fatalf("first Run() error: %v", err)
	}
	writesAfterFirst := counter.writes

	res, err := r.Run(context.Background(), stage)
	if err != nil {
		t.Fatalf("second Run() error: %v", err)
	}
	if res.Applied != 0 {
		t.Errorf("second Run() applied = %d, want 0", res.Applied)
	}
	if counter.writes != writesAfterFirst {
		t.Errorf("second Run() made %d writes, want 0", counter.writes-writesAfterFirst)
	}
}

// TestFormInterruptionMatrix stops the stage after every step boundary
// and re-runs the whole stage: the re-run must converge with exactly
// the remaining steps and no repeated writes.
func TestFormInterruptionMatrix(t *testing.T) {
	for interruptAfter := 0; interruptAfter <= 3; interruptAfter++ {
		t.Run(strings.Repeat("step-", interruptAfter)+"boundary", func(t *testing.T) {
			mock := newFormationMock(t)
			former, counter := newTestFormer(t, mock, testCluster())
			stage, err := former.Steps()
			if err != nil {
				t.Fatalf("Steps() error: %v", err)
			}

			var r steps.Runner
			// The interrupted run: only the first interruptAfter steps
			// happened before the "crash".
			if _, err := r.Run(context.Background(), stage[:interruptAfter]); err != nil {
				t.Fatalf("interrupted Run() error: %v", err)
			}

			// The operator re-runs the full stage.
			if _, err := r.Run(context.Background(), stage); err != nil {
				t.Fatalf("re-Run() error: %v", err)
			}

			if got, want := strings.Join(members(t, former), ","), "pve-01,pve-02,pve-03"; got != want {
				t.Errorf("membership after re-run = %q, want %q", got, want)
			}
			if counter.writes != 3 {
				t.Errorf("total writes = %d, want exactly 3 across both runs", counter.writes)
			}
		})
	}
}

func TestFormDryRunMakesNoWrites(t *testing.T) {
	mock := newFormationMock(t)
	former, counter := newTestFormer(t, mock, testCluster())
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}

	var out strings.Builder
	r := steps.Runner{DryRun: true, Out: &out}
	res, err := r.Run(context.Background(), stage)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if counter.writes != 0 {
		t.Fatalf("dry-run made %d writes, want 0", counter.writes)
	}
	if got := members(t, former); len(got) != 0 {
		t.Errorf("dry-run changed membership: %v", got)
	}
	// create + both joins pending; quorate reads done off the seed.
	if res.Pending != 3 {
		t.Errorf("Run() pending = %d, want 3", res.Pending)
	}
	for _, want := range []string{"create-cluster", "join-pve-02", "join-pve-03", "cluster-quorate"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run report missing %q:\n%s", want, out.String())
		}
	}
}

// TestFormJoinNeverConverges exercises the join-tolerance path: the
// mock has no queued identity for pve-03, so its join request errors
// and membership never grows — the step must fail with the bounded
// membership timeout, naming the node.
func TestFormJoinNeverConverges(t *testing.T) {
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	mock.SetClusterSelfNode("pve-01")
	mock.QueueClusterJoin("pve-02")
	// pve-03 deliberately not queued.
	mock.SetClusterStatusInfo("homelab", 3, true)
	for _, n := range []string{"pve-01", "pve-02", "pve-03"} {
		mock.AddClusterStatusNode(n, true)
	}
	former, _ := newTestFormer(t, mock, testCluster())
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}

	var r steps.Runner
	_, err = r.Run(context.Background(), stage)
	if err == nil {
		t.Fatal("Run() succeeded, want membership-timeout error for pve-03")
	}
	if !strings.Contains(err.Error(), "pve-03") {
		t.Errorf("Run() error = %q, want it to name pve-03", err)
	}
}

// TestFormQuorumTimeout: everything joins but the cluster never
// reports quorate — the join's quorum gate must fail bounded.
func TestFormQuorumTimeout(t *testing.T) {
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	mock.SetClusterSelfNode("pve-01")
	mock.QueueClusterJoin("pve-02")
	mock.QueueClusterJoin("pve-03")
	mock.SetClusterStatusInfo("homelab", 3, false) // never quorate
	former, _ := newTestFormer(t, mock, testCluster())
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}

	var r steps.Runner
	_, err = r.Run(context.Background(), stage)
	if err == nil {
		t.Fatal("Run() succeeded, want quorum-timeout error")
	}
	if !strings.Contains(err.Error(), "not quorate") {
		t.Errorf("Run() error = %q, want a quorum timeout", err)
	}
}

func TestFormerStepsRequiresPrimary(t *testing.T) {
	cfg := testCluster()
	for i := range cfg.PVE.Nodes {
		cfg.PVE.Nodes[i].Primary = false
	}
	former := &pve.Former{Cluster: cfg, Dial: nil}
	if _, err := former.Steps(); err == nil {
		t.Fatal("Steps() succeeded without a primary node, want error")
	}
}

// TestFormContactAddressFallback covers the branch a config exercising
// the optional address field never reaches: with address omitted, the
// joining node must be told to contact the primary on its endpoint
// host. Getting this wrong sends corosync at an empty string, and the
// join fails on the remote node where the error is hardest to read.
func TestFormContactAddressFallback(t *testing.T) {
	cfg := testCluster()
	for i := range cfg.PVE.Nodes {
		cfg.PVE.Nodes[i].Address = ""
	}

	mock := newFormationMock(t)
	former, counter := newTestFormer(t, mock, cfg)
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}
	var r steps.Runner
	if _, err := r.Run(context.Background(), stage); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// https://10.0.10.11:8006 → 10.0.10.11
	for i, host := range counter.joinHosts {
		if host != "10.0.10.11" {
			t.Errorf("join %d contacted %q, want the primary endpoint host 10.0.10.11", i, host)
		}
	}
	if len(counter.joinHosts) != 2 {
		t.Errorf("recorded %d joins, want 2", len(counter.joinHosts))
	}
}

// TestFormContactAddressPrefersCorosync: when address is declared it
// wins over the endpoint host — that is the whole point of the field,
// letting corosync run on a separate network from the API.
func TestFormContactAddressPrefersCorosync(t *testing.T) {
	cfg := testCluster()
	cfg.PVE.Nodes[0].Address = "10.99.0.11" // corosync link, not the API host

	mock := newFormationMock(t)
	former, counter := newTestFormer(t, mock, cfg)
	stage, err := former.Steps()
	if err != nil {
		t.Fatalf("Steps() error: %v", err)
	}
	var r steps.Runner
	if _, err := r.Run(context.Background(), stage); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	for i, host := range counter.joinHosts {
		if host != "10.99.0.11" {
			t.Errorf("join %d contacted %q, want the declared address 10.99.0.11", i, host)
		}
	}
}
