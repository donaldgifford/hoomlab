package talos

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clusterapi "github.com/siderolabs/talos/pkg/machinery/api/cluster"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// healthServer is a real ClusterService gRPC server: the health stream
// semantics (progress messages, EOF as success, mid-stream errors) are
// exactly what machineryClient.Health must translate, so they are
// tested against the genuine article rather than a hand-rolled fake.
type healthServer struct {
	clusterapi.UnimplementedClusterServiceServer
	messages []string
	failWith error
	// gotTimeout records the server-side wait the client requested.
	gotTimeout time.Duration
}

func (s *healthServer) HealthCheck(req *clusterapi.HealthCheckRequest,
	stream grpc.ServerStreamingServer[clusterapi.HealthCheckProgress],
) error {
	s.gotTimeout = req.GetWaitTimeout().AsDuration()
	for _, m := range s.messages {
		if err := stream.Send(&clusterapi.HealthCheckProgress{Message: m}); err != nil {
			return err
		}
	}
	return s.failWith
}

// dialHealth serves the ClusterService on a unix socket and returns a
// machineryClient connected to it.
func dialHealth(t *testing.T, srv *healthServer) *machineryClient {
	t.Helper()
	// Sockets live in the system temp dir, not t.TempDir(): macOS caps
	// sun_path at 104 bytes and go test's tempdirs blow through it.
	socket := filepath.Join(t.TempDir(), "t.sock")
	if len(socket) > 100 {
		t.Skipf("socket path %d bytes exceeds sun_path", len(socket))
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	clusterapi.RegisterClusterServiceServer(grpcServer, srv)
	go grpcServer.Serve(lis)
	t.Cleanup(grpcServer.GracefulStop)

	c, err := client.New(context.Background(), client.WithUnixSocket(socket),
		client.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &machineryClient{c: c}
}

func TestHealthSuccess(t *testing.T) {
	srv := &healthServer{messages: []string{"waiting for etcd", "all checks passed"}}
	m := dialHealth(t, srv)

	var logBuf strings.Builder
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	if err := m.Health(context.Background(), 5*time.Minute, log); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if srv.gotTimeout != 5*time.Minute {
		t.Errorf("server-side wait = %v, want 5m", srv.gotTimeout)
	}
	for _, want := range srv.messages {
		if !strings.Contains(logBuf.String(), want) {
			t.Errorf("log is missing progress message %q", want)
		}
	}
}

// TestHealthTimeout: the server-side wait expiring surfaces as an
// error carrying the server's diagnosis, not a silent success at
// stream end.
func TestHealthTimeout(t *testing.T) {
	srv := &healthServer{
		messages: []string{"waiting for all k8s nodes to report ready"},
		failWith: status.Error(codes.DeadlineExceeded, "health check timed out"),
	}
	m := dialHealth(t, srv)

	err := m.Health(context.Background(), time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("timed-out health check reported success")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error %q does not carry the server diagnosis", err)
	}
}
