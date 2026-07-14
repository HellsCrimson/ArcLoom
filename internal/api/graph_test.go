package api

import "testing"

func TestParseDOT_FiltersProvidersKeepsResources(t *testing.T) {
	dot := `digraph {
	compound = "true"
	subgraph "root" {
		"[root] provider[\"registry.opentofu.org/hashicorp/random\"]" [label = "provider", shape = "diamond"]
		"[root] random_password.db (expand)" [label = "random_password.db", shape = "box"]
		"[root] random_pet.srv (expand)" [label = "random_pet.srv", shape = "box"]
		"[root] random_password.db (expand)" -> "[root] random_pet.srv (expand)"
		"[root] random_password.db (expand)" -> "[root] provider[\"registry.opentofu.org/hashicorp/random\"]"
		"[root] root" -> "[root] random_password.db (expand)"
	}
}`
	nodes, edges := parseDOT(dot)

	if len(nodes) != 2 {
		t.Fatalf("expected 2 resource nodes, got %d: %+v", len(nodes), nodes)
	}
	// Only the resource->resource edge should survive (provider/root filtered).
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %+v", len(edges), edges)
	}
	if edges[0].From != "random_password.db" || edges[0].To != "random_pet.srv" {
		t.Errorf("wrong edge: %+v", edges[0])
	}
}

func TestParseDOT_DataSourceType(t *testing.T) {
	dot := `digraph {
	"[root] data.http.x (expand)" [label = "data.http.x"]
	"[root] aws_instance.web (expand)" -> "[root] data.http.x (expand)"
}`
	nodes, _ := parseDOT(dot)
	var dataNode bool
	for _, n := range nodes {
		if n.ID == "data.http.x" && n.Type == "data" {
			dataNode = true
		}
	}
	if !dataNode {
		t.Errorf("data source node not classified as data: %+v", nodes)
	}
}
