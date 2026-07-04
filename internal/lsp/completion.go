package lsp

import "strings"

// completionContextKind classifies why textDocument/completion returned
// (non-empty) items: what syntactic position the cursor is in.
type completionContextKind int

const (
	// completionContextNone means the cursor isn't in any of the three
	// recognized completion contexts; no items should be offered.
	completionContextNone completionContextKind = iota
	// completionContextReferences: cursor is completing the table name
	// after a REFERENCES keyword.
	completionContextReferences
	// completionContextDirective: cursor is completing a name inside a
	// "-- sqldefkit:require ..." directive comment.
	completionContextDirective
	// completionContextOn: cursor is completing the table name after ON
	// in a CREATE [UNIQUE] INDEX or CREATE TRIGGER statement.
	completionContextOn
)

// completionContext describes the detected completion context and the
// partial word (prefix) being typed, which callers filter candidate names
// by (case-insensitively, prefix match).
type completionContext struct {
	Kind   completionContextKind
	Prefix string
}

// detectCompletionContext scans content backward from byteOffset (the
// cursor position, a byte offset into content) to classify which of the
// three supported completion contexts (if any) the cursor is in, and
// extracts the current word prefix being typed so completion can work
// mid-word.
//
// This is a lightweight scan, not a full re-lex: the buffer being
// completed in is frequently syntactically incomplete (e.g. an unclosed
// paren while typing "REFERENCES u"), so it deliberately avoids
// lexer.Split (which expects complete, valid statement framing) in favor
// of directly walking bytes, skipping comments/strings well enough for
// the patterns this needs to recognize.
func detectCompletionContext(content string, byteOffset int) completionContext {
	if byteOffset < 0 {
		byteOffset = 0
	}
	if byteOffset > len(content) {
		byteOffset = len(content)
	}

	prefix, wordStart := wordPrefixBefore(content, byteOffset)

	// Context b: inside a "-- sqldefkit:require " directive comment. Check
	// this first since it's a same-line textual match, independent of
	// token scanning.
	if isInRequireDirective(content, wordStart) {
		return completionContext{Kind: completionContextDirective, Prefix: prefix}
	}

	prevWord, prevWordEnd := prevWordBefore(content, wordStart)
	if prevWord == "" {
		return completionContext{Kind: completionContextNone, Prefix: prefix}
	}

	// Context a: previous token is REFERENCES.
	if strings.EqualFold(prevWord, "REFERENCES") {
		return completionContext{Kind: completionContextReferences, Prefix: prefix}
	}

	// Context c: previous token is ON, and the enclosing statement begins
	// with CREATE [UNIQUE] INDEX or CREATE TRIGGER.
	if strings.EqualFold(prevWord, "ON") {
		stmtStart := enclosingStatementStart(content, wordStart)
		if statementStartsIndexOrTrigger(content[stmtStart:prevWordEnd]) {
			return completionContext{Kind: completionContextOn, Prefix: prefix}
		}
	}

	return completionContext{Kind: completionContextNone, Prefix: prefix}
}

// wordPrefixBefore scans backward from offset over identifier bytes
// (isIdentByte) and returns the partial word found (possibly "") along
// with its start offset. This is the word being completed, e.g. the "u" in
// "REFERENCES u|".
func wordPrefixBefore(content string, offset int) (word string, start int) {
	i := offset
	for i > 0 && isIdentByte(content[i-1]) {
		i--
	}
	return content[i:offset], i
}

// prevWordBefore skips whitespace and comments backward from offset and
// returns the identifier-word token immediately preceding it (case as
// written; callers compare case-insensitively) and the offset just past
// that word. Returns ("", 0) if no such word is found before hitting the
// start of content or a non-identifier, non-whitespace/comment byte
// (e.g. punctuation like "(" or "," breaks the search, since that means
// there's no bare "previous token" — REFERENCES/ON must be the
// immediately preceding token with only whitespace/comments between).
func prevWordBefore(content string, offset int) (word string, end int) {
	i := skipInsignificantBackward(content, offset)
	if i == 0 {
		return "", 0
	}
	if !isIdentByte(content[i-1]) {
		return "", 0
	}
	wordEnd := i
	for i > 0 && isIdentByte(content[i-1]) {
		i--
	}
	return content[i:wordEnd], wordEnd
}

// skipInsignificantBackward returns the offset after skipping backward
// over whitespace and line/block comments starting immediately before
// offset.
func skipInsignificantBackward(content string, offset int) int {
	i := offset
	for {
		j := skipWhitespaceBackward(content, i)
		if k, ok := skipLineCommentBackward(content, j); ok {
			i = k
			continue
		}
		if k, ok := skipBlockCommentBackward(content, j); ok {
			i = k
			continue
		}
		return j
	}
}

func skipWhitespaceBackward(content string, offset int) int {
	i := offset
	for i > 0 {
		switch content[i-1] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			i--
		default:
			return i
		}
	}
	return i
}

