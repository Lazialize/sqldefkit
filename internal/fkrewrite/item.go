package fkrewrite

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// columnConstraintKeywords starts a DIFFERENT column constraint than an
// inline REFERENCES clause, terminating that clause's span (see doc on
// extractInlineClause). "NOT" is handled specially: "NOT DEFERRABLE"
// belongs to the REFERENCES clause's own tail, but "NOT NULL" does not, so
// NOT is disambiguated by lookahead rather than listed here.
var columnConstraintKeywords = map[string]bool{
	"NULL":       true,
	"DEFAULT":    true,
	"CHECK":      true,
	"UNIQUE":     true,
	"PRIMARY":    true,
	"COLLATE":    true,
	"GENERATED":  true,
	"CONSTRAINT": true,
}

// referencesTailKeywords are keyword sequences that may legally follow a
// REFERENCES <target> [(<cols>)] clause and are still part of it: MATCH
// FULL/PARTIAL/SIMPLE, ON DELETE/UPDATE <action>, DEFERRABLE/NOT
// DEFERRABLE, INITIALLY DEFERRED/IMMEDIATE.
func isWord(t lexer.Token, kw string) bool {
	return t.Kind == lexer.TokenWord && strings.EqualFold(t.Value, kw)
}

// extractFromItem inspects one top-level list item (a column definition or
// a table-level constraint) for a foreign-key clause. found is false if
// the item has no FK at all (nothing to extract, not an error). ok is
// false if the item looks like it should be handled but contains something
// Extract can't bound with confidence — the caller must abort the whole
// statement's extraction in that case.
func extractFromItem(text string, toks []lexer.Token, item listItem) (cl Clause, found, ok bool) {
	first := toks[item.tokStart]

	// Table-level: [CONSTRAINT <name>] FOREIGN KEY (...) REFERENCES ...
	i := item.tokStart
	if isWord(toks[i], "CONSTRAINT") {
		// CONSTRAINT <name> FOREIGN KEY ...
		if i+1 < item.tokEnd && isIdentLike(toks[i+1]) && i+2 < item.tokEnd && isWord(toks[i+2], "FOREIGN") {
			return extractTableLevel(text, toks, item)
		}
		// CONSTRAINT <name> <something-else> (CHECK, UNIQUE, PRIMARY KEY,
		// etc.) — not an FK clause, nothing to extract from this item.
		return Clause{}, false, true
	}
	if isWord(first, "FOREIGN") {
		return extractTableLevel(text, toks, item)
	}
	if isWord(first, "PRIMARY") || isWord(first, "UNIQUE") || isWord(first, "CHECK") || isWord(first, "EXCLUDE") || isWord(first, "LIKE") {
		// Other recognized table-level constraint forms with no FK.
		return Clause{}, false, true
	}

	// Otherwise: a column definition, of the shape `<col> <type...>
	// [constraints...]`. Scan for a top-level REFERENCES keyword.
	return extractInlineClause(text, toks, item)
}

// isIdentLike reports whether t could be a name token (identifier or
// quoted identifier) — used to recognize a CONSTRAINT name without
// depending on it not colliding with a keyword.
func isIdentLike(t lexer.Token) bool {
	return t.Kind == lexer.TokenWord || t.Kind == lexer.TokenQuotedIdent
}

// extractTableLevel handles a table-level `[CONSTRAINT <name>] FOREIGN KEY
// (<cols>) REFERENCES <target> [(<cols>)] [options...]` item: the entire
// item is the FK clause, verbatim.
func extractTableLevel(text string, toks []lexer.Token, item listItem) (Clause, bool, bool) {
	i := item.tokStart
	if isWord(toks[i], "CONSTRAINT") {
		i += 2 // CONSTRAINT <name>
	}
	if !isWord(toks[i], "FOREIGN") {
		return Clause{}, false, false
	}
	i++
	if !isWord(toks[i], "KEY") {
		return Clause{}, false, false
	}
	i++
	// (<cols>)
	if i >= item.tokEnd || !(toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "(") {
		return Clause{}, false, false
	}
	i++
	for i < item.tokEnd && !(toks[i].Kind == lexer.TokenPunct && toks[i].Raw == ")") {
		i++
	}
	if i >= item.tokEnd {
		return Clause{}, false, false
	}
	i++ // consume ')'
	if i >= item.tokEnd || !isWord(toks[i], "REFERENCES") {
		return Clause{}, false, false
	}
	i++
	target, i2, ok := readQualifiedNameTokens(toks, i, item.tokEnd)
	if !ok {
		return Clause{}, false, false
	}
	i = i2

	clauseText := strings.TrimSpace(text[item.start:item.end])
	return Clause{
		TableLevel:  true,
		Target:      target,
		AddSQL:      clauseText,
		itemStart:   item.start,
		itemEnd:     item.end,
		clauseStart: item.start,
		clauseEnd:   item.end,
	}, true, true
}

