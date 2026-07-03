package bundle

import (
	"sort"

	"github.com/Lazialize/sqldefkit/internal/parse"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// Definition is one named object defined somewhere in the loaded schema
// tree.
type Definition struct {
	Name string
	Pos  pos.Position
	Kind parse.Kind
	File string
}

// Reference is one occurrence of a name used as a dependency somewhere in
// the loaded schema tree (a REFERENCES target, an INDEX/TRIGGER ON
// target, an ALTER TABLE target, a view FROM/JOIN best-effort scan, or a
// require-directive name).
type Reference struct {
	Name string
	Pos  pos.Position
	Kind parse.RefKind
	File string
}

// Symbols is a queryable index over every definition and reference found
// while loading a schema tree, built by Load. It's the Go API a future
// LSP server consumes for diagnostics and go-to-definition.
type Symbols struct {
	// Definitions maps a normalized name to every Definition recorded
	// under that name, in the order encountered (source file order).
	// Ordinarily this holds one entry; more than one means a duplicate
	// definition (see internal/diag diagnostics emitted by Load).
	Definitions map[string][]Definition
	// References holds every Reference found, in deterministic
	// (file, then source-encounter) order.
	References []Reference
}

// DefinitionNames returns every defined name in Symbols, sorted, for
// deterministic iteration.
func (s *Symbols) DefinitionNames() []string {
	names := make([]string, 0, len(s.Definitions))
	for name := range s.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FirstDefinition returns the first-encountered Definition for name (source
// order), and whether name is defined at all.
func (s *Symbols) FirstDefinition(name string) (Definition, bool) {
	defs := s.Definitions[name]
	if len(defs) == 0 {
		return Definition{}, false
	}
	return defs[0], true
}
