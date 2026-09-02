package pve_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// bootNIC is a single inline-form boot NIC; vmsCluster resolves after
// construction, the same way Load resolves a real config.
func bootNIC(mac, bridge string) []config.NetworkInterface {
	dhcp, primary := true, true
	return []config.NetworkInterface{{
		Name: "net0", MAC: mac, Bridge: bridge, DHCP: &dhcp, Primary: &primary,
	}}
}

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
				VMID: 200, Cores: 4, Memory: 8192,
				DiskGB: 64, Storage: "local-zfs",
				Interfaces: bootNIC("02:50:99:a2:00:01", "vmbr0"),
			},
			{
				Name: "cp-02", Role: config.RoleControlPlane, PVENode: "pve-02",
				VMID: 201, Cores: 4, Memory: 8192,
				DiskGB: 64, Storage: "local-zfs",
				Interfaces: bootNIC("02:50:99:a2:00:02", "vmbr0"),
			},
			{
				Name: "cp-03", Role: config.RoleControlPlane, PVENode: "pve-03",
				VMID: 202, Cores: 4, Memory: 8192,
				DiskGB: 64, Storage: "local-zfs",
				Interfaces: bootNIC("02:50:99:a2:00:03", "vmbr0"),
			},
			{
				Name: "worker-01", Role: config.RoleWorker, PVENode: "pve-01",
				VMID: 300, Cores: 8, Memory: 16384,
				DiskGB: 128, Storage: "local-lvm",
				Interfaces: bootNIC("02:50:99:a2:01:01", "vmbr1"),
			},
		},
	}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		panic("vmsCluster does not resolve: " + diags.Error())
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

