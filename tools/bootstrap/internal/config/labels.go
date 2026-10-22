package config

import (
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

// The labels the render itself puts on nodes: region and zone with
// the DESIGN-0002 completion surface (a cluster block), and the
// load-balancer exclusion machinery generates on control planes. An
// explicit label with one of these keys would be a second source for
// the same fact, so it is an error, never an override.
const (
	LabelTopologyRegion        = "topology.kubernetes.io/region"
	LabelTopologyZone          = "topology.kubernetes.io/zone"
	LabelExcludeFromExternalLB = "node.kubernetes.io/exclude-from-external-load-balancers"
)

// Kubernetes label syntax (apimachinery's IsQualifiedName and
// IsValidLabelValue): a name or value is at most 63 characters of
// alphanumerics, '-', '_', and '.', starting and ending alphanumeric;
// a key's optional prefix is a DNS-1123 subdomain of at most 253.
const (
	labelNameMax   = 63
	labelPrefixMax = 253
)

var (
	labelNamePattern   = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)
	labelPrefixPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

// The NodeRestriction admission plugin's allow-list for the
// kubernetes.io and k8s.io namespaces (kubelet's IsKubeletLabel): a
// node may set these exact keys, and any key under these namespaces
// or their subdomains. Every other key in those namespaces is refused.
var (
	kubeletLabels = []string{
		"kubernetes.io/hostname",
		"kubernetes.io/arch",
		"kubernetes.io/os",
		"beta.kubernetes.io/arch",
		"beta.kubernetes.io/os",
		"beta.kubernetes.io/instance-type",
		"failure-domain.beta.kubernetes.io/region",
		"failure-domain.beta.kubernetes.io/zone",
		"topology.kubernetes.io/region",
		"topology.kubernetes.io/zone",
		"node.kubernetes.io/instance-type",
	}
	kubeletLabelNamespaces = []string{"kubelet.kubernetes.io", "node.kubernetes.io"}
)

// validateLabels checks every node's explicit labels: Kubernetes
// syntax for keys and values, NodeRestriction's restricted namespaces,
// and collisions with the labels the render already sets. Each rule
// fails at load, where the error can name the node and the key —
// instead of on the node, as a kubelet retry loop.
func (c *Cluster) validateLabels() hcl.Diagnostics {
	var diags hcl.Diagnostics
	for i := range c.Talos.Nodes {
		n := &c.Talos.Nodes[i]
		keys := make([]string, 0, len(n.Labels))
		for k := range n.Labels {
			keys = append(keys, k)
		}
		slices.Sort(keys) // one diagnostic order for one config
		rendered := c.renderedLabels(n)
		for _, k := range keys {
			diags = append(diags, validateLabel(n.Name, k, n.Labels[k], rendered)...)
		}
	}
	return diags
}

// renderedLabels is the set of keys the render puts on node n.
func (c *Cluster) renderedLabels(n *TalosNode) []string {
	var keys []string
	if c.Talos.Cluster != nil {
		keys = append(keys, LabelTopologyRegion, LabelTopologyZone)
	}
	if n.Role == RoleControlPlane {
		keys = append(keys, LabelExcludeFromExternalLB)
	}
	return keys
}

// validateLabel checks one label; syntax first, since the namespace
// rules read the key's prefix.
func validateLabel(node, key, value string, rendered []string) hcl.Diagnostics {
	var diags hcl.Diagnostics
	if err := labelKeySyntax(key); err != "" {
		diags = append(diags, errf("Invalid label key",
			"talos node %q: label key %q %s.", node, key, err))
	}
	if value != "" && (len(value) > labelNameMax || !labelNamePattern.MatchString(value)) {
		diags = append(diags, errf("Invalid label value",
			"talos node %q: label %q value %q must be at most %d characters of alphanumerics, '-', '_', and '.', starting and ending alphanumeric.",
			node, key, value, labelNameMax))
	}
	if diags.HasErrors() {
		return diags
	}

	switch {
	case slices.Contains(rendered, key):
		diags = append(diags, errf("Label collides with a rendered label",
			"talos node %q: label %q is set by the render itself; drop it from labels.", node, key))
	case nodeRestricted(key):
		diags = append(diags, errf(
			"Label refused by NodeRestriction",
			"talos node %q: label %q is in a kubernetes.io / k8s.io namespace the NodeRestriction admission plugin refuses to let a node set; the kubelet would retry it forever. Use a namespace of your own, e.g. %q.",
			node,
			key,
			LabelPrefix+labelName(key),
		))
	}
	return diags
}

// labelKeySyntax returns why key is not a qualified name, or "".
func labelKeySyntax(key string) string {
	prefix, name, hasPrefix := strings.Cut(key, "/")
	if !hasPrefix {
		name, prefix = prefix, ""
	}
	switch {
	case hasPrefix && (prefix == "" || len(prefix) > labelPrefixMax || !labelPrefixPattern.MatchString(prefix)):
		return "has a prefix that is not a DNS subdomain (lowercase alphanumerics, '-', and '.')"
	case len(name) > labelNameMax || !labelNamePattern.MatchString(name):
		return "has a name that is not at most 63 characters of alphanumerics, '-', '_', and '.', starting and ending alphanumeric"
	}
	return ""
}

// labelName is the key without its prefix.
func labelName(key string) string {
	if _, name, ok := strings.Cut(key, "/"); ok {
		return name
	}
	return key
}

// nodeRestricted reports whether NodeRestriction refuses a node
// setting key: a key in the kubernetes.io or k8s.io namespaces (or
// their subdomains — node-role.kubernetes.io and
// node-restriction.kubernetes.io among them) that is not on the
// kubelet allow-list.
func nodeRestricted(key string) bool {
	ns, _, ok := strings.Cut(key, "/")
	if !ok || (!inNamespace(ns, "kubernetes.io") && !inNamespace(ns, "k8s.io")) {
		return false
	}
	if inNamespace(ns, "node-restriction.kubernetes.io") {
		return true
	}
	if slices.Contains(kubeletLabels, key) {
		return false
	}
	return !slices.ContainsFunc(kubeletLabelNamespaces, func(allowed string) bool { return inNamespace(ns, allowed) })
}

// inNamespace reports whether ns is base or a subdomain of it.
func inNamespace(ns, base string) bool {
	return ns == base || strings.HasSuffix(ns, "."+base)
}
