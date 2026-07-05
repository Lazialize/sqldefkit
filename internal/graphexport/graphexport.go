// Package graphexport builds a dependency-graph payload from a loaded
// schema tree, for two consumers: the `sqldefkit graph` CLI subcommand
// (DOT/Mermaid/JSON output) and the `sqldefkit/dependencyGraph` LSP
// request (JSON payload, overlay-aware). Both share the same Build
// function so the two stay in lockstep by construction.
//
// Unlike internal/graph.Sort, Build never fails on a dependency cycle:
// visualizing cycles is a core use case here, not an error condition. It
// only surfaces the I/O/discovery/lex errors bundle.Load itself can
// return.
package graphexport

import (
	"sort"

	"github.com/Lazialize/sqldefkit/internal/bundle"
	"github.com/Lazialize/sqldefkit/internal/graph"
	"github.com/Lazialize/sqldefkit/internal/parse"
)

// Version is the current payload schema version (see Graph.Version).
const Version = 1

// Graph is the versioned, JSON-serializable dependency graph payload.
// Nodes is a slice of objects (not bare strings) so a future version can
// attach more per-node data (e.g. column info) without changing the
// shape.
type Graph struct {
	Version int    `json:"version"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

// Node is one object in the dependency graph: either a definition found
// in the loaded schema tree, or an "external" placeholder standing in
// for a high-confidence reference to a name that isn't defined anywhere
// in it.
type Node struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	External bool   `json:"external,omitempty"`
	InCycle  bool   `json:"inCycle,omitempty"`
}

// Edge is one dependency edge: From depends on To.
type Edge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	InCycle bool   `json:"inCycle"`
}

// edgeKey uniquely identifies an edge for deduplication, ignoring
// InCycle (which is derived, not part of the edge's identity).
type edgeKey struct {
	from, to, kind string
}

// Build constructs a Graph from loaded, the result of bundle.Load. It
// does not fail on dependency cycles: every strongly connected component
// (per internal/graph.SCCs, using the same edge-resolution rules
// graph.Sort itself uses) has its member nodes and internal edges marked
// InCycle, but the full node/edge set is still returned regardless.
func Build(loaded bundle.Loaded) Graph {
	nodes := loaded.GraphNodes()

	sccNode := make(map[string]bool)
	for _, comp := range graph.SCCs(nodes) {
		for _, idx := range comp {
			if nodes[idx].Name != "" {
				sccNode[nodes[idx].Name] = true
			}
		}
	}

	// sccEdge marks (from, to) pairs where both ends are in the same SCC
	// (an edge internal to a cycle), computed directly from the same
	// adjacency SCCs uses rather than re-deriving it from Node/Edge
	// values built below.
	sccEdge := buildSCCEdgeSet(nodes)

	nodeOut := make(map[string]*Node)
	var order []string

	addNode := func(n Node) {
		if existing, ok := nodeOut[n.ID]; ok {
			// Keep the first-seen definition (source order); just make
			// sure InCycle reflects membership.
			existing.InCycle = existing.InCycle || sccNode[n.ID]
			return
		}
		n.InCycle = n.InCycle || sccNode[n.ID]
		nodeOut[n.ID] = &n
		order = append(order, n.ID)
	}

	// Definitions become real nodes.
	for _, name := range loaded.Symbols.DefinitionNames() {
		def, ok := loaded.Symbols.FirstDefinition(name)
		if !ok {
			continue
		}
		addNode(Node{
			ID:   name,
			Kind: kindString(def.Kind),
			File: def.File,
			Line: def.Pos.Line,
			Col:  def.Pos.Col,
		})
	}

	edgeSet := make(map[edgeKey]bool)
	var edges []Edge

	addEdge := func(from, to, kind string) {
		key := edgeKey{from, to, kind}
		if edgeSet[key] {
			return
		}
		edgeSet[key] = true
		edges = append(edges, Edge{
			From:    from,
			To:      to,
			Kind:    kind,
			InCycle: sccEdge[edgeKey{from, to, ""}],
		})
	}

	for _, st := range loaded.Statements() {
		from := st.Name
		if from == "" {
			continue
		}
		refs := classifyRefs(st.Kind, from, st.DepRefs)
		for _, cr := range refs {
			if cr.name == from {
				// Self-references never contribute an edge (matches
				// graph.Sort/SCCs' self-loop handling).
				continue
			}
			if cr.kind == "view" {
				if _, defined := loaded.Symbols.Definitions[cr.name]; !defined {
					// Best-effort view scan ref to an undefined name:
					// dropped entirely (alias false positives), per spec.
					continue
				}
			} else {
				// High-confidence (fk/on/alter/directive): synthesize an
				// external node if undefined.
				if _, defined := loaded.Symbols.Definitions[cr.name]; !defined {
					addNode(Node{ID: cr.name, Kind: "unknown", External: true})
				}
			}
			addEdge(from, cr.name, cr.kind)
		}
	}

	outNodes := make([]Node, 0, len(order))
	for _, id := range order {
		outNodes = append(outNodes, *nodeOut[id])
	}
	sort.Slice(outNodes, func(i, j int) bool { return outNodes[i].ID < outNodes[j].ID })

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})

	return Graph{Version: Version, Nodes: outNodes, Edges: edges}
}

// classifiedRef is a Ref resolved to its export edge kind.
type classifiedRef struct {
	name string
	kind string
}

// classifyRefs maps a statement's DepRefs to their export edge kinds,
// using the statement's own Kind (and, for ALTER TABLE, whether a ref
// names the statement's own object) to disambiguate cases where
// parse.RefKind alone (RefAuto) covers more than one edge kind:
//
//   - CREATE TABLE + RefAuto -> "fk" (a REFERENCES clause).
//   - CREATE INDEX / CREATE TRIGGER + RefAuto -> "on" for the ON target,
//     "fk" for any other RefAuto (e.g. REFERENCES found scanning a
//     trigger body).
//   - ALTER TABLE + RefAuto -> "alter" for the ref naming the table
//     itself (always first, per extractAlterTable), "fk" for the rest.
//   - any other statement kind + RefAuto -> "fk" (matches the
//     CREATE-TABLE-else-branch default: an auto-detected reference is
//     always a REFERENCES-shaped edge outside the ON/ALTER special
//     cases above).
//   - RefViewScan -> "view".
//   - RefDirective -> "directive".
func classifyRefs(stmtKind parse.Kind, selfName string, refs []parse.Ref) []classifiedRef {
	out := make([]classifiedRef, 0, len(refs))
	onConsumed := false
	alterConsumed := false
	for _, ref := range refs {
		switch ref.Kind {
		case parse.RefViewScan:
			out = append(out, classifiedRef{ref.Name, "view"})
		case parse.RefDirective:
			out = append(out, classifiedRef{ref.Name, "directive"})
		default: // RefAuto
			switch {
			case stmtKind == parse.KindAlterTable && !alterConsumed && ref.Name == selfName:
				alterConsumed = true
				out = append(out, classifiedRef{ref.Name, "alter"})
			case (stmtKind == parse.KindCreateIndex || stmtKind == parse.KindCreateTrigger) && !onConsumed:
				onConsumed = true
				out = append(out, classifiedRef{ref.Name, "on"})
			default:
				out = append(out, classifiedRef{ref.Name, "fk"})
			}
		}
	}
	return out
}

// kindString renders a parse.Kind as the lowercase snake_case word used
// in the export payload.
func kindString(k parse.Kind) string {
	switch k {
	case parse.KindCreateTable:
		return "table"
	case parse.KindCreateView:
		return "view"
	case parse.KindCreateIndex:
		return "index"
	case parse.KindCreateFunction:
		return "function"
	case parse.KindCreateTrigger:
		return "trigger"
	case parse.KindCreateType:
		return "type"
	case parse.KindCreateSequence:
		return "sequence"
	case parse.KindCreateExtension:
		return "extension"
	case parse.KindAlterTable:
		return "table"
	default:
		return "other"
	}
}

// buildSCCEdgeSet computes, for every edge (i -> j) in nodes' dependency
// graph (same resolution rules as graph.SCCs/graph.Sort: a dependency
// name not defined by any node is ignored, self-loops excluded) whether
// both ends belong to the same (>1-member) strongly connected component.
// Returned keys use an empty Kind field so callers can look up
// edgeKey{from, to, ""} regardless of the edge's own Kind.
func buildSCCEdgeSet(nodes []graph.Node) map[edgeKey]bool {
	byName := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.Name != "" {
			byName[n.Name] = i
		}
	}

	sccOf := make(map[int]int) // node index -> component id
	for compID, comp := range graph.SCCs(nodes) {
		for _, idx := range comp {
			sccOf[idx] = compID + 1 // +1 so the zero value means "no SCC"
		}
	}

	out := make(map[edgeKey]bool)
	for i, n := range nodes {
		if n.Name == "" {
			continue
		}
		for _, dep := range n.Deps {
			j, ok := byName[dep]
			if !ok || j == i {
				continue
			}
			ci, hasI := sccOf[i]
			cj, hasJ := sccOf[j]
			if hasI && hasJ && ci == cj {
				out[edgeKey{n.Name, dep, ""}] = true
			}
		}
	}
	return out
}
