package talos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"time"

	clusterapi "github.com/siderolabs/talos/pkg/machinery/api/cluster"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// Client is the narrow slice of the Talos API the bring-up stage
// drives. Mocks are generated with mockery (see .mockery.yml); the
// production implementation wraps the machinery client.
type Client interface {
	// Bootstrap issues the one-time etcd bootstrap. It returns the
	// server's error verbatim — the caller decides that "already
	// bootstrapped" is success, so that decision is testable.
	Bootstrap(ctx context.Context) error
	// Kubeconfig fetches the cluster's admin kubeconfig. It only
	// succeeds once etcd is bootstrapped and the API server is up,
	// which is what makes it double as the bootstrap step's Check.
	Kubeconfig(ctx context.Context) ([]byte, error)
	// Health blocks until the cluster reports healthy or the server-side
	// wait times out, streaming progress to log.
	Health(ctx context.Context, timeout time.Duration, log *slog.Logger) error
	// Close releases the underlying connection.
	Close() error
}

// endpointHost extracts the host the CLI dials for Talos API calls
// from the configured cluster endpoint. The config declares no per-VM
// IPs (identity is MAC-based, addresses come from DHCP reservations on
// those MACs), so talos.endpoint must resolve to the first
// control-plane node — the runbook states this as a prerequisite.
func endpointHost(cluster *config.Cluster) (string, error) {
	u, err := url.Parse(cluster.Talos.Endpoint)
	if err != nil {
		return "", fmt.Errorf("parse talos endpoint %q: %w", cluster.Talos.Endpoint, err)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("talos endpoint %q has no host", cluster.Talos.Endpoint)
	}
	return u.Hostname(), nil
}

// Talosconfig builds the admin client config from the secrets bundle:
// the OS CA plus a fresh admin client certificate, with the cluster
// endpoint host as the endpoint list. The certificate is generated
// anew on every call — write the result once and keep it, the same
// rule as the secrets bundle itself.
func Talosconfig(bundle *secrets.Bundle, cluster *config.Cluster) (*clientconfig.Config, error) {
	host, err := endpointHost(cluster)
	if err != nil {
		return nil, err
	}
	clientCert, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, fmt.Errorf("generate admin client certificate: %w", err)
	}
	return clientconfig.NewConfig(cluster.Name, []string{host}, bundle.Certs.OS.Crt, clientCert), nil
}

// NewClient dials the Talos API at the cluster endpoint host with
// admin credentials from the secrets bundle.
func NewClient(ctx context.Context, bundle *secrets.Bundle, cluster *config.Cluster) (Client, error) {
	cfg, err := Talosconfig(bundle, cluster)
	if err != nil {
		return nil, err
	}
	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dial talos api: %w", err)
	}
	return &machineryClient{c: c}, nil
}

// machineryClient adapts the machinery client to the Client interface.
type machineryClient struct {
	c *client.Client
}

func (m *machineryClient) Bootstrap(ctx context.Context) error {
	return m.c.Bootstrap(ctx, &machineapi.BootstrapRequest{})
}

func (m *machineryClient) Kubeconfig(ctx context.Context) ([]byte, error) {
	return m.c.Kubeconfig(ctx)
}

func (m *machineryClient) Health(ctx context.Context, timeout time.Duration, log *slog.Logger) error {
	// The server runs the wait; the local context gets slack on top so
	// the deadline that fires is the server's, with its structured
	// progress, not a bare local DeadlineExceeded.
	ctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()

	// An empty ClusterInfo makes the server enumerate members via its
	// own discovery — the config carries no per-node IPs to pass.
	stream, err := m.c.ClusterHealthCheck(ctx, timeout, &clusterapi.ClusterInfo{})
	if err != nil {
		return fmt.Errorf("start health check: %w", err)
	}
	for {
		progress, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("health check: %w", err)
		}
		if msg := progress.GetMessage(); msg != "" {
			log.Info("health", "status", msg)
		}
	}
}

func (m *machineryClient) Close() error {
	return m.c.Close()
}
