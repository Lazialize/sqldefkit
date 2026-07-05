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
	"github.com/Lazialize/sqldefkit/internal/fkrewrite"
	"github.com/Lazialize/sqldefkit/internal/graph"
	"github.com/Lazialize/sqldefkit/internal/parse"
)

// Version is the current payload schema version (see Graph.Version).
//
// v2 adds table nodes' Columns and FK edges' FromColumn/ToColumn (see
// Node/Edge doc comments) — DOT/Mermaid output is unaffected (both stay
// object-level; see FormatDOT/FormatMermaid).
const Version = 2

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
	// Columns holds the table's ordered column list (kind=table only, and
	// only when non-empty — omitted for views/indexes/etc. and for a
	// table ExtractColumns couldn't find a column list for at all).
	Columns []Column `json:"columns,omitempty"`
}

// Column is one column of a table node, mirroring
// internal/fkrewrite.Column/ColumnFK for JSON export.
type Column struct {
	Name    string    `json:"name"`
	Type    string    `json:"type,omitempty"`
	PK      bool      `json:"pk,omitempty"`
	NotNull bool      `json:"notNull,omitempty"`
	Unique  bool      `json:"unique,omitempty"`
	FK      *ColumnFK `json:"fk,omitempty"`
}

// ColumnFK is a column's foreign-key target.
type ColumnFK struct {
	Table  string `json:"table"`
	Column string `json:"column,omitempty"`
}

// Edge is one dependency edge: From depends on To.
type Edge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	InCycle bool   `json:"inCycle"`
	// FromColumn/ToColumn name the specific columns an "fk" edge connects
	// (when known), letting a column-level ER rendering anchor the edge to
	// the right rows instead of the table as a whole. Omitted (both empty)
	// for edge kinds other than "fk", or when the participating columns
	// aren't known.
	FromColumn string `json:"fromColumn,omitempty"`
	ToColumn   string `json:"toColumn,omitempty"`
}

// edgeKey uniquely identifies an edge for deduplication, ignoring
// InCycle (which is derived, not part of the edge's identity). FromColumn
// is part of the key (loosened from v1, which deduplicated purely on
// from/to/kind): two FK columns on the same table pointing at the same
// target table now produce two distinct payload edges, one per source
// column, so a column-level renderer can draw both. ToColumn is
// deliberately not part of the key — DOT/Mermaid formatters re-collapse to
// one object-level edge per (from,to,kind) regardless (see
// dedupeObjectLevel), so this key only needs to distinguish what the JSON
// payload itself must keep distinct.
type edgeKey struct {
	from, to, kind, fromColumn string
}

// nodePairKey identifies a (from, to) node pair, independent of edge kind
// or column — used for SCC/cycle-membership lookups, which only care
// about which nodes an edge connects.
type nodePairKey struct {
	from, to string
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

	// firstStmtByName maps a defined name to the first (source-order)
	// parsed statement that defines it, so table nodes can pull their
	// column list from the statement's own Text/Tokens — bundle.Definition
	// itself only carries position/kind/file, not the source text needed
	// to re-scan the column list.
	firstStmtByName := make(map[string]parse.Statement)
	for _, st := range loaded.Statements() {
		if st.Name == "" {
			continue
		}
		if _, ok := firstStmtByName[st.Name]; !ok {
			firstStmtByName[st.Name] = st
		}
	}

	// Definitions become real nodes.
	for _, name := range loaded.Symbols.DefinitionNames() {
		def, ok := loaded.Symbols.FirstDefinition(name)
		if !ok {
			continue
		}
		var columns []Column
		if def.Kind == parse.KindCreateTable {
			if st, ok := firstStmtByName[name]; ok {
				columns = exportColumns(fkrewrite.ExtractColumns(st.Text, st.Tokens))
			}
		}
		addNode(Node{
			ID:      name,
			Kind:    kindString(def.Kind),
			File:    def.File,
			Line:    def.Pos.Line,
			Col:     def.Pos.Col,
			Columns: columns,
		})
	}

	edgeSet := make(map[edgeKey]bool)
	var edges []Edge

	addEdge := func(from, to, kind, fromColumn, toColumn string) {
		key := edgeKey{from, to, kind, fromColumn}
		if edgeSet[key] {
			return
		}
		edgeSet[key] = true
		edges = append(edges, Edge{
			From:       from,
			To:         to,
			Kind:       kind,
			InCycle:    sccEdge[nodePairKey{from, to}],
			FromColumn: fromColumn,
			ToColumn:   toColumn,
		})
	}

	// fkCols maps a "from" CREATE TABLE statement's FK target tables to
	// the column-level pairings found on its own column list (see
	// internal/fkrewrite via fkColumnPairs). This is consulted below
	// instead of iterating classifyRefs' "fk" entries directly, because
	// parse.Parse deduplicates DepRefs by target name (see
	// parse.dedupeRefs): two distinct FK columns on the same table both
	// targeting the same table collapse to a single DepRef, which would
	// otherwise silently drop one of the two columns' edges. Iterating
	// fkCols directly (one edge per pairing, however many that is)
	// recovers the full column-level edge set regardless of that
	// name-level dedup upstream.
	for _, st := range loaded.Statements() {
		from := st.Name
		if from == "" {
			continue
		}
		fkCols := fkColumnPairs(st.Kind, st.Text, st.Tokens)
		refs := classifyRefs(st.Kind, from, st.DepRefs)
		fkTargetsSeen := make(map[string]bool)
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
			if cr.kind == "fk" {
				fkTargetsSeen[cr.name] = true
				continue // one edge per pairing added below, not per ref
			}
			addEdge(from, cr.name, cr.kind, "", "")
		}
		for target := range fkTargetsSeen {
			pairs := fkCols[target]
			if len(pairs) == 0 {
				// No column-level pairing resolved (shouldn't normally
				// happen since classifyRefs's "fk" kind came from a
				// REFERENCES clause fkColumnPairs should also have seen,
				// but stay fail-soft): still emit a plain edge.
				addEdge(from, target, "fk", "", "")
				continue
			}
			for _, p := range pairs {
				addEdge(from, target, "fk", p.from, p.to)
			}
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
// Returned keys are nodePairKey (from, to only), independent of edge kind
// or column, so callers can look up nodePairKey{from, to} regardless of
// the edge's own Kind/FromColumn.
func buildSCCEdgeSet(nodes []graph.Node) map[nodePairKey]bool {
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

	out := make(map[nodePairKey]bool)
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
				out[nodePairKey{n.Name, dep}] = true
			}
		}
	}
	return out
}
