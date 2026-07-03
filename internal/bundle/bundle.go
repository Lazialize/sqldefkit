// Package bundle discovers .sql files under a directory tree, parses and
// orders their statements, and emits a single combined .sql file.
package bundle

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Lazialize/sqldefkit/internal/diag"
	"github.com/Lazialize/sqldefkit/internal/graph"
	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// Dialect mirrors lexer.Dialect for callers outside this package that
// don't want to import internal/lexer directly (kept as an alias so
// there's exactly one enum definition).
type Dialect = lexer.Dialect

const (
	Postgres = lexer.Postgres
	MySQL    = lexer.MySQL
	SQLite   = lexer.SQLite
)

// ParseDialect parses a dialect name ("postgres", "mysql", or "sqlite")
// into a Dialect value, returning an error for any other value (including
// the empty string).
func ParseDialect(s string) (Dialect, error) {
	switch s {
	case "postgres":
		return Postgres, nil
	case "mysql":
		return MySQL, nil
	case "sqlite":
		return SQLite, nil
	default:
		return 0, fmt.Errorf("unknown dialect %q (expected postgres, mysql, or sqlite)", s)
	}
}

// DiscoverFiles recursively collects *.sql files under root, sorted
// lexicographically by path relative to root. Hidden directories (name
// starting with ".") are skipped entirely.
func DiscoverFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no .sql files found under %s", root)
	}
	return files, nil
}

// Build reads all .sql files under root (via Load), parses their
// statements, orders them topologically, and returns the combined output
// bytes ready to write to a file or stdout.
//
// Build preserves its historical fail-fast behavior on top of the shared
// Load step: the first error-severity diagnostic (duplicate definition or
// lex/parse failure) becomes the returned error, with the same message
// text as before. Warnings (unresolved references) are not surfaced here
// — use the `check` subcommand (internal/bundle.Load directly) to see
// them.
func Build(root string, dialect Dialect, readFile func(path string) ([]byte, error)) ([]byte, error) {
	loaded, err := Load(root, dialect, readFile)
	if err != nil {
		return nil, err
	}

	if err := firstErrorDiagnostic(loaded.Diags); err != nil {
		return nil, err
	}

	nodes := loaded.graphNodes()
	ordered, err := graph.Sort(nodes)
	if err != nil {
		return nil, err
	}

	// Re-associate ordered graph.Node results back to their full
	// statement (including text/comments) via (file, index) key, since
	// graph.Node doesn't carry statement text.
	byKey := make(map[string]statement, len(loaded.Stmts))
	for _, s := range loaded.Stmts {
		byKey[key(s.file, s.index)] = s
	}

	return emit(ordered, byKey), nil
}

// firstErrorDiagnostic returns an error built from the first error-severity
// diagnostic in diags (which is sorted by position, so this is
// deterministic), or nil if there are none. The message text matches
// Build's pre-refactor wording so existing callers/tests aren't affected.
func firstErrorDiagnostic(diags []diag.Diagnostic) error {
	for _, d := range diags {
		if d.Severity == diag.Error {
			return fmt.Errorf("%s", d.Message)
		}
	}
	return nil
}
