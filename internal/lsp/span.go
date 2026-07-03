package lsp

import "strings"

// lineText returns the text of the 1-based line-th line of content (no
// trailing newline), or "" if line is out of range. content is assumed to
// already be LF-normalized (as bundle.Load normalizes before computing
// positions), so lines are split on "\n" alone.
func lineText(content string, line int) string {
	if line < 1 {
		return ""
	}
	// Avoid splitting the whole file on every call in hot paths by
	// scanning forward for the line's start/end directly.
	start := 0
	cur := 1
	for cur < line {
		idx := strings.IndexByte(content[start:], '\n')
		if idx < 0 {
			return ""
		}
		start += idx + 1
		cur++
	}
	end := strings.IndexByte(content[start:], '\n')
	if end < 0 {
		return content[start:]
	}
	return content[start : start+end]
}

// isIdentByte reports whether b can appear in an unquoted identifier:
// letters, digits, underscore, or dollar sign (permissive across
// dialects, matching how internal/lexer treats identifier characters
// closely enough for span-scanning purposes).
func isIdentByte(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
		b >= 0x80 // treat any UTF-8 continuation/lead byte as part of the identifier
}

// identifierSpan scans content starting at byte offset start (which
// should point at the first byte of a name — quoted or unquoted) and
// returns the [start, end) byte range of the full identifier token,
// including surrounding quote characters for quoted identifiers. If start
// is out of bounds or doesn't point at a recognizable identifier
// character, it falls back to a zero-length span at start (per the "fall
// back to a zero-length range" requirement).
func identifierSpan(content string, start int) (end int) {
	if start < 0 || start >= len(content) {
		return start
	}

	switch content[start] {
	case '"':
		return quotedSpan(content, start, '"')
	case '`':
		return quotedSpan(content, start, '`')
	case '[':
		return quotedSpan(content, start, ']')
	}

	if !isIdentByte(content[start]) {
		return start
	}

	i := start
	for i < len(content) && isIdentByte(content[i]) {
		i++
	}
	return i
}

// quotedSpan returns the end offset of a quoted identifier starting at
// start (which must point at the opening quote rune), scanning for the
// matching closing rune close. Doubled-close-quote escaping (e.g. ""
// inside a double-quoted identifier) is treated as two separate
// identifiers ending at the first quote — an acceptable approximation for
// span-scanning purposes, since names containing embedded quotes are rare
// and this only affects diagnostic/definition range width, not
// correctness of lookups (which key on Definition/Reference.Name, not on
// re-scanning).
func quotedSpan(content string, start int, close byte) int {
	i := start + 1
	for i < len(content) {
		if content[i] == close {
			return i + 1
		}
		i++
	}
	return len(content)
}
