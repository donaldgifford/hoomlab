package config

import (
	"fmt"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/donaldgifford/hclkit/pkg/hclkit"
	"github.com/donaldgifford/hclkit/pkg/hclkit/validate"
)

// errExactlyOneCluster is the diagnostic summary for a config with
// zero or multiple cluster blocks; tests assert on the same text.
const errExactlyOneCluster = "Exactly one cluster block required"

// Load reads the bootstrap config file at path and decodes it into the
// schema, resolving env() references against the process environment.
// It returns the file's single cluster with every node MAC rewritten
// to the canonical NormalizeMAC form; a file with zero or multiple
// cluster blocks is an error. Decode problems (syntax, missing
// attributes, unresolvable env() references, duplicate vmid/mac
// literals) come back position-anchored; semantic violations
// (Cluster.Validate) follow without positions. Render everything with
// Diagnostics.WriteTo.
func Load(path string) (*Cluster, hclkit.Diagnostics) {
	loader := hclkit.New(
		hclkit.WithFunctions(map[string]function.Function{
			"env": envFunc(nil),
		}),
		hclkit.WithValidators(
			validate.NewUniqueValidator("node", "vmid"),
			validate.NewUniqueValidator("node", "mac"),
		),
	)

	var root Root
	diags := loader.LoadFile(path, &root)
	if diags.HasErrors() {
		return nil, diags
	}

	if n := len(root.Clusters); n != 1 {
		return nil, hclkit.NewDiagnostics(hcl.Diagnostics{{
			Severity: hcl.DiagError,
			Summary:  errExactlyOneCluster,
			Detail: fmt.Sprintf(
				"%s declares %d cluster blocks; the bootstrap CLI operates on exactly one.",
				path, n),
		}}, nil)
	}

	cluster := &root.Clusters[0]
	if semDiags := cluster.ValidateAndNormalize(); semDiags.HasErrors() {
		// Keep any decode warnings in front of the semantic errors.
		diags.Diagnostics = append(diags.Diagnostics, semDiags...)
		return nil, diags
	}
	// The cilium values file is operator input the same way the config
	// is, so it is validated at load, not first discovered broken at
	// emit. Relative paths resolve against the config file's directory.
	if vDiags := cluster.resolveCiliumValues(filepath.Dir(path)); vDiags.HasErrors() {
		diags.Diagnostics = append(diags.Diagnostics, vDiags...)
		return nil, diags
	}
	return cluster, diags
}
