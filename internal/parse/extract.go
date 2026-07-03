package parse

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// Parse extracts a Statement from a lexer.Statement.
//
// file and fileLineMap are used to compute absolute source positions for
// Statement.NamePos and each Statement.DepRefs[i].Pos: fileLineMap must
// be a *pos.LineMap built over the full original file text (the same
// text passed to lexer.Split), and stmt.Start/stmt.LeadingCommentStarts
// (byte offsets into that same text) are used as the base offset.
//
// Callers that don't need positions (e.g. tests exercising only
// Name/Deps) may pass file "" and fileLineMap nil; a line map will be
// built from stmt.Text alone, so positions come back statement-relative
// rather than file-absolute (still internally consistent, just not
// meaningful across statements).
func Parse(stmt lexer.Statement, file string, fileLineMap *pos.LineMap) Statement {
	out := Statement{
		Text:            stmt.Text,
		LeadingComments: stmt.LeadingComments,
	}

	lm := fileLineMap
	stmtStart := stmt.Start
	if lm == nil {
		lm = pos.NewLineMap(stmt.Text)
		stmtStart = 0
	}

	c := newCursor(stmt.Tokens)
	c.file = file
	c.lineMap = lm
	c.stmtStart = stmtStart

	kind, name, namePos, refs := extract(c)
	out.Kind = kind
	out.Name = name
	out.NamePos = namePos

	refs = dedupeRefs(refs)
	refs = append(refs, directiveRefs(file, stmt.LeadingComments, stmt.LeadingCommentStarts, lm)...)

	out.DepRefs = refs
	out.Deps = dedupe(refNames(refs))

	return out
}

func refNames(refs []Ref) []string {
	if len(refs) == 0 {
		return nil
	}
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name
	}
	return names
}

// dedupeRefs removes later Refs whose Name duplicates an earlier one,
// keeping the first occurrence's position (matches dedupe's
// first-occurrence-wins semantics for Deps).
func dedupeRefs(refs []Ref) []Ref {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	out := make([]Ref, 0, len(refs))
	for _, r := range refs {
		if !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r)
		}
	}
	return out
}

