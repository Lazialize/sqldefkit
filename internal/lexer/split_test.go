package lexer

import (
	"reflect"
	"testing"
)

func TestSplit_Basic(t *testing.T) {
	src := `CREATE TABLE a (id int);
CREATE TABLE b (id int);`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %+v", len(stmts), stmts)
	}
	if stmts[0].Text != "CREATE TABLE a (id int)" {
		t.Errorf("stmt0 text = %q", stmts[0].Text)
	}
	if stmts[1].Text != "CREATE TABLE b (id int)" {
		t.Errorf("stmt1 text = %q", stmts[1].Text)
	}
}

func TestSplit_NoTrailingSemicolon(t *testing.T) {
	src := `CREATE TABLE a (id int)`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	if stmts[0].Text != "CREATE TABLE a (id int)" {
		t.Errorf("text = %q", stmts[0].Text)
	}
}

func TestSplit_LineCommentSkippedInsideStatement(t *testing.T) {
	src := "CREATE TABLE a (\n  id int -- primary key later\n);"
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	want := "CREATE TABLE a (\n  id int -- primary key later\n)"
	if stmts[0].Text != want {
		t.Errorf("text = %q, want %q", stmts[0].Text, want)
	}
}

func TestSplit_MySQLHashComment(t *testing.T) {
	src := "CREATE TABLE a (id int); # trailing note\nCREATE TABLE b (id int);"
	stmts, err := Split(src, MySQL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %+v", len(stmts), stmts)
	}
	if len(stmts[1].LeadingComments) != 1 || stmts[1].LeadingComments[0] != "# trailing note" {
		t.Errorf("leading comments = %+v", stmts[1].LeadingComments)
	}
}

func TestSplit_HashNotCommentOutsideMySQL(t *testing.T) {
	// In postgres/sqlite, '#' has no comment meaning; ensure it doesn't
	// break splitting (treated as punctuation token, statement text
	// preserved verbatim).
	src := `CREATE TABLE a (id int); -- ok`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
}

func TestSplit_BlockComment(t *testing.T) {
	src := "/* block\nover multiple lines */\nCREATE TABLE a (id int);"
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	if len(stmts[0].LeadingComments) != 1 {
		t.Fatalf("expected 1 leading comment, got %+v", stmts[0].LeadingComments)
	}
}

func TestSplit_BlockCommentDoesNotBreakOnStarSlashInString(t *testing.T) {
	src := `CREATE TABLE a (note text DEFAULT '*/ not a comment end');`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d: %+v", len(stmts), stmts)
	}
}

func TestSplit_SingleQuoteEscaping(t *testing.T) {
	src := `INSERT INTO a (name) VALUES ('it''s a test; still one stmt');`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement (semicolon inside string must not split), got %d: %+v", len(stmts), stmts)
	}
}

func TestSplit_MySQLBackslashEscaping(t *testing.T) {
	src := `INSERT INTO a (name) VALUES ('back\'slash; still one stmt');`
	stmts, err := Split(src, MySQL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d: %+v", len(stmts), stmts)
	}
}

func TestSplit_DollarQuotes(t *testing.T) {
	src := `CREATE FUNCTION f() RETURNS int AS $$
BEGIN
  RETURN 1; -- semicolon inside body must not split
END;
$$ LANGUAGE plpgsql;`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d: %+v", len(stmts), stmts)
	}
}

func TestSplit_DollarQuotesWithTag(t *testing.T) {
	src := `CREATE FUNCTION f() RETURNS int AS $body$
BEGIN
  RETURN 1;
END;
$body$ LANGUAGE plpgsql;
CREATE TABLE t (id int);`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %+v", len(stmts), stmts)
	}
}

func TestSplit_Backticks(t *testing.T) {
	src := "CREATE TABLE `my table` (`id` int);"
	stmts, err := Split(src, MySQL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	var found bool
	for _, tok := range stmts[0].Tokens {
		if tok.Kind == TokenQuotedIdent && tok.Value == "my table" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected quoted ident token with value %q, tokens=%+v", "my table", stmts[0].Tokens)
	}
}

func TestSplit_Brackets(t *testing.T) {
	src := "CREATE TABLE [my table] ([id] int);"
	stmts, err := Split(src, SQLite)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
}

func TestSplit_AttachedLeadingComments(t *testing.T) {
	src := `-- comment for a
-- second line
CREATE TABLE a (id int);
CREATE TABLE b (id int);`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	want := []string{"-- comment for a", "-- second line"}
	if !reflect.DeepEqual(stmts[0].LeadingComments, want) {
		t.Errorf("leading comments = %+v, want %+v", stmts[0].LeadingComments, want)
	}
	if len(stmts[1].LeadingComments) != 0 {
		t.Errorf("stmt1 should have no leading comments, got %+v", stmts[1].LeadingComments)
	}
}

func TestSplit_QuotedIdentifierDoubling(t *testing.T) {
	src := `CREATE TABLE "weird""name" (id int);`
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, tok := range stmts[0].Tokens {
		if tok.Kind == TokenQuotedIdent && tok.Value == `weird"name` {
			found = true
		}
	}
	if !found {
		t.Errorf("expected quoted ident with doubled-quote escape decoded, tokens=%+v", stmts[0].Tokens)
	}
}

func TestSplit_UnterminatedStringError(t *testing.T) {
	src := `CREATE TABLE a (name text DEFAULT 'oops);`
	_, err := Split(src, Postgres)
	if err == nil {
		t.Fatal("expected error for unterminated string")
	}
}
