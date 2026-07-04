package fkrewrite

import (
	"strings"
	"testing"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

func extractOne(t *testing.T, sql string) ([]Clause, bool) {
	t.Helper()
	stmts, err := lexer.Split(sql, lexer.Postgres)
	if err != nil {
		t.Fatalf("lexer.Split error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected exactly 1 statement, got %d", len(stmts))
	}
	return Extract(stmts[0].Text, stmts[0].Tokens)
}

func TestExtract_TableLevelFirst(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int,
	FOREIGN KEY (user_id) REFERENCES users (id),
	amount int NOT NULL
);`
	clauses, ok := extractOne(t, sql)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d: %+v", len(clauses), clauses)
	}
	c := clauses[0]
	if !c.TableLevel {
		t.Errorf("expected TableLevel=true")
	}
	if c.Target != "users" {
		t.Errorf("target = %q, want users", c.Target)
	}
	if c.AddSQL != "FOREIGN KEY (user_id) REFERENCES users (id)" {
		t.Errorf("AddSQL = %q", c.AddSQL)
	}
}

func stmtText(t *testing.T, sql string) (string, []lexer.Token) {
	t.Helper()
	stmts, err := lexer.Split(sql, lexer.Postgres)
	if err != nil {
		t.Fatalf("lexer.Split error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	return stmts[0].Text, stmts[0].Tokens
}

func TestRemove_TableLevelFirstMiddleLast(t *testing.T) {
	const want = `CREATE TABLE orders (
	id int PRIMARY KEY,
	amount int NOT NULL
)`
	cases := []struct {
		name string
		sql  string
	}{
		{"first", `CREATE TABLE orders (
	FOREIGN KEY (user_id) REFERENCES users (id),
	id int PRIMARY KEY,
	amount int NOT NULL
)`},
		{"middle", `CREATE TABLE orders (
	id int PRIMARY KEY,
	FOREIGN KEY (user_id) REFERENCES users (id),
	amount int NOT NULL
)`},
		{"last", `CREATE TABLE orders (
	id int PRIMARY KEY,
	amount int NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users (id)
)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, toks := stmtText(t, tc.sql)
			clauses, ok := Extract(text, toks)
			if !ok || len(clauses) != 1 {
				t.Fatalf("Extract ok=%v clauses=%+v", ok, clauses)
			}
			out := Remove(text, clauses)
			if out != want {
				t.Errorf("out = %q, want %q", out, want)
			}
			if strings.Contains(out, "FOREIGN KEY") {
				t.Errorf("clause not removed: %q", out)
			}
			if strings.Contains(out, ",,") {
				t.Errorf("double comma left: %q", out)
			}
			if strings.Contains(out, ", )") || strings.Contains(out, ",)") {
				t.Errorf("dangling comma before close paren: %q", out)
			}
			if strings.Contains(out, "(,") || strings.Contains(out, "( ,") {
				t.Errorf("dangling comma after open paren: %q", out)
			}
		})
	}
}

func TestExtract_NamedConstraint(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int,
	CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users (id)
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 1 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	if !strings.Contains(clauses[0].AddSQL, "CONSTRAINT fk_user") {
		t.Errorf("AddSQL = %q", clauses[0].AddSQL)
	}
}

func TestExtract_InlineReferences(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 1 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	c := clauses[0]
	if c.TableLevel {
		t.Errorf("expected inline (TableLevel=false)")
	}
	if c.Column != "user_id" {
		t.Errorf("column = %q", c.Column)
	}
	if c.Target != "users" {
		t.Errorf("target = %q", c.Target)
	}
	if c.AddSQL != "FOREIGN KEY (user_id) REFERENCES users(id)" {
		t.Errorf("AddSQL = %q", c.AddSQL)
	}

	out := Remove(text, clauses)
	if strings.Contains(out, "REFERENCES") {
		t.Errorf("REFERENCES not removed: %q", out)
	}
	if !strings.Contains(out, "user_id int") {
		t.Errorf("column definition lost: %q", out)
	}
}

func TestExtract_InlineWithOnDeleteUpdateMatchDeferrable(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id) MATCH FULL ON DELETE CASCADE ON UPDATE SET NULL DEFERRABLE INITIALLY DEFERRED
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 1 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	c := clauses[0]
	want := "FOREIGN KEY (user_id) REFERENCES users(id) MATCH FULL ON DELETE CASCADE ON UPDATE SET NULL DEFERRABLE INITIALLY DEFERRED"
	if c.AddSQL != want {
		t.Errorf("AddSQL = %q, want %q", c.AddSQL, want)
	}
	out := Remove(text, clauses)
	if strings.Contains(out, "REFERENCES") || strings.Contains(out, "CASCADE") {
		t.Errorf("clause not fully removed: %q", out)
	}
	if !strings.Contains(out, "user_id int") {
		t.Errorf("column lost: %q", out)
	}
}

