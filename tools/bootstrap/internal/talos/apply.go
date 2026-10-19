package talos

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
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
