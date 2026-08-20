package pve

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"time"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/api"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/cluster"
)

// Formation cadence, straight from pvelab: joins are serialized
// (corosync membership changes must not race) and each converges in
// two bounded waits — the node appears in the primary's corosync
// nodelist, then the cluster reports quorate with every member so far
// online. The quorum gate exists because config presence precedes
// runtime health: a join fired while pmxcfs is read-only is accepted
// by the joining node yet fails server-side.
const (
	defaultPollInterval  = 5 * time.Second
	defaultJoinCeiling   = 3 * time.Minute
	defaultQuorumCeiling = 3 * time.Minute
)

// Former builds the Stage 1 step list: create the cluster on the
// primary node, join the remaining nodes serially, verify quorum.
// Cluster must have passed config validation (exactly one primary).
type Former struct {
	Cluster *config.Cluster
	Dial    Dialer

	// Wait tuning; zero values mean the pvelab-derived defaults.
	PollInterval  time.Duration
	JoinCeiling   time.Duration
	QuorumCeiling time.Duration

	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

// Steps returns the formation steps in apply order.
//
// Credential split (DESIGN-0001 secrets table): every primary-node
// read and the cluster create use the API token; the join request is
// dialed against the JOINING node with root@pam password credentials,
// because joining wipes that node's local pmxcfs config — API tokens
// do not survive it, root@pam does.
func (f *Former) Steps() ([]steps.Step, error) {
	primary, ok := f.Cluster.PrimaryNode()
	if !ok {
		return nil, fmt.Errorf("cluster %s: no primary pve node", f.Cluster.Name)
	}

	list := []steps.Step{{
		Name:  "create-cluster",
		Check: f.memberCheck(primary, primary.Name),
		Apply: f.applyCreate(primary),
	}}
	for _, n := range f.Cluster.PVE.Nodes {
		if n.Primary {
			continue
		}
		list = append(list, steps.Step{
			Name:  "join-" + n.Name,
			Check: f.memberCheck(primary, n.Name),
			Apply: f.applyJoin(primary, n),
		})
	}
	list = append(list, steps.Step{
		Name:  "cluster-quorate",
		Check: f.quorumCheck(primary),
		Apply: f.applyQuorumWait(primary),
	})
	return list, nil
}

// memberCheck reports whether member appears in the primary's corosync
// nodelist. Read failures count as "not done" rather than fatal: the
// reads only fail while a node is unformed or mid-restart, and the
// step's Apply carries its own bounded convergence waits — a genuine
// problem (bad credentials, unreachable node) surfaces there with a
// real error.
func (f *Former) memberCheck(primary config.PVENode, member string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		present, err := f.memberPresent(ctx, primary, member)
		if err != nil {
			f.log().Debug("membership check failed, treating as pending",
				"member", member, "err", err)
			return false, nil
		}
		return present, nil
	}
}

// applyCreate forms the cluster on the primary node and waits for the
// primary to appear in its own corosync nodelist. The write is
// fire-and-poll per the SDK contract — formation restarts pmxcfs
// underneath the call, so convergence is the poll, not the response.
func (f *Former) applyCreate(primary config.PVENode) func(context.Context) error {
	return func(ctx context.Context) error {
		svc, err := f.dialPrimary(ctx, primary)
		if err != nil {
			return err
		}
		spec := &cluster.ClusterCreateSpec{Name: f.Cluster.Name}
		if primary.Address != "" {
			spec.Extra = map[string]string{"link0": primary.Address}
		}
		if err := svc.CreateCluster(ctx, spec); err != nil {
			return fmt.Errorf("create cluster on %s: %w", primary.Name, err)
		}
		f.log().Info("cluster created", "name", f.Cluster.Name, "node", primary.Name)
		return f.waitForMember(ctx, primary, primary.Name)
	}
}

// applyJoin joins one node: fingerprint from the primary's join-info,
// the join issued on the joining node with root@pam credentials, then
// the two convergence waits. A join request error is only logged —
// the join restarts the joining node's API daemons mid-call and the
// connection drop is the expected shape; a genuinely rejected join
// never converges and surfaces as the membership timeout.
func (f *Former) applyJoin(primary, node config.PVENode) func(context.Context) error {
	return func(ctx context.Context) error {
		svc, err := f.dialPrimary(ctx, primary)
		if err != nil {
			return err
		}
		info, err := svc.JoinInfo(ctx)
		if err != nil {
			return fmt.Errorf("join-info from %s: %w", primary.Name, err)
		}
		fingerprint := info.Fingerprint()
		if fingerprint == "" {
			return fmt.Errorf("join-info from %s carries no certificate fingerprint", primary.Name)
		}

		contact, err := contactAddress(primary)
		if err != nil {
			return err
		}
		joinSvc, err := f.Dial(ctx, node.Endpoint,
			api.UserCredentials("root@pam", f.Cluster.PVE.RootPassword, ""))
		if err != nil {
			return fmt.Errorf("dial joining node %s: %w", node.Name, err)
		}
		spec := &cluster.JoinSpec{
			Hostname:    contact,
			Password:    f.Cluster.PVE.RootPassword,
			Fingerprint: fingerprint,
		}
		if node.Address != "" {
			spec.Extra = map[string]string{"link0": node.Address}
		}
		if err := joinSvc.JoinCluster(ctx, spec); err != nil {
			f.log().Warn("join request errored — relying on membership convergence",
				"node", node.Name, "err", err)
		}
		if err := f.waitForMember(ctx, primary, node.Name); err != nil {
			return fmt.Errorf("node %s never appeared in the cluster membership: %w", node.Name, err)
		}
		f.log().Info("node joined", "node", node.Name)

		// Gate on quorum with every member joined so far before the
		// next join touches corosync.
		members, err := f.listMembers(ctx, primary)
		if err != nil {
			return fmt.Errorf("count members after joining %s: %w", node.Name, err)
		}
		return f.waitForQuorum(ctx, primary, len(members))
	}
}

