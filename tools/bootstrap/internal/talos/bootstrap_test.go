package talos_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
	talosmocks "github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos/mocks"
)

const kubeconfigBytes = "apiVersion: v1\nkind: Config\nfake: kubeconfig\n"

// discardLogger keeps step progress out of the test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// errNotServing stands in for the gRPC failure an unbootstrapped (or
// unreachable) node returns from any query.
var errNotServing = status.Error(codes.Unavailable, "connection refused")

func newBootstrapper(t *testing.T) (*talos.Bootstrapper, *talosmocks.MockClient) {
	t.Helper()
	client := talosmocks.NewMockClient(t)
	return &talos.Bootstrapper{
		Cluster: testCluster(),
		Bundle:  testBundle(t),
		Client:  client,
		OutDir:  filepath.Join(t.TempDir(), "out"),
		Log:     discardLogger(),
	}, client
}

func runBootstrap(t *testing.T, b *talos.Bootstrapper) steps.Result {
	t.Helper()
	r := steps.Runner{Log: discardLogger()}
	res, err := r.Run(context.Background(), b.Steps())
	if err != nil {
		t.Fatalf("run bootstrap: %v", err)
	}
	return res
}

// TestBootstrapProbesEtcdNotKubeconfig pins the fix for INV-0001
// deviation 13. Talos GENERATES kubeconfigs locally from the cluster
// PKI — the fetch succeeds on any configured node, bootstrapped or
// not — so a Check probing with Kubeconfig reads "done" on a healthy
// waiting node and the stage skips the one step it exists to run,
// while `talos health` hangs forever on etcd in Preparing. This mock
// models the real cluster the drill met: kubeconfig always available,
// etcd never bootstrapped. The stage must still issue the bootstrap.
func TestBootstrapProbesEtcdNotKubeconfig(t *testing.T) {
	b, client := newBootstrapper(t)

	// The truth about an un-bootstrapped node: kubeconfig generation
	// works, etcd membership does not.
	client.EXPECT().Kubeconfig(mock.Anything).Return([]byte(kubeconfigBytes), nil).Maybe()
	client.EXPECT().EtcdMemberList(mock.Anything).
		Return(nil, status.Error(codes.FailedPrecondition, "etcd is not running")).Once()
	client.EXPECT().Bootstrap(mock.Anything).Return(nil).Once()

	if res := runBootstrap(t, b); res.Applied != 3 {
		t.Errorf("applied %d steps, want 3 (etcd-bootstrap must not skip itself)", res.Applied)
	}
}

// TestBootstrapProbeCannotHang pins the second round of INV-0001
// deviation 13. On an un-bootstrapped node, machined's internal etcd
// client retries its local dial until the caller's deadline — so a
// probe that passes no deadline sits forever, and the fixed Check's
// first live run hung silently instead of bootstrapping. The mock
// models that server: it returns only when the probe's context
// expires. The stage must still finish, and bootstrap.
func TestBootstrapProbeCannotHang(t *testing.T) {
	b, client := newBootstrapper(t)
	b.ProbeTimeout = 50 * time.Millisecond

	client.EXPECT().EtcdMemberList(mock.Anything).RunAndReturn(
		func(ctx context.Context) ([]string, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}).Once()
	client.EXPECT().Bootstrap(mock.Anything).Return(nil).Once()
	client.EXPECT().Kubeconfig(mock.Anything).Return([]byte(kubeconfigBytes), nil).Once()

	type runOut struct {
		res steps.Result
		err error
	}
	done := make(chan runOut, 1)
	go func() {
		r := steps.Runner{Log: discardLogger()}
		res, err := r.Run(context.Background(), b.Steps())
		done <- runOut{res, err}
	}()
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("run bootstrap: %v", out.err)
		}
		if out.res.Applied != 3 {
			t.Errorf("applied %d steps, want 3", out.res.Applied)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bootstrap run hung: the etcd probe carries no deadline")
	}
}

// TestBootstrapFreshRun is the first-call sequencing: probe fails (not
// bootstrapped), Bootstrap is issued exactly once, and both credential
// files land 0600.
func TestBootstrapFreshRun(t *testing.T) {
	b, client := newBootstrapper(t)

	// The etcd-bootstrap Check probes etcd membership and the node is
	// not bootstrapped yet; the kubeconfig fetch serves only its own
	// write step.
	client.EXPECT().EtcdMemberList(mock.Anything).Return(nil, errNotServing).Once()
	client.EXPECT().Bootstrap(mock.Anything).Return(nil).Once()
	client.EXPECT().Kubeconfig(mock.Anything).Return([]byte(kubeconfigBytes), nil).Once()

	if res := runBootstrap(t, b); res.Applied != 3 {
		t.Errorf("applied %d steps, want 3", res.Applied)
	}

	for name, wantContent := range map[string]string{
		"talosconfig": "",
		"kubeconfig":  kubeconfigBytes,
	} {
		path := filepath.Join(b.OutDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", name, got)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if wantContent != "" && string(data) != wantContent {
			t.Errorf("%s content = %q, want %q", name, data, wantContent)
		}
	}

	// The written talosconfig must load through machinery's own client
	// config parser and carry the endpoint host.
	cfg, err := clientconfig.Open(filepath.Join(b.OutDir, "talosconfig"))
	if err != nil {
		t.Fatalf("machinery open of written talosconfig: %v", err)
	}
	ctx, ok := cfg.Contexts[cfg.Context]
	if !ok {
		t.Fatalf("talosconfig has no context %q", cfg.Context)
	}
	if len(ctx.Endpoints) != 1 || ctx.Endpoints[0] != "10.0.20.10" {
		t.Errorf("talosconfig endpoints = %v, want [10.0.20.10]", ctx.Endpoints)
	}
	if ctx.CA == "" || ctx.Crt == "" || ctx.Key == "" {
		t.Error("talosconfig is missing CA or client credentials")
	}
}

