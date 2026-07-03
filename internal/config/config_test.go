package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lazialize/sqldefkit/internal/bundle"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_FromNestedDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")

	nested := filepath.Join(root, "schema", "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Discover(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Dir != root {
		t.Errorf("Dir = %q, want %q", cfg.Dir, root)
	}
	if !cfg.HasDialect || cfg.Dialect != bundle.Postgres {
		t.Errorf("Dialect = %v (has=%v), want postgres", cfg.Dialect, cfg.HasDialect)
	}
	wantSchemaDir := filepath.Join(root, "schema")
	if cfg.SchemaDir != wantSchemaDir {
		t.Errorf("SchemaDir = %q, want %q", cfg.SchemaDir, wantSchemaDir)
	}
}

func TestDiscover_NotFound(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(dir)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDiscover_BothYamlAndYmlIsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sqldefkit.yaml"), "dialect: postgres\n")
	writeFile(t, filepath.Join(dir, "sqldefkit.yml"), "dialect: postgres\n")

	_, err := Discover(dir)
	if err == nil {
		t.Fatal("expected error when both sqldefkit.yaml and sqldefkit.yml exist")
	}
}

func TestLoad_UnknownFieldIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqldefkit.yaml")
	writeFile(t, path, "dialect: postgres\nbogus_field: true\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoad_InvalidDialect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqldefkit.yaml")
	writeFile(t, path, "dialect: oracle\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid dialect")
	}
}

func TestLoad_RelativePathResolution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqldefkit.yaml")
	writeFile(t, path, "schema_dir: schema\nout: bundled.sql\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(dir, "schema"); cfg.SchemaDir != want {
		t.Errorf("SchemaDir = %q, want %q", cfg.SchemaDir, want)
	}
	if want := filepath.Join(dir, "bundled.sql"); cfg.Out != want {
		t.Errorf("Out = %q, want %q", cfg.Out, want)
	}
}

func TestLoad_AbsoluteSchemaDirPassthrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqldefkit.yaml")

	abs := filepath.Join(t.TempDir(), "somewhere", "schema")
	writeFile(t, path, "schema_dir: "+abs+"\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchemaDir != abs {
		t.Errorf("SchemaDir = %q, want %q (absolute passthrough)", cfg.SchemaDir, abs)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqldefkit.yaml")
	writeFile(t, path, "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error for empty config file: %v", err)
	}
	if cfg.HasDialect {
		t.Error("HasDialect = true for empty file")
	}
	if cfg.SchemaDir != "" || cfg.Out != "" {
		t.Errorf("expected empty SchemaDir/Out, got %q / %q", cfg.SchemaDir, cfg.Out)
	}
}

func TestDiscover_StopsAtFirstMatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: mysql\n")

	sub := filepath.Join(root, "sub")
	writeFile(t, filepath.Join(sub, "sqldefkit.yaml"), "dialect: postgres\n")

	cfg, err := Discover(sub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Dialect != bundle.Postgres {
		t.Errorf("Dialect = %v, want postgres (closest config file should win)", cfg.Dialect)
	}
}
