package talos

import (
	"bytes"
	"fmt"

	machcfg "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	netcfg "github.com/siderolabs/talos/pkg/machinery/config/types/network"
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
// seeded from the secrets bundle, validated in metal mode — with
// exactly two values swapped for booty template expressions
// afterwards: the hostname (per-node group var) and the installer
// image (per-role profile var). Validation always runs on the real
// config, never on template text.
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

	in, err := generate.NewInput(cluster.Name, cluster.Talos.Endpoint, kubernetesVersion,
		generate.WithSecretsBundle(bundle),
		generate.WithVersionContract(contract),
		generate.WithInstallImage(image),
		generate.WithInstallDisk(installDisk),
	)
	if err != nil {
		return Templates{}, fmt.Errorf("machinery generate input: %w", err)
	}

	controlPlane, cpWarnings, err := roleTemplate(in, machine.TypeControlPlane, image)
	if err != nil {
		return Templates{}, fmt.Errorf("controlplane template: %w", err)
	}
	worker, workerWarnings, err := roleTemplate(in, machine.TypeWorker, image)
	if err != nil {
		return Templates{}, fmt.Errorf("worker template: %w", err)
	}
	return Templates{
		ControlPlane: controlPlane,
		Worker:       worker,
		Warnings:     append(cpWarnings, workerWarnings...),
	}, nil
}

// roleTemplate generates, validates, and templatizes the config for
// one machine type.
func roleTemplate(in *generate.Input, machineType machine.Type, image string) (data []byte, warnings []string, err error) {
	generated, err := in.Config(machineType)
	if err != nil {
		return nil, nil, fmt.Errorf("generate config: %w", err)
	}
	provider, err := setHostname(generated)
	if err != nil {
		return nil, nil, err
	}
	warnings, err = provider.Validate(metalMode{})
	if err != nil {
		return nil, nil, fmt.Errorf("validate: %w", err)
	}
	raw, err := provider.Bytes()
	if err != nil {
		return nil, nil, fmt.Errorf("encode: %w", err)
	}
	data, err = templatize(raw, image)
	if err != nil {
		return nil, nil, err
	}
	return data, warnings, nil
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

// templatize performs the two overlay substitutions, failing loudly
// if either expected value is missing — a silent miss would emit a
// template with a baked-in hostname or image.
func templatize(data []byte, image string) ([]byte, error) {
	for _, swap := range []struct {
		value, expr string
	}{
		{hostnamePlaceholder, HostnameVar},
		{image, InstallImageVar},
	} {
		if !bytes.Contains(data, []byte(swap.value)) {
			return nil, fmt.Errorf("generated config does not contain %q to substitute", swap.value)
		}
		data = bytes.ReplaceAll(data, []byte(swap.value), []byte(swap.expr))
	}
	return data, nil
}
