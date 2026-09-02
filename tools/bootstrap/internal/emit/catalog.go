package emit

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// The catalog file names. booty loads every *.hcl in the directory and
// merges them, so the numeric prefixes are ordering for humans only.
const (
	variablesPath = "catalog/00-variables.hcl"
	profilesPath  = "catalog/10-profiles.hcl"
	groupsPath    = "catalog/20-groups.hcl"
)

// profileFor maps a config role to the base catalog profile name.
func profileFor(role config.Role) string {
	if role == config.RoleWorker {
		return "talos-worker"
	}
	return "talos-control"
}

// catalogData is the model for the three catalog templates.
type catalogData struct {
	ClusterName  string
	TalosVersion string
	BootyURL     string
	Profiles     []catalogProfile
	Nodes        []catalogGroup
}

// catalogProfile is one boot recipe — a node class in DESIGN-0002
// terms: a role plus a schematic. The machineconfig template is
// shared per role (the installer image is a profile var, so classes
// differ only in vars and boot paths), while the kernel, initramfs,
// and installer image are the schematic's own.
type catalogProfile struct {
	Name         string
	Role         string
	Template     string
	Schematic    string
	InstallImage string
}

// catalogGroup binds one VM to a profile by MAC. ExtraVars carries
// the per-node interface identity (each slot's MAC, static slots'
// addresses) the multi-interface machineconfig templates consume —
// empty on single-interface nodes, whose templates read only
// hostname (DESIGN-0004 OQ-2).
type catalogGroup struct {
	Name      string
	Profile   string
	MAC       string
	ExtraVars []catalogVar
}

// catalogVar is one rendered group var beside hostname.
type catalogVar struct {
	Key   string
	Value string
}

// renderCatalog renders the three catalog files for the cluster.
// perNode carries each node's resolved schematic ID, indexed like
// cluster.Talos.Nodes; the distinct (role, schematic) pairs become
// the profiles. A role with a single class keeps its plain profile
// name; only when profiles split a role into classes does the
// schematic's short ID join the name.
func renderCatalog(cluster *config.Cluster, perNode []string) (map[string]File, error) {
	if len(perNode) != len(cluster.Talos.Nodes) {
		return nil, fmt.Errorf(
			"renderCatalog: %d schematics for %d nodes", len(perNode), len(cluster.Talos.Nodes))
	}
	data := catalogData{
		ClusterName:  cluster.TalosName(),
		TalosVersion: cluster.Talos.Version,
		BootyURL:     cluster.Talos.Booty.URL,
	}

	templateFor := map[config.Role]string{
		config.RoleControlPlane: controlPlaneTemplatePath,
		config.RoleWorker:       workerTemplatePath,
	}
	type class struct {
		role      config.Role
		schematic string
	}
	classesPerRole := make(map[config.Role]int)
	profileNames := make(map[class]string)
	var classes []class
	for i := range cluster.Talos.Nodes {
		c := class{role: cluster.Talos.Nodes[i].Role, schematic: perNode[i]}
		if _, seen := profileNames[c]; seen {
			continue
		}
		profileNames[c] = "" // named below, once the class count per role is known
		classesPerRole[c.role]++
		classes = append(classes, c)
	}
	for _, c := range classes {
		name := profileFor(c.role)
		if classesPerRole[c.role] > 1 {
			name += "-" + c.schematic[:8]
		}
		profileNames[c] = name
		data.Profiles = append(data.Profiles, catalogProfile{
			Name:         name,
			Role:         string(c.role),
			Template:     templateFor[c.role],
			Schematic:    c.schematic,
			InstallImage: talos.InstallImage(c.schematic, cluster.Talos.Version),
		})
	}

	data.Nodes = make([]catalogGroup, 0, len(cluster.Talos.Nodes))
	for i := range cluster.Talos.Nodes {
		n := &cluster.Talos.Nodes[i]
		// The group selector is the primary interface's MAC — the NIC
		// that PXE boots and therefore the one iPXE reports.
		nic, ok := n.PrimaryInterface()
		if !ok {
			return nil, fmt.Errorf("renderCatalog: node %q has no resolved primary interface", n.Name)
		}
		data.Nodes = append(data.Nodes, catalogGroup{
			Name:      n.Name,
			Profile:   profileNames[class{role: n.Role, schematic: perNode[i]}],
			MAC:       nic.MAC,
			ExtraVars: interfaceVars(n),
		})
	}

	out := make(map[string]File, 3)
	for path, text := range map[string]string{
		variablesPath: variablesTemplate,
		profilesPath:  profilesTemplate,
		groupsPath:    groupsTemplate,
	} {
		rendered, err := renderText(path, text, data)
		if err != nil {
			return nil, err
		}
		out[path] = File{Data: rendered}
	}
	return out, nil
}

