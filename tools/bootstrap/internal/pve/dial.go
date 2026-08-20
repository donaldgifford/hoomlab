package pve

import (
	"context"
	"time"

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