// quorumCheck reports whether the cluster is quorate under the
// configured name with every configured node online. Like
// memberCheck, read failures count as "not done".
func (f *Former) quorumCheck(primary config.PVENode) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		ok, err := f.quorate(ctx, primary, len(f.Cluster.PVE.Nodes))
		if err != nil {
			f.log().Debug("quorum check failed, treating as pending", "err", err)
			return false, nil
		}
		return ok, nil
	}
}

// applyQuorumWait blocks until the full cluster is quorate — the
// re-run health verification when every join already checks as done.
func (f *Former) applyQuorumWait(primary config.PVENode) func(context.Context) error {
	return func(ctx context.Context) error {
		return f.waitForQuorum(ctx, primary, len(f.Cluster.PVE.Nodes))
	}
}

// waitForMember polls the primary's corosync nodelist until member
// appears. Poll errors count as "not yet" — the polled node's API may
// itself be restarting.
func (f *Former) waitForMember(ctx context.Context, primary config.PVENode, member string) error {
	ceiling := f.joinCeiling()
	pollCtx, cancel := context.WithTimeout(ctx, ceiling)
	defer cancel()
	for {
		present, err := f.memberPresent(pollCtx, primary, member)
		if err == nil && present {
			return nil
		}
		f.log().Debug("membership not converged yet", "member", member, "err", err)
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("member %s not in the corosync nodelist at %s within %s",
				member, primary.Name, ceiling)
		case <-time.After(f.interval()):
		}
	}
}

// waitForQuorum polls cluster status until it reports quorate under
// the configured name with want members online.
func (f *Former) waitForQuorum(ctx context.Context, primary config.PVENode, want int) error {
	ceiling := f.quorumCeiling()
	pollCtx, cancel := context.WithTimeout(ctx, ceiling)
	defer cancel()
	for {
		ok, err := f.quorate(pollCtx, primary, want)
		if err == nil && ok {
			f.log().Info("cluster quorate", "members", want)
			return nil
		}
		f.log().Debug("quorum not reached yet", "want", want, "err", err)
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("cluster %s not quorate with %d nodes online within %s",
				f.Cluster.Name, want, ceiling)
		case <-time.After(f.interval()):
		}
	}
}

func (f *Former) memberPresent(ctx context.Context, primary config.PVENode, member string) (bool, error) {
	svc, err := f.dialPrimary(ctx, primary)
	if err != nil {
		return false, err
	}
	members, err := svc.ListConfigNodes(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(members, func(m cluster.ConfigNode) bool {
		return m.NodeName() == member
	}), nil
}

func (f *Former) listMembers(ctx context.Context, primary config.PVENode) ([]cluster.ConfigNode, error) {
	svc, err := f.dialPrimary(ctx, primary)
	if err != nil {
		return nil, err
	}
	return svc.ListConfigNodes(ctx)
}

// quorate reports whether cluster status shows a quorate cluster with
// the configured name, at least want members configured, and at least
// want node entries online.
func (f *Former) quorate(ctx context.Context, primary config.PVENode, want int) (bool, error) {
	svc, err := f.dialPrimary(ctx, primary)
	if err != nil {
		return false, err
	}
	entries, err := svc.GetStatus(ctx)
	if err != nil {
		return false, err
	}
	var clusterOK bool
	var online int
	for i := range entries {
		switch entries[i].Type {
		case "cluster":
			clusterOK = entries[i].Name == f.Cluster.Name &&
				entries[i].Quorate.Bool() &&
				entries[i].Nodes >= want
		case "node":
			if entries[i].Online.Bool() {
				online++
			}
		}
	}
	return clusterOK && online >= want, nil
}

func (f *Former) dialPrimary(ctx context.Context, primary config.PVENode) (cluster.API, error) {
	svc, err := f.Dial(ctx, primary.Endpoint,
		api.TokenCredentials(f.Cluster.PVE.TokenID, f.Cluster.PVE.TokenSecret))
	if err != nil {
		return nil, fmt.Errorf("dial %s (%s): %w", primary.Name, primary.Endpoint, err)
	}
	return svc, nil
}

// contactAddress is the address a joining node contacts the primary
// on: the corosync address when declared, otherwise the endpoint host.
func contactAddress(primary config.PVENode) (string, error) {
	if primary.Address != "" {
		return primary.Address, nil
	}
	u, err := url.Parse(primary.Endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q of %s: %w", primary.Endpoint, primary.Name, err)
	}
	return u.Hostname(), nil
}

func (f *Former) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

func (f *Former) interval() time.Duration {
	if f.PollInterval > 0 {
		return f.PollInterval
	}
	return defaultPollInterval
}

func (f *Former) joinCeiling() time.Duration {
	if f.JoinCeiling > 0 {
		return f.JoinCeiling
	}
	return defaultJoinCeiling
}

func (f *Former) quorumCeiling() time.Duration {
	if f.QuorumCeiling > 0 {
		return f.QuorumCeiling
	}
	return defaultQuorumCeiling
}