// filterSelfRefs removes occurrences of self from refs. Used when scanning
// REFERENCES targets so that a self-referential foreign key doesn't
// introduce a graph self-loop. (Not applied to the explicit "ALTER TABLE
// depends on its own table" dependency, which is intentional and handled
// separately.)
func filterSelfRefs(self string, refs []Ref) []Ref {
	if self == "" || len(refs) == 0 {
		return refs
	}
	filtered := refs[:0:0]
	for _, r := range refs {
		if r.Name != self {
			filtered = append(filtered, r)
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
func extract(c *cursor) (Kind, string, pos.Position, []Ref) {
	if c.atEnd() {
		return KindOther, "", pos.Position{}, nil
	}

	switch {
	case c.matchWords("CREATE"):
		return extractCreate(c)
	case c.matchWords("ALTER", "TABLE"):
		return extractAlterTable(c)
	default:
		return KindOther, "", pos.Position{}, scanReferences(c)
	}
}

func extractCreate(c *cursor) (Kind, string, pos.Position, []Ref) {
	c.consumeWords("CREATE")
	// Skip modifiers that can precede the object keyword.
	c.consumeWords("OR", "REPLACE")
	c.consumeWords("UNIQUE") // CREATE UNIQUE INDEX

	switch {
	case c.consumeWords("TABLE"):
		c.skipIfNotExists()
		name, namePos, _ := c.readQualifiedName()
		refs := filterSelfRefs(name, scanReferences(c))
		return KindCreateTable, name, namePos, refs

	case c.matchWords("VIEW") || c.matchWords("MATERIALIZED", "VIEW"):
		c.consumeWords("MATERIALIZED")
		c.consumeWords("VIEW")
		c.skipIfNotExists()
		name, namePos, _ := c.readQualifiedName()
		refs := scanViewSources(c)
		return KindCreateView, name, namePos, refs

	case c.consumeWords("INDEX"):
		return extractIndexLike(c)

	case c.consumeWords("FUNCTION"):
		name, namePos, _ := c.readQualifiedName()
		return KindCreateFunction, name, namePos, nil

	case c.consumeWords("PROCEDURE"):
		name, namePos, _ := c.readQualifiedName()
		return KindCreateFunction, name, namePos, nil

	case c.consumeWords("TRIGGER"):
		return extractTrigger(c)

	case c.consumeWords("TYPE"):
		name, namePos, _ := c.readQualifiedName()
		return KindCreateType, name, namePos, nil

	case c.consumeWords("SEQUENCE"):
		c.skipIfNotExists()
		name, namePos, _ := c.readQualifiedName()
		return KindCreateSequence, name, namePos, nil

	case c.consumeWords("EXTENSION"):
		c.skipIfNotExists()
		name, namePos, _ := c.readQualifiedName()
		return KindCreateExtension, name, namePos, nil

	default:
		return KindOther, "", pos.Position{}, scanReferences(c)
	}
}

// extractIndexLike handles `CREATE [UNIQUE] INDEX [IF NOT EXISTS] name ON
// table ...`. The "INDEX" keyword has already been consumed.
func extractIndexLike(c *cursor) (Kind, string, pos.Position, []Ref) {
	c.skipIfNotExists()
	// Index name is optional in some dialects (rare); try to read it,
	// then expect ON <table>.
	var idxName string
	var idxNamePos pos.Position
	if !c.matchWords("ON") {
		idxName, idxNamePos, _ = c.readQualifiedName()
	}
	var refs []Ref
	if c.consumeWords("ON") {
		if tbl, tblPos, ok := c.readQualifiedName(); ok {
			refs = append(refs, Ref{Name: tbl, Pos: tblPos, Kind: RefAuto})
		}
	}
	return KindCreateIndex, idxName, idxNamePos, refs
}

// extractTrigger handles `CREATE TRIGGER name ... ON table ...`. The
// "TRIGGER" keyword has already been consumed.
func extractTrigger(c *cursor) (Kind, string, pos.Position, []Ref) {
	name, namePos, _ := c.readQualifiedName()
	var refs []Ref
	for !c.atEnd() {
		if c.consumeWords("ON") {
			if tbl, tblPos, ok := c.readQualifiedName(); ok {
				refs = append(refs, Ref{Name: tbl, Pos: tblPos, Kind: RefAuto})
			}
			break
		}
		c.pos++
	}
	// Continue scanning the rest for any REFERENCES (trigger bodies can
	// reference tables too, best-effort).
	refs = append(refs, scanReferences(c)...)
	return KindCreateTrigger, name, namePos, refs
}

// extractAlterTable handles `ALTER TABLE [IF EXISTS] name ...`. Depends on
// the table itself plus any REFERENCES targets found afterward.
func extractAlterTable(c *cursor) (Kind, string, pos.Position, []Ref) {
	c.consumeWords("ALTER", "TABLE")
	c.consumeWords("IF", "EXISTS")
	name, namePos, _ := c.readQualifiedName()
	refs := filterSelfRefs(name, scanReferences(c))
	if name != "" {
		refs = append([]Ref{{Name: name, Pos: namePos, Kind: RefAuto}}, refs...)
	}
	return KindAlterTable, name, namePos, refs
}

// scanReferences scans the remaining tokens for `REFERENCES <name>`
// occurrences, from the cursor's current position to the end.
func scanReferences(c *cursor) []Ref {
	var refs []Ref
	for !c.atEnd() {
		if isWordEq(mustPeek(c), "REFERENCES") {
			c.pos++
			if name, namePos, ok := c.readQualifiedName(); ok {
				refs = append(refs, Ref{Name: name, Pos: namePos, Kind: RefAuto})
			}
			continue
		}
		c.pos++
	}
	return refs
}

// scanViewSources scans for identifiers immediately following top-level
// FROM/JOIN keywords in a view body. "Top-level" here means not inside a
// parenthesized subexpression (e.g. a function call argument list or a
// subquery used as an expression), tracked via paren depth.
func scanViewSources(c *cursor) []Ref {
	var refs []Ref
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
			if name, namePos, ok := c.readQualifiedName(); ok {
				refs = append(refs, Ref{Name: name, Pos: namePos, Kind: RefViewScan})
			}
			continue
		}
		c.pos++
	}
	return refs
}

func mustPeek(c *cursor) lexer.Token {
	t, _ := c.peek()
	return t
}

// directiveRefs parses `sqldefkit:require <name> [<name>...]` directives
// out of a statement's leading comments and returns one Ref (Kind:
// RefDirective) per named identifier, with accurate positions. lm must be
// a line map whose offsets are consistent with commentStarts (both
// statement-relative for Parse, or both file-absolute for ParseInFile).
func directiveRefs(file string, comments []string, commentStarts []int, lm *pos.LineMap) []Ref {
	var refs []Ref
	const prefix = "sqldefkit:require"
	for i, comment := range comments {
		commentStart := 0
		if i < len(commentStarts) {
			commentStart = commentStarts[i]
		}
		marker, markerLen := commentMarkerPrefix(comment)
		body := comment[markerLen:]
		trimmedBody := strings.TrimLeft(body, " \t\r\n\v\f")
		leadingWS := len(body) - len(trimmedBody)
		bodyOffsetInComment := markerLen + leadingWS

		text := strings.TrimRight(trimmedBody, " \t\r\n\v\f")
		if marker == "/*" {
			text = strings.TrimSuffix(text, "*/")
			text = strings.TrimRight(text, " \t\r\n")
		}
		if !strings.HasPrefix(text, prefix) {
			continue
		}
		rest := text[len(prefix):]
		restOffsetInComment := bodyOffsetInComment + len(prefix)

		// Walk rest, finding each whitespace-separated field and its
		// offset within rest, to compute an accurate position per name.
		i2 := 0
		for i2 < len(rest) {
			for i2 < len(rest) && isDirectiveSep(rest[i2]) {
				i2++
			}
			start := i2
			for i2 < len(rest) && !isDirectiveSep(rest[i2]) {
				i2++
			}
			if i2 == start {
				break
			}
			field := rest[start:i2]
			fieldOffsetInComment := restOffsetInComment + start
			namePos := lm.Pos(file, commentStart+fieldOffsetInComment)
			refs = append(refs, Ref{Name: normalizeDirectiveName(field), Pos: namePos, Kind: RefDirective})
		}
	}
	return refs
}

// isDirectiveSep reports whether b separates fields in a require
// directive's argument list. Matches strings.Fields' notion of
// whitespace (via unicode.IsSpace, restricted here to the ASCII
// whitespace bytes that can occur in a single-line/block comment) so
// that behavior is identical to the pre-position-tracking
// implementation: e.g. "a, b" (comma NOT treated as a separator) yields
// fields "a," and "b", preserving existing (documented) semantics.
func isDirectiveSep(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

// commentMarkerPrefix returns the comment's opening marker ("--", "#", or
// "/*") and its byte length, or ("", 0) if unrecognized.
func commentMarkerPrefix(comment string) (string, int) {
	switch {
	case strings.HasPrefix(comment, "--"):
		return "--", 2
	case strings.HasPrefix(comment, "#"):
		return "#", 1
	case strings.HasPrefix(comment, "/*"):
		return "/*", 2
	default:
		return "", 0
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