// interfaceVars builds the per-node group vars a multi-interface
// machineconfig template consumes: every slot's MAC, plus each static
// slot's address. A single-interface node gets none — its template
// reads only hostname, and a var nothing reads is a lie waiting to
// drift. The var keys come from the talos package, the same source
// the template expressions are derived from, so the two sides agree
// by construction; per-role shape uniformity (enforced when the
// templates render, in the same emit) guarantees the template
// consuming these vars actually declares the matching slots.
func interfaceVars(n *config.TalosNode) []catalogVar {
	nics := n.ResolvedInterfaces()
	if len(nics) <= 1 {
		return nil
	}
	vars := make([]catalogVar, 0, 2*len(nics))
	for _, nic := range nics {
		vars = append(vars, catalogVar{Key: talos.MACVarKey(nic.Slot), Value: nic.MAC})
		if !nic.DHCP {
			vars = append(vars, catalogVar{Key: talos.AddressVarKey(nic.Slot), Value: nic.Address})
		}
	}
	return vars
}

// renderText executes one of this package's templates. The templates
// are compiled in, so a parse failure is a programming error, not
// operator input — but it still travels as an error rather than a
// panic.
func renderText(name, text string, data any) ([]byte, error) {
	t, err := template.New(name).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// Every variable and local below is load-bearing: a generated file that
// declares values nothing reads is a lie waiting to drift, so the
// cluster name lives in the header comment rather than in an unused
// variable.
const variablesTemplate = `# booty catalog for cluster {{ .ClusterName }} — generated by
# "bootstrap talos emit". Do not edit: re-emitting overwrites this
# file. Change the bootstrap config and re-run instead.
#
# booty loads every *.hcl in this directory and merges them; the 00/10/20
# split is organizational.

variable "talos_version" {
  description = "Talos release these machines boot"
  default     = "{{ .TalosVersion }}"
}

variable "booty_url" {
  description = "Base URL this booty server is reachable at"
  default     = "{{ .BootyURL }}"
}

locals {
  boot_base = "talos/${var.talos_version}"

  # Talos requires these arguments on metal. A node boots without them,
  # which is exactly why their absence goes unnoticed.
  # https://docs.siderolabs.com/talos/v1.13/platform-specific-installations/bare-metal-platforms/pxe
  common_cmdline = [
    "console=ttyS0,115200n8",
    "console=tty0",
    "talos.platform=metal",
    "init_on_alloc=1",
    "slab_nomerge",
    "pti=on",
  ]
}
`

const profilesTemplate = `# Boot recipes, one per node class (role × image schematic) —
# generated by "bootstrap talos emit". Do not edit. Groups
# (20-groups.hcl) bind machines to these.
#
# The machineconfig templates these profiles render are COMPLETE,
# machinery-generated configs carrying this cluster's secrets, not
# booty's secret-less built-ins. They consume exactly two variables:
# install_image (here) and hostname (per group). The kernel,
# initramfs, and installer image all carry the class's Image Factory
# schematic — the extensions are baked in at that address.
{{ range .Profiles }}
profile "{{ .Name }}" {
  boot {
    kernel = "${local.boot_base}/{{ .Schematic }}/vmlinuz"
    initrd = "${local.boot_base}/{{ .Schematic }}/initramfs.xz"
    # $${mac} is HCL's escape for a literal ${mac}: it must survive HCL
    # untouched so iPXE substitutes the booting machine's MAC at boot
    # time.
    cmdline = concat(local.common_cmdline, ["talos.config=${var.booty_url}/machine-config?mac=$${mac}"])
  }
  render {
    kind     = "talos-machineconfig"
    template = "{{ .Template }}"
  }
  vars = {
    role          = "{{ .Role }}"
    install_image = "{{ .InstallImage }}"
  }
}
{{ end -}}
`

const groupsTemplate = `# One group per Talos VM, pinned by MAC — generated by "bootstrap
# talos emit". Do not edit.
#
# The MAC comes from the bootstrap config, the same value the VM's NIC
# is created with, so the PXE identity binding agrees with the
# hypervisor by construction. Selector keys are a closed set: an unknown
# key silently never matches.
#
# hostname is the per-node variable the machineconfig template consumes.
{{ range .Nodes }}
group "{{ .Name }}" {
  profile = "{{ .Profile }}"
  selector = {
    mac = "{{ .MAC }}"
  }
  vars = {
    hostname = "{{ .Name }}"
{{- range .ExtraVars }}
    {{ .Key }} = "{{ .Value }}"
{{- end }}
  }
}
{{ end -}}
`