// extractInlineClause handles a column definition, scanning (at the
// item's own top paren-depth, i.e. relative depth 0 within the item —
// deeper parens like a type's precision/scale or a CHECK(...) expression
// are skipped over without inspection) for a REFERENCES keyword. If found,
// it bounds the clause's end at the first token that is: a comma (can't
// happen within an item, since items are already comma-split, so this
// never triggers — kept for clarity), the item's own end, or a keyword
// that starts a distinct column constraint. Returns found=false if no
// REFERENCES is present at top level (not an error). Returns ok=false if a
// REFERENCES is found but something about bounding it isn't certain (e.g.
// malformed column list after REFERENCES).
func extractInlineClause(text string, toks []lexer.Token, item listItem) (Clause, bool, bool) {
	col, afterCol, ok := readQualifiedNameTokens(toks, item.tokStart, item.tokEnd)
	if !ok || col == "" {
		// Can't even identify the column name; if there's no REFERENCES
		// at all that's fine (not found), but we can't safely claim
		// "found" without a column name. Scan for REFERENCES defensively:
		// if present, bail out as not-ok since we can't build `FOREIGN
		// KEY (<col>) ...` without a column name.
		if hasTopLevelReferences(toks, item.tokStart, item.tokEnd) {
			return Clause{}, false, false
		}
		return Clause{}, false, true
	}

	depth := 0
	refStart := -1
	for i := afterCol; i < item.tokEnd; i++ {
		t := toks[i]
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			depth++
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		if isWord(t, "REFERENCES") {
			refStart = i
			break
		}
	}
	if refStart < 0 {
		return Clause{}, false, true
	}

	// Found the REFERENCES keyword; parse target + tail, bounding the
	// clause end.
	i := refStart + 1
	target, i2, ok := readQualifiedNameTokens(toks, i, item.tokEnd)
	if !ok {
		return Clause{}, false, false
	}
	i = i2

	// Optional (<cols>) immediately after the target.
	if i < item.tokEnd && toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "(" {
		depth := 1
		i++
		for i < item.tokEnd && depth > 0 {
			if toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "(" {
				depth++
			} else if toks[i].Kind == lexer.TokenPunct && toks[i].Raw == ")" {
				depth--
			}
			i++
		}
		if depth != 0 {
			return Clause{}, false, false
		}
	}

	// Consume the recognized tail: MATCH x, ON DELETE/UPDATE action,
	// DEFERRABLE/NOT DEFERRABLE, INITIALLY DEFERRED/IMMEDIATE — stop at
	// the first token starting a different column constraint or the
	// item's end.
	clauseEndTok, ok := consumeReferencesTail(toks, i, item.tokEnd)
	if !ok {
		return Clause{}, false, false
	}

	clauseStart := toks[refStart].Start
	var clauseEnd int
	if clauseEndTok < item.tokEnd {
		clauseEnd = toks[clauseEndTok].Start
	} else {
		clauseEnd = item.end
	}

	addSQL := "FOREIGN KEY (" + col + ") " + strings.TrimSpace(text[clauseStart:clauseEnd])

	return Clause{
		TableLevel:  false,
		Column:      col,
		Target:      target,
		AddSQL:      addSQL,
		itemStart:   item.start,
		itemEnd:     item.end,
		clauseStart: clauseStart,
		clauseEnd:   clauseEnd,
	}, true, true
}

// hasTopLevelReferences reports whether a REFERENCES keyword appears at
// paren-depth 0 within [start, end).
func hasTopLevelReferences(toks []lexer.Token, start, end int) bool {
	depth := 0
	for i := start; i < end; i++ {
		t := toks[i]
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			depth++
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
			continue
		}
		if depth == 0 && isWord(t, "REFERENCES") {
			return true
		}
	}
	return false
}

