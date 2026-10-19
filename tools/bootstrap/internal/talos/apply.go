package talos

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
)

// NodeAddresses resolves every config node to the address the CLI
// dials it at (IMPL-0004 OQ-7 a). The config declares no per-node IPs
// — identity is MAC-based and the primary plane is DHCP — so the
// addresses come from Talos discovery, listed through the cluster
// endpoint and matched by hostname. A config node discovery doesn't
// know is an error naming it: applying to "the other nodes" silently
// would leave the fleet half-converged.
//
// A member reports every address it holds, including static
// secondary planes (the storage plane) that are unroutable from the
// operator's workstation by design. Addresses inside any static
// interface's prefix are skipped, and IPv4 is preferred.
func NodeAddresses(ctx context.Context, c Client, cluster *config.Cluster) (map[string]string, error) {
	members, err := c.Members(ctx)
	if err != nil {
		return nil, err
	}
	byHost := make(map[string]Member, len(members))
	for _, m := range members {
		byHost[m.Hostname] = m
	}
	skip := staticPrefixes(cluster)

	addrs := make(map[string]string, len(cluster.Talos.Nodes))
	var missing []string
	for i := range cluster.Talos.Nodes {
		name := cluster.Talos.Nodes[i].Name
		m, ok := byHost[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		addr, ok := dialAddress(m.Addresses, skip)
		if !ok {
			return nil, fmt.Errorf("talos node %q: discovery reports no dialable address (have %s)",
				name, strings.Join(m.Addresses, ", "))
		}
		addrs[name] = addr
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("talos nodes not found among the cluster members discovery reports: %s",
			strings.Join(missing, ", "))
	}
	return addrs, nil
}

// staticPrefixes collects the prefix of every static interface in the
// config — the secondary planes the workstation can't reach.
func staticPrefixes(cluster *config.Cluster) []netip.Prefix {
	var prefixes []netip.Prefix
	for i := range cluster.Talos.Nodes {
		for _, nic := range cluster.Talos.Nodes[i].ResolvedInterfaces() {
			if nic.DHCP {
				continue
			}
			p, err := netip.ParsePrefix(nic.Address)
			if err != nil {
				continue // validated at load; nothing to skip on
			}
			if p = p.Masked(); !slices.Contains(prefixes, p) {
				prefixes = append(prefixes, p)
			}
		}
	}
	return prefixes
}

// dialAddress picks the address to dial from a member's list: the
// first IPv4 address outside the skipped prefixes, else the first
// other address outside them.
func dialAddress(addresses []string, skip []netip.Prefix) (string, bool) {
	var fallback string
	for _, a := range addresses {
		ip, err := netip.ParseAddr(a)
		if err != nil || slices.ContainsFunc(skip, func(p netip.Prefix) bool { return p.Contains(ip) }) {
			continue
		}
		if ip.Is4() {
			return a, true
		}
		if fallback == "" {
			fallback = a
		}
	}
	return fallback, fallback != ""
}

// RenderedNode is one node's complete machine config as booty would
// serve it, and the installer image its class renders — the input the
// apply stage compares against the live node.
type RenderedNode struct {
	Name         string
	Config       []byte
	InstallImage string
}

// Applier builds the day-2 apply stage: for each node, compare the
// config booty would serve it against the one it is running, and
// apply the rendered config in NO_REBOOT mode where they differ. The
// render is the source of truth, so a node converged through the
// stage is one a fresh PXE boot would reproduce.
type Applier struct {
	Cluster *config.Cluster
	// Client is the Talos API session; each call targets one node.
	Client Client
	// Nodes are the rendered configs to converge, in order.
	Nodes []RenderedNode
	// Out receives the per-node diffs. Nil discards them.
	Out io.Writer
}

// Steps resolves every node's address and returns one apply-<node>
// step per rendered node. Resolution happens up front so a node
// discovery doesn't know fails the stage before anything is applied.
func (a *Applier) Steps(ctx context.Context) ([]steps.Step, error) {
	addrs, err := NodeAddresses(ctx, a.Client, a.Cluster)
	if err != nil {
		return nil, err
	}
	stage := make([]steps.Step, 0, len(a.Nodes))
	for _, n := range a.Nodes {
		addr, ok := addrs[n.Name]
		if !ok {
			return nil, fmt.Errorf("talos node %q is not in the config", n.Name)
		}
		stage = append(stage, steps.Step{
			Name:  "apply-" + n.Name,
			Check: a.check(n, addr),
			Apply: a.apply(n, addr),
		})
	}
	return stage, nil
}

// check reports whether the node already runs its rendered config,
// printing the redacted diff when it doesn't.
func (a *Applier) check(n RenderedNode, addr string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		live, err := a.Client.MachineConfig(ctx, addr)
		if err != nil {
			return false, fmt.Errorf("read %s machine config: %w", n.Name, err)
		}
		equal, err := configsEqual(live, n.Config)
		if err != nil || equal {
			return equal, err
		}
		diff, err := configDiff(live, n.Config)
		if err != nil {
			return false, err
		}
		if a.Out != nil {
			if _, err := fmt.Fprintf(a.Out, "--- %s (live)\n+++ %s (rendered)\n%s", n.Name, n.Name, diff); err != nil {
				return false, fmt.Errorf("write diff: %w", err)
			}
		}
		return false, nil
	}
}

// apply sends the whole rendered config in NO_REBOOT mode. A change
// Talos can only take with a reboot is refused; that refusal comes
// back naming the node and the change, and the reboot goes through
// cordon → drain (INV-0005) — a bare talosctl reboot would take the
// node's workloads down unannounced.
func (a *Applier) apply(n RenderedNode, addr string) func(context.Context) error {
	return func(ctx context.Context) error {
		err := a.Client.ApplyNoReboot(ctx, addr, n.Config)
		if err == nil {
			return nil
		}
		if !rebootRequired(err) {
			return fmt.Errorf("apply %s machine config: %w", n.Name, err)
		}
		return fmt.Errorf("talos node %s refused the change without a reboot: %w\n"+
			"take the reboot through cordon → drain (INV-0005), never a bare talosctl reboot:\n"+
			"  kubectl cordon %s && kubectl drain %s --ignore-daemonsets --delete-emptydir-data",
			n.Name, err, n.Name, n.Name)
	}
}

// rebootRequired reports whether an apply error is Talos refusing a
// NO_REBOOT apply because the change needs a reboot.
func rebootRequired(err error) bool {
	return status.Code(err) == codes.FailedPrecondition || strings.Contains(err.Error(), "reboot")
}
