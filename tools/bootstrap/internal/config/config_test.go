package config

import "testing"

func TestPrimaryNode(t *testing.T) {
	cluster := Cluster{PVE: PVE{Nodes: []PVENode{
		{Name: "pve-01"},
		{Name: "pve-02", Primary: true},
	}}}

	node, ok := cluster.PrimaryNode()
	if !ok || node.Name != "pve-02" {
		t.Errorf("PrimaryNode() = %q, %v, want pve-02, true", node.Name, ok)
	}

	cluster.PVE.Nodes[1].Primary = false
	if _, ok := cluster.PrimaryNode(); ok {
		t.Error("PrimaryNode() ok = true without a primary, want false")
	}
}
