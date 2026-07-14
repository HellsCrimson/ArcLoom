package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
)

type graphNode struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "resource" | "data"
}

type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type graphResponse struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
	DOT   string      `json:"dot"`
}

// handleGraph generates the project HCL, runs `graph` via the engine, and
// returns the dependency graph (parsed to resource nodes/edges plus raw DOT).
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	run, ok := s.runnerFor(string(p.Engine))
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}
	hcl, err := hclgen.Generate(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "generate: "+err.Error())
		return
	}
	dot, err := run.Graph(r.Context(), hcl)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "graph failed: "+err.Error())
		return
	}
	nodes, edges := parseDOT(dot)
	writeJSON(w, http.StatusOK, graphResponse{Nodes: nodes, Edges: edges, DOT: dot})
}

var (
	dotEdgeRe = regexp.MustCompile(`"([^"]+)"\s*->\s*"([^"]+)"`)
	dotNodeRe = regexp.MustCompile(`"([^"]+)"\s*\[`)
	// A managed resource ("aws_instance.web") or data source ("data.x.y") address.
	resourceAddrRe = regexp.MustCompile(`^(data\.)?[a-z][a-z0-9_]*\.[A-Za-z0-9_.\-]+$`)
)

// cleanNode strips the graph's decorations from a node label, returning the bare
// address and whether it is a resource/data node we want to keep.
func cleanNode(raw string) (string, bool) {
	s := raw
	s = strings.TrimPrefix(s, "[root] ")
	// Drop trailing annotations like " (expand)", " (close)".
	if i := strings.Index(s, " ("); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if strings.Contains(s, "provider[") || strings.HasPrefix(s, "var.") ||
		strings.HasPrefix(s, "local.") || strings.HasPrefix(s, "output.") ||
		s == "root" || s == "" {
		return "", false
	}
	if !resourceAddrRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// parseDOT extracts resource nodes and edges from Graphviz DOT output.
func parseDOT(dot string) ([]graphNode, []graphEdge) {
	nodeSet := map[string]bool{}
	add := func(raw string) (string, bool) {
		id, ok := cleanNode(raw)
		if ok {
			nodeSet[id] = true
		}
		return id, ok
	}

	for _, m := range dotNodeRe.FindAllStringSubmatch(dot, -1) {
		add(m[1])
	}

	var edges []graphEdge
	seen := map[string]bool{}
	for _, m := range dotEdgeRe.FindAllStringSubmatch(dot, -1) {
		from, okF := add(m[1])
		to, okT := add(m[2])
		if !okF || !okT || from == to {
			continue
		}
		key := from + "\x00" + to
		if seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, graphEdge{From: from, To: to})
	}

	nodes := make([]graphNode, 0, len(nodeSet))
	for id := range nodeSet {
		typ := "resource"
		if strings.HasPrefix(id, "data.") {
			typ = "data"
		}
		nodes = append(nodes, graphNode{ID: id, Type: typ})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return nodes, edges
}