// mustVMSpec builds the spec for a resolved node, failing the test on
// the unresolved-cluster error path (covered by its own test below).
func mustVMSpec(t *testing.T, node *config.TalosNode) *qemu.CreateSpec {
	t.Helper()
	spec, err := pve.VMSpec(node)
	if err != nil {
		t.Fatalf("VMSpec(%s): %v", node.Name, err)
	}
	return spec
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
// declared VLAN lands as a PVE-side tag on the NIC — the lab's
// vm-trunk bridge has no native VLAN, so an untagged VM is on no
// network at all — and an undeclared VLAN adds no tag parameter.
func TestVMSpecVLANTag(t *testing.T) {
	cfg := vmsCluster()
	node := &cfg.Talos.Nodes[0]

	if got := mustVMSpec(t, node).Net0; strings.Contains(got, "tag=") {
		t.Errorf("net0 = %q carries a tag with no vlan configured", got)
	}

	vlan := 11
	node.Interfaces[0].VLAN = &vlan
	if diags := cfg.ResolveInterfaces(); diags.HasErrors() {
		t.Fatalf("re-resolve with the vlan set: %s", diags.Error())
	}
	if got, want := mustVMSpec(t, node).Net0, ",tag=11"; !strings.HasSuffix(got, want) {
		t.Errorf("net0 = %q, want suffix %q", got, want)
	}
}

// TestVMSpecUnresolvedNodeErrors pins the failure mode for a node
// whose cluster never ran ResolveInterfaces: an error, not a spec
// with an empty NIC that would create a VM on no network at all.
func TestVMSpecUnresolvedNodeErrors(t *testing.T) {
	node := &config.TalosNode{Name: "orphan", VMID: 999}
	if spec, err := pve.VMSpec(node); err == nil {
		t.Fatalf("VMSpec on an unresolved node = %+v, want an error", spec)
	}
}

// addStorageNIC appends a static jumbo second NIC to a node — the
// storage-plane shape from IMPL-0003 — and re-resolves the cluster.
func addStorageNIC(t *testing.T, cfg *config.Cluster, node *config.TalosNode, mac, address string) {
	t.Helper()
	dhcp, mtu := false, 9000
	node.Interfaces = append(node.Interfaces, config.NetworkInterface{
		Name: "net1", MAC: mac, Bridge: "storbr0",
		DHCP: &dhcp, Address: address, MTU: &mtu,
	})
	if diags := cfg.ResolveInterfaces(); diags.HasErrors() {
		t.Fatalf("re-resolve with the storage NIC: %s", diags.Error())
	}
}

// TestVMSpecRendersAllInterfaces pins the multi-NIC derivations
// (DESIGN-0004): every declared interface lands in its slot — the
// secondary untagged on its own bridge with an explicit mtu, never
// PVE's mtu=1 magic — and the boot order still carries ONLY the
// primary slot. A second NIC in boot order is the silent PXE hang
// this rule exists to prevent.
func TestVMSpecRendersAllInterfaces(t *testing.T) {
	cfg := vmsCluster()
	node := &cfg.Talos.Nodes[3] // worker-01
	addStorageNIC(t, cfg, node, "02:50:99:a2:14:2f", "192.0.2.63/24")

	spec := mustVMSpec(t, node)
	if want := "virtio,bridge=vmbr1,macaddr=02:50:99:a2:01:01,firewall=0"; spec.Net0 != want {
		t.Errorf("net0 = %q, want %q", spec.Net0, want)
	}
	if want := "virtio,bridge=storbr0,macaddr=02:50:99:a2:14:2f,firewall=0,mtu=9000"; spec.Extra["net1"] != want {
		t.Errorf("net1 = %q, want %q", spec.Extra["net1"], want)
	}
	if want := "order=scsi0;net0"; spec.Boot != want {
		t.Errorf("boot = %q, want %q — only the primary slot may boot", spec.Boot, want)
	}
}

// TestVMsMultiNICWireFields drives the multi-NIC spec through a real
// create and reads it back: the secondary NIC and the pinned boot
// order must survive the wire, not just the struct.
func TestVMsMultiNICWireFields(t *testing.T) {
	cfg := vmsCluster()
	node := &cfg.Talos.Nodes[3] // worker-01
	addStorageNIC(t, cfg, node, "02:50:99:a2:14:2f", "192.0.2.63/24")

	p, qsvc := newProvisioner(t, cfg)
	runVMs(t, p)

	got, err := qsvc(node.PVENode).Config(context.Background(), node.VMID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if want := "virtio,bridge=storbr0,macaddr=02:50:99:a2:14:2f,firewall=0,mtu=9000"; got.Net1 != want {
		t.Errorf("net1 = %q, want %q", got.Net1, want)
	}
	if want := "order=scsi0;net0"; got.Boot != want {
		t.Errorf("boot = %q, want %q", got.Boot, want)
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
		spec := mustVMSpec(t, node)
		nic, ok := node.PrimaryInterface()
		if !ok {
			t.Fatalf("%s: no resolved primary interface", node.Name)
		}
		if !strings.Contains(spec.Net0, "macaddr="+nic.MAC) {
			t.Errorf("%s: net0 %q does not carry the config MAC %s", node.Name, spec.Net0, nic.MAC)
		}
		if spec.VMID != types.VMID(node.VMID) {
			t.Errorf("%s: vmid = %d, want %d", node.Name, spec.VMID, node.VMID)
		}
	}
}

// TestVMSpecPinsVirtIOSCSI pins the fix for INV-0001 deviation 12.
// With scsihw unset, PVE's API default is the emulated LSI 53C895A —
// a controller the Talos kernel has no driver for (the UI wizard
// defaults to VirtIO SCSI, which is why booty's walkthrough never hit
// it). The guest consequence is total: /dev/sda does not exist, the
// install sequence dies on lstat, and the node reboots back to PXE
// forever while every host-side check reports a healthy VM.
func TestVMSpecPinsVirtIOSCSI(t *testing.T) {
	cfg := vmsCluster()
	spec := mustVMSpec(t, &cfg.Talos.Nodes[0])
	if got := spec.Extra["scsihw"]; got != "virtio-scsi-single" {
		t.Fatalf("scsihw = %q, want %q (PVE's API default is LSI 53C895A, invisible to Talos)",
			got, "virtio-scsi-single")
	}
}

// TestVMSpecEnablesGuestAgent pins the fix for INV-0001 deviation 14.
// The base extension profile ships qemu-guest-agent, and its service
// talks to a virtio-serial channel that exists only when the VM
// config enables the agent. Without it the service can never start,
// startAllServices never completes, the machine stage sticks at
// "Booting" on every node, and `talos health` fails its
// boot-sequence phase against an otherwise healthy cluster.
func TestVMSpecEnablesGuestAgent(t *testing.T) {
	cfg := vmsCluster()
	spec := mustVMSpec(t, &cfg.Talos.Nodes[0])
	if got := spec.Extra["agent"]; got != "enabled=1" {
		t.Fatalf("agent = %q, want %q (the guest-agent extension needs its virtio channel)",
			got, "enabled=1")
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

// wartClient wires a client through a wrapper that restores real
// PVE's by-ID answer for a missing VM: HTTP 500 "Configuration file
// '…' does not exist", never mockpve's clean 404 (INV-0001 deviation
// 10, the third instance of the deviation 4/8 class — by-ID GETs are
// not existence probes). Existence for the wart itself is read from
// the mock's own index, the same source of truth the fixed code uses.
func wartClient(t *testing.T, mock *mockpve.Server) api.Client {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status/current") {
			// /api2/json/nodes/<node>/qemu/<vmid>/status/current
			parts := strings.Split(r.URL.Path, "/")
			node, vmid := parts[4], parts[6]
			if !mockHasVM(r.Context(), t, mock, node, vmid) {
				http.Error(w, fmt.Sprintf(
					"Configuration file 'nodes/%s/qemu-server/%s.conf' does not exist",
					node, vmid), http.StatusInternalServerError)
				return
			}
		}
		mock.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := api.New(srv.URL, api.TokenCredentials("root@pam!mock", "mock-secret"))
	if err != nil {
		t.Fatalf("wire client: %v", err)
	}
	return client
}

// mockHasVM asks the mock's list endpoint whether a VM exists.
func mockHasVM(ctx context.Context, t *testing.T, mock *mockpve.Server, node, vmid string) bool {
	t.Helper()
	req := httptest.NewRequestWithContext(
		ctx, http.MethodGet, "/api2/json/nodes/"+node+"/qemu", http.NoBody)
	req.Header.Set("Authorization", "PVEAPIToken=root@pam!mock=mock-secret")
	rec := httptest.NewRecorder()
	mock.ServeHTTP(rec, req)

	var envelope struct {
		Data []struct {
			VMID json.Number `json:"vmid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("parse mock vm list: %v (body: %s)", err, rec.Body.String())
	}
	for _, vm := range envelope.Data {
		if vm.VMID.String() == vmid {
			return true
		}
	}
	return false
}

// TestVMsSurviveByIDGetWart reproduces the drill's first Phase 4
// failure: against a server with the real 500-on-missing wart, the
// full stage must still converge — fresh run creates and starts
// everything, re-run applies nothing. The Get-based checks this
// replaced die in the first Check here, exactly as they did live.
func TestVMsSurviveByIDGetWart(t *testing.T) {
	cfg := vmsCluster()
	mock := mockpve.New()
	mock.SeedVersion("9.2.1", "9.2", "test")
	for _, n := range cfg.PVE.Nodes {
		mock.AddNode(n.Name)
	}
	client := wartClient(t, mock)

	var logBuf strings.Builder
	p := &pve.Provisioner{
		Cluster: cfg,
		QEMU:    qemuFor(client),
		Tasks:   tasks.NewService(client),
		Log:     slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	if res := runVMs(t, p); res.Applied != 8 {
		t.Errorf("fresh run applied %d steps, want 8", res.Applied)
	}
	if res := runVMs(t, p); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0", res.Applied)
	}
}
