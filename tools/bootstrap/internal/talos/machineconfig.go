package talos

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/siderolabs/go-pointer"
	machcfg "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	netcfg "github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// vanillaSchematicID is the Image Factory schematic with no system
// extensions — the default when the config doesn't pin one
// (IMPL-0001 OQ-1). It is version-independent: the factory derives
// images from <schematic>:<talos version>.
const vanillaSchematicID = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// hostnamePlaceholder stands in for the per-node hostname during
// config generation and validation; the emitted template carries the
// booty expression instead. The value never survives into any
// artifact — ClassTemplates fails if the swap doesn't happen.
const hostnamePlaceholder = "bootstrap-hostname-placeholder"

// installDisk is where Talos installs itself. The Phase 5 VM spec
// attaches the disk as scsi0 on a virtio-scsi controller, which the
// guest sees as /dev/sda.
const installDisk = "/dev/sda"

// The booty template expressions substituted into the emitted
// templates (booty's overlay walkthrough): hostname is a per-node
// group var, install_image a per-role profile var.
const (
	// HostnameVar is the booty template expression for the per-node
	// hostname; the emitted catalog groups must define this var.
	HostnameVar = `{{ index .Vars "hostname" }}`
	// InstallImageVar is the booty template expression for the
	// installer image; the emitted catalog profiles must define this
	// var.
	InstallImageVar = `{{ index .Vars "install_image" }}`
)

// The template var keys the booty catalog defines: hostname per node
// (group var), role and install_image per class (profile vars).
const (
	HostnameVarKey     = "hostname"
	RoleVarKey         = "role"
	InstallImageVarKey = "install_image"
)

// MACVarKey names the per-node catalog group var carrying one slot's
// MAC. The machineconfig templates and the emitted groups both derive
// the name from here, so the two sides agree by construction.
func MACVarKey(slot string) string { return slot + "_mac" }

// AddressVarKey names the per-node catalog group var carrying one
// static slot's address.
func AddressVarKey(slot string) string { return slot + "_address" }

// varExpr is the booty template expression reading one group var.
func varExpr(key string) string { return `{{ index .Vars "` + key + `" }}` }

// The per-slot placeholder identity baked into the config for
// machinery validation and swapped for booty expressions afterwards.
// The MAC placeholder is a plain marker string (deviceSelector's
// hardwareAddr is a matcher machinery does not parse); the address
// placeholder must survive real CIDR validation, so it comes from
// TEST-NET-3 — a documentation range that can never be a real plane.
func macPlaceholder(slot string) string { return "bootstrap-" + slot + "-mac-placeholder" }

func addressPlaceholder(i int) string { return fmt.Sprintf("203.0.113.%d/32", i) }

// ResolveSchematicID returns the configured Image Factory schematic
// or the vanilla no-extensions default when the config leaves it
// unset.
func ResolveSchematicID(schematicID string) string {
	if schematicID != "" {
		return schematicID
	}
	return vanillaSchematicID
}

// InstallImage returns the Image Factory installer image reference
// for the schematic (empty means vanilla) and Talos version — the
// value machineconfigs install and the catalog's install_image var
// carries.
func InstallImage(schematicID, version string) string {
	return fmt.Sprintf("factory.talos.dev/installer/%s:%s", ResolveSchematicID(schematicID), version)
}

// metalMode is the validation.RuntimeMode for bare-metal (and
// PXE-booted VM) installs. Machinery only ships concrete modes in the
// Talos runtime, not machinery — this mirrors runtime.ModeMetal.
type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

// Templates is the set of booty machineconfig templates, one per
// class, plus any validation warnings machinery raised on the
// underlying configs (the configs are valid; warnings are advisory).
type Templates struct {
	// Classes holds the control-plane classes first, then the worker
	// classes, each role's in first-node order.
	Classes  []ClassTemplate
	Warnings []string
}

