package bundle

import (
	"fmt"

	"github.com/Lazialize/sqldefkit/internal/fkrewrite"
	"github.com/Lazialize/sqldefkit/internal/graph"
	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/parse"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// unsplittableNote is appended (via graph.CycleError.Note) when a cycle is
// made entirely of foreign-key edges but at least one of them couldn't be
// extracted with certainty.
const unsplittableNote = "a foreign key in this cycle could not be split automatically; move it to a table-level CONSTRAINT ... FOREIGN KEY clause or an explicit ALTER TABLE"

// breakFKCycles inspects loaded's statements for dependency cycles
// (strongly connected components of size > 1 over the same edges
// graph.Sort uses) and resolves every cycle made entirely of foreign-key
// edges declared inside a CREATE TABLE, in a dialect-appropriate way:
//
//   - postgres / mysql: rewrites those CREATE TABLEs to drop the FK
//     clause(s) closing the cycle and appends synthesized ALTER TABLE
//     statements carrying them instead — sorting after both tables
//     involved, since they now depend on both.
//   - sqlite: no rewriting at all. SQLite has no ALTER TABLE ... ADD
//     FOREIGN KEY (emitting one would be a syntax error), but it also
//     doesn't need one: foreign keys are resolved lazily at DML time, so
//     a CREATE TABLE may legally reference a table that doesn't exist
//     yet. The intra-cycle FK edges are simply dropped from the
//     dependency graph so the sort can proceed; every statement is
//     emitted verbatim in deterministic tie-break order.
//
// Statements untouched by any cycle are left byte-identical in all
// dialects.
//
// If a cycle contains any non-FK edge (view scan, directive, INDEX/
// TRIGGER ON, ALTER TABLE), or (postgres/mysql only) a qualifying FK
// edge can't be extracted with certainty, breakFKCycles returns the same
// *graph.CycleError graph.Sort would have produced for that cycle
// (augmented with unsplittableNote in the latter case) and leaves loaded
// untouched.
//
// dialect selects the strategy above and, for postgres/mysql, is also
// needed to re-lex synthesized ALTER TABLE text through the same
// pipeline real statements go through, so its Name/Deps are computed
// identically to a hand-written statement.
func breakFKCycles(loaded Loaded, dialect Dialect) (Loaded, error) {
	nodes := loaded.graphNodes()
	sccs := graph.SCCs(nodes)
	if len(sccs) == 0 {
		return loaded, nil
	}

	stmts := append([]statement(nil), loaded.Stmts...)
	// nodeText/nodeTokens are recomputed lazily per statement as we
	// extract, since a table can participate in more than one SCC only
	// if... it can't (SCCs are disjoint by definition), so each
	// statement is rewritten at most once.
	rewritten := make(map[int]string) // statement slice index -> new Text

	for _, comp := range sccs {
		var ok bool
		var note string
		if dialect == SQLite {
			ok, note = dropFKEdgesSQLite(nodes, stmts, comp)
		} else {
			ok, note = breakOneSCC(nodes, stmts, comp, dialect, rewritten, &stmts)
		}
		if !ok {
			sub := make([]graph.Node, len(comp))
			for i, idx := range comp {
				sub[i] = nodes[idx]
			}
			_, err := graph.Sort(sub)
			cycleErr, isCycle := err.(*graph.CycleError)
			if !isCycle {
				// Sort on a >1-member SCC always fails; this should be
				// unreachable, but never silently succeed.
				return loaded, fmt.Errorf("internal error: expected cycle in SCC %v", comp)
			}
			cycleErr.Note = note
			return loaded, cycleErr
		}
	}

	// Apply the accumulated CREATE TABLE rewrites. The whole statement is
	// re-parsed from its new text (not just Text swapped in-place) so
	// Deps/DepRefs/Name/Kind are recomputed consistently — critical here,
	// since the whole point of the rewrite is that the extracted FK
	// target(s) must no longer appear in Deps.
	for i, text := range rewritten {
		file := stmts[i].file
		reparsed, err := synthesizeStatement(text, file, dialect)
		if err != nil {
			// Should be unreachable: text came from Remove() on
			// already-valid CREATE TABLE tokens, so it must still lex
			// and parse as exactly one statement. Fail safe rather than
			// silently keep stale Deps.
			return loaded, fmt.Errorf("internal error: rewritten statement %s#%d failed to re-parse: %w", file, i, err)
		}
		reparsed.index = stmts[i].index
		reparsed.subIndex = stmts[i].subIndex
		// The rewrite only ever touches the column/constraint list inside
		// Text; leading comments (attached separately, not part of Text)
		// are unaffected and must be carried over explicitly.
		reparsed.ps.LeadingComments = stmts[i].ps.LeadingComments
		stmts[i] = reparsed
	}

	return Loaded{Files: loaded.Files, Stmts: stmts, Symbols: loaded.Symbols, Diags: loaded.Diags}, nil
}

// sccIndex holds the per-SCC lookup maps shared by the classification
// and edge-dropping/extraction passes.
type sccIndex struct {
	inSCC  map[int]bool
	byName map[string]int
}

func buildSCCIndex(nodes []graph.Node, comp []int) sccIndex {
	inSCC := make(map[int]bool, len(comp))
	for _, idx := range comp {
		inSCC[idx] = true
	}
	byName := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.Name != "" {
			byName[n.Name] = i
		}
	}
	return sccIndex{inSCC: inSCC, byName: byName}
}

