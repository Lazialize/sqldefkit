package fkrewrite

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// Column is one column of a CREATE TABLE statement's column list, extracted
// for visualization (internal/graphexport's column-level ER payload).
//
// Unlike Extract/Clause (built for FK-cycle splitting, which must never
// emit anything it isn't certain is correct — a bad rewrite would corrupt
// generated SQL), ExtractColumns is fail-soft: it is used only to draw a
// picture of the schema, so an entry it can't parse confidently degrades to
// a partial Column (or is skipped, if even the name is unclear) rather than
// aborting the whole statement's extraction. The graph must always build.
type Column struct {
	// Name is the column's display name: quotes stripped, original case
	// kept for a quoted identifier, written case kept for an unquoted one
	// (contrast with the lower-cased normalization Extract/Clause use for
	// matching purposes — this is for display).
	Name string
	// Type is the verbatim type text (whitespace-normalized to single
	// spaces), e.g. "numeric(10, 2)", "timestamptz", "character varying(50)".
	Type string
	PK   bool
	// NotNull is true for an inline NOT NULL, and also set (not left
	// implied) when PK is true, so clients don't need to know that rule.
	NotNull bool
	// Unique is true for an inline UNIQUE, or membership in a
	// single-column table-level UNIQUE (...) constraint. A multi-column
	// table-level UNIQUE does not mark its member columns.
	Unique bool
	// FK is non-nil if this column participates in a foreign key, inline
	// or table-level (composite table-level FKs set FK on every
	// participating column; see assignCompositeFK).
	FK *ColumnFK
}

// ColumnFK is the foreign-key target of a Column.
type ColumnFK struct {
	// Table is the normalized target table name (matching graph node ids
	// — same normalization as Clause.Target).
	Table string
	// Column is the target column's display name, or "" if the
	// REFERENCES clause didn't specify one (or, for a composite
	// table-level FK, if the local/target column lists have different
	// lengths so positional pairing isn't possible).
	Column string
}

// typeContinuationWords are specific multi-word type continuations that
// follow a base type name and are still part of Type, not a constraint
// keyword: "double precision", "character varying", "timestamp/time
// [without/with] time zone". This is a pragmatic, non-exhaustive list
// covering the common standard/Postgres/MySQL spellings; anything else
// stops the type scan at the first keyword in typeStopKeywords.
var typeContinuationWords = map[string]bool{
	"PRECISION": true, // double precision
	"VARYING":   true, // character varying / bit varying
	"WITH":      true, // timestamp with time zone
	"WITHOUT":   true, // timestamp without time zone
	"TIME":      true, // ... time zone
	"ZONE":      true, // ... time zone
}

// typeStopKeywords are the column-constraint-starting keywords that end a
// column's Type scan (see the Column.Type doc and the package-level design
// note in ExtractColumns).
var typeStopKeywords = map[string]bool{
	"CONSTRAINT":     true,
	"PRIMARY":        true,
	"NOT":            true,
	"NULL":           true,
	"DEFAULT":        true,
	"UNIQUE":         true,
	"CHECK":          true,
	"REFERENCES":     true,
	"COLLATE":        true,
	"GENERATED":      true,
	"AUTO_INCREMENT": true,
	"AUTOINCREMENT":  true,
}

// ExtractColumns scans a CREATE TABLE statement's column/constraint list
// for its ordered columns, best-effort. It never fails: a statement with no
// recognizable column list (e.g. CREATE TABLE ... AS SELECT) yields nil.
func ExtractColumns(text string, tokens []lexer.Token) []Column {
	toks := filterComments(tokens)

	parenStart, parenEnd, ok := findColumnListParen(toks)
	if !ok {
		return nil
	}

	items, ok := splitTopLevelItems(toks, parenStart+1, parenEnd)
	if !ok {
		return nil
	}

	cols := make([]Column, 0, len(items))
	byName := make(map[string]int)

	// First pass: column definitions (inline constraints included).
	for _, item := range items {
		if isTableLevelConstraintItem(toks, item) {
			continue
		}
		col, ok := extractColumnDef(text, toks, item)
		if !ok || col.Name == "" {
			continue
		}
		byName[col.Name] = len(cols)
		cols = append(cols, col)
	}

	// Second pass: table-level constraints referring back to columns by
	// name (PRIMARY KEY, UNIQUE, FOREIGN KEY).
	for _, item := range items {
		if !isTableLevelConstraintItem(toks, item) {
			continue
		}
		applyTableLevelConstraint(toks, item, cols, byName)
	}

	return cols
}

// isTableLevelConstraintItem reports whether item's first token starts a
// table-level constraint form ([CONSTRAINT name] FOREIGN KEY / PRIMARY KEY
// / UNIQUE / CHECK / EXCLUDE / LIKE) rather than a column definition. This
// mirrors extractFromItem's dispatch in item.go.
func isTableLevelConstraintItem(toks []lexer.Token, item listItem) bool {
	if item.tokStart >= item.tokEnd {
		return false
	}
	first := toks[item.tokStart]
	if isWord(first, "CONSTRAINT") {
		return true
	}
	return isWord(first, "FOREIGN") || isWord(first, "PRIMARY") || isWord(first, "UNIQUE") ||
		isWord(first, "CHECK") || isWord(first, "EXCLUDE") || isWord(first, "LIKE")
}

