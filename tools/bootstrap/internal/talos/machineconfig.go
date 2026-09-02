package talos

import (
	"bytes"
	"fmt"
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
// artifact — RoleTemplates fails if the swap doesn't happen.
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

// Templates is the pair of booty machineconfig templates, one per
// role, plus any validation warnings machinery raised on the
// underlying configs (the configs are valid; warnings are advisory).
type Templates struct {
	ControlPlane []byte
	Worker       []byte
	Warnings     []string
}

// RoleTemplates generates the two per-role machineconfig templates
// for booty's overlay. Each is a complete machinery-generated config —
// seeded from the secrets bundle, validated in metal mode — with the
// per-node values swapped for booty template expressions afterwards:
// the hostname, the installer image, and (on multi-interface shapes)
// each slot's MAC and static address. Validation always runs on the
// real config, never on template text.
func RoleTemplates(bundle *secrets.Bundle, cluster *config.Cluster) (Templates, error) {
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
		return Templates{}, err
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

	controlPlane, cpWarnings, err := roleTemplate(
		in, machine.TypeControlPlane, image, cluster, shapes[config.RoleControlPlane])
	if err != nil {
		return Templates{}, fmt.Errorf("controlplane template: %w", err)
	}
	worker, workerWarnings, err := roleTemplate(
		in, machine.TypeWorker, image, cluster, shapes[config.RoleWorker])
	if err != nil {
		return Templates{}, fmt.Errorf("worker template: %w", err)
	}
	return Templates{
		ControlPlane: controlPlane,
		Worker:       worker,
		Warnings:     append(cpWarnings, workerWarnings...),
	}, nil
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
// resolved interfaces. One machineconfig template exists per role
// (booty's overlay contract — profiles split a role only by vars and
// boot paths, never by template), so the nodes of a role must agree
// on shape; a divergent node is an error naming both sides rather
// than a template that silently fits only one of them.
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
	cluster *config.Cluster, shape []slotShape,
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
// device type (the deprecation points at documents that don't exist
// yet at this version), and the legacy section is the exact shape the
// live fleet carries (IMPL-0003 Phase 2).
//
//nolint:staticcheck // machinery v1.13 ships no multi-doc network
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
				macPlaceholder(s.slot), varExpr(MACVarKey(s.slot)),
			})
			if !s.dhcp {
				swaps = append(swaps, struct{ value, expr string }{
					addressPlaceholder(i), varExpr(AddressVarKey(s.slot)),
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
