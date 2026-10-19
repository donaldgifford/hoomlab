package talos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	clusterapi "github.com/siderolabs/talos/pkg/machinery/api/cluster"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	clusterres "github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	runtimeres "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
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
	// Kubeconfig fetches the cluster's admin kubeconfig. Talos
	// GENERATES it from the cluster PKI in the machine config — apid
	// signs an admin certificate locally, no etcd or API server
	// involved — so it succeeds on any configured node, bootstrapped
	// or not (INV-0001 deviation 13: assuming otherwise made the
	// bootstrap step skip itself).
	Kubeconfig(ctx context.Context) ([]byte, error)
	// EtcdMemberList enumerates the etcd cluster's members. It fails
	// on a node whose etcd was never bootstrapped — there is no
	// cluster to enumerate — which is what makes it the bootstrap
	// step's Check probe. Errors return verbatim; the caller decides.
	EtcdMemberList(ctx context.Context) ([]string, error)
	// Health blocks until the cluster reports healthy or the server-side
	// wait times out, streaming progress to log.
	Health(ctx context.Context, timeout time.Duration, log *slog.Logger) error
	// Members lists the cluster members the endpoint node knows through
	// Talos discovery — the apply stage's way of finding node addresses,
	// since the config declares none.
	Members(ctx context.Context) ([]Member, error)
	// MachineConfig reads the active machine config of the node at
	// address, as the raw (multi-document) bytes Talos holds.
	MachineConfig(ctx context.Context, address string) ([]byte, error)
	// ApplyNoReboot applies a whole machine config to the node at
	// address in NO_REBOOT mode. A change Talos can only take with a
	// reboot is refused; the error returns verbatim.
	ApplyNoReboot(ctx context.Context, address string, data []byte) error
	// Extensions lists the system extensions running on the node at
	// address. Image Factory images carry one named "schematic" whose
	// version is the schematic ID.
	Extensions(ctx context.Context, address string) ([]Extension, error)
	// Close releases the underlying connection.
	Close() error
}

// Member is one Talos cluster member as discovery reports it.
type Member struct {
	Hostname  string
	Addresses []string
}

// Extension is one running system extension.
type Extension struct {
	Name    string
	Version string
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
	return clientconfig.NewConfig(cluster.TalosName(), []string{host}, bundle.Certs.OS.Crt, clientCert), nil
}

// NewClientFromTalosconfig dials the Talos API with the credentials in
// a talosconfig file — the operator's written <output>/out/talosconfig,
// which day-2 stages read rather than minting a fresh certificate.
func NewClientFromTalosconfig(ctx context.Context, path string) (Client, error) {
	cfg, err := clientconfig.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open talosconfig %s: %w", path, err)
	}
	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dial talos api: %w", err)
	}
	return &machineryClient{c: c}, nil
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

func (m *machineryClient) EtcdMemberList(ctx context.Context) ([]string, error) {
	resp, err := m.c.EtcdMemberList(ctx, &machineapi.EtcdMemberListRequest{})
	if err != nil {
		return nil, err
	}
	var members []string
	for _, msg := range resp.GetMessages() {
		for _, member := range msg.GetMembers() {
			members = append(members, member.GetHostname())
		}
	}
	return members, nil
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

func (m *machineryClient) Members(ctx context.Context) ([]Member, error) {
	list, err := safe.StateListAll[*clusterres.Member](ctx, m.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("list cluster members: %w", err)
	}
	members := make([]Member, 0, list.Len())
	for member := range list.All() {
		spec := member.TypedSpec()
		addrs := make([]string, 0, len(spec.Addresses))
		for _, a := range spec.Addresses {
			addrs = append(addrs, a.String())
		}
		members = append(members, Member{Hostname: spec.Hostname, Addresses: addrs})
	}
	return members, nil
}

func (m *machineryClient) MachineConfig(ctx context.Context, address string) ([]byte, error) {
	ctx = client.WithNode(ctx, address)
	mc, err := safe.StateGetByID[*configres.MachineConfig](ctx, m.c.COSI, configres.ActiveID)
	if err != nil {
		return nil, fmt.Errorf("read machine config: %w", err)
	}
	data, err := mc.Provider().Bytes()
	if err != nil {
		return nil, fmt.Errorf("encode machine config: %w", err)
	}
	return data, nil
}

func (m *machineryClient) ApplyNoReboot(ctx context.Context, address string, data []byte) error {
	ctx = client.WithNode(ctx, address)
	_, err := m.c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
		Data: data,
		Mode: machineapi.ApplyConfigurationRequest_NO_REBOOT,
	})
	return err
}

func (m *machineryClient) Extensions(ctx context.Context, address string) ([]Extension, error) {
	ctx = client.WithNode(ctx, address)
	list, err := safe.StateListAll[*runtimeres.ExtensionStatus](ctx, m.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("list extensions: %w", err)
	}
	exts := make([]Extension, 0, list.Len())
	for ext := range list.All() {
		md := ext.TypedSpec().Metadata
		exts = append(exts, Extension{Name: md.Name, Version: md.Version})
	}
	return exts, nil
}

func (m *machineryClient) Close() error {
	return m.c.Close()
}
