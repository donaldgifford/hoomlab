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
	return validateCiliumValues(tc.Cilium.Values, data, c.carriesRuntime("kata"))
}

// carriesRuntime reports whether any node's extensions provide the
// named runtime.
func (c *Cluster) carriesRuntime(name string) bool {
	for i := range c.Talos.Nodes {
		for _, r := range c.Talos.NodeRuntimes(&c.Talos.Nodes[i]) {
			if r.Name == name {
				return true
			}
		}
	}
	return false
}

// validateCiliumValues is the structural check DESIGN-0002 demands of
// the operator values before they are sealed into machineconfigs:
// unvalidated values rot silently (the archive shipped
// "algorithm: maglev" as a bogus top-level key for its whole life
// after an indentation slip), and two settings are load-bearing for
// bootstrap itself — kube-proxy replacement, because the emitted
// configs disable kube-proxy, and the KubePrism endpoint, because no
// other API server address exists before the CNI is up. With Kata on
// any node, the Kata invariants join them (kataCiliumValues).
func validateCiliumValues(path string, data []byte, kata bool) hcl.Diagnostics {
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
	if kata {
		diags = append(diags, kataCiliumValues(path, top)...)
	}
	return diags
}

// kataCiliumValues checks the two Cilium settings Kata depends on
// (DESIGN-0005 Phase 4). Socket-level load balancing must stay in the
// host namespace: a Kata pod's sockets live in the guest kernel, which
// Cilium's socket hooks can't see, so pod-namespace socket LB silently
// breaks Service traffic from Kata pods. Gateway API forces it on in
// Cilium 1.20 today, but that is an implementation detail, so the
// values must say it explicitly. The bandwidth manager is incompatible
// with Kata (talos#13745).
func kataCiliumValues(path string, top map[string]any) hcl.Diagnostics {
	var diags hcl.Diagnostics
	if v, ok := nested(top, "socketLB", "hostNamespaceOnly").(bool); !ok || !v {
		diags = append(diags, errf(
			"Cilium values miss socketLB.hostNamespaceOnly with Kata",
			"%s: socketLB.hostNamespaceOnly must be set to true explicitly while any node carries the kata extension — Kata pods' sockets live in the guest kernel, out of reach of pod-namespace socket load balancing.",
			path,
		))
	}
	if v, ok := nested(top, "bandwidthManager", "enabled").(bool); ok && v {
		diags = append(diags, errf(
			"Cilium bandwidth manager enabled with Kata",
			"%s: bandwidthManager.enabled must not be true while any node carries the kata extension — it is incompatible with Kata (talos#13745).",
			path,
		))
	}
	return diags
}

// nested reads a value at a path of map keys, nil when any step is
// missing or not a map.
func nested(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}
