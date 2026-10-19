package talos_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
	talosmocks "github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos/mocks"
)

const (
	applySchematic = "abc123"
	applyImage     = "factory.talos.dev/installer/" + applySchematic + ":" + talosVersion
	workerAddr     = "198.51.100.11"

	// liveV1alpha1 and liveHostname are a two-document live config; the
	// rendered side reuses them, reordered or edited per case.
	liveV1alpha1 = `version: v1alpha1
machine:
  type: worker
  token: supersecret
  install:
    image: ` + applyImage + `
cluster:
  clusterName: homelab
`
	liveHostname = `apiVersion: v1alpha1
kind: HostnameConfig
hostname: worker-01
`
)

func docs(d ...string) []byte { return []byte(strings.Join(d, "---\n")) }

// applyCluster is one DHCP-primary worker with a static storage NIC,
// so address selection has a plane to skip.
func applyCluster() *config.Cluster {
	dhcp, static, primary := true, false, true
	c := testCluster()
	c.Talos.Nodes = []config.TalosNode{{
		Name: "worker-01", Role: config.RoleWorker, PVENode: "pve-01", VMID: 300,
		Interfaces: []config.NetworkInterface{
			{Name: "net0", MAC: "02:50:99:a2:01:01", Bridge: "vmbr0", DHCP: &dhcp, Primary: &primary},
			{Name: "net1", MAC: "02:50:99:a2:14:01", Bridge: "storbr0", DHCP: &static, Address: "192.0.2.61/24"},
		},
	}}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		panic("applyCluster does not resolve: " + diags.Error())
	}
	return c
}

func workerMember() talos.Member {
	return talos.Member{Hostname: "worker-01", Addresses: []string{"192.0.2.61", "fd00::11", workerAddr}}
}

func TestNodeAddresses(t *testing.T) {
	tests := []struct {
		name    string
		members []talos.Member
		want    string
		wantErr string
	}{
		{
			name:    "skips the storage plane and prefers IPv4",
			members: []talos.Member{workerMember()},
			want:    workerAddr,
		},
		{
			name:    "falls back to IPv6",
			members: []talos.Member{{Hostname: "worker-01", Addresses: []string{"192.0.2.61", "fd00::11"}}},
			want:    "fd00::11",
		},
		{
			name:    "unknown node is named",
			members: []talos.Member{{Hostname: "cp-01", Addresses: []string{"198.51.100.10"}}},
			wantErr: "not found among the cluster members discovery reports: worker-01",
		},
		{
			name:    "only unroutable addresses",
			members: []talos.Member{{Hostname: "worker-01", Addresses: []string{"192.0.2.61"}}},
			wantErr: `talos node "worker-01": discovery reports no dialable address`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := talosmocks.NewMockClient(t)
			client.EXPECT().Members(mock.Anything).Return(tt.members, nil).Once()

			got, err := talos.NodeAddresses(context.Background(), client, applyCluster())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NodeAddresses error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NodeAddresses: %v", err)
			}
			if got["worker-01"] != tt.want {
				t.Errorf("worker-01 address = %q, want %q", got["worker-01"], tt.want)
			}
		})
	}
}

// runApply builds and runs the apply stage for one worker whose live
// config and running schematic are given.
func runApply(t *testing.T, client *talosmocks.MockClient, rendered []byte, dryRun bool) (steps.Result, string, error) {
	t.Helper()
	var out bytes.Buffer
	client.EXPECT().Members(mock.Anything).Return([]talos.Member{workerMember()}, nil).Once()
	a := &talos.Applier{
		Cluster: applyCluster(),
		Client:  client,
		Nodes:   []talos.RenderedNode{{Name: "worker-01", Config: rendered, InstallImage: applyImage}},
		Out:     &out,
	}
	stage, err := a.Steps(context.Background())
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	if len(stage) != 1 || stage[0].Name != "apply-worker-01" {
		t.Fatalf("Steps = %v, want one apply-worker-01 step", stage)
	}
	r := steps.Runner{DryRun: dryRun, Out: &out, Log: discardLogger()}
	res, err := r.Run(context.Background(), stage)
	return res, out.String(), err
}

func expectNode(client *talosmocks.MockClient, schematic string, live []byte) {
	client.EXPECT().Extensions(mock.Anything, workerAddr).
		Return([]talos.Extension{{Name: "iscsi-tools", Version: "v0.2.0"}, {Name: "schematic", Version: schematic}}, nil).Once()
	if live != nil {
		client.EXPECT().MachineConfig(mock.Anything, workerAddr).Return(live, nil).Once()
	}
}

