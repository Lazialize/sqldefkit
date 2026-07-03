package parse

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// Parse extracts a Statement from a lexer.Statement.
func Parse(stmt lexer.Statement) Statement {
	out := Statement{
		Text:            stmt.Text,
		LeadingComments: stmt.LeadingComments,
	}
	c := newCursor(stmt.Tokens)

	kind, name, deps := extract(c)
	out.Kind = kind
	out.Name = name
	out.Deps = dedupe(deps)

	out.Deps = append(out.Deps, directiveDeps(stmt.LeadingComments)...)
	out.Deps = dedupe(out.Deps)

	return out
}

// filterSelfRefs removes occurrences of self from deps. Used when scanning
// REFERENCES targets so that a self-referential foreign key doesn't
// introduce a graph self-loop. (Not applied to the explicit "ALTER TABLE
// depends on its own table" dependency, which is intentional and handled
// separately.)
func filterSelfRefs(self string, deps []string) []string {
	if self == "" || len(deps) == 0 {
		return deps
	}
	filtered := deps[:0:0]
	for _, d := range deps {
		if d != self {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

func dedupe(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// extract dispatches on the leading keyword(s) of the statement.
func extract(c *cursor) (Kind, string, []string) {
	if c.atEnd() {
		return KindOther, "", nil
	}

	switch {
	case c.matchWords("CREATE"):
		return extractCreate(c)
	case c.matchWords("ALTER", "TABLE"):
		return extractAlterTable(c)
	default:
		return KindOther, "", scanReferences(c)
	}
}

func extractCreate(c *cursor) (Kind, string, []string) {
	c.consumeWords("CREATE")
	// Skip modifiers that can precede the object keyword.
	c.consumeWords("OR", "REPLACE")
	c.consumeWords("UNIQUE") // CREATE UNIQUE INDEX

	switch {
	case c.consumeWords("TABLE"):
		c.skipIfNotExists()
		name, _ := c.readQualifiedName()
		deps := filterSelfRefs(name, scanReferences(c))
		return KindCreateTable, name, deps

	case c.matchWords("VIEW") || c.matchWords("MATERIALIZED", "VIEW"):
		c.consumeWords("MATERIALIZED")
		c.consumeWords("VIEW")
		c.skipIfNotExists()
		name, _ := c.readQualifiedName()
		deps := scanViewSources(c)
		return KindCreateView, name, deps

	case c.consumeWords("INDEX"):
		return extractIndexLike(c)

	case c.consumeWords("FUNCTION"):
		name, _ := c.readQualifiedName()
		return KindCreateFunction, name, nil

	case c.consumeWords("PROCEDURE"):
		name, _ := c.readQualifiedName()
		return KindCreateFunction, name, nil

	case c.consumeWords("TRIGGER"):
		return extractTrigger(c)

	case c.consumeWords("TYPE"):
		name, _ := c.readQualifiedName()
		return KindCreateType, name, nil

	case c.consumeWords("SEQUENCE"):
		c.skipIfNotExists()
		name, _ := c.readQualifiedName()
		return KindCreateSequence, name, nil

	case c.consumeWords("EXTENSION"):
		c.skipIfNotExists()
		name, _ := c.readQualifiedName()
		return KindCreateExtension, name, nil

	default:
		return KindOther, "", scanReferences(c)
	}
}

// extractIndexLike handles `CREATE [UNIQUE] INDEX [IF NOT EXISTS] name ON
// table ...`. The "INDEX" keyword has already been consumed.
func extractIndexLike(c *cursor) (Kind, string, []string) {
	c.skipIfNotExists()
	// Index name is optional in some dialects (rare); try to read it,
	// then expect ON <table>.
	var idxName string
	if !c.matchWords("ON") {
		idxName, _ = c.readQualifiedName()
	}
	var deps []string
	if c.consumeWords("ON") {
		if tbl, ok := c.readQualifiedName(); ok {
			deps = append(deps, tbl)
		}
	}
	return KindCreateIndex, idxName, deps
}

// extractTrigger handles `CREATE TRIGGER name ... ON table ...`. The
// "TRIGGER" keyword has already been consumed.
func extractTrigger(c *cursor) (Kind, string, []string) {
	name, _ := c.readQualifiedName()
	var deps []string
	for !c.atEnd() {
		if c.consumeWords("ON") {
			if tbl, ok := c.readQualifiedName(); ok {
				deps = append(deps, tbl)
			}
			break
		}
		c.pos++
	}
	// Continue scanning the rest for any REFERENCES (trigger bodies can
	// reference tables too, best-effort).
	deps = append(deps, scanReferences(c)...)
	return KindCreateTrigger, name, deps
}

// extractAlterTable handles `ALTER TABLE [IF EXISTS] name ...`. Depends on
// the table itself plus any REFERENCES targets found afterward.
func extractAlterTable(c *cursor) (Kind, string, []string) {
	c.consumeWords("ALTER", "TABLE")
	c.consumeWords("IF", "EXISTS")
	name, _ := c.readQualifiedName()
	deps := filterSelfRefs(name, scanReferences(c))
	if name != "" {
		deps = append([]string{name}, deps...)
	}
	return KindAlterTable, name, deps
}

// scanReferences scans the remaining tokens for `REFERENCES <name>`
// occurrences, from the cursor's current position to the end.
func scanReferences(c *cursor) []string {
	var deps []string
	for !c.atEnd() {
		if isWordEq(mustPeek(c), "REFERENCES") {
			c.pos++
			if name, ok := c.readQualifiedName(); ok {
				deps = append(deps, name)
			}
			continue
		}
		c.pos++
	}
	return deps
}

// scanViewSources scans for identifiers immediately following top-level
// FROM/JOIN keywords in a view body. "Top-level" here means not inside a
// parenthesized subexpression (e.g. a function call argument list or a
// subquery used as an expression), tracked via paren depth.
func scanViewSources(c *cursor) []string {
	var deps []string
	depth := 0
	for !c.atEnd() {
		t := mustPeek(c)
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			depth++
			c.pos++
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
			c.pos++
			continue
		}
		if depth == 0 && (isWordEq(t, "FROM") || isWordEq(t, "JOIN")) {
			c.pos++
			if name, ok := c.readQualifiedName(); ok {
				deps = append(deps, name)
			}
			continue
		}
		c.pos++
	}
	return deps
}

func mustPeek(c *cursor) lexer.Token {
	t, _ := c.peek()
	return t
}

// directiveDeps parses `sqldefkit:require <name> [<name>...]` directives
// out of leading comment text.
func directiveDeps(comments []string) []string {
	var deps []string
	for _, comment := range comments {
		body := stripCommentMarker(comment)
		body = strings.TrimSpace(body)
		const prefix = "sqldefkit:require"
		if !strings.HasPrefix(body, prefix) {
			continue
		}
		rest := strings.TrimSpace(body[len(prefix):])
		for _, name := range strings.Fields(rest) {
			deps = append(deps, normalizeDirectiveName(name))
		}
	}
	return deps
}

// stripCommentMarker removes the leading "--", "#", or "/* ... */"
// delimiters from a raw comment string, leaving the inner text.
func stripCommentMarker(comment string) string {
	switch {
	case strings.HasPrefix(comment, "--"):
		return strings.TrimSpace(comment[2:])
	case strings.HasPrefix(comment, "#"):
		return strings.TrimSpace(comment[1:])
	case strings.HasPrefix(comment, "/*"):
		s := strings.TrimSuffix(comment, "*/")
		s = strings.TrimPrefix(s, "/*")
		return strings.TrimSpace(s)
	default:
		return comment
	}
}

// normalizeDirectiveName applies the same identifier-folding rule as
// normalizeToken, but operating on raw directive text rather than a
// lexer.Token (directives are written as plain words in a comment, with
// optional quoting reused from SQL syntax).
func normalizeDirectiveName(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = normalizeRawIdent(p)
	}
	return strings.Join(parts, ".")
}

func normalizeRawIdent(s string) string {
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
		}
		if s[0] == '`' && s[len(s)-1] == '`' {
			return strings.ReplaceAll(s[1:len(s)-1], "``", "`")
		}
	}
	return strings.ToLower(s)
}
