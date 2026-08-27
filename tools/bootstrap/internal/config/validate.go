package config

import (
	"fmt"
	"net/url"

	"github.com/hashicorp/hcl/v2"
)

// dnsCloudflare is the one DNS-01 provider the certs stage supports
// (ADR-0001: Cloudflare is the blessed provider).
const dnsCloudflare = "cloudflare"

// pveVMIDMin is the lowest VMID Proxmox allows for user guests; IDs
// below 100 are reserved for internal use.
const pveVMIDMin = 100

// The valid 802.1Q VLAN ID range: 0 is priority-tagged-only and 4095
// is reserved, so a talos node's vlan must land inside this window
// (or be omitted entirely for untagged).
const (
	vlanMin = 1
	vlanMax = 4094
)

// ValidateAndNormalize runs the semantic checks gohcl decoding cannot
// express and returns every violation as a diagnostic naming the
// offending block and field. The "normalize" in the name is real: it
// rewrites each talos node MAC to the canonical NormalizeMAC form, so
// a validated cluster always carries canonical MACs. Positions are
// not available post-decode; these diagnostics carry summaries and
// details only.
func (c *Cluster) ValidateAndNormalize() hcl.Diagnostics {
	diags := c.validatePVE()
	diags = append(diags, c.validateACME()...)
	diags = append(diags, c.validateTalos()...)
	return diags
}

func (c *Cluster) validatePVE() hcl.Diagnostics {
	var diags hcl.Diagnostics

	var primaries []string
	for _, n := range c.PVE.Nodes {
		if n.Primary {
			primaries = append(primaries, n.Name)
		}
		if err := validateURL(n.Endpoint); err != nil {
			diags = append(diags, errf("Invalid pve node endpoint",
				"pve node %q: endpoint %q: %v.", n.Name, n.Endpoint, err))
		}
	}
	if len(primaries) != 1 {
		diags = append(diags, errf("Exactly one primary pve node required",
			"pve declares %d nodes with primary = true (%v); the cluster is created on exactly one.",
			len(primaries), primaries))
	}
	return diags
}

func (c *Cluster) validateACME() hcl.Diagnostics {
	var diags hcl.Diagnostics

	if c.ACME.DNS != dnsCloudflare {
		diags = append(diags, errf("Unsupported acme dns provider",
			"acme: dns %q: %q is the only supported provider (ADR-0001).",
			c.ACME.DNS, dnsCloudflare))
	}
	if c.ACME.Directory != "" {
		if err := validateURL(c.ACME.Directory); err != nil {
			diags = append(diags, errf("Invalid acme directory",
				"acme: directory %q: %v.", c.ACME.Directory, err))
		}
	}
	return diags
}

func (c *Cluster) validateTalos() hcl.Diagnostics {
	var diags hcl.Diagnostics

	if err := validateURL(c.Talos.Endpoint); err != nil {
		diags = append(diags, errf("Invalid talos endpoint",
			"talos: endpoint %q: %v.", c.Talos.Endpoint, err))
	}
	if err := validateURL(c.Talos.Booty.URL); err != nil {
		diags = append(diags, errf("Invalid booty url",
			"talos booty: url %q: %v.", c.Talos.Booty.URL, err))
	}

	pveNodes := make(map[string]struct{}, len(c.PVE.Nodes))
	for _, n := range c.PVE.Nodes {
		pveNodes[n.Name] = struct{}{}
	}

	seenMACs := make(map[string]string, len(c.Talos.Nodes))
	var controlPlanes int
	for i := range c.Talos.Nodes {
		n := &c.Talos.Nodes[i]

		switch n.Role {
		case RoleControlPlane:
			controlPlanes++
		case RoleWorker:
		default:
			diags = append(diags, errf("Invalid talos node role",
				"talos node %q: role %q must be %q or %q.",
				n.Name, n.Role, RoleControlPlane, RoleWorker))
		}

		if _, ok := pveNodes[n.PVENode]; !ok {
			diags = append(diags, errf("Unknown pve_node reference",
				"talos node %q: pve_node %q names no declared pve node.",
				n.Name, n.PVENode))
		}

		if n.VMID < pveVMIDMin {
			diags = append(diags, errf("Invalid talos node vmid",
				"talos node %q: vmid %d is below %d, the lowest ID Proxmox allows for guests.",
				n.Name, n.VMID, pveVMIDMin))
		}

		if n.VLAN != 0 && (n.VLAN < vlanMin || n.VLAN > vlanMax) {
			diags = append(diags, errf("Invalid talos node vlan",
				"talos node %q: vlan %d is outside the 802.1Q range %d-%d (omit the attribute for untagged).",
				n.Name, n.VLAN, vlanMin, vlanMax))
		}

		mac, err := NormalizeMAC(n.MAC)
		if err != nil {
			diags = append(diags, errf("Invalid talos node mac",
				"talos node %q: %v.", n.Name, err))
			continue
		}
		n.MAC = mac
		if first, dup := seenMACs[mac]; dup {
			diags = append(diags, errf("Duplicate talos node mac",
				"talos node %q: mac %q is already used by node %q.", n.Name, mac, first))
			continue
		}
		seenMACs[mac] = n.Name
	}
	if controlPlanes == 0 {
		diags = append(diags, errf("No controlplane node declared",
			"talos declares no node with role %q; a cluster needs at least one.",
			RoleControlPlane))
	}
	return diags
}

// validateURL checks that s is an absolute http(s) URL with a host —
// the shape every endpoint in the config must have.
func validateURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

// errf builds one error diagnostic from a summary and a formatted
// detail.
func errf(summary, format string, args ...any) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
	}
}
