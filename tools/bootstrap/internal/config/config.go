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

// TalosName returns the Talos cluster's own name: the talos block's
// name attribute when set, else the cluster label. Everything on the
// Talos side of the boundary — machineconfig clusterName, the
// talosconfig context, the emitted booty catalog — carries this name;
// the PVE side always carries the label.
func (c *Cluster) TalosName() string {
	if c.Talos.Name != "" {
		return c.Talos.Name
	}
	return c.Name
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

// PVE describes the Proxmox side: API credentials (env() references),
// the physical nodes the cluster is formed from (Stage 1), and the
// cluster storage entries the VMs depend on (the pve storage stage).
type PVE struct {
	TokenID      string       `hcl:"token_id"`
	TokenSecret  string       `hcl:"token_secret"`
	RootPassword string       `hcl:"root_password"`
	Nodes        []PVENode    `hcl:"node,block"`
	Storage      []PVEStorage `hcl:"storage,block"`
}

// PVEStorage is one declared cluster storage entry, converged by the
// pve storage stage: created when missing, updated in place when a
// declared field drifted. Declared fields are the CLI's opinion —
// an empty list or false bool means "no opinion", so settings the
// block does not name are never touched on an existing entry (the
// stock local-zfs keeps its content types when only nodes is
// declared). Identity is fixed: type (and path, for dir storage)
// cannot be converged by update, so a mismatch on an existing entry
// is an error, never a delete-and-recreate — deletion could orphan
// VM disks. List-valued fields are sets on the PVE side; declaration
// order never matters.
type PVEStorage struct {
	Name    string   `hcl:"name,label"`
	Type    string   `hcl:"type"`             // "zfspool", "dir", ... (create-fixed)
	Pool    string   `hcl:"pool,optional"`    // zfspool dataset, e.g. "fast/vm"
	Path    string   `hcl:"path,optional"`    // dir backing path (create-fixed)
	Content []string `hcl:"content,optional"` // "images", "rootdir", "iso", ...
	Nodes   []string `hcl:"nodes,optional"`   // node restriction; empty = no opinion
	Sparse  bool     `hcl:"sparse,optional"`  // zfspool thin provisioning
	Disable bool     `hcl:"disable,optional"`
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
	// Name names the Talos cluster independently of the PVE cluster
	// (the block label). Empty inherits the label. The two layers need
	// separable names because pve form pins the label against the live
	// PVE cluster's name — renaming the shared name would read as
	// drift on the PVE side — and because a second Talos cluster on
	// the same PVE cluster needs a name of its own.
	Name    string `hcl:"name,optional"`
	Version string `hcl:"version"`
	// Endpoint is the cluster endpoint (VIP or first control plane),
	// e.g. "https://10.0.20.10:6443".
	Endpoint string `hcl:"endpoint"`
	// KubernetesVersion pins the Kubernetes version the generated
	// machineconfigs install. Empty means the default of the machinery
	// release this CLI was built against.
	KubernetesVersion string `hcl:"kubernetes_version,optional"`
	// SchematicID pins one Image Factory schematic for every node's
	// boot assets and installer image. Empty means the vanilla
	// no-extensions schematic (IMPL-0001 OQ-1). Mutually exclusive
	// with profile blocks, which derive schematics instead.
	SchematicID string `hcl:"schematic_id,optional"`
	// Cluster opts into the cluster-completion surface (DESIGN-0002):
	// CNI choice, Cilium delivery, and the completion knobs the
	// emitted machineconfigs then carry (topology node labels, kubelet
	// serving-certificate rotation). Absent means the legacy surface —
	// machinery defaults, flannel included.
	Cluster *TalosCluster `hcl:"cluster,block"`
	// Networks declares the named network planes node interfaces
	// reference (DESIGN-0004): the shared facts stated once and
	// inherited whole, never overridden.
	Networks []Network      `hcl:"network,block"`
	Profiles []TalosProfile `hcl:"profile,block"`
	Booty    Booty          `hcl:"booty,block"`
	Nodes    []TalosNode    `hcl:"node,block"`
}

// Network is a named network plane (DESIGN-0004): the facts every
// member interface shares, declared once. vlan omitted means untagged
// — the interface's bridge and its switch port's native VLAN own
// membership. dhcp is required: every plane states its addressing
// mode. primary marks the boot plane (at most one); its member
// interface is each node's PXE path and booty identity. cidr is
// required exactly when dhcp = false — static member addresses are
// validated against it. mtu, when set, renders into both the VM NIC
// and the machineconfig; omitted renders no override anywhere,
// leaving the fabric default in charge.
type Network struct {
	Name    string `hcl:"name,label"`
	VLAN    int    `hcl:"vlan,optional"`
	DHCP    bool   `hcl:"dhcp"`
	Primary bool   `hcl:"primary,optional"`
	CIDR    string `hcl:"cidr,optional"`
	MTU     int    `hcl:"mtu,optional"`
}

// The valid TalosCluster.CNI values. Empty defaults to flannel — the
// machinery default, made explicit here so "no opinion" and "flannel"
// mean the same thing on purpose.
const (
	CNICilium  = "cilium"
	CNIFlannel = "flannel"
	CNINone    = "none"
)

// TalosCluster selects the cluster network. With cni = "cilium" the
// emitted machineconfigs disable the built-in CNI and kube-proxy and
// deliver Cilium via manifest injection at bootstrap (DESIGN-0002
// Cilium delivery); the cilium block is then required. With "none"
// the operator brings their own CNI. Flannel needs no block of its
// own — the block's presence is what turns the completion knobs on.
type TalosCluster struct {
	CNI    string        `hcl:"cni,optional"`
	Cilium *CiliumConfig `hcl:"cilium,block"`
}

// CiliumConfig pins the Cilium delivery. Values names the
// operator-supplied Helm values file, resolved relative to the config
// file and validated at load time — YAML shape, the KubePrism
// invariant (k8sServiceHost localhost:7445), and kube-proxy
// replacement, because the emitted machineconfigs disable kube-proxy
// and a values file that doesn't replace it would strand the cluster.
type CiliumConfig struct {
	// Version is the Cilium release the install Job pins, e.g.
	// "v1.20.1".
	Version string `hcl:"version"`
	// Values is the path to the Helm values file, relative to the
	// config file. Load rewrites it absolute.
	Values string `hcl:"values"`
	// GatewayAPIVersion pins the Gateway API CRD set delivered via
	// extraManifests before Cilium starts, e.g. "v1.6.1".
	GatewayAPIVersion string `hcl:"gateway_api_version"`
	// CLIVersion pins the cilium-cli image the install Job runs.
	// Empty means the release this CLI was tested against.
	CLIVersion string `hcl:"cli_version,optional"`
}

// TalosProfile is a named, composable extension set (DESIGN-0002
// extensions model, adopted from the packer generation). Nodes
// reference profiles; emit flattens each node's profiles into an
// Image Factory schematic, so the extensions are baked into the boot
// and installer images — the only time that decision can be made.
type TalosProfile struct {
	Name       string   `hcl:"name,label"`
	Extensions []string `hcl:"extensions"`
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

// TalosNode is one VM, declared in full: identity, placement
// (PVENode), shape, and its NICs as network_interface blocks. The
// primary interface's MAC lands both on the VM NIC and in the emitted
// booty group selector, so the PXE identity binding agrees by
// construction.
type TalosNode struct {
	Name    string `hcl:"name,label"`
	Role    Role   `hcl:"role"`
	PVENode string `hcl:"pve_node"`
	VMID    int    `hcl:"vmid"`
	Cores   int    `hcl:"cores"`
	Memory  int    `hcl:"memory"`
	DiskGB  int    `hcl:"disk_gb"`
	Storage string `hcl:"storage"`
	// Profiles names the extension profiles baked into this node's
	// boot image. Empty means the vanilla no-extensions image.
	Profiles []string `hcl:"profiles,optional"`
	// Interfaces declares the node's NICs, one labeled block per PVE
	// slot (net0, net1, …). At least one; exactly one resolves
	// primary.
	Interfaces []NetworkInterface `hcl:"network_interface,block"`

	// resolved is the fully-explicit form of Interfaces, populated by
	// Cluster.ResolveInterfaces and read via ResolvedInterfaces —
	// unexported so the resolver is the only write path.
	resolved []ResolvedInterface
}

// NetworkInterface is the raw HCL form of one VM NIC — a labeled
// network_interface block inside a node (DESIGN-0004). The label is
// the PVE slot (net0, net1, …). Identity (mac, bridge) is always
// per-interface; the mode facts arrive exactly one of two ways:
// network references a plane and inherits its facts whole, or the
// inline attrs state them all here. Setting both is an error, never
// an override (the XOR rule). Optionality here is data — the pointer
// types distinguish absent from set, which is what the XOR check
// reads — so nothing outside the resolver reads these mode fields;
// consumers use the resolved form (TalosNode.ResolvedInterfaces).
type NetworkInterface struct {
	Name    string `hcl:"name,label"` // PVE slot: net0, net1, …
	Network string `hcl:"network,optional"`
	MAC     string `hcl:"mac"`
	Bridge  string `hcl:"bridge"`
	VLAN    *int   `hcl:"vlan,optional"` // inline form only, as are the rest
	DHCP    *bool  `hcl:"dhcp,optional"`
	Primary *bool  `hcl:"primary,optional"`
	Address string `hcl:"address,optional"`
	CIDR    string `hcl:"cidr,optional"`
	MTU     *int   `hcl:"mtu,optional"`
}
