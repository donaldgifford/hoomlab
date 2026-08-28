package pve

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/qemu"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/tasks"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/types"
)

// The VM settings booty's Proxmox+Talos walkthrough proved load-bearing.
// Each one is here because leaving it to the PVE default breaks the
// PXE boot in a way that is hard to diagnose from inside the guest.
const (
	// bootOrder puts the disk first and the NIC second. The empty disk
	// falls through to PXE on the first boot; once Talos has installed
	// itself the node boots from disk. Re-imaging is "wipe the disk and
	// reboot" — the firmware falls back to PXE again.
	bootOrder = "order=scsi0;net0"
	// cpuType must expose a modern instruction set: Talos requires
	// x86-64-v2, and PVE's default kvm64 panics the kernel.
	cpuType = "host"
	// osType is PVE's Linux 2.6+ profile.
	osType = "l26"
	// machineType is q35, the modern chipset UEFI expects.
	machineType = "q35"
	// biosType selects UEFI, which is what iPXE's .efi binary needs.
	biosType = "ovmf"
	// rngDevice adds a VirtIO RNG. Post-PixieFail EDK2 silently drops
	// the PXE boot option without an entropy source — the VM simply
	// never offers to network boot, with no error anywhere.
	rngDevice = "source=/dev/urandom"
	// serialDevice gives "qm terminal" somewhere to attach, which is
	// the only console into a Talos node that will not boot.
	serialDevice = "socket"
)

