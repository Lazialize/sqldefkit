package bundle

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Lazialize/sqldefkit/internal/diag"
	"github.com/Lazialize/sqldefkit/internal/graph"
	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/parse"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// statement is an internal record combining a parsed statement with its
// source location, used to build the graph and emit output.
type statement struct {
	file  string
	index int
	ps    parse.Statement
}

// Loaded is the result of reading and parsing every .sql file under a
// schema root: every statement (in file-then-source order, not yet
// dependency-sorted), the symbol index built from them, and any
// diagnostics found along the way (duplicate definitions, unresolved
// high-confidence references — dependency cycles are added separately by
// callers that need topological order, since detecting a cycle requires
// running the sort). Load does not fail fast: I/O errors reading an
// individual file are the only thing that aborts loading early (returned
// as err), since there's no statement content to diagnose or index in
// that case.
type Loaded struct {
	Files   []string
	Stmts   []statement
	Symbols *Symbols
	Diags   []diag.Diagnostic
}

// Load discovers, reads, and parses every .sql file under root, building
// the combined statement list and symbol index in one pass. It is the
// shared first step for both Build (bundle) and a future `check`
// subcommand: Build turns error-severity diagnostics into a hard error to
// preserve its historical fail-fast behavior; check reports every
// diagnostic (errors and warnings) without failing.
//
// Load itself only returns a non-nil error for problems that make
// loading impossible to reason about further (a file can't be read, or
// DiscoverFiles finds no .sql files at all). Lex failures, duplicate
// definitions, and unresolved references are reported as Diagnostics in
// the returned Loaded.Diags, not as err.
func Load(root string, dialect Dialect, readFile func(path string) ([]byte, error)) (Loaded, error) {
	files, err := DiscoverFiles(root)
	if err != nil {
		return Loaded{}, err
	}

	var all []statement
	symbols := &Symbols{Definitions: make(map[string][]Definition)}
	var diags []diag.Diagnostic

	for _, rel := range files {
		abs := filepath.Join(root, rel)
		data, err := readFile(abs)
		if err != nil {
			return Loaded{}, fmt.Errorf("reading %s: %w", rel, err)
		}
		// Normalize CRLF to LF right after reading so output is
		// deterministic (LF-only) regardless of the source files' line
		// endings (e.g. CRLF checkouts on Windows). Positions are
		// computed against this normalized text; see internal/pos's
		// Position doc comment.
		src := strings.ReplaceAll(string(data), "\r\n", "\n")
		stmts, lexErr := lexer.Split(src, dialect)
		if lexErr != nil {
			diags = append(diags, diag.Diagnostic{
				Pos:      pos.Position{File: rel, Line: 1, Col: 1},
				Severity: diag.Error,
				Message:  fmt.Sprintf("%s: %s", rel, lexErr.Error()),
			})
			continue
		}

		lm := pos.NewLineMap(src)
		for i, st := range stmts {
			ps := parse.Parse(st, rel, lm)

			if ps.Name != "" {
				def := Definition{Name: ps.Name, Pos: ps.NamePos, Kind: ps.Kind, File: rel}
				if prev, ok := symbols.FirstDefinition(ps.Name); ok {
					diags = append(diags, diag.Diagnostic{
						Pos:      ps.NamePos,
						Severity: diag.Error,
						Message:  fmt.Sprintf("duplicate definition of %q: first defined at %s, redefined at %s", ps.Name, prev.Pos.String(), ps.NamePos.String()),
					})
				}
				symbols.Definitions[ps.Name] = append(symbols.Definitions[ps.Name], def)
			}

			for _, ref := range ps.DepRefs {
				symbols.References = append(symbols.References, Reference{
					Name: ref.Name,
					Pos:  ref.Pos,
					Kind: ref.Kind,
					File: rel,
				})
			}

			all = append(all, statement{file: rel, index: i, ps: ps})
		}
	}

	diags = append(diags, unresolvedReferenceDiagnostics(symbols)...)

	diag.SortDiagnostics(diags)

	return Loaded{Files: files, Stmts: all, Symbols: symbols, Diags: diags}, nil
}

// unresolvedReferenceDiagnostics reports a warning for every high-confidence
// Reference (RefAuto or RefDirective) whose name isn't defined anywhere in
// symbols. RefViewScan references are best-effort and deliberately never
// warned about (see parse.RefKind doc comment).
func unresolvedReferenceDiagnostics(symbols *Symbols) []diag.Diagnostic {
	var diags []diag.Diagnostic
	for _, ref := range symbols.References {
		if ref.Kind == parse.RefViewScan {
			continue
		}
		if _, ok := symbols.Definitions[ref.Name]; ok {
			continue
		}
		diags = append(diags, diag.Diagnostic{
			Pos:      ref.Pos,
			Severity: diag.Warning,
			Message:  fmt.Sprintf("unknown reference %q: not defined in this schema", ref.Name),
		})
	}
	return diags
}

// graphNodes builds graph.Node values from loaded statements, for
// topological sorting.
func (l Loaded) graphNodes() []graph.Node {
	nodes := make([]graph.Node, len(l.Stmts))
	for i, s := range l.Stmts {
		nodes[i] = graph.Node{
			File:  s.file,
			Index: s.index,
			Name:  s.ps.Name,
			Deps:  s.ps.Deps,
		}
	}
	return nodes
}

func key(file string, index int) string {
	return fmt.Sprintf("%s\x00%d", file, index)
}

// CheckDiagnostics returns every diagnostic for the schema tree under
// root: everything Load already found (duplicate definitions, lex
// failures, unresolved high-confidence references) plus a dependency
// cycle diagnostic if the statements don't topologically sort. This is
// the full diagnostic set the `check` subcommand reports; unlike Build,
// it never fails fast on error-severity diagnostics.
//
// A cycle is reported once, as a single error-severity diagnostic
// positioned at the first-sorted participant in the cycle (the same node
// internal/graph's error message starts from), with the same "a -> b ->
// ... -> a" path text graph.Sort has always produced.
func CheckDiagnostics(root string, dialect Dialect, readFile func(path string) ([]byte, error)) ([]diag.Diagnostic, error) {
	loaded, err := Load(root, dialect, readFile)
	if err != nil {
		return nil, err
	}

	diags := append([]diag.Diagnostic(nil), loaded.Diags...)

	if _, err := graph.Sort(loaded.graphNodes()); err != nil {
		var cycleErr *graph.CycleError
		if errors.As(err, &cycleErr) && len(cycleErr.Nodes) > 0 {
			first := cycleErr.Nodes[0]
			p := pos.Position{File: first.File, Line: 1, Col: 1}
			if def, ok := loaded.Symbols.FirstDefinition(first.Name); ok {
				p = def.Pos
			}
			diags = append(diags, diag.Diagnostic{
				Pos:      p,
				Severity: diag.Error,
				Message:  cycleErr.Error(),
			})
		} else {
			diags = append(diags, diag.Diagnostic{
				Pos:      pos.Position{Line: 1, Col: 1},
				Severity: diag.Error,
				Message:  err.Error(),
			})
		}
	}

	diag.SortDiagnostics(diags)
	return diags, nil
}
