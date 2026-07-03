package parse

import (
	"testing"

	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// parseFile splits src (as if it were the full content of a file named
// "f.sql") and parses every statement with file-absolute positions, the
// way internal/bundle does.
func parseFile(t *testing.T, src string, dialect lexer.Dialect) []Statement {
	t.Helper()
	stmts, err := lexer.Split(src, dialect)
	if err != nil {
		t.Fatalf("lexer.Split error: %v", err)
	}
	lm := pos.NewLineMap(src)
	out := make([]Statement, len(stmts))
	for i, s := range stmts {
		out[i] = Parse(s, "f.sql", lm)
	}
	return out
}

func findRef(t *testing.T, refs []Ref, name string) Ref {
	t.Helper()
	for _, r := range refs {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("ref %q not found in %+v", name, refs)
	return Ref{}
}

func TestPosition_DefinitionNameBasic(t *testing.T) {
	src := `CREATE TABLE users (id int primary key);`
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	if s.Name != "users" {
		t.Fatalf("name = %q", s.Name)
	}
	// "CREATE TABLE " is 13 bytes, so "users" starts at column 14.
	if s.NamePos.Line != 1 || s.NamePos.Col != 14 {
		t.Errorf("NamePos = %+v, want line=1 col=14", s.NamePos)
	}
	if s.NamePos.File != "f.sql" {
		t.Errorf("NamePos.File = %q", s.NamePos.File)
	}
	if s.NamePos.Offset != 13 {
		t.Errorf("NamePos.Offset = %d, want 13", s.NamePos.Offset)
	}
}

func TestPosition_DefinitionNameSchemaQualifiedQuoted(t *testing.T) {
	src := `CREATE TABLE IF NOT EXISTS public."Users" (id int);`
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	if s.Name != "public.Users" {
		t.Fatalf("name = %q", s.Name)
	}
	// Position should point at the start of "public" (the first token of
	// the qualified name), not at "Users".
	prefix := `CREATE TABLE IF NOT EXISTS `
	wantCol := len(prefix) + 1
	if s.NamePos.Line != 1 || s.NamePos.Col != wantCol {
		t.Errorf("NamePos = %+v, want line=1 col=%d", s.NamePos, wantCol)
	}
}

func TestPosition_ReferencesTargetMultilineCreateTable(t *testing.T) {
	src := "CREATE TABLE orders (\n" +
		"\tid int PRIMARY KEY,\n" +
		"\tuser_id int REFERENCES users(id)\n" +
		");"
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	ref := findRef(t, s.DepRefs, "users")
	if ref.Kind != RefAuto {
		t.Errorf("ref kind = %v, want RefAuto", ref.Kind)
	}
	// Line 3 is "\tuser_id int REFERENCES users(id)". "users" begins
	// after "\tuser_id int REFERENCES ".
	wantLine := 3
	wantCol := len("\tuser_id int REFERENCES ") + 1
	if ref.Pos.Line != wantLine || ref.Pos.Col != wantCol {
		t.Errorf("ref pos = %+v, want line=%d col=%d", ref.Pos, wantLine, wantCol)
	}
}

func TestPosition_DirectiveNames(t *testing.T) {
	src := "-- sqldefkit:require users accounts\nCREATE VIEW v AS SELECT 1;"
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]

	usersRef := findRef(t, s.DepRefs, "users")
	if usersRef.Kind != RefDirective {
		t.Errorf("users ref kind = %v, want RefDirective", usersRef.Kind)
	}
	wantUsersCol := len("-- sqldefkit:require ") + 1
	if usersRef.Pos.Line != 1 || usersRef.Pos.Col != wantUsersCol {
		t.Errorf("users ref pos = %+v, want line=1 col=%d", usersRef.Pos, wantUsersCol)
	}

	accountsRef := findRef(t, s.DepRefs, "accounts")
	wantAccountsCol := len("-- sqldefkit:require users ") + 1
	if accountsRef.Pos.Line != 1 || accountsRef.Pos.Col != wantAccountsCol {
		t.Errorf("accounts ref pos = %+v, want line=1 col=%d", accountsRef.Pos, wantAccountsCol)
	}
}

func TestPosition_DirectiveNameQuoted(t *testing.T) {
	src := "-- sqldefkit:require \"Users\"\nCREATE VIEW v AS SELECT 1;"
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	ref := findRef(t, s.DepRefs, "Users")
	wantCol := len("-- sqldefkit:require ") + 1
	if ref.Pos.Line != 1 || ref.Pos.Col != wantCol {
		t.Errorf("ref pos = %+v, want line=1 col=%d", ref.Pos, wantCol)
	}
}

// TestPosition_SecondStatementOffsetAccumulation is the critical
// regression test for position math across multiple statements in one
// file: the second statement's name position must be computed relative
// to the whole file, not reset per-statement.
func TestPosition_SecondStatementOffsetAccumulation(t *testing.T) {
	src := "CREATE TABLE a (id int);\nCREATE TABLE b (id int);\nCREATE TABLE c (id int);"
	stmts := parseFile(t, src, lexer.Postgres)
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(stmts))
	}

	// stmt 1 ("b") is on line 2.
	s1 := stmts[1]
	if s1.Name != "b" {
		t.Fatalf("stmt1 name = %q", s1.Name)
	}
	if s1.NamePos.Line != 2 {
		t.Errorf("stmt1 NamePos.Line = %d, want 2", s1.NamePos.Line)
	}
	wantCol := len("CREATE TABLE ") + 1
	if s1.NamePos.Col != wantCol {
		t.Errorf("stmt1 NamePos.Col = %d, want %d", s1.NamePos.Col, wantCol)
	}
	wantOffset := len("CREATE TABLE a (id int);\nCREATE TABLE ")
	if s1.NamePos.Offset != wantOffset {
		t.Errorf("stmt1 NamePos.Offset = %d, want %d", s1.NamePos.Offset, wantOffset)
	}

	// stmt 2 ("c") is on line 3.
	s2 := stmts[2]
	if s2.Name != "c" {
		t.Fatalf("stmt2 name = %q", s2.Name)
	}
	if s2.NamePos.Line != 3 {
		t.Errorf("stmt2 NamePos.Line = %d, want 3", s2.NamePos.Line)
	}
}

