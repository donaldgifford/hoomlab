package talos_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// testCiliumValues satisfies the load-time checks and carries a
// marker key the ConfigMap assertions look for.
const testCiliumValues = `kubeProxyReplacement: true
k8sServiceHost: localhost
k8sServicePort: 7445
ipam:
  mode: kubernetes
`

// ciliumCluster is testCluster with the full completion surface: cni
// cilium and a values file written to a test temp dir (Load would
// have resolved the path absolute; the tests hand it absolute
// directly).
func ciliumCluster(t *testing.T) *config.Cluster {
	t.Helper()
	values := filepath.Join(t.TempDir(), "cilium-values.yaml")
	if err := os.WriteFile(values, []byte(testCiliumValues), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	c := testCluster()
	c.Talos.Cluster = &config.TalosCluster{
		CNI: config.CNICilium,
		Cilium: &config.CiliumConfig{
			Version:           "v1.20.1",
			Values:            values,
			GatewayAPIVersion: "v1.6.1",
		},
	}
	return c
}

// render substitutes the two booty expressions the way booty would —
// the raw template is a text template first and YAML only after.
func render(data []byte, hostname string) []byte {
	out := bytes.ReplaceAll(data, []byte(talos.HostnameVar), []byte(hostname))
	return bytes.ReplaceAll(out, []byte(talos.InstallImageVar),
		[]byte(talos.InstallImage("", talosVersion)))
}

// v1alpha1Doc renders a template for hostname "cp-01" and returns its
// v1alpha1 machine config document.
func v1alpha1Doc(t *testing.T, data []byte) map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(render(data, "cp-01")))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("no cluster document in rendered template (decode: %v)", err)
		}
		if _, ok := doc["cluster"]; ok {
			return doc
		}
	}
}

// dig walks nested YAML maps; a missing step fails the test.
func dig(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for i, step := range path {
		asMap, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig %v: step %q is not a map", path[:i+1], step)
		}
		cur, ok = asMap[step]
		if !ok {
			t.Fatalf("dig %v: key %q missing", path[:i+1], step)
		}
	}
	return cur
}

// TestCompletionAbsentWithoutClusterBlock pins back-compat: a config
// without a cluster block emits exactly the legacy machinery shape —
// no manifests, no rotation, no topology labels.
func TestCompletionAbsentWithoutClusterBlock(t *testing.T) {
	tmpl, err := talos.RoleTemplates(testBundle(t), testCluster())
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}
	// Machinery serializes empty extraManifests/inlineManifests lists
	// even on legacy configs, so absence is asserted by content, not
	// field names.
	for _, marker := range []string{
		"rotate-server-certificates",
		"topology.kubernetes.io/region",
		"cilium",
		"gateway-api",
		"cert-approver",
	} {
		if bytes.Contains(tmpl.ControlPlane, []byte(marker)) {
			t.Errorf("legacy controlplane template unexpectedly contains %q", marker)
		}
	}
}

// TestCompletionCilium asserts the full DESIGN-0002 Cilium delivery
// on both role templates: CNI none + kube-proxy off, the pinned
// Gateway API CRD set ahead of the cert-approver, the values
// ConfigMap and install Job inline, kubelet certificate rotation, and
// the topology labels with the zone riding the hostname expression.
func TestCompletionCilium(t *testing.T) {
	cluster := ciliumCluster(t)
	tmpl, err := talos.RoleTemplates(testBundle(t), cluster)
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}
	if len(tmpl.Warnings) != 0 {
		t.Errorf("validation warnings: %q", tmpl.Warnings)
	}

	for name, data := range map[string][]byte{
		"controlplane": tmpl.ControlPlane,
		"worker":       tmpl.Worker,
	} {
		doc := v1alpha1Doc(t, data)

		if got := dig(t, doc, "cluster", "network", "cni", "name"); got != "none" {
			t.Errorf("%s: cni name = %v, want none", name, got)
		}
		if got := dig(t, doc, "cluster", "proxy", "disabled"); got != true {
			t.Errorf("%s: proxy disabled = %v, want true", name, got)
		}

		// Cilium 1.20's required CRD set: 7 standard-channel files
		// (TLSRoute and BackendTLSPolicy graduated there in Gateway
		// API v1.5), then the cert-approver.
		extras, ok := dig(t, doc, "cluster", "extraManifests").([]any)
		if !ok || len(extras) != 8 {
			t.Fatalf("%s: extraManifests = %v, want 7 CRD URLs + cert-approver", name, extras)
		}
		first, last := extras[0].(string), extras[7].(string)
		if !strings.Contains(first, "gateway-api/v1.6.1/config/crd/standard/") ||
			!strings.HasSuffix(first, "gatewayclasses.yaml") {
			t.Errorf("%s: extraManifests[0] = %q, want the pinned standard-channel gatewayclasses CRD first", name, first)
		}
		for _, graduated := range []string{"backendtlspolicies", "tlsroutes"} {
			found := false
			for _, e := range extras {
				url := e.(string)
				if strings.Contains(url, "/standard/") && strings.Contains(url, graduated) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: extraManifests missing the standard-channel %s CRD", name, graduated)
			}
		}
		if !strings.Contains(last, "kubelet-serving-cert-approver") {
			t.Errorf("%s: extraManifests[7] = %q, want the cert-approver", name, last)
		}

		inline, ok := dig(t, doc, "cluster", "inlineManifests").([]any)
		if !ok || len(inline) != 2 {
			t.Fatalf("%s: inlineManifests = %v, want cilium-values + cilium-bootstrap", name, inline)
		}
		values := inline[0].(map[string]any)
		if got := values["name"]; got != "cilium-values" {
			t.Errorf("%s: inlineManifests[0] name = %v, want cilium-values", name, got)
		}
		if contents := values["contents"].(string); !strings.Contains(contents, "kubeProxyReplacement: true") {
			t.Errorf("%s: cilium-values ConfigMap does not carry the operator values:\n%s", name, contents)
		}
		install := inline[1].(map[string]any)["contents"].(string)
		for _, want := range []string{
			"quay.io/cilium/cilium-cli:v0.19.7",
			"--version=v1.20.1",
			"backoffLimit: 10",
			"fieldPath: status.podIP",
		} {
			if !strings.Contains(install, want) {
				t.Errorf("%s: cilium-bootstrap manifest is missing %q", name, want)
			}
		}

		labels := dig(t, doc, "machine", "nodeLabels").(map[string]any)
		if got := labels["topology.kubernetes.io/region"]; got != cluster.Name {
			t.Errorf("%s: region label = %v, want %q", name, got, cluster.Name)
		}
		// v1alpha1Doc rendered the template for hostname cp-01; the
		// zone matching it proves the label rides the hostname var.
		if got := labels["topology.kubernetes.io/zone"]; got != "cp-01" {
			t.Errorf("%s: zone label = %v, want the rendered hostname cp-01", name, got)
		}
		_, excluded := labels["node.kubernetes.io/exclude-from-external-load-balancers"]
		if wantExcluded := name == "controlplane"; excluded != wantExcluded {
			t.Errorf("%s: exclude-from-external-load-balancers present = %v, want %v",
				name, excluded, wantExcluded)
		}

		if got := dig(t, doc, "machine", "kubelet", "extraArgs", "rotate-server-certificates"); got != true && got != "true" {
			t.Errorf("%s: rotate-server-certificates = %v, want true", name, got)
		}
	}
}

