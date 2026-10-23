package config

import "slices"

// Runtime is one row of the runtime table (DESIGN-0005 Phase 4): a
// container runtime a node can run because an extension baked into its
// image provides it. Capability is decided at image-build time by the
// node's profiles; which pods use it is the workload's choice
// (runtimeClassName), never the node's.
type Runtime struct {
	// Name is the runtime's short name, the last element of its label.
	Name string
	// Extension is the Image Factory extension that provides it.
	Extension string
	// Handler is the CRI handler a RuntimeClass names.
	Handler string
	// WorkerOnly rejects the runtime on control planes, which stay
	// minimal and are tainted against workloads anyway.
	WorkerOnly bool
}

// runtimes is the table. Adding a runtime is one row plus its rules
// (gvisor, crun, and nvidia are deferred — DESIGN-0005 Non-Goals).
var runtimes = []Runtime{
	{Name: "kata", Extension: "siderolabs/kata-containers", Handler: "kata", WorkerOnly: true},
}

// runtimeLabelPrefix is the namespace of the derived runtime labels,
// a subdomain of LabelPrefix that no explicit label may use.
const runtimeLabelPrefix = "runtime." + LabelPrefix

// Label is the key of the label derived on every node that carries
// the runtime, e.g. runtime.hoomlab.dev/kata. It exists because the
// Talos extension label carries a version: a RuntimeClass selecting on
// it would strand every pod at the next extension bump.
func (r Runtime) Label() string { return runtimeLabelPrefix + r.Name }

// NodeExtensions flattens a node's profile references into its
// extension set: the union of every referenced profile's extensions,
// sorted and deduped, so composable profiles ("base" + "kata") yield
// one canonical set regardless of declaration order. Validation
// guarantees the references resolve; an empty result means the
// vanilla image. The canonical order is what makes the set a stable
// identity — two nodes with the same profiles always produce the same
// factory schematic.
func (t *Talos) NodeExtensions(n *TalosNode) []string {
	var exts []string
	for _, ref := range n.Profiles {
		for i := range t.Profiles {
			if t.Profiles[i].Name == ref {
				exts = append(exts, t.Profiles[i].Extensions...)
			}
		}
	}
	if len(exts) == 0 {
		return nil
	}
	return slices.Compact(slices.Sorted(slices.Values(exts)))
}

// NodeRuntimes returns the runtimes a node's extensions provide, in
// table order.
func (t *Talos) NodeRuntimes(n *TalosNode) []Runtime {
	exts := t.NodeExtensions(n)
	var out []Runtime
	for _, r := range runtimes {
		if slices.Contains(exts, r.Extension) {
			out = append(out, r)
		}
	}
	return out
}
