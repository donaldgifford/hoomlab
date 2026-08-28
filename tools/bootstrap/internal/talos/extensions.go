package talos

import (
	"slices"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// NodeExtensions flattens a node's profile references into its
// extension set: the union of every referenced profile's extensions,
// sorted and deduped, so composable profiles ("base" + "gpu") yield
// one canonical set regardless of declaration order. Validation
// guarantees the references resolve; an empty result means the
// vanilla image. The canonical order is what makes the set a stable
// identity — two nodes with the same profiles always produce the same
// factory schematic.
func NodeExtensions(t *config.Talos, node *config.TalosNode) []string {
	var exts []string
	for _, ref := range node.Profiles {
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
