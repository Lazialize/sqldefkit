package parse

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// cursor walks a token stream, skipping comments (the lexer already keeps
// comments out of the semantic token stream except TokenComment entries
// interleaved for inline comments; we filter those out up front).
type cursor struct {
	toks []lexer.Token
	pos  int
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
// word_or_quoted )*. Returns the normalized dotted name and whether a name
// was found.
func (c *cursor) readQualifiedName() (string, bool) {
	t, ok := c.peek()
	if !ok || (t.Kind != lexer.TokenWord && t.Kind != lexer.TokenQuotedIdent) {
		return "", false
	}
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
	return strings.Join(parts, "."), true
}

// skipIfNotExists consumes an optional "IF NOT EXISTS" sequence.
func (c *cursor) skipIfNotExists() {
	c.consumeWords("IF", "NOT", "EXISTS")
}