// ClassTemplate is one template class: a role and the label set its
// nodes share (DESIGN-0005 Phase 3). Labels are a map, so they can't
// ride booty's textual var substitution the way the hostname does —
// nodes with different label sets need different templates.
type ClassTemplate struct {
	Role   config.Role
	Labels map[string]string // nil for the unlabelled class
	Data   []byte
}

// For returns the template for a role and label set.
func (t Templates) For(role config.Role, labels map[string]string) ([]byte, bool) {
	key := LabelSetKey(labels)
	for _, c := range t.Classes {
		if c.Role == role && LabelSetKey(c.Labels) == key {
			return c.Data, true
		}
	}
	return nil, false
}

// LabelSetKey is the canonical form of a label set — sorted k=v pairs
// joined by commas — that classes are keyed and named by. Empty for
// no labels.
func LabelSetKey(labels map[string]string) string {
	keys := slices.Sorted(maps.Keys(labels))
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+labels[k])
	}
	return strings.Join(pairs, ",")
}

// templateClasses lists the (role, label set) classes the cluster's
// nodes fall into. A role with no nodes still gets its unlabelled
// class, so both role templates always exist and a config without
// labels emits exactly the two it always has.
func templateClasses(cluster *config.Cluster) []ClassTemplate {
	var classes []ClassTemplate
	for _, role := range []config.Role{config.RoleControlPlane, config.RoleWorker} {
		seen := map[string]bool{}
		for i := range cluster.Talos.Nodes {
			n := &cluster.Talos.Nodes[i]
			labels := n.ResolvedLabels()
			if key := LabelSetKey(labels); n.Role == role && !seen[key] {
				seen[key] = true
				classes = append(classes, ClassTemplate{Role: role, Labels: labels})
			}
		}
		if len(seen) == 0 {
			classes = append(classes, ClassTemplate{Role: role})
		}
	}
	return classes
}

// ClassTemplates generates one machineconfig template per class for
// booty's overlay. Each is a complete machinery-generated config —
// seeded from the secrets bundle, validated in metal mode — with the
// per-node values swapped for booty template expressions afterwards:
// the hostname, the installer image, and (on multi-interface shapes)
// each slot's MAC and static address. Validation always runs on the
// real config, never on template text.
func ClassTemplates(bundle *secrets.Bundle, cluster *config.Cluster) (Templates, error) {
	contract, err := machcfg.ParseContractFromVersion(cluster.Talos.Version)
	if err != nil {
		return Templates{}, fmt.Errorf("parse talos version %q: %w", cluster.Talos.Version, err)
	}
	kubernetesVersion := cluster.Talos.KubernetesVersion
	if kubernetesVersion == "" {
		kubernetesVersion = constants.DefaultKubernetesVersion
	}
	image := InstallImage(cluster.Talos.SchematicID, cluster.Talos.Version)
	shapes, err := roleShapes(cluster)
	if err != nil {
		return Templates{}, fmt.Errorf("derive role interface shapes: %w", err)
	}

	in, err := generate.NewInput(cluster.TalosName(), cluster.Talos.Endpoint, kubernetesVersion,
		generate.WithSecretsBundle(bundle),
		generate.WithVersionContract(contract),
		generate.WithInstallImage(image),
		generate.WithInstallDisk(installDisk),
	)
	if err != nil {
		return Templates{}, fmt.Errorf("machinery generate input: %w", err)
	}

	machineType := map[config.Role]machine.Type{
		config.RoleControlPlane: machine.TypeControlPlane,
		config.RoleWorker:       machine.TypeWorker,
	}
	var out Templates
	for _, c := range templateClasses(cluster) {
		data, warnings, err := roleTemplate(in, machineType[c.Role], image, cluster, shapes[c.Role], c.Labels)
		if err != nil {
			return Templates{}, fmt.Errorf("%s template %s: %w", c.Role, LabelSetKey(c.Labels), err)
		}
		c.Data = data
		out.Classes = append(out.Classes, c)
		for _, w := range warnings {
			if !slices.Contains(out.Warnings, w) {
				out.Warnings = append(out.Warnings, w)
			}
		}
	}
	return out, nil
}

