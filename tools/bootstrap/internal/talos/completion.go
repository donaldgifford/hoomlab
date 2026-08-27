package talos

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/siderolabs/go-pointer"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// The completion-surface default pins (DESIGN-0002). Overridable ones
// live in the config; these are the values this CLI was tested
// against.
const (
	// defaultCiliumCLIVersion is the cilium-cli image tag the install
	// Job runs when the config doesn't pin cli_version.
	defaultCiliumCLIVersion = "v0.19.7"
	// certApproverVersion pins the kubelet-serving-cert-approver
	// manifest paired with rotate-server-certificates: kubelets then
	// request serving certificates that need an approver from minute
	// one, so the pair always ships together (DESIGN-0002 OQ-D).
	certApproverVersion = "v0.11.1"
)

// The topology labels every completion-surface node carries
// (DESIGN-0002 node labels): region is the cluster, zone is the node,
// which makes topology spread constraints meaningful on a small
// cluster.
const (
	labelTopologyRegion = "topology.kubernetes.io/region"
	labelTopologyZone   = "topology.kubernetes.io/zone"
)

// certApproverManifestURL is the pinned kubelet-serving-cert-approver
// standalone install manifest.
func certApproverManifestURL() string {
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/alex1989hu/kubelet-serving-cert-approver/%s/deploy/standalone-install.yaml",
		certApproverVersion)
}

// gatewayAPICRDURLs is the pinned Gateway API CRD set Cilium's
// Gateway API support requires, delivered via extraManifests so the
// CRDs exist before anything ships a Gateway (DESIGN-0002 T2→T3
// contract). The list matches Cilium 1.20's installation docs and
// assumes the v1.5+ channel layout — TLSRoute and BackendTLSPolicy
// graduated to the standard channel there, which is why config
// validation floors gateway_api_version at v1.5 (the archive's Gen 2
// era pulled TLSRoute from the experimental channel instead).
func gatewayAPICRDURLs(version string) []string {
	base := "https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/" + version + "/config/crd/standard/"
	return []string{
		base + "gateway.networking.k8s.io_gatewayclasses.yaml",
		base + "gateway.networking.k8s.io_gateways.yaml",
		base + "gateway.networking.k8s.io_httproutes.yaml",
		base + "gateway.networking.k8s.io_referencegrants.yaml",
		base + "gateway.networking.k8s.io_grpcroutes.yaml",
		base + "gateway.networking.k8s.io_backendtlspolicies.yaml",
		base + "gateway.networking.k8s.io_tlsroutes.yaml",
	}
}

// applyCompletion mutates the machinery-generated config with the
// cluster-completion surface (DESIGN-0002). A config without a
// cluster block is left exactly as machinery generated it — the block
// is the opt-in. The zone label carries the hostname placeholder and
// rides the same templatize swap as the hostname itself. Role
// asymmetries need no handling here: generate already gives control
// planes the exclude-from-external-load-balancers label, which the
// label merge preserves.
func applyCompletion(cfg *v1alpha1.Config, cluster *config.Cluster) error {
	tc := cluster.Talos.Cluster
	if tc == nil {
		return nil
	}

	// Merge, never replace: generate already labels control planes
	// with node.kubernetes.io/exclude-from-external-load-balancers.
	if cfg.MachineConfig.MachineNodeLabels == nil {
		cfg.MachineConfig.MachineNodeLabels = map[string]string{}
	}
	cfg.MachineConfig.MachineNodeLabels[labelTopologyRegion] = cluster.Name
	cfg.MachineConfig.MachineNodeLabels[labelTopologyZone] = hostnamePlaceholder

	// Kubelet serving-certificate rotation, paired with its approver:
	// rotation without an approver strands every kubelet CSR as
	// Pending.
	if cfg.MachineConfig.MachineKubelet == nil {
		cfg.MachineConfig.MachineKubelet = &v1alpha1.KubeletConfig{}
	}
	if cfg.MachineConfig.MachineKubelet.KubeletExtraArgs == nil {
		cfg.MachineConfig.MachineKubelet.KubeletExtraArgs = v1alpha1.Args{}
	}
	cfg.MachineConfig.MachineKubelet.KubeletExtraArgs["rotate-server-certificates"] = v1alpha1.NewArgValue("true", nil)
	cfg.ClusterConfig.ExtraManifests = append(cfg.ClusterConfig.ExtraManifests, certApproverManifestURL())

	switch tc.CNI {
	case "", config.CNIFlannel:
		// The machinery default; the completion knobs above are the
		// whole surface.
	case config.CNINone:
		cfg.ClusterConfig.ClusterNetwork.CNI = &v1alpha1.CNIConfig{CNIName: constants.NoneCNI}
	case config.CNICilium:
		if err := assertKubePrism(cfg); err != nil {
			return err
		}
		cfg.ClusterConfig.ClusterNetwork.CNI = &v1alpha1.CNIConfig{CNIName: constants.NoneCNI}
		cfg.ClusterConfig.ProxyConfig = &v1alpha1.ProxyConfig{Disabled: pointer.To(true)}
		cfg.ClusterConfig.ExtraManifests = append(
			gatewayAPICRDURLs(tc.Cilium.GatewayAPIVersion),
			cfg.ClusterConfig.ExtraManifests...)
		inline, err := ciliumInlineManifests(tc.Cilium)
		if err != nil {
			return err
		}
		cfg.ClusterConfig.ClusterInlineManifests = inline
	default:
		// Load validation rejects everything else; reaching here is a
		// programming error.
		return fmt.Errorf("unhandled cni %q", tc.CNI)
	}
	return nil
}

