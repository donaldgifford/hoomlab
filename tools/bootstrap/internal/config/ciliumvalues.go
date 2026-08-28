package config

import (
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"gopkg.in/yaml.v3"
)

// The KubePrism invariant (DESIGN-0002): the Cilium values must point
// the agent at Talos's KubePrism endpoint, the only API server address
// that exists on every node before the CNI does.
const (
	kubePrismHost = "localhost"
	kubePrismPort = 7445
)

// resolveCiliumValues rewrites the cilium values path absolute
// (relative means relative to the config file, like every operator
// path) and validates the file's content. Called by Load; a nil
// return means no cilium block or a values file that passed.
func (c *Cluster) resolveCiliumValues(baseDir string) hcl.Diagnostics {
	tc := c.Talos.Cluster
	if tc == nil || tc.Cilium == nil {
		return nil
	}
	if !filepath.IsAbs(tc.Cilium.Values) {
		tc.Cilium.Values = filepath.Join(baseDir, tc.Cilium.Values)
	}
	data, err := os.ReadFile(tc.Cilium.Values)
	if err != nil {
		return hcl.Diagnostics{errf("Unreadable cilium values file",
			"talos cluster cilium: values file: %v.", err)}
	}
	return validateCiliumValues(tc.Cilium.Values, data)
}

// validateCiliumValues is the structural check DESIGN-0002 demands of
// the operator values before they are sealed into machineconfigs:
// unvalidated values rot silently (the archive shipped
// "algorithm: maglev" as a bogus top-level key for its whole life
// after an indentation slip), and two settings are load-bearing for
// bootstrap itself — kube-proxy replacement, because the emitted
// configs disable kube-proxy, and the KubePrism endpoint, because no
// other API server address exists before the CNI is up.
func validateCiliumValues(path string, data []byte) hcl.Diagnostics {
	var diags hcl.Diagnostics

	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return hcl.Diagnostics{errf("Invalid cilium values file",
			"%s: not valid YAML: %v.", path, err)}
	}

	for _, key := range slices.Sorted(maps.Keys(top)) {
		if top[key] == nil {
			diags = append(diags, errf(
				"Null top-level cilium value",
				"%s: %q has no value — usually a lost indent that turned a nested setting into a bogus top-level key, which the chart silently ignores.",
				path,
				key,
			))
		}
	}

	if v, ok := top["kubeProxyReplacement"].(bool); !ok || !v {
		diags = append(diags, errf(
			"Cilium values do not replace kube-proxy",
			"%s: kubeProxyReplacement must be true — the emitted machineconfigs disable kube-proxy, so a Cilium that doesn't replace it strands every Service.",
			path,
		))
	}

	if host, ok := top["k8sServiceHost"].(string); !ok || host != kubePrismHost {
		diags = append(diags, errf("Cilium values miss the KubePrism host",
			"%s: k8sServiceHost must be %q — Talos's KubePrism is the only API server address that exists on every node before the CNI does.",
			path, kubePrismHost))
	}
	if port, ok := top["k8sServicePort"].(int); !ok || port != kubePrismPort {
		diags = append(diags, errf("Cilium values miss the KubePrism port",
			"%s: k8sServicePort must be %d (Talos's KubePrism port).",
			path, kubePrismPort))
	}
	return diags
}