// skipLineCommentBackward checks whether content[:offset] ends with a
// complete "-- ...\n" or "# ...\n" line comment (the newline itself having
// already been consumed by whitespace-skipping, so it looks for the
// comment's line by scanning back to the start of the current line and
// checking whether that line begins with "--" or "#"). Returns the offset
// before the comment and true if so.
func skipLineCommentBackward(content string, offset int) (int, bool) {
	lineStart := offset
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	line := content[lineStart:offset]
	trimmed := strings.TrimLeft(line, " \t\r\v\f")
	if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") {
		return lineStart, true
	}
	return offset, false
}

// skipBlockCommentBackward checks whether content[:offset] ends with a
// complete "/* ... */" block comment and, if so, returns the offset
// before its opening "/*" and true.
func skipBlockCommentBackward(content string, offset int) (int, bool) {
	if offset < 2 || content[offset-2:offset] != "*/" {
		return offset, false
	}
	idx := strings.LastIndex(content[:offset-2], "/*")
	if idx < 0 {
		return offset, false
	}
	return idx, true
}

// isInRequireDirective reports whether wordStart (the offset just before
// the word being typed) falls inside a "-- sqldefkit:require ..." (or "#
// sqldefkit:require ...") directive comment: the text on the same line
// before wordStart, trimmed of leading whitespace, starts with a line
// comment marker followed by "sqldefkit:require" and at least one
// separating space, meaning we're positioned somewhere in its
// (space-separated) name list.
func isInRequireDirective(content string, wordStart int) bool {
	lineStart := wordStart
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	line := content[lineStart:wordStart]
	trimmed := strings.TrimLeft(line, " \t\r\v\f")

	var marker string
	switch {
	case strings.HasPrefix(trimmed, "--"):
		marker = "--"
	case strings.HasPrefix(trimmed, "#"):
		marker = "#"
	default:
		return false
	}

	rest := strings.TrimLeft(trimmed[len(marker):], " \t")
	const prefix = "sqldefkit:require"
	if !strings.HasPrefix(rest, prefix) {
		return false
	}
	rest = rest[len(prefix):]
	// Need at least one whitespace separator between the directive name
	// and the name list (matches internal/parse/extract.go's directive
	// parsing, which requires the same).
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

// enclosingStatementStart scans backward from offset for the nearest
// top-level ';' (i.e. one not inside a string literal — comments/strings
// aren't fully tracked backward, so this is a pragmatic approximation: it
// simply finds the last ';' byte before offset, which is correct for the
// overwhelming majority of schema files where ';' doesn't appear inside
// string literals in DDL) and returns the offset just past it (or 0 if
// none found).
func enclosingStatementStart(content string, offset int) int {
	idx := strings.LastIndexByte(content[:offset], ';')
	if idx < 0 {
		return 0
	}
	return idx + 1
}

// statementStartsIndexOrTrigger reports whether stmtText (from the start
// of the enclosing statement up to and including the "ON" keyword) begins
// with CREATE [UNIQUE] INDEX or CREATE TRIGGER, per a simple
// whitespace-tokenized scan of its first few words (deliberately not a
// full lex: this only needs to recognize the statement's opening
// keywords).
func statementStartsIndexOrTrigger(stmtText string) bool {
	words := statementLeadingWords(stmtText, 4)
	if len(words) < 2 {
		return false
	}
	if !strings.EqualFold(words[0], "CREATE") {
		return false
	}
	i := 1
	if i < len(words) && strings.EqualFold(words[i], "UNIQUE") {
		i++
	}
	if i < len(words) && strings.EqualFold(words[i], "INDEX") {
		return true
	}
	if i < len(words) && strings.EqualFold(words[i], "TRIGGER") {
		return true
	}
	return false
}

// statementLeadingWords extracts up to max identifier-words from the
// start of s, skipping whitespace and comments between them.
func statementLeadingWords(s string, max int) []string {
	var words []string
	i := 0
	for i < len(s) && len(words) < max {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n', '\r', '\v', '\f':
				i++
				continue
			}
			if consumed, ok := skipCommentForward(s, i); ok {
				i += consumed
				continue
			}
			break
		}
		if i >= len(s) {
			break
		}
		if !isIdentByte(s[i]) {
			// Punctuation (e.g. "(") ends the leading-keyword run.
			break
		}
		start := i
		for i < len(s) && isIdentByte(s[i]) {
			i++
		}
		words = append(words, s[start:i])
	}
	return words
}

// skipCommentForward checks whether a "--", "#", or "/* */" comment
// starts at s[i:], returning its length and true if so.
func skipCommentForward(s string, i int) (int, bool) {
	if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
		j := i + 2
		for j < len(s) && s[j] != '\n' {
			j++
		}
		return j - i, true
	}
	if i < len(s) && s[i] == '#' {
		j := i + 1
		for j < len(s) && s[j] != '\n' {
			j++
		}
		return j - i, true
	}
	if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
		j := i + 2
		for j+1 < len(s) && !(s[j] == '*' && s[j+1] == '/') {
			j++
		}
		if j+1 < len(s) {
			j += 2
		} else {
			j = len(s)
		}
		return j - i, true
	}
	return 0, false
}
