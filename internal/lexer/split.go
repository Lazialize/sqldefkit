package lexer

import (
	"fmt"
	"strings"
)

// Split breaks src into top-level statements separated by ';'. It is
// dialect-aware for comment/string/quoting syntax. Comments that
// immediately precede a statement (i.e. with no blank line containing
// only whitespace... actually: with nothing but other comments/whitespace
// between them and the statement) are attached to that statement as
// LeadingComments and are not treated as part of Text.
func Split(src string, dialect Dialect) ([]Statement, error) {
	s := &scanner{src: src, dialect: dialect}
	var statements []Statement

	for {
		s.skipInsignificant()
		pendingComments := s.pendingComments
		pendingCommentStarts := s.pendingCommentStarts
		s.pendingComments = nil
		s.pendingCommentStarts = nil

		if s.pos >= len(s.src) {
			// Only trailing comments/whitespace left; nothing to attach
			// them to.
			break
		}

		bodyStart := s.pos
		var tokens []Token
		terminated := false

		for s.pos < len(s.src) {
			c := s.src[s.pos]

			if isSpace(c) {
				s.pos++
				continue
			}

			if handled, err := s.tryConsumeComment(&tokens); err != nil {
				return nil, err
			} else if handled {
				continue
			}

			if c == ';' {
				s.pos++
				terminated = true
				break
			}

			if c == '\'' {
				start := s.pos
				if err := s.consumeSingleQuoted(); err != nil {
					return nil, err
				}
				tokens = append(tokens, Token{Kind: TokenString, Raw: s.src[start:s.pos], Start: start, End: s.pos})
				continue
			}

			if c == '"' {
				start := s.pos
				if err := s.consumeQuotedIdent('"'); err != nil {
					return nil, err
				}
				raw := s.src[start:s.pos]
				tokens = append(tokens, Token{Kind: TokenQuotedIdent, Raw: raw, Value: unquoteIdent(raw, '"'), Start: start, End: s.pos})
				continue
			}

			if c == '`' && (s.dialect == MySQL || s.dialect == SQLite) {
				start := s.pos
				if err := s.consumeQuotedIdent('`'); err != nil {
					return nil, err
				}
				raw := s.src[start:s.pos]
				tokens = append(tokens, Token{Kind: TokenQuotedIdent, Raw: raw, Value: unquoteIdent(raw, '`'), Start: start, End: s.pos})
				continue
			}

			if c == '[' && s.dialect == SQLite {
				start := s.pos
				if err := s.consumeBracketIdent(); err != nil {
					return nil, err
				}
				raw := s.src[start:s.pos]
				tokens = append(tokens, Token{Kind: TokenQuotedIdent, Raw: raw, Value: unquoteIdent(raw, ']'), Start: start, End: s.pos})
				continue
			}

			if c == '$' && s.dialect == Postgres {
				if tag, ok := s.matchDollarTagStart(); ok {
					start := s.pos
					if err := s.consumeDollarQuoted(tag); err != nil {
						return nil, err
					}
					tokens = append(tokens, Token{Kind: TokenString, Raw: s.src[start:s.pos], Start: start, End: s.pos})
					continue
				}
			}

			if isWordChar(c) {
				start := s.pos
				for s.pos < len(s.src) && isWordChar(s.src[s.pos]) {
					s.pos++
				}
				word := s.src[start:s.pos]
				tokens = append(tokens, Token{Kind: TokenWord, Raw: word, Value: word, Start: start, End: s.pos})
				continue
			}

			// Any other punctuation: emit as a single-char token.
			tokens = append(tokens, Token{Kind: TokenPunct, Raw: string(c), Start: s.pos, End: s.pos + 1})
			s.pos++
		}

		bodyEnd := s.pos
		if terminated {
			bodyEnd = s.pos - 1 // exclude the ';'
		}

		text := strings.TrimSpace(s.src[bodyStart:bodyEnd])
		if text == "" {
			// No actual statement body (e.g. stray ';', or only
			// comments before a ';'/EOF). Skip, but don't lose
			// comments already collected: reattach them (in order)
			// as pending for the next statement.
			var carried []string
			var carriedStarts []int
			carried = append(carried, pendingComments...)
			carriedStarts = append(carriedStarts, pendingCommentStarts...)
			for _, t := range tokens {
				if t.Kind == TokenComment {
					carried = append(carried, t.Raw)
					carriedStarts = append(carriedStarts, t.Start)
				}
			}
			s.pendingComments = carried
			s.pendingCommentStarts = carriedStarts
			if s.pos >= len(s.src) {
				break
			}
			continue
		}

		// Adjust token positions to be relative to text start (after
		// trim). Find how much leading whitespace was trimmed.
		trimOffset := strings.Index(s.src[bodyStart:bodyEnd], text)
		if trimOffset < 0 {
			trimOffset = 0
		}
		base := bodyStart + trimOffset
		relTokens := make([]Token, len(tokens))
		for i, t := range tokens {
			relTokens[i] = Token{
				Kind:  t.Kind,
				Raw:   t.Raw,
				Value: t.Value,
				Start: t.Start - base,
				End:   t.End - base,
			}
		}

		statements = append(statements, Statement{
			Text:                 text,
			Start:                base,
			End:                  base + len(text),
			LeadingComments:      pendingComments,
			LeadingCommentStarts: pendingCommentStarts,
			Tokens:               relTokens,
		})
	}

	return statements, nil
}