// extractColumnDef parses one column-definition list item: `<name>
// <type...> [constraints...]`. ok is false only if even the column name
// can't be identified (fail-soft: the caller skips such an item entirely).
func extractColumnDef(text string, toks []lexer.Token, item listItem) (Column, bool) {
	if item.tokStart >= item.tokEnd {
		return Column{}, false
	}
	nameTok := toks[item.tokStart]
	if !isIdentLike(nameTok) {
		return Column{}, false
	}
	col := Column{Name: displayName(nameTok)}

	i := item.tokStart + 1

	// Type: tokens up to the first top-level (depth-0 within the item)
	// stop keyword, including specific multi-word continuations.
	typeStart := i
	depth := 0
	typeEnd := i
	for i < item.tokEnd {
		t := toks[i]
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			depth++
			i++
			typeEnd = i
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
			i++
			typeEnd = i
			continue
		}
		if depth == 0 && t.Kind == lexer.TokenWord {
			upper := strings.ToUpper(t.Value)
			if typeStopKeywords[upper] && !typeContinuationWords[upper] {
				break
			}
		}
		i++
		typeEnd = i
	}
	col.Type = normalizeTypeText(text, toks, typeStart, typeEnd)

	// Remaining constraints, scanned at depth 0 within the item.
	depth = 0
	for i < item.tokEnd {
		t := toks[i]
		if t.Kind == lexer.TokenPunct && t.Raw == "(" {
			depth++
			i++
			continue
		}
		if t.Kind == lexer.TokenPunct && t.Raw == ")" {
			depth--
			i++
			continue
		}
		if depth != 0 {
			i++
			continue
		}
		switch {
		case isWord(t, "PRIMARY"):
			col.PK = true
			col.NotNull = true
			i++
			if i < item.tokEnd && isWord(toks[i], "KEY") {
				i++
			}
		case isWord(t, "NOT"):
			if i+1 < item.tokEnd && isWord(toks[i+1], "NULL") {
				col.NotNull = true
				i += 2
			} else {
				i++
			}
		case isWord(t, "UNIQUE"):
			col.Unique = true
			i++
		case isWord(t, "REFERENCES"):
			i++
			target, i2, ok := readQualifiedNameTokens(toks, i, item.tokEnd)
			if !ok {
				i++
				continue
			}
			i = i2
			targetCol := ""
			if i < item.tokEnd && toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "(" {
				cols, i3, ok := readParenColumnList(toks, i, item.tokEnd)
				if ok {
					i = i3
					if len(cols) > 0 {
						targetCol = cols[0]
					}
				} else {
					i++
				}
			}
			col.FK = &ColumnFK{Table: target, Column: targetCol}
		default:
			i++
		}
	}

	return col, true
}

// displayName renders a name token for display: quotes stripped, original
// case preserved for a quoted identifier, as-written case for an unquoted
// one (i.e. it does not lower-case, unlike normalizeNameToken which is used
// for matching/normalized identifiers elsewhere in this package).
func displayName(t lexer.Token) string {
	if t.Kind == lexer.TokenQuotedIdent {
		return t.Value
	}
	return t.Value
}

