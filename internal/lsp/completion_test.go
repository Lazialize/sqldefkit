package lsp

import "testing"

func TestDetectCompletionContext_References(t *testing.T) {
	content := "CREATE TABLE orders (\n\tuser_id int REFERENCES u\n);\n"
	offset := len("CREATE TABLE orders (\n\tuser_id int REFERENCES u")
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextReferences {
		t.Fatalf("Kind = %v, want completionContextReferences", ctx.Kind)
	}
	if ctx.Prefix != "u" {
		t.Errorf("Prefix = %q, want %q", ctx.Prefix, "u")
	}
}

func TestDetectCompletionContext_ReferencesMidWord(t *testing.T) {
	// Cursor placed in the middle of "users" (after "use"), completing a
	// partial prefix mid-word.
	content := "CREATE TABLE orders (\n\tuser_id int REFERENCES users(id)\n);\n"
	offset := len("CREATE TABLE orders (\n\tuser_id int REFERENCES use")
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextReferences {
		t.Fatalf("Kind = %v, want completionContextReferences", ctx.Kind)
	}
	if ctx.Prefix != "use" {
		t.Errorf("Prefix = %q, want %q", ctx.Prefix, "use")
	}
}

func TestDetectCompletionContext_ReferencesEmptyPrefix(t *testing.T) {
	content := "CREATE TABLE orders (\n\tuser_id int REFERENCES \n);\n"
	offset := len("CREATE TABLE orders (\n\tuser_id int REFERENCES ")
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextReferences {
		t.Fatalf("Kind = %v, want completionContextReferences", ctx.Kind)
	}
	if ctx.Prefix != "" {
		t.Errorf("Prefix = %q, want empty", ctx.Prefix)
	}
}

func TestDetectCompletionContext_Directive(t *testing.T) {
	content := "-- sqldefkit:require users acc\nCREATE VIEW v AS SELECT 1 FROM users;\n"
	offset := len("-- sqldefkit:require users acc")
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextDirective {
		t.Fatalf("Kind = %v, want completionContextDirective", ctx.Kind)
	}
	if ctx.Prefix != "acc" {
		t.Errorf("Prefix = %q, want %q", ctx.Prefix, "acc")
	}
}

func TestDetectCompletionContext_DirectiveFirstName(t *testing.T) {
	content := "-- sqldefkit:require us\nCREATE VIEW v AS SELECT 1;\n"
	offset := len("-- sqldefkit:require us")
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextDirective {
		t.Fatalf("Kind = %v, want completionContextDirective", ctx.Kind)
	}
	if ctx.Prefix != "us" {
		t.Errorf("Prefix = %q, want %q", ctx.Prefix, "us")
	}
}

func TestDetectCompletionContext_DirectiveHashComment(t *testing.T) {
	content := "# sqldefkit:require us\nCREATE VIEW v AS SELECT 1;\n"
	offset := len("# sqldefkit:require us")
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextDirective {
		t.Fatalf("Kind = %v, want completionContextDirective", ctx.Kind)
	}
}

func TestDetectCompletionContext_OnIndex(t *testing.T) {
	content := "CREATE INDEX idx_users_email ON u"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextOn {
		t.Fatalf("Kind = %v, want completionContextOn", ctx.Kind)
	}
	if ctx.Prefix != "u" {
		t.Errorf("Prefix = %q, want %q", ctx.Prefix, "u")
	}
}

func TestDetectCompletionContext_OnUniqueIndex(t *testing.T) {
	content := "CREATE UNIQUE INDEX idx_users_email ON u"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextOn {
		t.Fatalf("Kind = %v, want completionContextOn", ctx.Kind)
	}
}

func TestDetectCompletionContext_OnTrigger(t *testing.T) {
	content := "CREATE TRIGGER trg_users BEFORE INSERT ON u"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextOn {
		t.Fatalf("Kind = %v, want completionContextOn", ctx.Kind)
	}
}

func TestDetectCompletionContext_OnNotIndexOrTrigger(t *testing.T) {
	// "ON" appears in a CREATE TABLE statement (e.g. as part of a
	// constraint clause referencing another keyword pattern this server
	// doesn't special-case) - should not trigger context c since the
	// statement doesn't start with CREATE INDEX/TRIGGER.
	content := "CREATE TABLE t (a int) ON u"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextNone {
		t.Fatalf("Kind = %v, want completionContextNone", ctx.Kind)
	}
}

func TestDetectCompletionContext_PlainColumnPosition(t *testing.T) {
	content := "CREATE TABLE orders (\n\tuser_id in"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextNone {
		t.Fatalf("Kind = %v, want completionContextNone", ctx.Kind)
	}
}

func TestDetectCompletionContext_StartOfFile(t *testing.T) {
	content := "CREATE"
	ctx := detectCompletionContext(content, len(content))
	if ctx.Kind != completionContextNone {
		t.Fatalf("Kind = %v, want completionContextNone", ctx.Kind)
	}
}

func TestDetectCompletionContext_SecondStatementOn(t *testing.T) {
	// Two statements; the ON belongs to the second statement's CREATE
	// INDEX, and the enclosing-statement scan must not be confused by the
	// first statement's own content/semicolon.
	content := "CREATE TABLE users (id int);\nCREATE INDEX idx ON u"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextOn {
		t.Fatalf("Kind = %v, want completionContextOn", ctx.Kind)
	}
}

func TestDetectCompletionContext_ReferencesAfterPunctuationBreaksSearch(t *testing.T) {
	// Previous token before the word is "(" not REFERENCES/ON: no context.
	content := "CREATE TABLE orders (u"
	offset := len(content)
	ctx := detectCompletionContext(content, offset)
	if ctx.Kind != completionContextNone {
		t.Fatalf("Kind = %v, want completionContextNone", ctx.Kind)
	}
}