type scanner struct {
	src                  string
	pos                  int
	dialect              Dialect
	pendingComments      []string
	pendingCommentStarts []int
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isWordChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// skipInsignificant advances past whitespace and comments before a
// statement begins, collecting comment text into s.pendingComments.
func (s *scanner) skipInsignificant() {
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if isSpace(c) {
			s.pos++
			continue
		}
		if consumed, comment, ok := s.peekComment(); ok {
			start := s.pos
			s.pos += consumed
			s.pendingComments = append(s.pendingComments, comment)
			s.pendingCommentStarts = append(s.pendingCommentStarts, start)
			continue
		}
		break
	}
}

// tryConsumeComment consumes a comment at the current position if present,
// appending a TokenComment to tokens (used so parse package could see
// comment adjacency if ever needed) and also recording it so that it does
// NOT interrupt attachment of comments to a following statement. Note:
// comments occurring *inside* a statement are just skipped from the
// token stream's semantic content but are kept in Raw text via Text field
// (Text preserves verbatim source, comments included).
func (s *scanner) tryConsumeComment(tokens *[]Token) (bool, error) {
	consumed, comment, ok := s.peekComment()
	if !ok {
		return false, nil
	}
	start := s.pos
	s.pos += consumed
	*tokens = append(*tokens, Token{Kind: TokenComment, Raw: comment, Start: start, End: s.pos})
	return true, nil
}

// peekComment checks whether a comment starts at s.pos. Returns the number
// of bytes it spans, its raw text, and whether one was found. Does not
// advance s.pos.
func (s *scanner) peekComment() (int, string, bool) {
	src := s.src
	i := s.pos
	if i >= len(src) {
		return 0, "", false
	}
	if src[i] == '-' && i+1 < len(src) && src[i+1] == '-' {
		j := i + 2
		for j < len(src) && src[j] != '\n' {
			j++
		}
		return j - i, src[i:j], true
	}
	if s.dialect == MySQL && src[i] == '#' {
		j := i + 1
		for j < len(src) && src[j] != '\n' {
			j++
		}
		return j - i, src[i:j], true
	}
	if src[i] == '/' && i+1 < len(src) && src[i+1] == '*' {
		j := i + 2
		for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
			j++
		}
		if j+1 < len(src) {
			j += 2
		} else {
			j = len(src)
		}
		return j - i, src[i:j], true
	}
	return 0, "", false
}

// consumeSingleQuoted consumes a '...'-delimited string literal starting
// at the opening quote (s.pos must point at it), handling doubled single
// quote escaping and, for MySQL, backslash escaping.
func (s *scanner) consumeSingleQuoted() error {
	src := s.src
	start := s.pos
	i := s.pos + 1
	for {
		if i >= len(src) {
			return fmt.Errorf("unterminated string literal starting at offset %d", start)
		}
		c := src[i]
		if c == '\\' && s.dialect == MySQL {
			i += 2
			continue
		}
		if c == '\'' {
			if i+1 < len(src) && src[i+1] == '\'' {
				i += 2
				continue
			}
			i++
			break
		}
		i++
	}
	s.pos = i
	return nil
}

// consumeQuotedIdent consumes a close-delimited quoted identifier (e.g.
// "..." or a backtick-quoted identifier), handling doubled-delimiter
// escaping (e.g. two double quotes or two backticks in a row).
func (s *scanner) consumeQuotedIdent(close byte) error {
	src := s.src
	start := s.pos
	i := s.pos + 1
	for {
		if i >= len(src) {
			return fmt.Errorf("unterminated quoted identifier starting at offset %d", start)
		}
		c := src[i]
		if c == close {
			if i+1 < len(src) && src[i+1] == close {
				i += 2
				continue
			}
			i++
			break
		}
		i++
	}
	s.pos = i
	return nil
}

// consumeBracketIdent consumes a [...] quoted identifier (SQLite).
func (s *scanner) consumeBracketIdent() error {
	src := s.src
	start := s.pos
	i := s.pos + 1
	for {
		if i >= len(src) {
			return fmt.Errorf("unterminated bracketed identifier starting at offset %d", start)
		}
		if src[i] == ']' {
			i++
			break
		}
		i++
	}
	s.pos = i
	return nil
}

// matchDollarTagStart checks for a dollar-quote opening tag ($$ or
// $tag$) at s.pos without consuming input. Returns the tag (without the
// surrounding $) and whether one was found.
func (s *scanner) matchDollarTagStart() (string, bool) {
	src := s.src
	i := s.pos
	if i >= len(src) || src[i] != '$' {
		return "", false
	}
	j := i + 1
	for j < len(src) && (isWordChar(src[j])) {
		j++
	}
	if j < len(src) && src[j] == '$' {
		return src[i+1 : j], true
	}
	return "", false
}

// consumeDollarQuoted consumes a dollar-quoted string starting at the
// opening tag (s.pos at first '$'), given the tag text (without $ signs).
func (s *scanner) consumeDollarQuoted(tag string) error {
	opener := "$" + tag + "$"
	src := s.src
	start := s.pos
	i := s.pos + len(opener)
	idx := strings.Index(src[i:], opener)
	if idx < 0 {
		return fmt.Errorf("unterminated dollar-quoted string starting at offset %d", start)
	}
	s.pos = i + idx + len(opener)
	return nil
}

// unquoteIdent strips the surrounding delimiters and un-escapes doubled
// delimiters within a quoted identifier's raw text. Assumes a single-byte
// opening delimiter (stripped positionally) and the given closing
// delimiter (un-escaped where doubled).
func unquoteIdent(raw string, close byte) string {
	if len(raw) < 2 {
		return raw
	}
	inner := raw[1 : len(raw)-1]
	doubled := string([]byte{close, close})
	single := string([]byte{close})
	return strings.ReplaceAll(inner, doubled, single)
}