// sccAllFKEdges reports whether every internal edge of the SCC (an edge
// (idx -> target) where idx and target are both in comp) originates from
// an FK REFERENCES clause in a CREATE TABLE. It's not enough to check the
// source statement's Kind alone, nor to check just one Ref per distinct
// target name: a CREATE TABLE can carry both a genuine REFERENCES clause
// AND a redundant `-- sqldefkit:require` directive naming the same table
// (parse.Parse appends directive Refs unconditionally, never
// deduplicating them against auto-scanned ones), and treating that
// directive edge as FK would silently fail to break the cycle. So every
// DepRefs entry landing on an in-SCC target must individually be a
// RefAuto from a KindCreateTable statement — not just the first one seen.
func sccAllFKEdges(stmts []statement, comp []int, ix sccIndex) bool {
	for _, idx := range comp {
		st := stmts[idx]
		for _, ref := range st.ps.DepRefs {
			j, isNode := ix.byName[ref.Name]
			if !isNode || j == idx || !ix.inSCC[j] {
				continue
			}
			if st.ps.Kind != parse.KindCreateTable || ref.Kind != parse.RefAuto {
				return false // non-FK edge closes this cycle
			}
		}
	}
	return true
}

// dropFKEdgesSQLite resolves an all-FK SCC for the sqlite dialect by
// removing the intra-SCC dependency edges from each participant's Deps —
// no statement text is touched and nothing is synthesized. SQLite has no
// ALTER TABLE ... ADD FOREIGN KEY form (the postgres/mysql strategy
// would emit a syntax error), and none is needed: SQLite resolves
// foreign keys lazily at DML time, so a CREATE TABLE referencing a
// not-yet-created table is valid as-is. Returns ok=false (empty note) if
// the SCC has any non-FK edge, which stays a hard cycle error exactly
// like the other dialects.
func dropFKEdgesSQLite(nodes []graph.Node, stmts []statement, comp []int) (ok bool, note string) {
	ix := buildSCCIndex(nodes, comp)
	if !sccAllFKEdges(stmts, comp, ix) {
		return false, ""
	}

	for _, idx := range comp {
		st := stmts[idx]
		kept := make([]string, 0, len(st.ps.Deps))
		for _, dep := range st.ps.Deps {
			j, isNode := ix.byName[dep]
			if isNode && j != idx && ix.inSCC[j] {
				continue // intra-cycle FK edge: drop from the sort graph
			}
			kept = append(kept, dep)
		}
		// Replace with a fresh slice (never mutate in place: the backing
		// array is shared with the caller's original Loaded.Stmts, which
		// must stay untouched if a later SCC turns out to be unbreakable).
		stmts[idx].ps.Deps = kept
	}
	return true, ""
}

