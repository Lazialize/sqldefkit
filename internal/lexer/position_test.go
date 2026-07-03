package lexer

import "testing"

// TestSplit_StatementStartEndOffsets checks that Statement.Start/End are
// correct absolute byte offsets into the original source, including for
// a second statement (offset accumulation) and one preceded by a leading
// comment (comments are excluded from Text/Start).
func TestSplit_StatementStartEndOffsets(t *testing.T) {
	src := "CREATE TABLE a (id int);\n-- comment\nCREATE TABLE b (id int);"
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}

	s0 := stmts[0]
	if s0.Start != 0 {
		t.Errorf("stmt0 Start = %d, want 0", s0.Start)
	}
	if got := src[s0.Start:s0.End]; got != s0.Text {
		t.Errorf("stmt0 src[Start:End] = %q, want Text %q", got, s0.Text)
	}

	s1 := stmts[1]
	wantStart := len("CREATE TABLE a (id int);\n-- comment\n")
	if s1.Start != wantStart {
		t.Errorf("stmt1 Start = %d, want %d", s1.Start, wantStart)
	}
	if got := src[s1.Start:s1.End]; got != s1.Text {
		t.Errorf("stmt1 src[Start:End] = %q, want Text %q", got, s1.Text)
	}

	if len(s1.LeadingCommentStarts) != 1 {
		t.Fatalf("expected 1 leading comment start, got %+v", s1.LeadingCommentStarts)
	}
	wantCommentStart := len("CREATE TABLE a (id int);\n")
	if s1.LeadingCommentStarts[0] != wantCommentStart {
		t.Errorf("comment start = %d, want %d", s1.LeadingCommentStarts[0], wantCommentStart)
	}
	if got := src[s1.LeadingCommentStarts[0] : s1.LeadingCommentStarts[0]+len(s1.LeadingComments[0])]; got != s1.LeadingComments[0] {
		t.Errorf("src at comment start = %q, want %q", got, s1.LeadingComments[0])
	}
}

// TestSplit_TokenOffsetsRelativeToStatementStart verifies that adding a
// token's Start to its Statement's Start yields the token's true absolute
// offset in the original source, for a token in the second statement of a
// multi-statement source (offset accumulation across statements).
func TestSplit_TokenOffsetsRelativeToStatementStart(t *testing.T) {
	src := "CREATE TABLE a (id int);\nCREATE TABLE b (id int);"
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	s1 := stmts[1]

	// Find the "b" token (a TokenWord with Value "b").
	var tok *Token
	for i := range s1.Tokens {
		if s1.Tokens[i].Kind == TokenWord && s1.Tokens[i].Value == "b" {
			tok = &s1.Tokens[i]
			break
		}
	}
	if tok == nil {
		t.Fatalf("token 'b' not found in %+v", s1.Tokens)
	}
	absStart := s1.Start + tok.Start
	absEnd := s1.Start + tok.End
	if got := src[absStart:absEnd]; got != "b" {
		t.Errorf("src at computed absolute offset = %q, want %q", got, "b")
	}
}

// TestSplit_DollarQuotedBodyOffsets checks that offsets remain correct
// for a statement following one with a dollar-quoted body (multi-line,
// containing characters that could otherwise confuse offset tracking).
func TestSplit_DollarQuotedBodyOffsets(t *testing.T) {
	src := "CREATE FUNCTION f() RETURNS int AS $$\nBEGIN\n  RETURN 1;\nEND;\n$$ LANGUAGE plpgsql;\nCREATE TABLE t (id int);"
	stmts, err := Split(src, Postgres)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %+v", len(stmts), stmts)
	}
	s1 := stmts[1]
	if got := src[s1.Start:s1.End]; got != s1.Text {
		t.Errorf("stmt1 src[Start:End] = %q, want Text %q", got, s1.Text)
	}
}
