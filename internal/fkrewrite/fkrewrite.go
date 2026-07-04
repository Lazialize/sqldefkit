// Package fkrewrite extracts foreign-key constraints out of a CREATE TABLE
// statement's text and synthesizes equivalent ALTER TABLE ... ADD ...
// statements, so that dependency cycles formed entirely of FK edges can be
// broken automatically (see internal/bundle's cycle-breaking pass).
//
// It operates on the same token stream internal/parse scans (never on raw
// text via regex), so strings, comments, and quoted identifiers are
// respected exactly as they are elsewhere in sqldefkit.
//
// Extraction is deliberately conservative: Extract returns ok=false the
// moment it encounters anything about a table's column/constraint list it
// cannot bound with certainty, so callers can fall back to a hard error
// instead of ever emitting broken SQL.
package fkrewrite

import (
	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// Clause is one extractable foreign-key constraint found inside a CREATE
// TABLE's column/constraint list.
type Clause struct {
	// TableLevel is true for a `[CONSTRAINT name] FOREIGN KEY (...)
	// REFERENCES ...` item in the table's top-level list, false for an
	// inline column-level `REFERENCES ...` clause.
	TableLevel bool
	// Column is the column name the FK applies to. For table-level
	// clauses this is informational only (the ALTER TABLE statement uses
	// the clause's own column list); for inline clauses it is the single
	// column being defined, used to build `FOREIGN KEY (<col>)`.
	Column string
	// Target is the normalized referenced table name (schema-qualified,
	// normalized the same way internal/parse normalizes names), used to
	// decide whether this clause participates in a given cycle.
	Target string
	// AddSQL is the clause rendered as the argument to `ALTER TABLE
	// <table> ADD `, verbatim source text for table-level clauses
	// (`[CONSTRAINT name] FOREIGN KEY (...) REFERENCES ...`), or
	// synthesized as `FOREIGN KEY (<col>) REFERENCES <remainder>` for
	// inline clauses.
	AddSQL string

	// itemStart/itemEnd bound the entire top-level list item (column
	// definition or table-level constraint) this clause was found in,
	// byte offsets into the CREATE TABLE statement's Text.
	itemStart, itemEnd int
	// clauseStart/clauseEnd bound exactly the span to remove from the
	// item: for table-level clauses this equals itemStart/itemEnd (the
	// whole item is the clause); for inline clauses it's the narrower
	// REFERENCES-and-tail span within the column definition.
	clauseStart, clauseEnd int
}

// listItem is one top-level (paren-depth-1) comma-separated entry in a
// CREATE TABLE's column/constraint list.
type listItem struct {
	start, end int // byte offsets into stmt text, excluding surrounding whitespace at top level (we keep raw span; hygiene is handled by the rewriter)
	tokStart   int // index into the statement's filtered token slice
	tokEnd     int // exclusive
}

// Extract scans a CREATE TABLE statement (by its lexer tokens and text) for
// every foreign-key clause it can safely identify and remove. ok is false
// if the statement's column/constraint list contains anything Extract
// isn't confident about bounding correctly (in which case clauses is nil
// and callers must not attempt any rewrite of this statement).
func Extract(text string, tokens []lexer.Token) (clauses []Clause, ok bool) {
	toks := filterComments(tokens)

	parenStart, parenEnd, ok := findColumnListParen(toks)
	if !ok {
		return nil, false
	}

	items, ok := splitTopLevelItems(toks, parenStart+1, parenEnd)
	if !ok {
		return nil, false
	}

	var out []Clause
	for _, item := range items {
		cl, found, itemOK := extractFromItem(text, toks, item)
		if !itemOK {
			return nil, false
		}
		if found {
			out = append(out, cl)
		}
	}
	return out, true
}

// TableName returns the verbatim source text (quoting/casing preserved
// exactly as written — e.g. `"Users"` or `public."Users"`) of a CREATE
// TABLE statement's own table name, for building a synthesized ALTER
// TABLE statement that must refer to the same table without risking a
// case-folding mismatch (internal/parse's normalized name strips quotes,
// which would silently change meaning if used verbatim in new SQL text
// for a quoted, case-sensitive identifier). ok is false if the statement
// doesn't have the expected `CREATE TABLE [IF NOT EXISTS] <name> (`
// shape.
func TableName(text string, tokens []lexer.Token) (name string, ok bool) {
	toks := filterComments(tokens)
	i := 0
	if !(isWord(peekTok(toks, i), "CREATE") && isWord(peekTok(toks, i+1), "TABLE")) {
		return "", false
	}
	i += 2
	if isWord(peekTok(toks, i), "IF") && isWord(peekTok(toks, i+1), "NOT") && isWord(peekTok(toks, i+2), "EXISTS") {
		i += 3
	}
	start := i
	if !isIdentLike(peekTok(toks, i)) {
		return "", false
	}
	i++
	for isPunct(peekTok(toks, i), ".") && isIdentLike(peekTok(toks, i+1)) {
		i += 2
	}
	if start >= len(toks) || i-1 >= len(toks) {
		return "", false
	}
	return text[toks[start].Start:toks[i-1].End], true
}

func peekTok(toks []lexer.Token, i int) lexer.Token {
	if i < 0 || i >= len(toks) {
		return lexer.Token{}
	}
	return toks[i]
}

func isPunct(t lexer.Token, raw string) bool {
	return t.Kind == lexer.TokenPunct && t.Raw == raw
}

// filterComments drops TokenComment entries (parse.newCursor does the same
// before scanning) so indices here line up with how internal/parse sees
// the stream.
func filterComments(tokens []lexer.Token) []lexer.Token {
	out := make([]lexer.Token, 0, len(tokens))
	for _, t := range tokens {
		if t.Kind != lexer.TokenComment {
			out = append(out, t)
		}
	}
	return out
}

// findColumnListParen locates the `(` ... `)` pair that opens the table's
// column/constraint list: CREATE TABLE [IF NOT EXISTS] <name> ( ... )
// [options]. It returns the token indices of that opening and closing
// paren. ok is false if the statement doesn't have the expected shape
// (e.g. CREATE TABLE ... AS SELECT, or no paren found at all) — Extract
// treats that as "nothing to do" only when there truly is no FK to find,
// but to stay conservative we simply refuse (callers only invoke Extract
// for statements known to be KindCreateTable with at least one FK-typed
// dependency, so a missing paren here is unexpected and should fall back
// to the cycle error).
func findColumnListParen(toks []lexer.Token) (openIdx, closeIdx int, ok bool) {
	depth := 0
	for i, t := range toks {
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			if depth == 0 {
				open := i
				// Find its matching close.
				d := 1
				for j := i + 1; j < len(toks); j++ {
					if toks[j].Kind == lexer.TokenPunct && toks[j].Raw == "(" {
						d++
					} else if toks[j].Kind == lexer.TokenPunct && toks[j].Raw == ")" {
						d--
						if d == 0 {
							return open, j, true
						}
					}
				}
				return 0, 0, false // unterminated
			}
			depth++
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
		}
	}
	return 0, 0, false
}

// splitTopLevelItems splits the token range (tokStart, tokEnd) — the
// contents of the column list, exclusive of the surrounding parens — into
// comma-separated items at paren-depth 0 relative to this range.
func splitTopLevelItems(toks []lexer.Token, tokStart, tokEnd int) ([]listItem, bool) {
	if tokStart >= tokEnd {
		return nil, false
	}
	var items []listItem
	depth := 0
	itemTokStart := tokStart
	for i := tokStart; i < tokEnd; i++ {
		t := toks[i]
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			depth++
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
			if depth < 0 {
				return nil, false
			}
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == "," && depth == 0 {
			items = append(items, listItem{tokStart: itemTokStart, tokEnd: i, start: toks[itemTokStart].Start, end: toks[i-1].End})
			itemTokStart = i + 1
		}
	}
	if depth != 0 {
		return nil, false
	}
	if itemTokStart < tokEnd {
		items = append(items, listItem{tokStart: itemTokStart, tokEnd: tokEnd, start: toks[itemTokStart].Start, end: toks[tokEnd-1].End})
	}
	return items, true
}
