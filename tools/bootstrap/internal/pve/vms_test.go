package pve_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/api"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/mockpve"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/qemu"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/tasks"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/types"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/version"
)

// vmsCluster is the three-PVE-node cluster with four Talos VMs spread
// across it — two roles, three hosts, so placement is actually
// exercised rather than assumed.
func vmsCluster() *config.Cluster {
	c := testCluster()
	c.Talos = config.Talos{
		Version:  "v1.13.8",
		Endpoint: "https://10.0.20.10:6443",
		Booty:    config.Booty{URL: "http://10.0.10.5:8080"},
		Nodes: []config.TalosNode{
			{
				Name: "cp-01", Role: config.RoleControlPlane, PVENode: "pve-01",
				VMID: 200, MAC: "02:50:99:a2:00:01", Cores: 4, Memory: 8192,
				DiskGB: 64, Storage: "local-zfs", Bridge: "vmbr0",
			},
			{
				Name: "cp-02", Role: config.RoleControlPlane, PVENode: "pve-02",
				VMID: 201, MAC: "02:50:99:a2:00:02", Cores: 4, Memory: 8192,
				DiskGB: 64, Storage: "local-zfs", Bridge: "vmbr0",
			},
			{
				Name: "cp-03", Role: config.RoleControlPlane, PVENode: "pve-03",
				VMID: 202, MAC: "02:50:99:a2:00:03", Cores: 4, Memory: 8192,
				DiskGB: 64, Storage: "local-zfs", Bridge: "vmbr0",
			},
			{
				Name: "worker-01", Role: config.RoleWorker, PVENode: "pve-01",
				VMID: 300, MAC: "02:50:99:a2:01:01", Cores: 8, Memory: 16384,
				DiskGB: 128, Storage: "local-lvm", Bridge: "vmbr1",
			},
		},
	}
	return c
}

// qemuFor is the per-node VM service factory the Provisioner takes —
// the same shape as the production client's QEMU method.
func qemuFor(client api.Client) func(node string) *qemu.Service {
	return func(node string) *qemu.Service {
		return qemu.NewService(client, node, version.Capabilities{})
	}
}

// newProvisioner wires a Provisioner against a mock carrying the three
// PVE nodes and no VMs.
func newProvisioner(t *testing.T, cfg *config.Cluster) (*pve.Provisioner, func(node string) *qemu.Service) {
	t.Helper()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	var logBuf strings.Builder
	svc := qemuFor(client)
	return &pve.Provisioner{
		Cluster: cfg,
		QEMU:    svc,
		Tasks:   tasks.NewService(client),
		Log:     slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, svc
}

func runVMs(t *testing.T, p *pve.Provisioner) steps.Result {
	t.Helper()
	r := steps.Runner{Log: p.Log}
	res, err := r.Run(context.Background(), p.Steps())
	if err != nil {
		t.Fatalf("run vms: %v", err)
	}
	return res
}

func TestVMsFreshRun(t *testing.T) {
	cfg := vmsCluster()
	p, qsvc := newProvisioner(t, cfg)

	stage := p.Steps()
	if len(stage) != 8 {
		t.Fatalf("got %d steps, want 8 (create+start for 4 VMs)", len(stage))
	}

	res := runVMs(t, p)
	if res.Applied != 8 {
		t.Errorf("applied %d steps, want 8", res.Applied)
	}

	for i := range cfg.Talos.Nodes {
		node := &cfg.Talos.Nodes[i]
		status, err := qsvc(node.PVENode).Get(context.Background(), node.VMID)
		if err != nil {
			t.Fatalf("vm %d on %s: %v", node.VMID, node.PVENode, err)
		}
		if status.Status != types.PowerStateRunning {
			t.Errorf("vm %d is %q, want running", node.VMID, status.Status)
		}
		if status.Name != node.Name {
			t.Errorf("vm %d is named %q, want %q", node.VMID, status.Name, node.Name)
		}
	}
}

func TestVMsRerunIsNoOp(t *testing.T) {
	p, _ := newProvisioner(t, vmsCluster())
	runVMs(t, p)

	if res := runVMs(t, p); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0", res.Applied)
	}
}

