package nodeedit

import "testing"

func TestSelectNodeAcceptsItsDaemonConfigName(t *testing.T) {
	repo := lifecycleRepo(t)
	node, err := selectNode(repo, "namenlos", "wauland-pq-servicenode")
	if err != nil {
		t.Fatal(err)
	}
	if node.Name != "waulandservice" || node.Role != "servicenode" {
		t.Fatalf("wrong selected node: %s %s", node.Name, node.Role)
	}
}
