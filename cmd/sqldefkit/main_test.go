package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// chdir changes the working directory for the duration of the test and
// restores it afterward.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRun_NoCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for no command")
	}
	if !strings.Contains(stderr.String(), "Commands:") {
		t.Errorf("stderr = %q, want usage listing commands", stderr.String())
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !strings.Contains(stderr.String(), "Commands:") {
		t.Errorf("stderr = %q, want usage listing commands", stderr.String())
	}
}

func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
}

func TestRun_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Errorf("stdout = %q, want %q", stdout.String(), version)
	}
}

func TestRun_Bundle_MissingDialect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "CREATE TABLE a (id int);")
	chdir(t, dir)

	var stdout, stderr bytes.Buffer
	err := run([]string{"bundle"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when dialect is missing from both flag and config")
	}
	if !strings.Contains(err.Error(), "--dialect") || !strings.Contains(err.Error(), "sqldefkit.yaml") {
		t.Errorf("error = %v, want mention of both --dialect and sqldefkit.yaml", err)
	}
}

func TestRun_Bundle_FlagsOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "CREATE TABLE a (id int);")

	var stdout, stderr bytes.Buffer
	err := run([]string{"bundle", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CREATE TABLE a") {
		t.Errorf("stdout = %q, want CREATE TABLE a", stdout.String())
	}
}

func TestRun_Bundle_ConfigSuppliesDialectAndDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	writeFile(t, filepath.Join(dir, "schema", "a.sql"), "CREATE TABLE a (id int);")
	chdir(t, dir)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"bundle"}, &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CREATE TABLE a") {
		t.Errorf("stdout = %q, want CREATE TABLE a", stdout.String())
	}
}

func TestRun_Bundle_FlagOverridesConfigDialect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sqldefkit.yaml"), "dialect: mysql\nschema_dir: schema\n")
	// Use backtick-quoted identifier, valid dialect-agnostic-ish SQL that
	// still parses under postgres lexing rules for this smoke test.
	writeFile(t, filepath.Join(dir, "schema", "a.sql"), "CREATE TABLE a (id int);")
	chdir(t, dir)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"bundle", "--dialect", "postgres"}, &stdout, &stderr); err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CREATE TABLE a") {
		t.Errorf("stdout = %q, want CREATE TABLE a", stdout.String())
	}
}

func TestRun_Bundle_ExplicitConfigMissingFileIsError(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"bundle", "--config", filepath.Join(dir, "nope.yaml"), "--dialect", "postgres"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing --config file")
	}
}

func TestRun_Bundle_OutputToFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "CREATE TABLE a (id int);")
	outPath := filepath.Join(dir, "out.sql")

	var stdout, stderr bytes.Buffer
	err := run([]string{"bundle", "--dir", dir, "--dialect", "postgres", "-o", outPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "CREATE TABLE a") {
		t.Errorf("output file = %q, want CREATE TABLE a", string(data))
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty when -o is given", stdout.String())
	}
}