func TestExtract_InlineNotDeferrable(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id) NOT DEFERRABLE
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 1 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	want := "FOREIGN KEY (user_id) REFERENCES users(id) NOT DEFERRABLE"
	if clauses[0].AddSQL != want {
		t.Errorf("AddSQL = %q, want %q", clauses[0].AddSQL, want)
	}
}

func TestExtract_InlineFollowedByNotNull(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id) NOT NULL
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 1 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	want := "FOREIGN KEY (user_id) REFERENCES users(id)"
	if clauses[0].AddSQL != want {
		t.Errorf("AddSQL = %q, want %q", clauses[0].AddSQL, want)
	}
	out := Remove(text, clauses)
	if !strings.Contains(out, "user_id int NOT NULL") {
		t.Errorf("expected NOT NULL retained on column: %q", out)
	}
	if strings.Contains(out, "REFERENCES") {
		t.Errorf("REFERENCES not removed: %q", out)
	}
}

func TestExtract_QuotedSchemaQualifiedTarget(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES public."Users"(id)
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 1 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	if clauses[0].Target != "public.Users" {
		t.Errorf("target = %q, want public.Users", clauses[0].Target)
	}
}

func TestExtract_NoFK(t *testing.T) {
	sql := `CREATE TABLE users (
	id int PRIMARY KEY,
	email text NOT NULL UNIQUE
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(clauses) != 0 {
		t.Errorf("expected no clauses, got %+v", clauses)
	}
}

func TestTableName_Simple(t *testing.T) {
	text, toks := stmtText(t, `CREATE TABLE orders (id int)`)
	name, ok := TableName(text, toks)
	if !ok || name != "orders" {
		t.Errorf("name=%q ok=%v, want \"orders\"", name, ok)
	}
}

func TestTableName_QuotedPreservesCaseAndQuotes(t *testing.T) {
	text, toks := stmtText(t, `CREATE TABLE "Orders" (id int)`)
	name, ok := TableName(text, toks)
	if !ok || name != `"Orders"` {
		t.Errorf(`name=%q ok=%v, want "\"Orders\""`, name, ok)
	}
}

func TestTableName_SchemaQualifiedQuoted(t *testing.T) {
	text, toks := stmtText(t, `CREATE TABLE public."Orders" (id int)`)
	name, ok := TableName(text, toks)
	if !ok || name != `public."Orders"` {
		t.Errorf(`name=%q ok=%v, want public."Orders"`, name, ok)
	}
}

func TestTableName_IfNotExists(t *testing.T) {
	text, toks := stmtText(t, `CREATE TABLE IF NOT EXISTS orders (id int)`)
	name, ok := TableName(text, toks)
	if !ok || name != "orders" {
		t.Errorf("name=%q ok=%v, want \"orders\"", name, ok)
	}
}

func TestExtract_MultipleFKs(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id),
	product_id int REFERENCES products(id)
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 2 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	out := Remove(text, clauses)
	if strings.Contains(out, "REFERENCES") {
		t.Errorf("clauses not removed: %q", out)
	}
	if !strings.Contains(out, "user_id int") || !strings.Contains(out, "product_id int") {
		t.Errorf("columns lost: %q", out)
	}
	if strings.Contains(out, ",,") {
		t.Errorf("double comma: %q", out)
	}
}

// TestRemove_AdjacentTableLevelClauses verifies that removing two
// adjacent table-level FOREIGN KEY items back to back leaves no blank
// line or stray comma behind.
func TestRemove_AdjacentTableLevelClauses(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	FOREIGN KEY (user_id) REFERENCES users (id),
	FOREIGN KEY (product_id) REFERENCES products (id),
	amount int NOT NULL
)`
	const want = `CREATE TABLE orders (
	id int PRIMARY KEY,
	amount int NOT NULL
)`
	text, toks := stmtText(t, sql)
	clauses, ok := Extract(text, toks)
	if !ok || len(clauses) != 2 {
		t.Fatalf("ok=%v clauses=%+v", ok, clauses)
	}
	out := Remove(text, clauses)
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}