// TestVMsPartialExists is the convergence case an interrupted run
// leaves behind: some VMs defined, one of them already running, the
// rest missing. Only the gaps should be filled.
func TestVMsPartialExists(t *testing.T) {
	cfg := vmsCluster()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	// cp-01 fully done, cp-02 created but never started.
	mock.AddVM("pve-01", 200, "cp-01", "running")
	mock.AddVM("pve-02", 201, "cp-02", "stopped")

	client, cleanup := mock.NewClient()
	t.Cleanup(cleanup)

	var logBuf strings.Builder
	qsvc := qemuFor(client)
	p := &pve.Provisioner{
		Cluster: cfg,
		QEMU:    qsvc,
		Tasks:   tasks.NewService(client),
		Log:     slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	// 8 steps total; cp-01's create and start are done and cp-02's
	// create is done, so 5 remain.
	res := runVMs(t, p)
	if res.Applied != 5 {
		t.Errorf("applied %d steps, want 5", res.Applied)
	}
	for i := range cfg.Talos.Nodes {
		node := &cfg.Talos.Nodes[i]
		status, err := qsvc(node.PVENode).Get(context.Background(), node.VMID)
		if err != nil {
			t.Fatalf("vm %d: %v", node.VMID, err)
		}
		if status.Status != types.PowerStateRunning {
			t.Errorf("vm %d is %q, want running", node.VMID, status.Status)
		}
	}
}

func TestVMsDryRunCreatesNothing(t *testing.T) {
	cfg := vmsCluster()
	p, qsvc := newProvisioner(t, cfg)

	var out strings.Builder
	r := steps.Runner{DryRun: true, Out: &out, Log: p.Log}
	res, err := r.Run(context.Background(), p.Steps())
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Applied != 0 {
		t.Errorf("dry run applied %d steps, want 0", res.Applied)
	}
	if res.Pending != 8 {
		t.Errorf("dry run reported %d pending, want 8", res.Pending)
	}
	for i := range cfg.Talos.Nodes {
		node := &cfg.Talos.Nodes[i]
		if _, err := qsvc(node.PVENode).Get(context.Background(), node.VMID); err == nil {
			t.Errorf("dry run created vm %d", node.VMID)
		}
	}
}

// TestVMSpecWireFields is the regression net for the settings booty's
// walkthrough proved load-bearing. Each of these produces a VM that
// looks correct in the UI and never PXE boots, so they are asserted on
// the wire — read back from the mock after a real create, not from the
// spec struct.
func TestVMSpecWireFields(t *testing.T) {
	cfg := vmsCluster()
	p, qsvc := newProvisioner(t, cfg)
	runVMs(t, p)

	node := &cfg.Talos.Nodes[3] // worker-01: distinct storage and bridge.
	got, err := qsvc(node.PVENode).Config(context.Background(), node.VMID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if got.Boot != "order=scsi0;net0" {
		t.Errorf("boot = %q, want order=scsi0;net0 (disk first, PXE fallback)", got.Boot)
	}
	if got.CPU != "host" {
		t.Errorf("cpu = %q, want host (kvm64 panics: Talos needs x86-64-v2)", got.CPU)
	}
	if want := "local-lvm:128"; got.SCSI0 != want {
		t.Errorf("scsi0 = %q, want %q", got.SCSI0, want)
	}
	if want := "virtio,bridge=vmbr1,macaddr=02:50:99:a2:01:01,firewall=0"; got.Net0 != want {
		t.Errorf("net0 = %q, want %q", got.Net0, want)
	}
	if int(got.Cores) != node.Cores {
		t.Errorf("cores = %d, want %d", got.Cores, node.Cores)
	}
	if int(got.Memory) != node.Memory {
		t.Errorf("memory = %d, want %d", got.Memory, node.Memory)
	}

	for key, want := range map[string]string{
		"bios":     "ovmf",
		"machine":  "q35",
		"efidisk0": "local-lvm:1,efitype=4m,pre-enrolled-keys=0",
		"rng0":     "source=/dev/urandom",
		"serial0":  "socket",
	} {
		if got.Extra[key] != want {
			t.Errorf("%s = %q, want %q", key, got.Extra[key], want)
		}
	}

	// Pre-enrolled Secure Boot keys reject the unsigned iPXE binary and
	// the Talos kernel, so their absence is the point of efidisk0's
	// options — call it out separately from the exact-string check.
	if !strings.Contains(got.Extra["efidisk0"], "pre-enrolled-keys=0") {
		t.Error("efidisk0 must set pre-enrolled-keys=0 or the node refuses to boot what booty serves")
	}
}

// TestVMSpecVLANTag pins the net0 tag behavior (IMPL-0002 OQ-5): a
// configured VLAN lands as a PVE-side tag on the NIC — the lab's
// vm-trunk bridge has no native VLAN, so an untagged VM is on no
// network at all — and an unset VLAN adds no tag parameter.
func TestVMSpecVLANTag(t *testing.T) {
	cfg := vmsCluster()
	node := &cfg.Talos.Nodes[0]

	if got := pve.VMSpec(node).Net0; strings.Contains(got, "tag=") {
		t.Errorf("net0 = %q carries a tag with no vlan configured", got)
	}

	node.VLAN = 11
	if got, want := pve.VMSpec(node).Net0, ",tag=11"; !strings.HasSuffix(got, want) {
		t.Errorf("net0 = %q, want suffix %q", got, want)
	}
}

// TestVMSpecMACMatchesConfig pins the identity binding: the MAC on the
// NIC is the one from the config, which is the same one the emitted
// booty group selects on. If these ever diverge the node PXE boots and
// is handed nothing.
func TestVMSpecMACMatchesConfig(t *testing.T) {
	cfg := vmsCluster()
	for i := range cfg.Talos.Nodes {
		node := &cfg.Talos.Nodes[i]
		spec := pve.VMSpec(node)
		if !strings.Contains(spec.Net0, "macaddr="+node.MAC) {
			t.Errorf("%s: net0 %q does not carry the config MAC %s", node.Name, spec.Net0, node.MAC)
		}
		if spec.VMID != types.VMID(node.VMID) {
			t.Errorf("%s: vmid = %d, want %d", node.Name, spec.VMID, node.VMID)
		}
	}
}

// TestVMsNoNodesIsEmptyStage guards the degenerate config rather than
// letting it panic somewhere downstream.
func TestVMsNoNodesIsEmptyStage(t *testing.T) {
	cfg := testCluster()
	p := &pve.Provisioner{Cluster: cfg}
	if stage := p.Steps(); len(stage) != 0 {
		t.Errorf("got %d steps for a cluster with no talos nodes, want 0", len(stage))
	}
}