// TestCompletionCiliumRoundTrip renders the cilium controlplane
// template the way booty would and re-validates it with machinery —
// proving the completion surface (zone label value included) survives
// substitution as a loadable, valid metal config.
func TestCompletionCiliumRoundTrip(t *testing.T) {
	tmpl, err := talos.RoleTemplates(testBundle(t), ciliumCluster(t))
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}

	image := talos.InstallImage("", talosVersion)
	rendered := strings.ReplaceAll(string(tmpl.ControlPlane), talos.HostnameVar, "cp-01")
	rendered = strings.ReplaceAll(rendered, talos.InstallImageVar, image)

	provider, err := configloader.NewFromBytes([]byte(rendered))
	if err != nil {
		t.Fatalf("machinery load of rendered config: %v", err)
	}
	warnings, err := provider.Validate(metalMode{})
	if err != nil {
		t.Errorf("rendered config invalid in metal mode: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("rendered config warnings: %q", warnings)
	}
	if got := provider.Cluster().Network().CNI().Name(); got != "none" {
		t.Errorf("rendered cni = %q, want none", got)
	}
}

// TestCompletionCNINone covers the bring-your-own-CNI escape hatch:
// CNI none without any Cilium delivery, completion knobs still on.
func TestCompletionCNINone(t *testing.T) {
	cluster := testCluster()
	cluster.Talos.Cluster = &config.TalosCluster{CNI: config.CNINone}
	tmpl, err := talos.RoleTemplates(testBundle(t), cluster)
	if err != nil {
		t.Fatalf("RoleTemplates: %v", err)
	}

	doc := v1alpha1Doc(t, tmpl.Worker)
	if got := dig(t, doc, "cluster", "network", "cni", "name"); got != "none" {
		t.Errorf("cni name = %v, want none", got)
	}
	clusterSection := doc["cluster"].(map[string]any)
	if inline, ok := clusterSection["inlineManifests"].([]any); ok && len(inline) > 0 {
		t.Errorf("cni none emitted inline manifests: %v", inline)
	}
	if proxy, ok := clusterSection["proxy"].(map[string]any); ok && proxy["disabled"] == true {
		t.Error("cni none disabled kube-proxy — that choice belongs to the operator's CNI")
	}
	if !bytes.Contains(tmpl.Worker, []byte("rotate-server-certificates")) {
		t.Error("completion knobs missing under cni none")
	}
}

// TestCompletionMissingValuesFile pins the direct-call path: Load
// normally validates the file exists, but RoleTemplates must still
// fail loudly rather than seal an empty ConfigMap.
func TestCompletionMissingValuesFile(t *testing.T) {
	cluster := ciliumCluster(t)
	cluster.Talos.Cluster.Cilium.Values = filepath.Join(t.TempDir(), "nope.yaml")
	_, err := talos.RoleTemplates(testBundle(t), cluster)
	if err == nil || !strings.Contains(err.Error(), "read cilium values") {
		t.Errorf("RoleTemplates error = %v, want a read cilium values failure", err)
	}
}

// TestCompletionDeterministic extends the byte-stability invariant to
// the completion surface — maps feed the emitted YAML (labels,
// kubelet args), and map iteration order must never leak into bytes.
func TestCompletionDeterministic(t *testing.T) {
	cluster := ciliumCluster(t)
	bundle := testBundle(t)
	first, err := talos.RoleTemplates(bundle, cluster)
	if err != nil {
		t.Fatalf("first RoleTemplates: %v", err)
	}
	for i := range 3 {
		again, err := talos.RoleTemplates(bundle, cluster)
		if err != nil {
			t.Fatalf("RoleTemplates round %d: %v", i, err)
		}
		if !bytes.Equal(first.ControlPlane, again.ControlPlane) || !bytes.Equal(first.Worker, again.Worker) {
			t.Fatalf("templates differ between renders (round %d)", i)
		}
	}
}
