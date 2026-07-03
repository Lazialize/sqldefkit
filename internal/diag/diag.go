// Package diag defines the diagnostic type shared by internal/bundle
// (which turns error-severity diagnostics into a hard error, preserving
// today's bundle behavior) and the sqldefkit check subcommand (which
// reports every diagnostic, errors and warnings alike, without failing
// fast).
package diag

import (
	"sort"

	"github.com/Lazialize/sqldefkit/internal/pos"
)

// Severity classifies a Diagnostic.
type Severity int

const (
	// Error indicates a problem that should fail a build: a lex/parse
	// failure, a duplicate definition, or a dependency cycle.
	Error Severity = iota
	// Warning indicates a likely problem that doesn't prevent bundling:
	// currently, an unresolved high-confidence reference (REFERENCES,
	// INDEX/TRIGGER ON, ALTER TABLE target, or a require directive
	// name).
	Warning
)

// String renders the severity the way it appears in check's output
// ("error" / "warning").
func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic is one reported problem, located at a source Position.
type Diagnostic struct {
	Pos      pos.Position
	Severity Severity
	Message  string
}

// SortDiagnostics sorts diags in place by (file, line, col), matching the
// order `sqldefkit check` prints them in.
func SortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i].Pos, diags[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}

// HasError reports whether diags contains at least one Error-severity
// diagnostic.
func HasError(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}