// normalizeTypeText renders the verbatim source text of tokens
// [start,end), whitespace-normalized to single spaces between tokens
// (collapsing the original source formatting) — e.g. "numeric ( 10 , 2 )"
// as-adjacent-tokens becomes "numeric(10, 2)".
func normalizeTypeText(text string, toks []lexer.Token, start, end int) string {
	if start >= end {
		return ""
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		t := toks[i]
		if i > start {
			if needsSpaceBefore(toks[i-1], t) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(text[t.Start:t.End])
	}
	return b.String()
}

// needsSpaceBefore decides whether a space belongs between consecutive
// tokens prev and cur when rendering normalized type text: punctuation
// like "(", ")", ",", "." binds tightly to its neighbor on the appropriate
// side, everything else (word/word, word/paren-open like "varying (") gets
// a single space.
func needsSpaceBefore(prev, cur lexer.Token) bool {
	prevIsOpenOrDot := prev.Kind == lexer.TokenPunct && (prev.Raw == "(" || prev.Raw == ".")
	curIsCloseCommaDot := cur.Kind == lexer.TokenPunct && (cur.Raw == ")" || cur.Raw == "," || cur.Raw == ".")
	curIsOpen := cur.Kind == lexer.TokenPunct && cur.Raw == "("
	prevIsWordOrCloseParen := prev.Kind == lexer.TokenWord || prev.Kind == lexer.TokenQuotedIdent ||
		(prev.Kind == lexer.TokenPunct && prev.Raw == ")")
	if prevIsOpenOrDot {
		return false
	}
	if curIsCloseCommaDot {
		return false
	}
	if curIsOpen {
		// Bind a type's own parenthesized args tightly: "numeric(10, 2)",
		// but this only fires directly after the base type name/prior
		// paren-arg token, which is exactly the case here since Type
		// scanning stops before any constraint keyword could introduce an
		// unrelated "(".
		return false
	}
	if prevIsWordOrCloseParen {
		return true
	}
	return true
}

// readParenColumnList reads a parenthesized, comma-separated column name
// list starting at i (which must be the opening "("), returning the
// display names, the index just past the closing ")", and ok=false if the
// list isn't well-formed enough to read with confidence (caller treats
// that as "no column list", not an error, per fail-soft ExtractColumns
// semantics).
func readParenColumnList(toks []lexer.Token, i, end int) ([]string, int, bool) {
	if i >= end || !(toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "(") {
		return nil, i, false
	}
	i++
	var names []string
	for i < end {
		if toks[i].Kind == lexer.TokenPunct && toks[i].Raw == ")" {
			return names, i + 1, true
		}
		if isIdentLike(toks[i]) {
			names = append(names, displayName(toks[i]))
			i++
			if i < end && toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "," {
				i++
				continue
			}
			continue
		}
		// Unrecognized token inside the list (shouldn't normally happen);
		// skip it defensively rather than abort.
		i++
	}
	return names, i, false
}

// applyTableLevelConstraint inspects one table-level constraint item
// (PRIMARY KEY(...), UNIQUE(...), [CONSTRAINT name] FOREIGN KEY(...)
// REFERENCES ...) and applies its effect onto the already-extracted cols,
// looked up by their normalized (matching) name in byName. Anything it
// can't confidently parse is simply skipped (fail-soft).
func applyTableLevelConstraint(toks []lexer.Token, item listItem, cols []Column, byName map[string]int) {
	i := item.tokStart
	if i >= item.tokEnd {
		return
	}
	if isWord(toks[i], "CONSTRAINT") {
		i += 2 // CONSTRAINT <name>
		if i >= item.tokEnd {
			return
		}
	}

	switch {
	case isWord(toks[i], "PRIMARY"):
		i++
		if i < item.tokEnd && isWord(toks[i], "KEY") {
			i++
		}
		names, _, ok := readParenColumnList(toks, i, item.tokEnd)
		if !ok {
			return
		}
		for _, n := range names {
			if idx, ok := lookupColumn(cols, byName, n); ok {
				cols[idx].PK = true
				cols[idx].NotNull = true
			}
		}
	case isWord(toks[i], "UNIQUE"):
		i++
		names, _, ok := readParenColumnList(toks, i, item.tokEnd)
		if !ok {
			return
		}
		if len(names) == 1 {
			if idx, ok := lookupColumn(cols, byName, names[0]); ok {
				cols[idx].Unique = true
			}
		}
		// Multi-column table-level UNIQUE does not mark individual
		// columns, per spec.
	case isWord(toks[i], "FOREIGN"):
		i++
		if i < item.tokEnd && isWord(toks[i], "KEY") {
			i++
		}
		localNames, i2, ok := readParenColumnList(toks, i, item.tokEnd)
		if !ok {
			return
		}
		i = i2
		if i >= item.tokEnd || !isWord(toks[i], "REFERENCES") {
			return
		}
		i++
		target, i3, ok := readQualifiedNameTokens(toks, i, item.tokEnd)
		if !ok {
			return
		}
		i = i3
		var targetNames []string
		if i < item.tokEnd && toks[i].Kind == lexer.TokenPunct && toks[i].Raw == "(" {
			targetNames, _, _ = readParenColumnList(toks, i, item.tokEnd)
		}
		assignCompositeFK(cols, byName, localNames, target, targetNames)
	}
}

// lookupColumn finds a column by its display name, matching
// case-insensitively for an unquoted name and exactly for a quoted one —
// mirroring how a table-level constraint's column-list entries are
// themselves just name tokens without their own quoting semantics beyond
// what readParenColumnList already preserved via displayName. To keep
// lookup simple and fail-soft, this tries an exact match first, then a
// case-insensitive fallback.
func lookupColumn(cols []Column, byName map[string]int, name string) (int, bool) {
	if idx, ok := byName[name]; ok {
		return idx, true
	}
	for i, c := range cols {
		if strings.EqualFold(c.Name, name) {
			return i, true
		}
	}
	return 0, false
}

// assignCompositeFK sets FK on every column named in localNames, pairing
// each with the target column at the same position in targetNames when
// both lists are present and of equal length; otherwise every assigned FK
// gets an empty target column, per spec.
func assignCompositeFK(cols []Column, byName map[string]int, localNames []string, target string, targetNames []string) {
	positional := len(targetNames) == len(localNames) && len(localNames) > 0
	for pos, n := range localNames {
		idx, ok := lookupColumn(cols, byName, n)
		if !ok {
			continue
		}
		targetCol := ""
		if positional {
			targetCol = targetNames[pos]
		}
		cols[idx].FK = &ColumnFK{Table: target, Column: targetCol}
	}
}