// TestBootstrapAlreadyBootstrapped is the idempotency contract: Talos
// answers a second bootstrap with FailedPrecondition, and that is
// success, not an error.
func TestBootstrapAlreadyBootstrapped(t *testing.T) {
	b, client := newBootstrapper(t)

	// The probe fails (node mid-restart, say) so the step runs, and the
	// bootstrap call reports the work already done.
	client.EXPECT().EtcdMemberList(mock.Anything).Return(nil, errNotServing).Once()
	client.EXPECT().Bootstrap(mock.Anything).
		Return(status.Error(codes.FailedPrecondition, "etcd data directory is not empty")).Once()
	client.EXPECT().Kubeconfig(mock.Anything).Return([]byte(kubeconfigBytes), nil).Once()

	if res := runBootstrap(t, b); res.Applied != 3 {
		t.Errorf("applied %d steps, want 3", res.Applied)
	}
}

// TestBootstrapRerunIsNoOp is the convergence pass: with both files on
// disk and the cluster answering, nothing is applied and Bootstrap is
// never called again.
func TestBootstrapRerunIsNoOp(t *testing.T) {
	b, client := newBootstrapper(t)

	client.EXPECT().EtcdMemberList(mock.Anything).Return(nil, errNotServing).Once()
	client.EXPECT().Bootstrap(mock.Anything).Return(nil).Once()
	client.EXPECT().Kubeconfig(mock.Anything).Return([]byte(kubeconfigBytes), nil).Once()
	runBootstrap(t, b)

	// Second run: only the etcd-bootstrap Check queries the cluster,
	// and a bootstrapped etcd answers with its members.
	client.EXPECT().EtcdMemberList(mock.Anything).
		Return([]string{"cp-01", "cp-02"}, nil).Once()
	if res := runBootstrap(t, b); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0", res.Applied)
	}
}

// TestBootstrapRealFailure: any error that is not "already done" must
// stop the run, not be papered over.
func TestBootstrapRealFailure(t *testing.T) {
	b, client := newBootstrapper(t)

	client.EXPECT().EtcdMemberList(mock.Anything).Return(nil, errNotServing).Once()
	client.EXPECT().Bootstrap(mock.Anything).
		Return(status.Error(codes.Unavailable, "connection refused")).Once()

	r := steps.Runner{Log: discardLogger()}
	_, err := r.Run(context.Background(), b.Steps())
	if err == nil {
		t.Fatal("a failed bootstrap reported success")
	}
	if !strings.Contains(err.Error(), "etcd bootstrap") {
		t.Errorf("error %q does not name the bootstrap", err)
	}
	// The kubeconfig step never ran, so no file may exist.
	if _, err := os.Stat(filepath.Join(b.OutDir, "kubeconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Error("kubeconfig written despite failed bootstrap")
	}
}

// TestBootstrapNeverOverwritesCredentials: like the secrets bundle,
// existing credential files are left alone — a re-run must not rotate
// the operator's working credentials behind their back.
func TestBootstrapNeverOverwritesCredentials(t *testing.T) {
	b, client := newBootstrapper(t)

	if err := os.MkdirAll(b.OutDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"talosconfig", "kubeconfig"} {
		if err := os.WriteFile(filepath.Join(b.OutDir, name), []byte("operator-owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client.EXPECT().EtcdMemberList(mock.Anything).
		Return([]string{"cp-01", "cp-02"}, nil).Once()

	if res := runBootstrap(t, b); res.Applied != 0 {
		t.Errorf("applied %d steps over existing credentials, want 0", res.Applied)
	}
	for _, name := range []string{"talosconfig", "kubeconfig"} {
		data, err := os.ReadFile(filepath.Join(b.OutDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "operator-owned" {
			t.Errorf("%s was overwritten", name)
		}
	}
}

func TestBootstrapDryRun(t *testing.T) {
	b, client := newBootstrapper(t)
	client.EXPECT().EtcdMemberList(mock.Anything).Return(nil, errNotServing).Once()

	var out strings.Builder
	r := steps.Runner{DryRun: true, Out: &out, Log: discardLogger()}
	res, err := r.Run(context.Background(), b.Steps())
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Pending != 3 {
		t.Errorf("dry run reported %d pending, want 3", res.Pending)
	}
	if _, err := os.Stat(b.OutDir); !errors.Is(err, os.ErrNotExist) {
		t.Error("dry run created the output directory")
	}
}
