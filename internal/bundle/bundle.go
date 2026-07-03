// Package bundle discovers .sql files under a directory tree, parses and
// orders their statements, and emits a single combined .sql file.
package bundle

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Lazialize/sqldefkit/internal/graph"
	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/parse"
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

// statement is an internal record combining a parsed statement with its
// source location, used to build the graph and emit output.
type statement struct {
	file  string
	index int
	ps    parse.Statement
}

// Build reads all .sql files under root (via DiscoverFiles), parses their
// statements, orders them topologically, and returns the combined output
// bytes ready to write to a file or stdout.
func Build(root string, dialect Dialect, readFile func(path string) ([]byte, error)) ([]byte, error) {
	files, err := DiscoverFiles(root)
	if err != nil {
		return nil, err
	}

	var all []statement
	definedAt := make(map[string]string) // name -> "file:index" of first definition

	for _, rel := range files {
		abs := filepath.Join(root, rel)
		data, err := readFile(abs)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", rel, err)
		}
		// Normalize CRLF to LF right after reading so output is
		// deterministic (LF-only) regardless of the source files' line
		// endings (e.g. CRLF checkouts on Windows).
		src := strings.ReplaceAll(string(data), "\r\n", "\n")
		stmts, err := lexer.Split(src, dialect)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		for i, st := range stmts {
			ps := parse.Parse(st)
			if ps.Name != "" {
				loc := fmt.Sprintf("%s:%d", rel, i)
				if prev, ok := definedAt[ps.Name]; ok {
					return nil, fmt.Errorf("duplicate definition of %q: first defined at %s, redefined at %s", ps.Name, prev, loc)
				}
				definedAt[ps.Name] = loc
			}
			all = append(all, statement{file: rel, index: i, ps: ps})
		}
	}

	nodes := make([]graph.Node, len(all))
	for i, s := range all {
		nodes[i] = graph.Node{
			File:  s.file,
			Index: s.index,
			Name:  s.ps.Name,
			Deps:  s.ps.Deps,
		}
	}

	ordered, err := graph.Sort(nodes)
	if err != nil {
		return nil, err
	}

	// Re-associate ordered graph.Node results back to their full
	// statement (including text/comments) via (file, index) key, since
	// graph.Node doesn't carry statement text.
	byKey := make(map[string]statement, len(all))
	for _, s := range all {
		byKey[key(s.file, s.index)] = s
	}

	return emit(ordered, byKey), nil
}

func key(file string, index int) string {
	return fmt.Sprintf("%s\x00%d", file, index)
}