// breakOneSCC attempts to break a single SCC (comp holds indices into
// nodes/base statement slice, i.e. only real statements — synthesized ones
// are appended to *stmtsPtr as they're created) by extracting the FK
// clauses that close the cycle into synthesized ALTER TABLE statements
// (the postgres/mysql strategy). Returns ok=false with a note (possibly
// empty) if the SCC can't be broken; the caller derives the actual
// CycleError from the SCC subgraph itself.
func breakOneSCC(nodes []graph.Node, stmts []statement, comp []int, dialect Dialect, rewritten map[int]string, stmtsPtr *[]statement) (ok bool, note string) {
	ix := buildSCCIndex(nodes, comp)
	inSCC, byName := ix.inSCC, ix.byName

	if !sccAllFKEdges(stmts, comp, ix) {
		return false, "" // non-FK edge closes this cycle
	}

	// Every internal edge is FK-from-CREATE-TABLE. Extract the clauses
	// that close the cycle from each participating CREATE TABLE.
	type extraction struct {
		stmtIdx int
		clause  fkrewrite.Clause
	}
	var toExtract []extraction
	// tableNames holds each statement's table name exactly as written in
	// source (quoting/casing preserved), for building the synthesized
	// ALTER TABLE — using parse.Statement.Name (normalized, unquoted)
	// there could silently change meaning for a quoted, case-sensitive
	// identifier.
	tableNames := make(map[int]string, len(comp))

	for _, idx := range comp {
		st := stmts[idx]
		tmpStmts, lexErr := lexer.Split(st.ps.Text, dialect)
		if lexErr != nil || len(tmpStmts) != 1 {
			return false, unsplittableNote
		}
		clauses, extractOK := fkrewrite.Extract(tmpStmts[0].Text, tmpStmts[0].Tokens)
		if !extractOK {
			return false, unsplittableNote
		}
		rawName, nameOK := fkrewrite.TableName(tmpStmts[0].Text, tmpStmts[0].Tokens)
		if !nameOK {
			return false, unsplittableNote
		}
		tableNames[idx] = rawName

		// Which distinct target tables does this table need to shed an
		// edge to, to break the cycle? A set (not a count) of normalized
		// names is all that's safe to derive from DepRefs: parse.Parse
		// deduplicates auto-scanned Refs by name (see dedupeRefs in
		// internal/parse/extract.go), so DepRefs collapses two separate
		// FK columns referencing the same table down to one Ref — the
		// occurrence count is lost, only "is there at least one edge to
		// this target" survives. So: extract EVERY clause fkrewrite.Extract
		// found whose Target is one of these needed names (per the spec,
		// every FK edge inside the SCC is extracted, not just enough to
		// break the cycle), then verify every needed target name was
		// covered by at least one extracted clause — if a needed target
		// has zero matching clauses, extraction didn't fully capture this
		// table's FK edges to it (bail out safely) — but a target with
		// two matching clauses (two FK columns to the same table) is
		// expected and both are taken.
		neededTargets := make(map[string]bool)
		for _, ref := range st.ps.DepRefs {
			if ref.Kind != parse.RefAuto {
				continue
			}
			j, isNode := byName[ref.Name]
			if !isNode || j == idx || !inSCC[j] {
				continue
			}
			neededTargets[ref.Name] = true
		}
		if len(neededTargets) == 0 {
			continue // this table has no internal-to-this-SCC FK edge
		}

		covered := make(map[string]bool, len(neededTargets))
		for _, cl := range clauses {
			if neededTargets[cl.Target] {
				toExtract = append(toExtract, extraction{stmtIdx: idx, clause: cl})
				covered[cl.Target] = true
			}
		}
		for target := range neededTargets {
			if !covered[target] {
				// A needed internal edge has no corresponding extracted
				// clause at all — extraction's view of this table's FK
				// shape doesn't match what the dependency graph says is
				// there. Fail safe rather than leave the edge in place
				// while claiming success.
				return false, unsplittableNote
			}
		}
	}

	if len(toExtract) == 0 {
		// Nothing to do (shouldn't happen: every SCC member has at least
		// one internal edge by definition of a >1-node SCC), but don't
		// claim success without having broken anything.
		return false, unsplittableNote
	}

	// Group clauses by source statement to rewrite each CREATE TABLE
	// once with all of its extracted clauses removed together.
	byStmt := make(map[int][]fkrewrite.Clause)
	var order []int
	for _, e := range toExtract {
		if _, ok := byStmt[e.stmtIdx]; !ok {
			order = append(order, e.stmtIdx)
		}
		byStmt[e.stmtIdx] = append(byStmt[e.stmtIdx], e.clause)
	}

	for _, idx := range order {
		st := stmts[idx]
		text := st.ps.Text
		if already, done := rewritten[idx]; done {
			text = already
		}
		newText := fkrewrite.Remove(text, byStmt[idx])
		rewritten[idx] = newText

		for subN, cl := range byStmt[idx] {
			alterSQL := fmt.Sprintf("ALTER TABLE %s ADD %s;", tableNames[idx], cl.AddSQL)
			synth, err := synthesizeStatement(alterSQL, st.file, dialect)
			if err != nil {
				return false, unsplittableNote
			}
			synth.index = st.index
			synth.subIndex = nextSubIndex(*stmtsPtr, st.file, st.index) + subN
			synth.synthesized = true
			synth.synthesizedFrom = st.file
			*stmtsPtr = append(*stmtsPtr, synth)
		}
	}

	return true, ""
}

// nextSubIndex returns the smallest subIndex not yet used by any
// statement sharing (file, index) in stmts, starting at 1 (0 is reserved
// for the real, source-parsed statement).
func nextSubIndex(stmts []statement, file string, index int) int {
	max := 0
	for _, s := range stmts {
		if s.file == file && s.index == index && s.subIndex >= max {
			max = s.subIndex + 1
		}
	}
	if max == 0 {
		max = 1
	}
	return max
}

// synthesizeStatement re-lexes and parses a synthesized ALTER TABLE
// string through the same pipeline real statements go through, so its
// Name/Deps/Kind come out identically to a hand-written equivalent. file
// is used only to build a fresh line map (positions on synthesized
// statements are not meaningful source locations, but parse.Parse still
// needs a line map/base to run).
func synthesizeStatement(sql, file string, dialect Dialect) (statement, error) {
	stmts, err := lexer.Split(sql, dialect)
	if err != nil {
		return statement{}, err
	}
	if len(stmts) != 1 {
		return statement{}, fmt.Errorf("synthesized ALTER TABLE did not parse as exactly one statement: %q", sql)
	}
	lm := pos.NewLineMap(sql)
	ps := parse.Parse(stmts[0], file, lm)
	return statement{file: file, ps: ps}, nil
}
