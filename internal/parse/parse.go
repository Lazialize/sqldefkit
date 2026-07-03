// Package parse extracts, from each lexer.Statement, the kind of DDL
// object it defines, the object's normalized name, and the normalized
// names of objects it depends on. It also reads sqldefkit directives from
// attached leading comments.
//
// This is not a general SQL parser: it scans the statement's token stream
// for a small set of recognizable keyword patterns. Anything it doesn't
// recognize is classified as KindOther and carries no dependency
// information beyond directives.
package parse

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// Kind classifies the DDL statement.
type Kind int

const (
	KindOther Kind = iota
	KindCreateTable
	KindCreateView
	KindCreateIndex
	KindCreateFunction
	KindCreateTrigger
	KindCreateType
	KindCreateSequence
	KindCreateExtension
	KindAlterTable
)

// RefKind classifies how a dependency reference (Ref) was discovered,
// which in turn determines its confidence level for diagnostics: Auto and
// Directive are high-confidence (worth warning about if unresolved),
// ViewScan is best-effort and deliberately not warned about (see
// internal/bundle diagnostics).
type RefKind int

const (
	// RefAuto is a REFERENCES target, an INDEX/TRIGGER "ON" target, or an
	// ALTER TABLE target — all unambiguous, syntax-driven edges.
	RefAuto RefKind = iota
	// RefViewScan is an identifier following a top-level FROM/JOIN in a
	// view/materialized view body — best-effort, prone to false
	// positives (aliases, subqueries), so never warned about when
	// unresolved.
	RefViewScan
	// RefDirective is a name listed in a `-- sqldefkit:require` comment
	// directive.
	RefDirective
)

// Ref is a single named reference (dependency) found within a statement,
// carrying the exact source position of the name and how it was found.
type Ref struct {
	Name string
	Pos  pos.Position
	Kind RefKind
}

// Statement is the result of parsing one lexer.Statement.
type Statement struct {
	Kind Kind
	// Name is the normalized, schema-qualified name of the object this
	// statement defines, or "" if it doesn't define a named object
	// (e.g. KindOther).
	Name string
	// NamePos is the source position of Name's token (the first token of
	// a schema-qualified name), valid only when Name != "".
	NamePos pos.Position
	// Deps holds normalized names this statement depends on (may
	// reference objects not defined anywhere in the bundle). Kept as a
	// plain string slice for existing consumers (e.g. internal/graph);
	// see DepRefs for the same information with positions and edge
	// kinds attached.
	Deps []string
	// DepRefs holds one Ref per entry in Deps (same names, same order
	// before deduplication would apply — DepRefs is not deduplicated,
	// so a name referenced twice appears twice, once per occurrence).
	DepRefs []Ref

	// Source data carried through for bundling/diagnostics.
	Text            string
	LeadingComments []string
}

// normalizeToken folds a single identifier token's value the way
// PostgreSQL does: quoted identifiers keep their case as written; unquoted
// identifiers are lower-cased. This is applied uniformly across dialects
// as a documented approximation for MySQL/SQLite, whose real
// case-sensitivity rules are platform/collation dependent.
func normalizeToken(t lexer.Token) string {
	if t.Kind == lexer.TokenQuotedIdent {
		return t.Value
	}
	return strings.ToLower(t.Value)
}
