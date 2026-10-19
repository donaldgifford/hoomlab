package emit

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// NodeConfigs renders every node's complete machine config exactly as
// booty serves it at install: the node's class template (per role)
// executed with the node's template vars — the class's role and
// install_image, plus the per-node group vars — through the same
// text/template .Vars contract booty uses. It is the "render" half of
// the day-2 apply stage (DESIGN-0005 Phase 2), and it shares schematic
// resolution, template generation, and var assembly with Tree, so what
// apply compares against a live node is what that node would get from
// a fresh install.
func (e *Emitter) NodeConfigs(ctx context.Context) ([]talos.RenderedNode, error) {
	perNode, _, err := e.nodeSchematics(ctx)
	if err != nil {
		return nil, err
	}
	templates, err := talos.RoleTemplates(e.Bundle, e.Cluster)
	if err != nil {
		return nil, err
	}
	out := make([]talos.RenderedNode, 0, len(e.Cluster.Talos.Nodes))
	for i := range e.Cluster.Talos.Nodes {
		n := &e.Cluster.Talos.Nodes[i]
		tmpl := templates.Worker
		if n.Role == config.RoleControlPlane {
			tmpl = templates.ControlPlane
		}
		image := talos.InstallImage(perNode[i], e.Cluster.Talos.Version)
		vars := map[string]string{
			talos.RoleVarKey:         string(n.Role),
			talos.InstallImageVarKey: image,
		}
		for _, v := range groupVars(n) {
			vars[v.Key] = v.Value
		}
		data, err := renderMachineConfig(n.Name, tmpl, vars)
		if err != nil {
			return nil, err
		}
		out = append(out, talos.RenderedNode{Name: n.Name, Config: data, InstallImage: image})
	}
	return out, nil
}

// renderMachineConfig executes one machineconfig template with booty's
// data shape: the template vars under .Vars.
func renderMachineConfig(node string, tmpl []byte, vars map[string]string) ([]byte, error) {
	t, err := template.New(node).Option("missingkey=error").Parse(string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("node %q: parse machineconfig template: %w", node, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, struct{ Vars map[string]string }{Vars: vars}); err != nil {
		return nil, fmt.Errorf("node %q: render machineconfig: %w", node, err)
	}
	return buf.Bytes(), nil
}