// TestPosition_StatementAfterComment verifies name position accounts for
// a preceding leading-comment statement (comments are excluded from Text
// but still occupy lines before it in the file).
func TestPosition_StatementAfterComment(t *testing.T) {
	src := "-- a comment\n-- another comment\nCREATE TABLE users (id int);"
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	if s.Name != "users" {
		t.Fatalf("name = %q", s.Name)
	}
	if s.NamePos.Line != 3 {
		t.Errorf("NamePos.Line = %d, want 3", s.NamePos.Line)
	}
	wantCol := len("CREATE TABLE ") + 1
	if s.NamePos.Col != wantCol {
		t.Errorf("NamePos.Col = %d, want %d", s.NamePos.Col, wantCol)
	}
}

// TestPosition_StatementAfterDollarQuotedBody verifies offsets remain
// correct for a statement following one containing a multi-line
// dollar-quoted function body.
func TestPosition_StatementAfterDollarQuotedBody(t *testing.T) {
	src := "CREATE FUNCTION f() RETURNS int AS $$\n" +
		"BEGIN\n" +
		"  RETURN 1;\n" +
		"END;\n" +
		"$$ LANGUAGE plpgsql;\n" +
		"CREATE TABLE t (id int);"
	stmts := parseFile(t, src, lexer.Postgres)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	s1 := stmts[1]
	if s1.Name != "t" {
		t.Fatalf("stmt1 name = %q", s1.Name)
	}
	if s1.NamePos.Line != 6 {
		t.Errorf("stmt1 NamePos.Line = %d, want 6", s1.NamePos.Line)
	}
	wantCol := len("CREATE TABLE ") + 1
	if s1.NamePos.Col != wantCol {
		t.Errorf("stmt1 NamePos.Col = %d, want %d", s1.NamePos.Col, wantCol)
	}
}

func TestPosition_IndexAndTriggerOnTarget(t *testing.T) {
	src := `CREATE INDEX idx_orders_user ON orders (user_id);`
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	ref := findRef(t, s.DepRefs, "orders")
	if ref.Kind != RefAuto {
		t.Errorf("kind = %v, want RefAuto", ref.Kind)
	}
	wantCol := len("CREATE INDEX idx_orders_user ON ") + 1
	if ref.Pos.Line != 1 || ref.Pos.Col != wantCol {
		t.Errorf("ref pos = %+v, want line=1 col=%d", ref.Pos, wantCol)
	}
}

func TestPosition_AlterTableTarget(t *testing.T) {
	src := `ALTER TABLE orders ADD CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id);`
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	selfRef := findRef(t, s.DepRefs, "orders")
	wantCol := len("ALTER TABLE ") + 1
	if selfRef.Pos.Line != 1 || selfRef.Pos.Col != wantCol {
		t.Errorf("self ref pos = %+v, want line=1 col=%d", selfRef.Pos, wantCol)
	}

	usersRef := findRef(t, s.DepRefs, "users")
	if usersRef.Kind != RefAuto {
		t.Errorf("users ref kind = %v, want RefAuto", usersRef.Kind)
	}
}

func TestPosition_ViewScanKindIsViewScan(t *testing.T) {
	src := `CREATE VIEW active_users AS SELECT * FROM users u JOIN accounts a ON u.account_id = a.id;`
	stmts := parseFile(t, src, lexer.Postgres)
	s := stmts[0]
	usersRef := findRef(t, s.DepRefs, "users")
	if usersRef.Kind != RefViewScan {
		t.Errorf("kind = %v, want RefViewScan", usersRef.Kind)
	}
	accountsRef := findRef(t, s.DepRefs, "accounts")
	if accountsRef.Kind != RefViewScan {
		t.Errorf("kind = %v, want RefViewScan", accountsRef.Kind)
	}
}

// TestPosition_NoFileLineMapDoesNotPanic exercises the nil-fileLineMap
// path (statement-relative positions), used by callers that don't need
// file-absolute positions.
func TestPosition_NoFileLineMapDoesNotPanic(t *testing.T) {
	src := "-- sqldefkit:require users\nCREATE VIEW v AS SELECT 1;"
	stmts, err := lexer.Split(src, lexer.Postgres)
	if err != nil {
		t.Fatalf("lexer.Split error: %v", err)
	}
	s := Parse(stmts[0], "", nil)
	if s.Name != "v" {
		t.Fatalf("name = %q", s.Name)
	}
	// Just check it doesn't panic and produces a valid (if
	// statement-relative) line/col.
	if s.NamePos.Line < 1 {
		t.Errorf("NamePos = %+v, want Line >= 1", s.NamePos)
	}
}
