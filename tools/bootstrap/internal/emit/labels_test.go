package emit_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

const sizeLabel = "hoomlab.dev/size"

// labelledTestCluster is testCluster with a second worker carrying
// the size label, so the worker role splits into an unlabelled and a
// labelled class (DESIGN-0005 Phase 3: work01–03 vs work04/05).
func labelledTestCluster() *config.Cluster {
	c := testCluster()
	c.Talos.Nodes = append(c.Talos.Nodes, config.TalosNode{
		Name: "worker-02", Role: config.RoleWorker, PVENode: "pve-02", VMID: 301,
		Interfaces: bootNIC("02:50:99:a2:01:02"),
		Labels:     map[string]string{sizeLabel: "large"},
	})
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		panic("labelledTestCluster does not resolve: " + diags.Error())
	}
	return c
}

// TestTreeGoldenLabels pins the split-role catalog: the labelled
// worker class gets its own profile and template, suffixed with the
// label-set hash, while the unlabelled worker and the control plane
// keep their plain names.
func TestTreeGoldenLabels(t *testing.T) {
	e := testEmitter(t)
	e.Cluster = labelledTestCluster()
	treeGolden(t, e, "golden-labels")

	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	var templates []string
	for path := range tree {
		if strings.HasPrefix(path, "templates/") {
			templates = append(templates, path)
		}
	}
	slices.Sort(templates)
	if len(templates) != 3 ||
		templates[0] != "templates/talos/controlplane.yaml.tmpl" ||
		!strings.HasPrefix(templates[1], "templates/talos/worker-") ||
		templates[2] != "templates/talos/worker.yaml.tmpl" {
		t.Errorf("templates = %v, want controlplane, worker, and one worker-<hash>", templates)
	}
	if strings.Contains(string(tree["templates/talos/worker.yaml.tmpl"].Data), sizeLabel) {
		t.Error("the unlabelled worker template carries the size label")
	}
	if !strings.Contains(string(tree[templates[1]].Data), sizeLabel+": large") {
		t.Errorf("%s is missing the size label in machine.nodeLabels", templates[1])
	}
}

// TestUnsplitLabelsKeepPlainNames pins that labels alone don't rename
// anything: a role whose nodes all share one label set is still one
// class, so it keeps today's template and profile names.
func TestUnsplitLabelsKeepPlainNames(t *testing.T) {
	c := testCluster()
	for i := range c.Talos.Nodes {
		if c.Talos.Nodes[i].Role == config.RoleWorker {
			c.Talos.Nodes[i].Labels = map[string]string{sizeLabel: "large"}
		}
	}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		t.Fatalf("resolve: %v", diags)
	}
	e := testEmitter(t)
	e.Cluster = c
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	worker, ok := tree["templates/talos/worker.yaml.tmpl"]
	if !ok {
		t.Fatal("single-class worker role lost its plain template name")
	}
	if !strings.Contains(string(worker.Data), sizeLabel+": large") {
		t.Error("worker template is missing the shared size label")
	}
	if profiles := string(tree["catalog/10-profiles.hcl"].Data); !strings.Contains(profiles, `profile "talos-worker" {`) {
		t.Errorf("single-class worker role lost its plain profile name:\n%s", profiles)
	}
}

// kataCluster is the drill shape with Kata: base on every node, kata
// on every worker, and the size label on worker-02 — so the worker
// role splits by the size label only, never by the derived runtime
// label every worker shares.
func kataCluster() *config.Cluster {
	c := labelledTestCluster()
	c.Talos.Profiles = []config.TalosProfile{
		{Name: "base", Extensions: []string{"siderolabs/qemu-guest-agent"}},
		{Name: "kata", Extensions: []string{"siderolabs/kata-containers"}},
	}
	for i := range c.Talos.Nodes {
		c.Talos.Nodes[i].Profiles = []string{"base"}
		if c.Talos.Nodes[i].Role == config.RoleWorker {
			c.Talos.Nodes[i].Profiles = append(c.Talos.Nodes[i].Profiles, "kata")
		}
	}
	if diags := c.ResolveInterfaces(); diags.HasErrors() {
		panic("kataCluster does not resolve: " + diags.Error())
	}
	return c
}

// TestRuntimeLabelInEveryWorkerClass pins DESIGN-0005 Phase 4's emit
// contract: the derived runtime label lands in every worker class's
// template and in no control-plane one, and — being the same on every
// worker — splits nothing: the worker role still has exactly its two
// size classes.
func TestRuntimeLabelInEveryWorkerClass(t *testing.T) {
	const kataLabel = "runtime.hoomlab.dev/kata: \"true\""
	e := testEmitter(t)
	e.Cluster = kataCluster()
	e.Factory = &fakeResolver{}
	tree, err := e.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	var workers int
	for path, f := range tree {
		if !strings.HasPrefix(path, "templates/talos/") {
			continue
		}
		has := strings.Contains(string(f.Data), kataLabel)
		switch {
		case strings.HasPrefix(path, "templates/talos/worker"):
			workers++
			if !has {
				t.Errorf("%s is missing %s", path, kataLabel)
			}
		case has:
			t.Errorf("%s carries the kata label, but control planes never run Kata", path)
		}
	}
	if workers != 2 {
		t.Errorf("got %d worker templates, want 2 (the size split only)", workers)
	}
}
