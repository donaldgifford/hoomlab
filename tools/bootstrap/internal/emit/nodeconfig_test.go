package emit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"

	"github.com/donaldgifford/booty/catalog"
	"github.com/donaldgifford/booty/render"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// TestNodeConfigsMatchBooty pins the apply stage's premise: the config
// NodeConfigs renders for a node is byte-for-byte the one booty serves
// that node from the emitted tree, and machinery accepts it in metal
// mode. If the two ever diverge, apply would converge nodes to a config
// a fresh PXE boot would not reproduce.
func TestNodeConfigsMatchBooty(t *testing.T) {
	e := testEmitter(t)
	e.Cluster = multiNICTestCluster()
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	cat, err := catalog.DirSource{Root: filepath.Join(e.Root, "catalog")}.Load(context.Background())
	if err != nil {
		t.Fatalf("booty catalog load: %v", err)
	}
	renderer, err := render.New(render.WithTemplates(os.DirFS(filepath.Join(e.Root, "templates"))))
	if err != nil {
		t.Fatalf("booty renderer: %v", err)
	}

	nodes, err := e.NodeConfigs(context.Background())
	if err != nil {
		t.Fatalf("NodeConfigs: %v", err)
	}
	if len(nodes) != len(e.Cluster.Talos.Nodes) {
		t.Fatalf("NodeConfigs returned %d nodes, want %d", len(nodes), len(e.Cluster.Talos.Nodes))
	}
	for i, n := range nodes {
		t.Run(n.Name, func(t *testing.T) {
			cfgNode := &e.Cluster.Talos.Nodes[i]
			if n.Name != cfgNode.Name {
				t.Fatalf("node %d is %q, want config order (%q)", i, n.Name, cfgNode.Name)
			}
			if want := talos.InstallImage("", talosVersion); n.InstallImage != want {
				t.Errorf("InstallImage = %q, want %q", n.InstallImage, want)
			}

			id := catalog.Identity{MAC: cfgNode.ResolvedInterfaces()[0].MAC}
			res, err := cat.Match(id)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			served, err := renderer.Config(id, res, bootyURL)
			if err != nil {
				t.Fatalf("booty render: %v", err)
			}
			if string(n.Config) != served {
				t.Errorf("NodeConfigs render differs from what booty serves:\n--- booty\n%s\n--- NodeConfigs\n%s",
					served, n.Config)
			}

			provider, err := configloader.NewFromBytes(n.Config)
			if err != nil {
				t.Fatalf("machinery load: %v", err)
			}
			if _, err := provider.Validate(metalMode{}); err != nil {
				t.Errorf("rendered config invalid in metal mode: %v", err)
			}
		})
	}
}
