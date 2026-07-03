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

// Statement is the result of parsing one lexer.Statement.
type Statement struct {
	Kind Kind
	// Name is the normalized, schema-qualified name of the object this
	// statement defines, or "" if it doesn't define a named object
	// (e.g. KindOther).
	Name string
	// Deps holds normalized names this statement depends on (may
	// reference objects not defined anywhere in the bundle).
	Deps []string

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
