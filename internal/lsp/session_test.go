package lsp

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProject_FileUnderSchemaDir(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	sqlPath := filepath.Join(root, "schema", "users.sql")
	writeTestFile(t, sqlPath, "CREATE TABLE users (id int PRIMARY KEY);")

	s := newSession(newLog(io.Discard))
	proj, ok := s.resolveProject(sqlPath)
	if !ok {
		t.Fatalf("expected project to be resolved")
	}
	if proj.cfg.Dir != root {
		t.Errorf("proj.cfg.Dir = %q, want %q", proj.cfg.Dir, root)
	}
}

func TestResolveProject_FileOutsideSchemaDir(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	// A .sql file that lives outside schema_dir, in a sibling directory.
	outside := filepath.Join(root, "other", "users.sql")
	writeTestFile(t, outside, "CREATE TABLE users (id int PRIMARY KEY);")

	s := newSession(newLog(io.Discard))
	_, ok := s.resolveProject(outside)
	if ok {
		t.Fatalf("expected no project for file outside schema_dir")
	}
}

func TestResolveProject_NoConfig(t *testing.T) {
	root := t.TempDir()
	sqlPath := filepath.Join(root, "users.sql")
	writeTestFile(t, sqlPath, "CREATE TABLE users (id int PRIMARY KEY);")

	s := newSession(newLog(io.Discard))
	_, ok := s.resolveProject(sqlPath)
	if ok {
		t.Fatalf("expected no project when no config file exists")
	}
}

func TestResolveProject_NoDialect(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "schema_dir: schema\n")
	sqlPath := filepath.Join(root, "schema", "users.sql")
	writeTestFile(t, sqlPath, "CREATE TABLE users (id int PRIMARY KEY);")

	s := newSession(newLog(io.Discard))
	_, ok := s.resolveProject(sqlPath)
	if ok {
		t.Fatalf("expected no project when config has no dialect")
	}
}

func TestIdentifierSpan_Unquoted(t *testing.T) {
	content := "CREATE TABLE users (id int);"
	start := 13 // 'u' of users
	end := identifierSpan(content, start)
	if content[start:end] != "users" {
		t.Errorf("identifierSpan = %q, want %q", content[start:end], "users")
	}
}

func TestIdentifierSpan_Quoted(t *testing.T) {
	content := `CREATE TABLE "my table" (id int);`
	start := 13 // the opening quote
	end := identifierSpan(content, start)
	if content[start:end] != `"my table"` {
		t.Errorf("identifierSpan = %q, want %q", content[start:end], `"my table"`)
	}
}

func TestIdentifierSpan_OutOfBounds(t *testing.T) {
	content := "abc"
	end := identifierSpan(content, 100)
	if end != 100 {
		t.Errorf("identifierSpan out of bounds = %d, want 100 (zero-length fallback)", end)
	}
}