// assertKubePrism is the DESIGN-0002 invariant as code: the Cilium
// values are validated to say k8sServiceHost localhost:7445, which is
// only true because Talos's KubePrism listens there. If a future
// change generates configs without it, Cilium would come up with no
// API server to talk to — so emit refuses instead.
func assertKubePrism(cfg *v1alpha1.Config) error {
	features := cfg.MachineConfig.MachineFeatures
	if features == nil || features.KubePrismSupport == nil ||
		!pointer.SafeDeref(features.KubePrismSupport.ServerEnabled) ||
		features.KubePrismSupport.ServerPort != constants.DefaultKubePrismPort {
		return fmt.Errorf(
			"cilium requires KubePrism on port %d (the validated values point the agent at localhost:%d), but the generated machine config does not enable it",
			constants.DefaultKubePrismPort,
			constants.DefaultKubePrismPort,
		)
	}
	return nil
}

// ciliumInlineManifests renders the two inline manifests of the Gen 2
// install pattern: the operator's validated values as a ConfigMap,
// and the cilium-install Job that consumes it. Talos applies both at
// bootstrap; the cluster converges to networked with zero operator
// action.
func ciliumInlineManifests(cilium *config.CiliumConfig) ([]v1alpha1.ClusterInlineManifest, error) {
	values, err := os.ReadFile(cilium.Values)
	if err != nil {
		return nil, fmt.Errorf("read cilium values: %w", err)
	}
	valuesManifest, err := ciliumValuesConfigMap(values)
	if err != nil {
		return nil, err
	}

	cliVersion := cilium.CLIVersion
	if cliVersion == "" {
		cliVersion = defaultCiliumCLIVersion
	}
	var job bytes.Buffer
	if err := ciliumInstallTemplate.Execute(&job, map[string]string{
		"CLIVersion":    cliVersion,
		"CiliumVersion": cilium.Version,
	}); err != nil {
		return nil, fmt.Errorf("render cilium-install manifest: %w", err)
	}

	return []v1alpha1.ClusterInlineManifest{
		{InlineManifestName: "cilium-values", InlineManifestContents: valuesManifest},
		{InlineManifestName: "cilium-bootstrap", InlineManifestContents: job.String()},
	}, nil
}

// ciliumValuesConfigMap wraps the operator values file in the
// ConfigMap the install Job mounts. Marshalled, not templated, so the
// values bytes survive verbatim regardless of their shape.
func ciliumValuesConfigMap(values []byte) (string, error) {
	manifest := struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Data map[string]string `yaml:"data"`
	}{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Data:       map[string]string{"values.yaml": string(values)},
	}
	manifest.Metadata.Name = "cilium-values"
	manifest.Metadata.Namespace = "kube-system"

	out, err := yaml.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("marshal cilium-values ConfigMap: %w", err)
	}
	return string(out), nil
}

// ciliumInstallTemplate is the cilium-install Job and its RBAC,
// adopted from the archive's proven Gen 2 pattern. The load-bearing
// details: the Job tolerates everything and pins itself to a control
// plane; it reaches the API server via its own node's address
// (status.podIP with hostNetwork) on 6443 because neither CNI nor
// kube-proxy exists yet; backoffLimit 10 retries until the API server
// answers; kubeProxyReplacement is enforced by the validated values.
var ciliumInstallTemplate = template.Must(template.New("cilium-install").Parse(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cilium-install
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
  - kind: ServiceAccount
    name: cilium-install
    namespace: kube-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cilium-install
  namespace: kube-system
---
apiVersion: batch/v1
kind: Job
metadata:
  name: cilium-install
  namespace: kube-system
spec:
  backoffLimit: 10
  template:
    metadata:
      labels:
        app: cilium-install
    spec:
      restartPolicy: OnFailure
      tolerations:
        - operator: Exists
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: node-role.kubernetes.io/control-plane
                    operator: Exists
      serviceAccountName: cilium-install
      hostNetwork: true
      containers:
        - name: cilium-install
          image: quay.io/cilium/cilium-cli:{{ .CLIVersion }}
          env:
            - name: KUBERNETES_SERVICE_HOST
              valueFrom:
                fieldRef:
                  apiVersion: v1
                  fieldPath: status.podIP
            - name: KUBERNETES_SERVICE_PORT
              value: "6443"
          volumeMounts:
            - name: values
              mountPath: /root/app/values.yaml
              subPath: values.yaml
          command:
            - cilium
            - install
            - --version={{ .CiliumVersion }}
            - --values
            - values.yaml
      volumes:
        - name: values
          configMap:
            name: cilium-values
`))
