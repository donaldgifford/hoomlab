package talos

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
)

// The credential files the stage writes under <output>/out/. Both are
// 0600: each is a full-power credential to its half of the cluster.
const (
	talosconfigName = "talosconfig"
	kubeconfigName  = "kubeconfig"
	credentialMode  = 0o600
)

// defaultProbeTimeout bounds the etcd membership probe. On an
// un-bootstrapped node machined's internal etcd client retries its
// local dial until the caller's deadline — pass none and the probe
// hangs forever (INV-0001 deviation 13, second round: the fixed
// Check's first live run sat silently instead of bootstrapping).
// Fifteen seconds is generous for a live etcd answering its own node.
const defaultProbeTimeout = 15 * time.Second

// Bootstrapper builds the Stage 5 step list: write the talosconfig,
// bootstrap etcd once, and write the kubeconfig fetched from the live
// cluster — the credentials the operator (and later the Hoomlab
// service install) talks to the cluster with.
type Bootstrapper struct {
	Cluster *config.Cluster
	// Bundle seeds the talosconfig's admin certificate.
	Bundle *secrets.Bundle
	// Client is the Talos API session, dialed at the endpoint host.
	Client Client
	// OutDir is where the credentials land, e.g. <output>/out.
	OutDir string
	// ProbeTimeout bounds the etcd membership probe; zero means
	// defaultProbeTimeout. Tests shrink it.
	ProbeTimeout time.Duration
	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

func (b *Bootstrapper) logger() *slog.Logger {
	if b.Log == nil {
		return slog.Default()
	}
	return b.Log
}

// Steps returns the bring-up steps in order. The talosconfig comes
// first — it needs no cluster and is the operator's debugging line
// into nodes that will not bootstrap.
func (b *Bootstrapper) Steps() []steps.Step {
	return []steps.Step{
		{
			Name:  "write-talosconfig",
			Check: b.fileExists(talosconfigName),
			Apply: b.applyTalosconfig,
		},
		{
			Name:  "etcd-bootstrap",
			Check: b.bootstrapped,
			Apply: b.applyBootstrap,
		},
		{
			Name:  "write-kubeconfig",
			Check: b.fileExists(kubeconfigName),
			Apply: b.applyKubeconfig,
		},
	}
}

// fileExists is the Check for both credential files. Their contents
// carry freshly generated certificates, so a byte-diff would always
// differ — existence is the convergence signal, the same skip-if-present
// rule as the secrets bundle.
func (b *Bootstrapper) fileExists(name string) func(context.Context) (bool, error) {
	return func(context.Context) (bool, error) {
		_, err := os.Stat(filepath.Join(b.OutDir, name))
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", name, err)
	}
}

// bootstrapped probes etcd membership on the endpoint node: only a
// bootstrapped etcd has a member list to enumerate. Any error reads
// as "not yet" — an unreachable node and an unbootstrapped one both
// mean the step should run, Apply's error carries the real diagnosis,
// and Apply already treats "data directory is not empty" as success,
// so a false pending is harmless.
//
// The previous probe fetched a kubeconfig, assuming that needs a live
// API server. It does not: Talos generates the kubeconfig locally
// from the cluster PKI, so the probe read "done" on every healthy
// un-bootstrapped node and the stage skipped the one step it exists
// to run — while the cluster waited for bootstrap all night and
// `talos health` hung on etcd in Preparing (INV-0001 deviation 13).
func (b *Bootstrapper) bootstrapped(ctx context.Context) (bool, error) {
	timeout := b.ProbeTimeout
	if timeout == 0 {
		timeout = defaultProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	members, err := b.Client.EtcdMemberList(ctx)
	if err != nil {
		b.logger().Debug("etcd has no member list yet, bootstrap pending", "err", err)
		return false, nil
	}
	return len(members) > 0, nil
}

func (b *Bootstrapper) applyTalosconfig(context.Context) error {
	cfg, err := Talosconfig(b.Bundle, b.Cluster)
	if err != nil {
		return err
	}
	data, err := cfg.Bytes()
	if err != nil {
		return fmt.Errorf("encode talosconfig: %w", err)
	}
	return b.writeCredential(talosconfigName, data)
}

// applyBootstrap issues the one-time etcd bootstrap. "Already
// bootstrapped" is success (DESIGN-0001): Talos answers the second
// bootstrap with FailedPrecondition ("etcd data directory is not
// empty"), which is the cluster saying the step's work is done.
func (b *Bootstrapper) applyBootstrap(ctx context.Context) error {
	b.logger().Info("bootstrapping etcd", "endpoint", b.Cluster.Talos.Endpoint)
	err := b.Client.Bootstrap(ctx)
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok &&
		(s.Code() == codes.FailedPrecondition || s.Code() == codes.AlreadyExists) {
		b.logger().Info("etcd already bootstrapped", "detail", s.Message())
		return nil
	}
	return fmt.Errorf("etcd bootstrap: %w", err)
}

func (b *Bootstrapper) applyKubeconfig(ctx context.Context) error {
	data, err := b.Client.Kubeconfig(ctx)
	if err != nil {
		return fmt.Errorf("fetch kubeconfig: %w", err)
	}
	return b.writeCredential(kubeconfigName, data)
}

func (b *Bootstrapper) writeCredential(name string, data []byte) error {
	if err := os.MkdirAll(b.OutDir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", b.OutDir, err)
	}
	path := filepath.Join(b.OutDir, name)
	if err := os.WriteFile(path, data, credentialMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	b.logger().Info("credential written", "path", path)
	return nil
}