func TestApplierConverged(t *testing.T) {
	live := docs(liveV1alpha1, liveHostname)
	tests := []struct {
		name     string
		rendered []byte
	}{
		{"equal", live},
		{"document reordering is not drift", docs(liveHostname, liveV1alpha1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := talosmocks.NewMockClient(t)
			expectNode(client, applySchematic, live)

			res, out, err := runApply(t, client, tt.rendered, false)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if res.Applied != 0 {
				t.Errorf("applied = %d, want 0", res.Applied)
			}
			if strings.Contains(out, "+++") {
				t.Errorf("converged node printed a diff:\n%s", out)
			}
		})
	}
}

func TestApplierDiffers(t *testing.T) {
	live := docs(liveV1alpha1, liveHostname)
	rendered := docs(liveV1alpha1, strings.ReplaceAll(liveHostname, "worker-01", "worker-99"))

	t.Run("dry-run prints the redacted diff and applies nothing", func(t *testing.T) {
		client := talosmocks.NewMockClient(t)
		expectNode(client, applySchematic, live)

		res, out, err := runApply(t, client, rendered, true)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.Pending != 1 {
			t.Errorf("pending = %d, want 1", res.Pending)
		}
		for _, want := range []string{"+++ worker-01 (rendered)", "- hostname: worker-01", "+ hostname: worker-99"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "supersecret") {
			t.Errorf("output leaks a secret:\n%s", out)
		}
	})

	t.Run("apply sends the whole rendered config", func(t *testing.T) {
		client := talosmocks.NewMockClient(t)
		expectNode(client, applySchematic, live)
		client.EXPECT().ApplyNoReboot(mock.Anything, workerAddr, rendered).Return(nil).Once()

		res, _, err := runApply(t, client, rendered, false)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.Applied != 1 {
			t.Errorf("applied = %d, want 1", res.Applied)
		}
	})

	t.Run("a reboot refusal names the node and the cordon → drain path", func(t *testing.T) {
		client := talosmocks.NewMockClient(t)
		expectNode(client, applySchematic, live)
		client.EXPECT().ApplyNoReboot(mock.Anything, workerAddr, rendered).
			Return(status.Error(codes.FailedPrecondition, "apply requires a reboot")).Once()

		_, _, err := runApply(t, client, rendered, false)
		if err == nil {
			t.Fatal("run succeeded, want the refusal")
		}
		for _, want := range []string{"worker-01 refused the change without a reboot", "kubectl cordon worker-01"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error missing %q: %v", want, err)
			}
		}
	})

	t.Run("other apply errors are wrapped, not called refusals", func(t *testing.T) {
		client := talosmocks.NewMockClient(t)
		expectNode(client, applySchematic, live)
		sentinel := errors.New("connection reset")
		client.EXPECT().ApplyNoReboot(mock.Anything, workerAddr, rendered).Return(sentinel).Once()

		_, _, err := runApply(t, client, rendered, false)
		if !errors.Is(err, sentinel) || strings.Contains(err.Error(), "cordon") {
			t.Errorf("error = %v, want the wrapped sentinel without reboot guidance", err)
		}
	})
}

// TestApplierSchematicMismatch pins that a schematic change is never
// applied: an apply run stops on it, and a dry-run survey reports it
// against the node and moves on.
func TestApplierSchematicMismatch(t *testing.T) {
	want := "talosctl -n " + workerAddr + " upgrade --image " + applyImage
	for _, dryRun := range []bool{false, true} {
		client := talosmocks.NewMockClient(t)
		expectNode(client, "def456", nil)

		_, out, err := runApply(t, client, docs(liveV1alpha1, liveHostname), dryRun)
		report := out
		if !dryRun {
			if err == nil {
				t.Fatal("apply run succeeded, want the schematic mismatch")
			}
			report = err.Error()
		} else if err != nil {
			t.Fatalf("dry-run: %v", err)
		}
		if !strings.Contains(report, want) || !strings.Contains(report, "runs schematic def456") {
			t.Errorf("dryRun=%v report = %q, want it to carry %q", dryRun, report, want)
		}
	}
}

func TestApplierUnresolvableNode(t *testing.T) {
	client := talosmocks.NewMockClient(t)
	client.EXPECT().Members(mock.Anything).Return(nil, nil).Once()
	a := &talos.Applier{
		Cluster: applyCluster(),
		Client:  client,
		Nodes:   []talos.RenderedNode{{Name: "worker-01"}},
	}
	if _, err := a.Steps(context.Background()); err == nil || !strings.Contains(err.Error(), "worker-01") {
		t.Errorf("Steps error = %v, want one naming worker-01", err)
	}
}