// Provisioner builds the Stage 4 step list: create each configured VM
// on its target PVE node, then start it. Both halves are convergent —
// an existing VM is left alone, a stopped one is started — so a re-run
// after any interruption creates only what is missing.
type Provisioner struct {
	Cluster *config.Cluster
	// QEMU returns the VM service for one PVE node. This is the test
	// seam; production passes the client's QEMU method.
	QEMU func(node string) *qemu.Service
	// Tasks waits for the create and start tasks to finish.
	Tasks *tasks.Service
	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

func (p *Provisioner) logger() *slog.Logger {
	if p.Log == nil {
		return slog.Default()
	}
	return p.Log
}

// Steps returns create-then-start for every configured Talos node, in
// config order. Creates and starts are interleaved per node rather
// than batched: a VM that fails to start is worth stopping on before
// creating the rest.
func (p *Provisioner) Steps() []steps.Step {
	list := make([]steps.Step, 0, 2*len(p.Cluster.Talos.Nodes))
	for i := range p.Cluster.Talos.Nodes {
		node := &p.Cluster.Talos.Nodes[i]
		list = append(list,
			steps.Step{
				Name:  "vm-create-" + node.Name,
				Check: func(ctx context.Context) (bool, error) { return p.vmExists(ctx, node) },
				Apply: func(ctx context.Context) error { return p.applyCreate(ctx, node) },
			},
			steps.Step{
				Name:  "vm-start-" + node.Name,
				Check: func(ctx context.Context) (bool, error) { return p.vmRunning(ctx, node) },
				Apply: func(ctx context.Context) error { return p.applyStart(ctx, node) },
			},
		)
	}
	return list
}

// findVM resolves a VM through its node's index. Existence is never
// decided by the by-ID status GET: real PVE answers it with HTTP 500
// "Configuration file '…/<vmid>.conf' does not exist" for a missing
// VM, not a 404 — the third instance of the INV-0001 deviation 4/8
// class (mockpve answers a clean 404, which is how the Get-based
// check passed every test and then died on the first live run). The
// index entry carries the power state too, so both checks read from
// it.
func (p *Provisioner) findVM(ctx context.Context, node *config.TalosNode) (*qemu.VM, bool, error) {
	list, err := p.QEMU(node.PVENode).List(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("list vms on %s: %w", node.PVENode, err)
	}
	for i := range list {
		if int(list[i].VMID) == node.VMID {
			return &list[i], true, nil
		}
	}
	return nil, false, nil
}

// vmExists reports whether the VM is already defined on its node.
func (p *Provisioner) vmExists(ctx context.Context, node *config.TalosNode) (bool, error) {
	_, ok, err := p.findVM(ctx, node)
	return ok, err
}

// vmRunning reports whether the VM is up. A VM that does not exist yet
// is not an error here: the create step runs first in the same stage,
// and reporting "not running" keeps the survey readable in dry-run.
func (p *Provisioner) vmRunning(ctx context.Context, node *config.TalosNode) (bool, error) {
	vm, ok, err := p.findVM(ctx, node)
	if err != nil || !ok {
		return false, err
	}
	return vm.Status == types.PowerStateRunning, nil
}

func (p *Provisioner) applyCreate(ctx context.Context, node *config.TalosNode) error {
	spec := VMSpec(node)
	p.logger().Info("creating vm",
		"vm", node.Name, "vmid", node.VMID, "pve_node", node.PVENode, "mac", node.MAC)
	ref, err := p.QEMU(node.PVENode).Create(ctx, spec)
	if err != nil {
		return fmt.Errorf("create vm %d (%s) on %s: %w", node.VMID, node.Name, node.PVENode, err)
	}
	return p.waitTask(ctx, ref, fmt.Sprintf("create of vm %d (%s)", node.VMID, node.Name))
}

func (p *Provisioner) applyStart(ctx context.Context, node *config.TalosNode) error {
	p.logger().Info("starting vm", "vm", node.Name, "vmid", node.VMID, "pve_node", node.PVENode)
	ref, err := p.QEMU(node.PVENode).Start(ctx, node.VMID)
	if err != nil {
		return fmt.Errorf("start vm %d (%s) on %s: %w", node.VMID, node.Name, node.PVENode, err)
	}
	return p.waitTask(ctx, ref, fmt.Sprintf("start of vm %d (%s)", node.VMID, node.Name))
}

func (p *Provisioner) waitTask(ctx context.Context, ref tasks.Ref, what string) error {
	status, err := p.Tasks.Wait(ctx, ref)
	if err != nil {
		return fmt.Errorf("wait for %s: %w", what, err)
	}
	if !status.OK() {
		return fmt.Errorf("%s failed: %s", what, status.ExitStatus)
	}
	return nil
}

// net0 renders the NIC. The tag goes on the PVE side of the bridge
// when the config sets a VLAN (a trunk port with no native VLAN drops
// untagged frames — IMPL-0002 OQ-5), and PVE strips it before the
// guest, so the firmware's PXE stack sees plain Ethernet either way.
func net0(node *config.TalosNode) string {
	s := fmt.Sprintf("virtio,bridge=%s,macaddr=%s,firewall=0", node.Bridge, node.MAC)
	if node.VLAN > 0 {
		s += fmt.Sprintf(",tag=%d", node.VLAN)
	}
	return s
}

// VMSpec builds the create request for one Talos node. It is exported
// so the spec assertions live in a test rather than in a comment: every
// field here is load-bearing, and a regression in any of them produces
// a VM that looks fine and never boots.
//
// The MAC is the one pinned in the config, which is also the one the
// emitted booty group selects on — identity flows from a single source
// to both sides of the PXE handshake.
func VMSpec(node *config.TalosNode) *qemu.CreateSpec {
	return &qemu.CreateSpec{
		VMID:   types.VMID(node.VMID),
		Name:   node.Name,
		Memory: node.Memory,
		Cores:  node.Cores,
		CPU:    cpuType,
		OSType: osType,
		SCSI0:  fmt.Sprintf("%s:%d", node.Storage, node.DiskGB),
		Net0:   net0(node),
		Boot:   bootOrder,
		Extra: map[string]string{
			"bios":    biosType,
			"machine": machineType,
			// The EFI vars disk must NOT carry pre-enrolled Secure Boot
			// keys: they reject the unsigned iPXE binary and the Talos
			// kernel, so the node refuses to boot what we serve it.
			"efidisk0": fmt.Sprintf("%s:1,efitype=4m,pre-enrolled-keys=0", node.Storage),
			"rng0":     rngDevice,
			"serial0":  serialDevice,
		},
	}
}
