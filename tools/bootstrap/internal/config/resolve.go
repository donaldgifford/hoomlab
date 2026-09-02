package config

// The resolution model (DESIGN-0004): HCL decodes into raw types
// where the mode attrs are optional, one resolver turns each raw
// network_interface into a resolved interface with every fact
// explicit, and everything downstream — validation's cross-node
// rules, VMSpec, emit — consumes only the resolved form. Consumers
// never know whether a fact arrived inline or via a network plane,
// which is what keeps a future plane capability a one-line resolver
// change instead of a refactor.

import (
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

// slotPattern is the interface label shape: the PVE NIC slot the
// block programs. Anything else (eth0, ens18) is a guest-side name
// that PVE would not recognize.
var slotPattern = regexp.MustCompile(`^net\d+$`)

// ResolvedInterface is one NIC with no optionality left. Validation,
// VMSpec, and emit consume only this form.
type ResolvedInterface struct {
	Slot    string // net0, net1, …
	MAC     string // canonical NormalizeMAC form
	Bridge  string
	VLAN    int // 0 = untagged
	DHCP    bool
	Primary bool
	Address string // empty iff DHCP
	CIDR    string // empty when ungoverned
	MTU     int    // 0 = no override rendered
}

// ResolvedInterfaces returns the node's interfaces in slot order,
// every fact explicit. Empty until Cluster.ResolveInterfaces has run;
// Load always runs it. The slice is a copy — the resolver stays the
// only write path to the resolved state.
func (n *TalosNode) ResolvedInterfaces() []ResolvedInterface {
	return slices.Clone(n.resolved)
}

// PrimaryInterface returns the node's boot NIC — the one interface on
// the primary plane. Resolved nodes always have exactly one; ok is
// false on an unresolved node.
func (n *TalosNode) PrimaryInterface() (ResolvedInterface, bool) {
	for _, r := range n.resolved {
		if r.Primary {
			return r, true
		}
	}
	return ResolvedInterface{}, false
}

// ResolveInterfaces resolves every node's network_interface blocks
// against the declared network planes and stores the results,
// readable via TalosNode.ResolvedInterfaces. ValidateAndNormalize
// calls it as part of loading; tests that build clusters as struct
// literals call it directly for the same effect. A node stores its
// resolved interfaces only when every one of them resolved cleanly —
// partial resolution never leaks downstream.
func (c *Cluster) ResolveInterfaces() hcl.Diagnostics {
	planes := make(map[string]*Network, len(c.Talos.Networks))
	for i := range c.Talos.Networks {
		planes[c.Talos.Networks[i].Name] = &c.Talos.Networks[i]
	}
	diags := make(hcl.Diagnostics, 0, len(c.Talos.Nodes))
	for i := range c.Talos.Nodes {
		diags = append(diags, resolveNode(planes, &c.Talos.Nodes[i])...)
	}
	return diags
}

// resolveNode resolves one node's interfaces: label shape and
// per-node label uniqueness, each interface's form and mode, and the
// exactly-one-primary rule — checked only once every interface
// resolved, so a node reports its structural mistakes before its
// aggregate ones.
func resolveNode(planes map[string]*Network, n *TalosNode) hcl.Diagnostics {
	if len(n.Interfaces) == 0 {
		return hcl.Diagnostics{errf("Missing network interface",
			"talos node %q declares no network_interface block; a VM needs at least its boot NIC.",
			n.Name)}
	}

	var diags hcl.Diagnostics
	resolved := make([]ResolvedInterface, 0, len(n.Interfaces))
	labels := make(map[string]struct{}, len(n.Interfaces))
	var primaries []string
	for i := range n.Interfaces {
		iface := &n.Interfaces[i]
		if !slotPattern.MatchString(iface.Name) {
			diags = append(diags, errf("Invalid network_interface label",
				"talos node %q: network_interface %q must be the PVE slot form net<N> (net0, net1, …).",
				n.Name, iface.Name))
		}
		if _, dup := labels[iface.Name]; dup {
			diags = append(diags, errf("Duplicate network_interface label",
				"talos node %q declares network_interface %q more than once.",
				n.Name, iface.Name))
		}
		labels[iface.Name] = struct{}{}

		r, ifaceDiags := resolveInterface(planes, n, iface)
		diags = append(diags, ifaceDiags...)
		if ifaceDiags.HasErrors() {
			continue
		}
		if r.Primary {
			primaries = append(primaries, r.Slot)
		}
		resolved = append(resolved, r)
	}

	if diags.HasErrors() {
		return diags
	}
	if len(primaries) != 1 {
		return append(diags, errf(
			"Exactly one primary interface required",
			"talos node %q resolves %d primary interfaces (%v); exactly one interface is the boot path — put it on the primary plane, or set primary = true inline.",
			n.Name,
			len(primaries),
			primaries,
		))
	}
	sort.Slice(resolved, func(i, j int) bool {
		return slotIndex(resolved[i].Slot) < slotIndex(resolved[j].Slot)
	})
	n.resolved = resolved
	return diags
}

// slotIndex extracts the numeric slot from a label the slotPattern
// already accepted, so downstream renders in slot order rather than
// declaration order.
func slotIndex(slot string) int {
	i, err := strconv.Atoi(strings.TrimPrefix(slot, "net"))
	if err != nil {
		// Unreachable: only slotPattern-accepted labels arrive here.
		return -1
	}
	return i
}

// resolveInterface turns one raw interface into its resolved form.
// The MAC is checked and canonicalized first — it is independent of
// which form the block takes — then the form resolves (the XOR rule),
// then the mode's address rules run on the resolved facts.
func resolveInterface(
	planes map[string]*Network, n *TalosNode, iface *NetworkInterface,
) (ResolvedInterface, hcl.Diagnostics) {
	var diags hcl.Diagnostics
	r := ResolvedInterface{Slot: iface.Name, Bridge: iface.Bridge, Address: iface.Address}

	mac, err := NormalizeMAC(iface.MAC)
	if err != nil {
		diags = append(diags, errf("Invalid network_interface mac",
			"talos node %q network_interface %q: %v.", n.Name, iface.Name, err))
	} else {
		// Raw and resolved agree on the canonical form, so a validated
		// cluster carries canonical MACs in both layers.
		iface.MAC = mac
		r.MAC = mac
	}

	modeDiags := resolveMode(planes, n, iface, &r)
	diags = append(diags, modeDiags...)
	if modeDiags.HasErrors() {
		return r, diags
	}
	diags = append(diags, validateAddress(n, iface.Name, &r)...)
	return r, diags
}

// resolveMode fills the mode facts from exactly one source. Referenced
// form: the plane's facts, inherited whole — any plane-owned attr set
// beside the reference is a conflict, never an override. Inline form:
// dhcp is the one required fact; vlan, primary, cidr, and mtu default
// to untagged, non-primary, ungoverned, and no-override.
func resolveMode(
	planes map[string]*Network, n *TalosNode, iface *NetworkInterface, r *ResolvedInterface,
) hcl.Diagnostics {
	inline := inlineModeAttrs(iface)

	switch {
	case iface.Network != "" && len(inline) > 0:
		return hcl.Diagnostics{
			errf(
				"Conflicting network_interface forms",
				"talos node %q network_interface %q: references network %q and sets plane-owned %s; a reference inherits every plane fact — there is no override. Drop the reference or the attributes.",
				n.Name,
				iface.Name,
				iface.Network,
				strings.Join(inline, ", "),
			),
		}

	case iface.Network != "":
		plane, ok := planes[iface.Network]
		if !ok {
			return hcl.Diagnostics{errf("Unknown network reference",
				"talos node %q network_interface %q: network %q names no declared network block.",
				n.Name, iface.Name, iface.Network)}
		}
		r.VLAN, r.DHCP, r.Primary = plane.VLAN, plane.DHCP, plane.Primary
		r.CIDR, r.MTU = plane.CIDR, plane.MTU
		return nil

	case iface.DHCP == nil:
		return hcl.Diagnostics{
			errf(
				"Incomplete network_interface",
				"talos node %q network_interface %q: sets neither network (a plane reference) nor dhcp (the inline mode); every interface states its mode one way or the other.",
				n.Name,
				iface.Name,
			),
		}
	}

	return resolveInline(n, iface, r)
}

// inlineModeAttrs names the plane-owned attrs the block sets inline —
// what the XOR conflict diagnostic lists, and what decides the form.
func inlineModeAttrs(iface *NetworkInterface) []string {
	var inline []string
	if iface.VLAN != nil {
		inline = append(inline, "vlan")
	}
	if iface.DHCP != nil {
		inline = append(inline, "dhcp")
	}
	if iface.Primary != nil {
		inline = append(inline, "primary")
	}
	if iface.CIDR != "" {
		inline = append(inline, "cidr")
	}
	if iface.MTU != nil {
		inline = append(inline, "mtu")
	}
	return inline
}

// resolveInline fills the mode facts from the block's own attrs.
// Plane-sourced values are validated on the plane (validateNetworks);
// inline-sourced values are validated here — one error per mistake,
// whichever way the fact arrived.
func resolveInline(n *TalosNode, iface *NetworkInterface, r *ResolvedInterface) hcl.Diagnostics {
	var diags hcl.Diagnostics
	r.DHCP = *iface.DHCP
	if iface.VLAN != nil {
		r.VLAN = *iface.VLAN
	}
	if iface.Primary != nil {
		r.Primary = *iface.Primary
	}
	r.CIDR = iface.CIDR
	if iface.MTU != nil {
		r.MTU = *iface.MTU
	}
	if r.VLAN != 0 && (r.VLAN < vlanMin || r.VLAN > vlanMax) {
		diags = append(diags, errf("Invalid network_interface vlan",
			"talos node %q network_interface %q: vlan %d is outside the 802.1Q range %d-%d (omit the attribute for untagged).",
			n.Name, iface.Name, r.VLAN, vlanMin, vlanMax))
	}
	if r.MTU != 0 && (r.MTU < mtuMin || r.MTU > mtuMax) {
		diags = append(diags, errf("Invalid network_interface mtu",
			"talos node %q network_interface %q: mtu %d is outside virtio's %d-%d (omit the attribute for the fabric default).",
			n.Name, iface.Name, r.MTU, mtuMin, mtuMax))
	}
	if r.CIDR != "" {
		if _, err := netip.ParsePrefix(r.CIDR); err != nil {
			diags = append(diags, errf("Invalid network_interface cidr",
				"talos node %q network_interface %q: cidr %q is not CIDR form (network/prefix): %v.",
				n.Name, iface.Name, r.CIDR, err))
		}
	}
	return diags
}

// validateAddress enforces the mode's address rules on the resolved
// facts: dhcp forbids an address (the lease owns addressing), static
// requires one in CIDR form, contained in the governing cidr when one
// exists. A malformed plane cidr is the plane's own diagnostic, so
// containment silently skips it here — one error per mistake.
func validateAddress(n *TalosNode, slot string, r *ResolvedInterface) hcl.Diagnostics {
	if r.DHCP {
		if r.Address != "" {
			return hcl.Diagnostics{
				errf(
					"Address on a dhcp interface",
					"talos node %q network_interface %q: address %q is declared but the mode is dhcp — the lease owns addressing. Drop the address or make the mode static.",
					n.Name,
					slot,
					r.Address,
				),
			}
		}
		return nil
	}
	if r.Address == "" {
		return hcl.Diagnostics{errf("Missing static address",
			"talos node %q network_interface %q: a static (dhcp = false) interface requires address in CIDR form, e.g. \"192.0.2.51/24\".",
			n.Name, slot)}
	}
	addr, err := netip.ParsePrefix(r.Address)
	if err != nil {
		return hcl.Diagnostics{errf("Invalid network_interface address",
			"talos node %q network_interface %q: address %q must be CIDR form (address/prefix): %v.",
			n.Name, slot, r.Address, err)}
	}
	// A malformed governing cidr already has its own diagnostic (on
	// the plane or the inline attr), so containment only runs against
	// one that parses.
	if cidr, err := netip.ParsePrefix(r.CIDR); err == nil && r.CIDR != "" && !cidr.Contains(addr.Addr()) {
		return hcl.Diagnostics{errf("Address outside the governing cidr",
			"talos node %q network_interface %q: address %q is not inside cidr %q.",
			n.Name, slot, r.Address, r.CIDR)}
	}
	return nil
}
