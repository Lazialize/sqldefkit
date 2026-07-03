package bundle

import (
	"os"
	"strings"
	"testing"

	"github.com/Lazialize/sqldefkit/internal/diag"
	"github.com/Lazialize/sqldefkit/internal/parse"
)

// TestLoad_SymbolsDefinitionsAndReferences checks that Load's Symbols
// index contains the expected definitions and references, with correct
// kinds, across multiple files.
func TestLoad_SymbolsDefinitionsAndReferences(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users.sql", `CREATE TABLE users (id int PRIMARY KEY);`)
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)
	writeFile(t, dir, "idx.sql", `CREATE INDEX idx_orders_user ON orders (user_id);`)
	writeFile(t, dir, "views.sql", `-- sqldefkit:require users
CREATE VIEW v AS SELECT * FROM orders o;`)

	loaded, err := Load(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{"users", "orders", "idx_orders_user", "v"} {
		if _, ok := loaded.Symbols.FirstDefinition(name); !ok {
			t.Errorf("expected definition for %q", name)
		}
	}

	var foundAuto, foundDirective, foundViewScan bool
	for _, ref := range loaded.Symbols.References {
		switch {
		case ref.Name == "users" && ref.Kind == parse.RefAuto:
			foundAuto = true
		case ref.Name == "users" && ref.Kind == parse.RefDirective:
			foundDirective = true
		case ref.Name == "orders" && ref.Kind == parse.RefViewScan:
			foundViewScan = true
		}
	}
	if !foundAuto {
		t.Error("expected a RefAuto reference to users (REFERENCES/ON)")
	}
	if !foundDirective {
		t.Error("expected a RefDirective reference to users (require directive)")
	}
	if !foundViewScan {
		t.Error("expected a RefViewScan reference to orders (view FROM)")
	}

	if len(loaded.Diags) != 0 {
		t.Errorf("expected no diagnostics for a clean schema, got %+v", loaded.Diags)
	}
}

// TestLoad_Deterministic verifies that repeated Load calls over the same
// input produce identical Symbols.References order and Diags content.
func TestLoad_Deterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (id int, b_id int REFERENCES b(id));`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id int);`)
	writeFile(t, dir, "c.sql", `-- sqldefkit:require missing_one
CREATE VIEW v AS SELECT 1;`)

	var prevRefs []Reference
	var prevDiagsLen = -1
	for i := 0; i < 5; i++ {
		loaded, err := Load(dir, Postgres, os.ReadFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prevRefs != nil {
			if len(loaded.Symbols.References) != len(prevRefs) {
				t.Fatalf("non-deterministic reference count across runs")
			}
			for i, r := range loaded.Symbols.References {
				if r != prevRefs[i] {
					t.Fatalf("non-deterministic reference order across runs: %+v vs %+v", loaded.Symbols.References, prevRefs)
				}
			}
		}
		prevRefs = loaded.Symbols.References
		if prevDiagsLen != -1 && len(loaded.Diags) != prevDiagsLen {
			t.Fatalf("non-deterministic diagnostic count across runs")
		}
		prevDiagsLen = len(loaded.Diags)
	}
}

func TestLoad_DuplicateDefinitionDiagnostic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE users (id int);`)
	writeFile(t, dir, "b.sql", `CREATE TABLE users (id int);`)

	loaded, err := Load(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var found bool
	for _, d := range loaded.Diags {
		if d.Severity == diag.Error && d.Pos.File == "b.sql" {
			found = true
			if !strings.Contains(d.Message, "a.sql") {
				t.Errorf("message = %q, expected mention of a.sql", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected duplicate-definition diagnostic at b.sql, got %+v", loaded.Diags)
	}
}

func TestLoad_UnknownReferenceWarningOnlyForHighConfidence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)
	writeFile(t, dir, "views.sql", `CREATE VIEW v AS SELECT * FROM some_undefined_table;`)

	loaded, err := Load(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawUsersWarning, sawViewScanWarning bool
	for _, d := range loaded.Diags {
		if d.Severity != diag.Warning {
			continue
		}
		if strings.Contains(d.Message, `"users"`) {
			sawUsersWarning = true
		}
		if strings.Contains(d.Message, "some_undefined_table") {
			sawViewScanWarning = true
		}
	}
	if !sawUsersWarning {
		t.Errorf("expected warning for unresolved REFERENCES target 'users', got %+v", loaded.Diags)
	}
	if sawViewScanWarning {
		t.Errorf("did not expect a warning for best-effort view FROM/JOIN scan, got %+v", loaded.Diags)
	}
}

func TestCheckDiagnostics_CyclePositionAtFirstParticipant(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (id int, b_id int REFERENCES b(id));`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id int, a_id int REFERENCES a(id));`)

	diags, err := CheckDiagnostics(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var found bool
	for _, d := range diags {
		if d.Severity == diag.Error && strings.Contains(d.Message, "dependency cycle detected") {
			found = true
			if d.Pos.File != "a.sql" {
				t.Errorf("cycle diagnostic file = %q, want a.sql (first-sorted participant)", d.Pos.File)
			}
			if d.Pos.Line == 0 {
				t.Errorf("cycle diagnostic has no line info: %+v", d.Pos)
			}
		}
	}
	if !found {
		t.Errorf("expected a cycle diagnostic, got %+v", diags)
	}
}

func TestCheckDiagnostics_CleanTreeNoDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users.sql", `CREATE TABLE users (id int PRIMARY KEY);`)
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (id int PRIMARY KEY, user_id int REFERENCES users(id));`)

	diags, err := CheckDiagnostics(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v", diags)
	}
}
