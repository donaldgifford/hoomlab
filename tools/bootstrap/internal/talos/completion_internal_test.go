package talos

import (
	"strings"
	"testing"

	"github.com/siderolabs/go-pointer"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// TestAssertKubePrism is DESIGN-0002's named invariant test, white-box
// because every supported Talos contract enables KubePrism and the
// public path can't produce a config without it: should machinery ever
// generate one — feature regression, future knob — emitting Cilium
// against it must fail, because the validated values point the agent
// at localhost:7445 and nothing would answer there.
func TestAssertKubePrism(t *testing.T) {
	prism := func(enabled bool, port int) *v1alpha1.Config {
		return &v1alpha1.Config{
			MachineConfig: &v1alpha1.MachineConfig{
				MachineFeatures: &v1alpha1.FeaturesConfig{
					KubePrismSupport: &v1alpha1.KubePrism{
						ServerEnabled: pointer.To(enabled),
						ServerPort:    port,
					},
				},
			},
		}
	}

	tests := []struct {
		name    string
		cfg     *v1alpha1.Config
		wantErr bool
	}{
		{"enabled on the default port", prism(true, constants.DefaultKubePrismPort), false},
		{"disabled", prism(false, constants.DefaultKubePrismPort), true},
		{"wrong port", prism(true, 6443), true},
		{
			"feature absent entirely",
			&v1alpha1.Config{MachineConfig: &v1alpha1.MachineConfig{}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := assertKubePrism(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("assertKubePrism() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "KubePrism") {
				t.Errorf("error = %v, want it to name KubePrism", err)
			}
		})
	}
}
