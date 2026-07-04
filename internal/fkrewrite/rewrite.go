package fkrewrite

import "strings"

// Remove produces the CREATE TABLE text with the given clauses' spans
// removed, cleaning up comma/whitespace hygiene so the result has no
// dangling commas, no double commas, and no comma immediately before the
// list's closing paren. clauses must all have come from Extract on this
// same text and may be passed in any order.
//
// For a table-level clause, the whole list item (comma included, per
// removeSpans) is removed. For an inline clause, only the REFERENCES...
// span within the column definition is removed, leaving the column and
// its other constraints intact.
func Remove(text string, clauses []Clause) string {
	if len(clauses) == 0 {
		return text
	}

	type span struct {
		start, end int
		tableLevel bool
	}
	spans := make([]span, len(clauses))
	for i, c := range clauses {
		spans[i] = span{start: c.clauseStart, end: c.clauseEnd, tableLevel: c.TableLevel}
	}

	// Sort spans by start offset so we can walk the text once.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j-1].start > spans[j].start; j-- {
			spans[j-1], spans[j] = spans[j], spans[j-1]
		}
	}

	var b strings.Builder
	cursor := 0
	for _, sp := range spans {
		start, end := sp.start, sp.end
		if sp.tableLevel {
			start, end = expandTableLevelSpan(text, start, end)
		}
		if start < cursor {
			// Overlapping spans shouldn't occur (Extract yields one
			// clause per list item, and items are disjoint), but guard
			// defensively rather than corrupt output.
			continue
		}
		b.WriteString(text[cursor:start])
		cursor = end
	}
	b.WriteString(text[cursor:])

	return cleanupListHygiene(b.String())
}

// expandTableLevelSpan widens a table-level clause's [start,end) span to
// also swallow one adjacent comma (preferring the following comma, i.e.
// the one that separated this item from the next, falling back to a
// preceding comma if this was the last item) plus the whitespace between
// the clause and that comma, so removing a whole-item clause doesn't
// leave a stray comma behind. If, after that, the clause spans a whole
// line by itself (nothing but horizontal whitespace between the previous
// newline and the clause, and nothing but horizontal whitespace between
// the clause and the next newline), that surrounding newline+indentation
// is swallowed too, so the common case (one constraint per line) leaves
// no blank line behind. cleanupListHygiene still runs afterward as a
// second safety net for any remaining whitespace irregularities.
func expandTableLevelSpan(text string, start, end int) (int, int) {
	// Look forward for a comma, skipping whitespace (including
	// newlines): the next item, if any, may start on a later line.
	j := end
	for j < len(text) && isSQLSpace(text[j]) {
		j++
	}
	if j < len(text) && text[j] == ',' {
		end = j + 1
	} else {
		// Otherwise look backward for a comma, skipping whitespace
		// (including newlines): this was the last item, so the comma
		// separating it from the previous item may be on an earlier
		// line.
		i := start
		for i > 0 && isSQLSpace(text[i-1]) {
			i--
		}
		if i > 0 && text[i-1] == ',' {
			start = i - 1
		}
	}

	// Absorb the clause's own line: leading indentation back to (and
	// including) the preceding newline, and trailing horizontal
	// whitespace up to (and including) the following newline — but only
	// when nothing else shares that line with the clause, so removal
	// only ever eats whitespace it introduced no ambiguity about.
	lineStart := start
	for lineStart > 0 && isHSpace(text[lineStart-1]) {
		lineStart--
	}
	lineEnd := end
	for lineEnd < len(text) && isHSpace(text[lineEnd]) {
		lineEnd++
	}
	startsLine := lineStart == 0 || text[lineStart-1] == '\n'
	endsLine := lineEnd == len(text) || text[lineEnd] == '\n'
	if startsLine && endsLine {
		start = lineStart
		end = lineEnd
		if start > 0 && text[start-1] == '\n' {
			start--
		} else if end < len(text) && text[end] == '\n' {
			end++
		}
	}

	return start, end
}

func isHSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\v', '\f':
		return true
	default:
		return false
	}
}

func isSQLSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

// cleanupListHygiene collapses any ", ," left by an inline-clause removal
// that happened to butt up against another removed span, and strips
// horizontal whitespace an inline clause's removal left dangling at the
// end of its line (formatting differences from run to run are not
// attempted beyond that — see package doc).
func cleanupListHygiene(s string) string {
	// Collapse doubled commas (with optional whitespace/newlines between)
	// down to one, repeatedly (handles 3+ in a row from multiple adjacent
	// removals).
	for {
		replaced := collapseDoubleComma(s)
		if replaced == s {
			break
		}
		s = replaced
	}
	return trimTrailingHSpacePerLine(s)
}

// trimTrailingHSpacePerLine removes horizontal whitespace immediately
// before each newline (and at the very end of s), which an inline
// clause's removal can leave behind when it was the last thing on its
// line (e.g. "user_id int \n" -> "user_id int\n").
func trimTrailingHSpacePerLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lineStart := 0
	flushLine := func(end int) {
		trimEnd := end
		for trimEnd > lineStart && isHSpace(s[trimEnd-1]) {
			trimEnd--
		}
		b.WriteString(s[lineStart:trimEnd])
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			flushLine(i)
			b.WriteByte('\n')
			lineStart = i + 1
		}
	}
	flushLine(len(s))
	return b.String()
}

// collapseDoubleComma finds the first occurrence of a comma, optional
// whitespace, then another comma, and removes the second comma (keeping
// the whitespace as-is for a minimal diff).
func collapseDoubleComma(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ',' {
			continue
		}
		j := i + 1
		for j < len(s) && isSQLSpace(s[j]) {
			j++
		}
		if j < len(s) && s[j] == ',' {
			return s[:j] + s[j+1:]
		}
	}
	return s
}