// slotShape is the machineconfig-relevant shape of one NIC slot —
// what the per-role template bakes in. Identity (MAC, address) is
// per-node and rides group vars, so it is not part of the shape.
type slotShape struct {
	slot string
	dhcp bool
	mtu  int
}

// roleShapes derives each role's interface shape from its nodes'
// resolved interfaces. A role's templates split only by label set
// (DESIGN-0005 Phase 3) — interface shape stays a per-role rule, so
// the nodes of a role must agree on it; a divergent node is an error
// naming both sides rather than a template that silently fits only
// one of them.
func roleShapes(cluster *config.Cluster) (map[config.Role][]slotShape, error) {
	shapes := make(map[config.Role][]slotShape, 2)
	owners := make(map[config.Role]string, 2)
	for i := range cluster.Talos.Nodes {
		n := &cluster.Talos.Nodes[i]
		shape := nodeShape(n)
		prior, seen := shapes[n.Role]
		if !seen {
			shapes[n.Role] = shape
			owners[n.Role] = n.Name
			continue
		}
		if !slices.Equal(prior, shape) {
			return nil, fmt.Errorf(
				"talos node %q: interface shape [%s] differs from node %q's [%s]; nodes of a role share one machineconfig template, so their interface shapes must agree",
				n.Name,
				formatShape(shape),
				owners[n.Role],
				formatShape(prior),
			)
		}
	}
	return shapes, nil
}

// nodeShape reduces a node's resolved interfaces to their shape.
func nodeShape(n *config.TalosNode) []slotShape {
	nics := n.ResolvedInterfaces()
	shape := make([]slotShape, 0, len(nics))
	for _, nic := range nics {
		shape = append(shape, slotShape{slot: nic.Slot, dhcp: nic.DHCP, mtu: nic.MTU})
	}
	return shape
}

// formatShape renders a shape for the divergence diagnostic, e.g.
// "net0=dhcp net1=static,mtu=9000".
func formatShape(shape []slotShape) string {
	parts := make([]string, 0, len(shape))
	for _, s := range shape {
		mode := "static"
		if s.dhcp {
			mode = "dhcp"
		}
		if s.mtu > 0 {
			mode += fmt.Sprintf(",mtu=%d", s.mtu)
		}
		parts = append(parts, s.slot+"="+mode)
	}
	return strings.Join(parts, " ")
}

// roleTemplate generates, customizes, validates, and templatizes the
// config for one machine type. The completion surface (DESIGN-0002)
// and the interface declarations (DESIGN-0004) are applied before
// validation, so machinery checks the real emitted shape — inline
// manifests, CNI knobs, and network devices included.
func roleTemplate(
	in *generate.Input, machineType machine.Type, image string,
	cluster *config.Cluster, shape []slotShape, labels map[string]string,
) (data []byte, warnings []string, err error) {
	generated, err := in.Config(machineType)
	if err != nil {
		return nil, nil, fmt.Errorf("generate config: %w", err)
	}
	provider, err := setHostname(generated)
	if err != nil {
		return nil, nil, err
	}
	if err := applyCompletion(provider.RawV1Alpha1(), cluster); err != nil {
		return nil, nil, err
	}
	applyLabels(provider.RawV1Alpha1(), labels)
	if len(shape) > 1 {
		setInterfaces(provider.RawV1Alpha1(), shape)
	}
	warnings, err = provider.Validate(metalMode{})
	if err != nil {
		return nil, nil, fmt.Errorf("validate: %w", err)
	}
	raw, err := provider.Bytes()
	if err != nil {
		return nil, nil, fmt.Errorf("encode: %w", err)
	}
	data, err = templatize(raw, image, shape)
	if err != nil {
		return nil, nil, err
	}
	return data, warnings, nil
}

