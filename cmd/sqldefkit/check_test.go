package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_Check_DuplicateDefinition(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "CREATE TABLE users (id int);")
	writeFile(t, filepath.Join(dir, "b.sql"), "CREATE TABLE users (id int);")

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error (exit 1) for duplicate definition")
	}
	out := stdout.String()
	if !strings.Contains(out, "b.sql:1:") || !strings.Contains(out, "error:") {
		t.Errorf("stdout = %q, want a b.sql error line", out)
	}
	if !strings.Contains(out, "a.sql") {
		t.Errorf("stdout = %q, want mention of first definition's file a.sql", out)
	}
}

// TestRun_Check_Cycle uses a cycle closed by a directive edge (not a
// foreign key), which stays a hard error: only FK-only cycles are
// auto-split now (see TestRun_Check_FKCycleBreakableExitZero).
func TestRun_Check_Cycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "-- sqldefkit:require b\nCREATE TABLE a (id int);")
	writeFile(t, filepath.Join(dir, "b.sql"), "CREATE TABLE b (id int, a_id int REFERENCES a(id));")

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error (exit 1) for dependency cycle")
	}
	out := stdout.String()
	if !strings.Contains(out, "error:") || !strings.Contains(out, "dependency cycle detected") {
		t.Errorf("stdout = %q, want a dependency cycle error line", out)
	}
}

// TestRun_Check_FKCycleBreakableExitZero verifies that a cycle made
// entirely of foreign keys exits 0 with no output, since bundle now
// splits it automatically instead of erroring.
func TestRun_Check_FKCycleBreakableExitZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "CREATE TABLE a (id int, b_id int REFERENCES b(id));")
	writeFile(t, filepath.Join(dir, "b.sql"), "CREATE TABLE b (id int, a_id int REFERENCES a(id));")

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error (breakable FK cycle should exit 0): %v (stderr=%s)", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty output for a breakable FK cycle", stdout.String())
	}
}

func TestRun_Check_UnknownReferenceWarningExitZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "orders.sql"), `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error (warnings alone should exit 0): %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "warning:") || !strings.Contains(out, `"users"`) {
		t.Errorf("stdout = %q, want a warning line mentioning users", out)
	}
}

func TestRun_Check_UnknownDirectiveNameWarning(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "views.sql"), `-- sqldefkit:require typo_name
CREATE VIEW v AS SELECT 1;`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "typo_name") {
		t.Errorf("stdout = %q, want a warning line mentioning typo_name", out)
	}
}

func TestRun_Check_ViewBestEffortRefDoesNotWarn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "orders.sql"), `CREATE TABLE orders (id int PRIMARY KEY);`)
	writeFile(t, filepath.Join(dir, "views.sql"), `CREATE VIEW v AS SELECT * FROM orders o JOIN nonexistent_thing x ON x.id = o.id;`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "nonexistent_thing") {
		t.Errorf("stdout = %q, did not want a warning for a view FROM/JOIN best-effort ref", out)
	}
}

func TestRun_Check_CleanTreeEmptyOutputExitZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id int PRIMARY KEY, email text NOT NULL);`)
	writeFile(t, filepath.Join(dir, "orders.sql"), `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"check", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty output for a clean schema", stdout.String())
	}
}

func TestRun_Check_MissingDialectIsStderrError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), "CREATE TABLE a (id int);")
	chdir(t, dir)

	var stdout, stderr bytes.Buffer
	err := run([]string{"check"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when dialect is missing from both flag and config")
	}
	if !strings.Contains(err.Error(), "--dialect") {
		t.Errorf("error = %v, want mention of --dialect", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (error should not print diagnostics)", stdout.String())
	}
}
