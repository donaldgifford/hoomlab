package config

// The gohcl schema for the bootstrap config file (DESIGN-0001 Data
// Model). One file describes one cluster: the PVE nodes it is formed
// from, the ACME certificate setup, and the Talos cluster that runs on
// top. Every VM is declared explicitly — MAC included — so the config
// is the single source of identity for both the PVE API calls and the
// emitted booty artifacts (DESIGN-0001 OQ-5).
//
// Secrets never appear as values: secret-bearing attributes carry
// env("HOOMLAB_…") references resolved at load time (DESIGN-0001
// OQ-4), so the same file serves the CLI now and the Hoomlab service
// later.

// Root is the top of the config file: exactly one cluster block.
// The slice shape is what gohcl decodes; Load enforces the count.
type Root struct {
	Clusters []Cluster `hcl:"cluster,block"`
}

// Cluster is one labeled cluster block — the unit the bootstrap CLI
// operates on and the Hoomlab service later takes ownership of.
type Cluster struct {
	Name  string `hcl:"name,label"`
	PVE   PVE    `hcl:"pve,block"`
	ACME  ACME   `hcl:"acme,block"`
	Talos Talos  `hcl:"talos,block"`
}

// PrimaryNode returns the PVE node declared primary = true. Validated
// clusters always have exactly one; ok is false on an unvalidated
// cluster without one.
func (c *Cluster) PrimaryNode() (node PVENode, ok bool) {
	for _, n := range c.PVE.Nodes {
		if n.Primary {
			return n, true
		}
	}
	return PVENode{}, false
}

// PVE describes the Proxmox side: API credentials (env() references)
// and the physical nodes the cluster is formed from (Stage 1).
type PVE struct {
	TokenID      string    `hcl:"token_id"`
	TokenSecret  string    `hcl:"token_secret"`
	RootPassword string    `hcl:"root_password"`
	Nodes        []PVENode `hcl:"node,block"`
}

// PVENode is one Proxmox node. Exactly one node sets primary = true;
// the cluster is created there and the rest join it. Address, when
// set, becomes the corosync link0 for the join.
type PVENode struct {
	Name     string `hcl:"name,label"`
	Endpoint string `hcl:"endpoint"`
	Address  string `hcl:"address,optional"`
	Primary  bool   `hcl:"primary,optional"`
}

// ACME configures Stage 2: account registration, the DNS-01 plugin,
// and per-node certificates for <node>.<domain>. Token is an env()
// reference to the DNS provider API token. Directory optionally names
// the CA directory URL (e.g. Let's Encrypt staging while drilling, so
// failed orders don't burn production rate limits); empty means the
// CA default (Let's Encrypt production).
type ACME struct {
	Email     string `hcl:"email"`
	Domain    string `hcl:"domain"`
	DNS       string `hcl:"dns"`
	Token     string `hcl:"token"`
	Directory string `hcl:"directory,optional"`
}

// Talos describes the Kubernetes cluster built in Stages 3–5: the
// Talos release, the cluster endpoint, the booty server the VMs boot
// from, and one node block per VM.
type Talos struct {
	Version string `hcl:"version"`
	// Endpoint is the cluster endpoint (VIP or first control plane),
	// e.g. "https://10.0.20.10:6443".
	Endpoint string `hcl:"endpoint"`
	// KubernetesVersion pins the Kubernetes version the generated
	// machineconfigs install. Empty means the default of the machinery
	// release this CLI was built against.
	KubernetesVersion string `hcl:"kubernetes_version,optional"`
	// SchematicID selects a Talos Image Factory schematic for the
	// downloaded boot assets and the installer image. Empty means the
	// vanilla no-extensions schematic (IMPL-0001 OQ-1).
	SchematicID string      `hcl:"schematic_id,optional"`
	Booty       Booty       `hcl:"booty,block"`
	Nodes       []TalosNode `hcl:"node,block"`
}

// Booty locates the operator-run booty container serving the PXE
// chain. Version pins the container image in the emitted booty-run.sh;
// empty means the release this CLI was tested against (IMPL-0001
// OQ-4).
type Booty struct {
	URL     string `hcl:"url"`
	Version string `hcl:"version,optional"`
}

// Role is a Talos node's cluster role.
type Role string

// The two valid Role values.
const (
	RoleControlPlane Role = "controlplane"
	RoleWorker       Role = "worker"
)

// TalosNode is one VM, declared in full: identity (VMID, MAC),
// placement (PVENode), and shape. The MAC pinned here lands both on
// the VM NIC and in the emitted booty group selector, so the PXE
// identity binding agrees by construction.
type TalosNode struct {
	Name    string `hcl:"name,label"`
	Role    Role   `hcl:"role"`
	PVENode string `hcl:"pve_node"`
	VMID    int    `hcl:"vmid"`
	MAC     string `hcl:"mac"`
	Cores   int    `hcl:"cores"`
	Memory  int    `hcl:"memory"`
	DiskGB  int    `hcl:"disk_gb"`
	Storage string `hcl:"storage"`
	Bridge  string `hcl:"bridge"`
	// VLAN tags net0 on the PVE side of the bridge (IMPL-0002 OQ-5:
	// a trunk with no native VLAN drops untagged frames, so the tag
	// is what puts the VM on its network). PVE strips the tag before
	// the guest, so the firmware's PXE stack still sees plain
	// Ethernet. Zero means untagged.
	VLAN int `hcl:"vlan,optional"`
}