// setInterfaces declares every NIC slot in machine.network.interfaces,
// selection by deviceSelector.hardwareAddr only — virtio PCI
// enumeration order is not a contract. Called only for
// multi-interface shapes (DESIGN-0004 OQ-2): a primary-only node
// emits no machine.network section at all, keeping single-interface
// artifacts byte-identical to v0.2.0. Static slots carry addresses
// and mtu but no routes, gateway, or DNS — a secondary plane must
// never attract the default route.
//
// The deprecated v1alpha1 section is deliberate. Its replacements
// (LinkAliasConfig selecting by MAC, LinkConfig, DHCPv4Config) exist
// in machinery since Talos 1.12, but the legacy section is the exact
// shape the live fleet carries (IMPL-0003 Phase 2), and a deprecated
// field and its replacement document are mutually exclusive — moving
// is a whole-config replacement on every node, not an edit here. That
// migration is DESIGN-0006's (INV-0004 F5).
//
//nolint:staticcheck // legacy shape the live fleet carries; the multi-doc move is DESIGN-0006.
func setInterfaces(cfg *v1alpha1.Config, shape []slotShape) {
	devices := make([]*v1alpha1.Device, 0, len(shape))
	for i, s := range shape {
		d := &v1alpha1.Device{
			DeviceSelector: &v1alpha1.NetworkDeviceSelector{
				NetworkDeviceHardwareAddress: macPlaceholder(s.slot),
			},
			DeviceDHCP: pointer.To(s.dhcp),
		}
		if s.mtu > 0 {
			d.DeviceMTU = s.mtu
		}
		if !s.dhcp {
			d.DeviceAddresses = []string{addressPlaceholder(i)}
		}
		devices = append(devices, d)
	}
	if cfg.MachineConfig.MachineNetwork == nil {
		cfg.MachineConfig.MachineNetwork = &v1alpha1.NetworkConfig{}
	}
	cfg.MachineConfig.MachineNetwork.NetworkInterfaces = devices
}

// setHostname swaps generate's HostnameConfig document (hostname
// auto-derivation) for one pinning the static hostname placeholder.
// Machinery has no generate option for a static hostname — setting it
// the legacy v1alpha1 way instead conflicts with the auto document —
// so the document list is rebuilt with the one swap.
func setHostname(provider machcfg.Provider) (machcfg.Provider, error) {
	docs := provider.Documents()
	found := false
	for i, doc := range docs {
		if _, ok := doc.(*netcfg.HostnameConfigV1Alpha1); !ok {
			continue
		}
		hostname := netcfg.NewHostnameConfigV1Alpha1()
		hostname.ConfigHostname = hostnamePlaceholder
		docs[i] = hostname
		found = true
	}
	if !found {
		return nil, fmt.Errorf("generated config has no HostnameConfig document to pin (talos version too old?)")
	}
	rebuilt, err := container.New(docs...)
	if err != nil {
		return nil, fmt.Errorf("rebuild config with static hostname: %w", err)
	}
	return rebuilt, nil
}

// templatize performs the overlay substitutions, failing loudly if
// any expected value is missing — a silent miss would emit a template
// with a baked-in hostname, image, or placeholder identity. On
// multi-interface shapes each slot's MAC placeholder and each static
// slot's address placeholder become per-node group var expressions.
func templatize(data []byte, image string, shape []slotShape) ([]byte, error) {
	swaps := []struct {
		value, expr string
	}{
		{hostnamePlaceholder, HostnameVar},
		{image, InstallImageVar},
	}
	if len(shape) > 1 {
		for i, s := range shape {
			swaps = append(swaps, struct{ value, expr string }{
				value: macPlaceholder(s.slot),
				expr:  varExpr(MACVarKey(s.slot)),
			})
			if !s.dhcp {
				swaps = append(swaps, struct{ value, expr string }{
					value: addressPlaceholder(i),
					expr:  varExpr(AddressVarKey(s.slot)),
				})
			}
		}
	}
	for _, swap := range swaps {
		if !bytes.Contains(data, []byte(swap.value)) {
			return nil, fmt.Errorf("generated config does not contain %q to substitute", swap.value)
		}
		data = bytes.ReplaceAll(data, []byte(swap.value), []byte(swap.expr))
	}
	return data, nil
}
