package parse

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// cursor walks a token stream, skipping comments (the lexer already keeps
// comments out of the semantic token stream except TokenComment entries
// interleaved for inline comments; we filter those out up front). It also
// knows how to translate a token's statement-relative byte offset into an
// absolute source Position, via stmtStart (the statement's own byte
// offset in the file) and a shared line map for the whole file.
type cursor struct {
	toks      []lexer.Token
	pos       int
	file      string
	stmtStart int
	lineMap   *pos.LineMap
}

func newCursor(toks []lexer.Token) *cursor {
	filtered := make([]lexer.Token, 0, len(toks))
	for _, t := range toks {
		if t.Kind != lexer.TokenComment {
			filtered = append(filtered, t)
		}
	}
	return &cursor{toks: filtered}
}

// tokenPos returns the absolute source Position of token t, given the
// cursor's file/stmtStart/lineMap. If lineMap is nil (position tracking
// not requested, e.g. in tests that only care about names/deps), it
// returns a zero Position.
func (c *cursor) tokenPos(t lexer.Token) pos.Position {
	if c.lineMap == nil {
		return pos.Position{}
	}
	return c.lineMap.Pos(c.file, c.stmtStart+t.Start)
}

func (c *cursor) atEnd() bool { return c.pos >= len(c.toks) }

func (c *cursor) peek() (lexer.Token, bool) {
	if c.atEnd() {
		return lexer.Token{}, false
	}
	return c.toks[c.pos], true
}

func (c *cursor) peekAt(offset int) (lexer.Token, bool) {
	i := c.pos + offset
	if i < 0 || i >= len(c.toks) {
		return lexer.Token{}, false
	}
	return c.toks[i], true
}

// isWordEq reports whether t is a TokenWord equal to kw, case-insensitively.
func isWordEq(t lexer.Token, kw string) bool {
	return t.Kind == lexer.TokenWord && strings.EqualFold(t.Value, kw)
}

// matchWords checks whether the upcoming tokens starting at c.pos are the
// given sequence of case-insensitive keywords, without consuming them.
func (c *cursor) matchWords(kws ...string) bool {
	for i, kw := range kws {
		t, ok := c.peekAt(i)
		if !ok || !isWordEq(t, kw) {
			return false
		}
	}
	return true
}

// consumeWords consumes the given keyword sequence if it matches at the
// current position; returns whether it matched (and was consumed).
func (c *cursor) consumeWords(kws ...string) bool {
	if !c.matchWords(kws...) {
		return false
	}
	c.pos += len(kws)
	return true
}

// readQualifiedName reads a (possibly schema-qualified, possibly quoted)
// name starting at the current position: word_or_quoted ( '.'
// word_or_quoted )*. Returns the normalized dotted name, the position of
// its first token (the schema part if schema-qualified), and whether a
// name was found.
func (c *cursor) readQualifiedName() (string, pos.Position, bool) {
	t, ok := c.peek()
	if !ok || (t.Kind != lexer.TokenWord && t.Kind != lexer.TokenQuotedIdent) {
		return "", pos.Position{}, false
	}
	namePos := c.tokenPos(t)
	var parts []string
	parts = append(parts, normalizeToken(t))
	c.pos++
	for {
		dot, ok := c.peek()
		if !ok || dot.Kind != lexer.TokenPunct || dot.Raw != "." {
			break
		}
		nt, ok := c.peekAt(1)
		if !ok || (nt.Kind != lexer.TokenWord && nt.Kind != lexer.TokenQuotedIdent) {
			break
		}
		parts = append(parts, normalizeToken(nt))
		c.pos += 2
	}
	return strings.Join(parts, "."), namePos, true
}

// skipIfNotExists consumes an optional "IF NOT EXISTS" sequence.
func (c *cursor) skipIfNotExists() {
	c.consumeWords("IF", "NOT", "EXISTS")
}