// consumeReferencesTail scans forward from i (immediately after the
// REFERENCES target and its optional column list) over recognized
// FK-tail syntax: MATCH FULL/PARTIAL/SIMPLE; ON DELETE/UPDATE <action
// words/CASCADE/RESTRICT/etc, including "SET NULL"/"SET DEFAULT" and "NO
// ACTION">; DEFERRABLE / NOT DEFERRABLE; INITIALLY DEFERRED/IMMEDIATE.
// Stops (returns that index, ok=true) at the item's end or at a token
// that starts a different column constraint. Returns ok=false if it
// encounters a token it doesn't recognize as either tail syntax or a
// known constraint-start keyword — i.e. something it can't bound with
// certainty.
func consumeReferencesTail(toks []lexer.Token, i, end int) (int, bool) {
	for i < end {
		t := toks[i]
		switch {
		case isWord(t, "MATCH"):
			i++
			if i < end && (isWord(toks[i], "FULL") || isWord(toks[i], "PARTIAL") || isWord(toks[i], "SIMPLE")) {
				i++
				continue
			}
			return 0, false
		case isWord(t, "ON"):
			i++
			if i < end && (isWord(toks[i], "DELETE") || isWord(toks[i], "UPDATE")) {
				i++
			} else {
				return 0, false
			}
			n, ok := consumeReferentialAction(toks, i, end)
			if !ok {
				return 0, false
			}
			i = n
		case isWord(t, "DEFERRABLE"):
			i++
		case isWord(t, "NOT"):
			// "NOT DEFERRABLE" belongs to this clause and is consumed;
			// any other use of NOT here — most commonly "NOT NULL" as a
			// separate column constraint — terminates the clause at this
			// token instead (disambiguated by this one-token lookahead).
			if i+1 < end && isWord(toks[i+1], "DEFERRABLE") {
				i += 2
				continue
			}
			return i, true
		case isWord(t, "INITIALLY"):
			i++
			if i < end && (isWord(toks[i], "DEFERRED") || isWord(toks[i], "IMMEDIATE")) {
				i++
			} else {
				return 0, false
			}
		case t.Kind == lexer.TokenPunct && t.Raw == ",":
			// Shouldn't occur (items are already comma-split at depth 0),
			// but treat as end-of-clause defensively.
			return i, true
		case t.Kind == lexer.TokenWord && columnConstraintKeywords[strings.ToUpper(t.Value)]:
			return i, true
		default:
			return 0, false
		}
	}
	return end, true
}

// consumeReferentialAction consumes one referential action after ON
// DELETE/UPDATE: CASCADE | RESTRICT | NO ACTION | SET NULL | SET DEFAULT.
func consumeReferentialAction(toks []lexer.Token, i, end int) (int, bool) {
	if i >= end {
		return 0, false
	}
	switch {
	case isWord(toks[i], "CASCADE"), isWord(toks[i], "RESTRICT"):
		return i + 1, true
	case isWord(toks[i], "NO"):
		if i+1 < end && isWord(toks[i+1], "ACTION") {
			return i + 2, true
		}
		return 0, false
	case isWord(toks[i], "SET"):
		if i+1 < end && (isWord(toks[i+1], "NULL") || isWord(toks[i+1], "DEFAULT")) {
			return i + 2, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// readQualifiedNameTokens reads a (possibly schema-qualified, possibly
// quoted) dotted name starting at i, mirroring
// internal/parse.cursor.readQualifiedName but operating on a plain token
// slice/range. Returns the normalized name, the index just past it, and
// whether a name was found.
func readQualifiedNameTokens(toks []lexer.Token, i, end int) (string, int, bool) {
	if i >= end || (toks[i].Kind != lexer.TokenWord && toks[i].Kind != lexer.TokenQuotedIdent) {
		return "", i, false
	}
	var parts []string
	parts = append(parts, normalizeNameToken(toks[i]))
	i++
	for i+1 < end && toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "." &&
		(toks[i+1].Kind == lexer.TokenWord || toks[i+1].Kind == lexer.TokenQuotedIdent) {
		parts = append(parts, normalizeNameToken(toks[i+1]))
		i += 2
	}
	return strings.Join(parts, "."), i, true
}

// normalizeNameToken mirrors internal/parse's normalizeToken (unexported
// there): quoted identifiers keep case, unquoted are lower-cased.
func normalizeNameToken(t lexer.Token) string {
	if t.Kind == lexer.TokenQuotedIdent {
		return t.Value
	}
	return strings.ToLower(t.Value)
}
