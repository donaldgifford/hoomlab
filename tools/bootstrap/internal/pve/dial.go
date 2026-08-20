package pve

import (
	"context"
	"fmt"
	"time"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/api"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/cluster"
)

// dialTimeout bounds every request of a dialed session; convergence
// waits poll with fresh dials rather than waiting on one long call.
const dialTimeout = 30 * time.Second

// Dialer opens a cluster API session to one node's endpoint — the
// test seam. Production dials a fresh SDK client per call: cheap, and
// immune to the daemon restarts and ticket churn cluster formation
// causes mid-stage.
type Dialer func(ctx context.Context, endpoint string, creds api.Credentials) (cluster.API, error)

// NewDialer returns the production Dialer. TLS verification is
// skipped deliberately: pre-formation nodes serve self-signed
// certificates and the certificates churn again during joins; proper
// certificates are exactly what Stage 2 (pve certs) installs
// afterwards.
func NewDialer() Dialer {
	return func(ctx context.Context, endpoint string, creds api.Credentials) (cluster.API, error) {
		c, err := proxmox.NewClient(ctx, endpoint, creds,
			proxmox.WithInsecureSkipVerify(true),
			proxmox.WithRequestTimeout(dialTimeout))
		if err != nil {
			return nil, err
		}
		return c.Cluster(), nil
	}
}

// NewClient dials the cluster's primary node with the config's token
// credentials — the session the post-formation stages run on. TLS
// verification is skipped for the same reason as NewDialer: until
// Stage 2 completes, the cluster serves self-signed certificates. No
// request timeout is set — certificate orders legitimately run for
// minutes, bounded by the command context instead.
func NewClient(ctx context.Context, cfg *config.Cluster) (*proxmox.Client, error) {
	primary, ok := cfg.PrimaryNode()
	if !ok {
		return nil, fmt.Errorf("cluster %s: no primary pve node", cfg.Name)
	}
	c, err := proxmox.NewClient(ctx, primary.Endpoint,
		api.TokenCredentials(cfg.PVE.TokenID, cfg.PVE.TokenSecret),
		proxmox.WithInsecureSkipVerify(true))
	if err != nil {
		return nil, fmt.Errorf("dial %s (%s): %w", primary.Name, primary.Endpoint, err)
	}
	return c, nil
}
