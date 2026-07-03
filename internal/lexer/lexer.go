// Package lexer provides a dialect-aware SQL tokenizer and statement
// splitter. It does not attempt to fully parse SQL; it only understands
// enough syntax (strings, comments, quoted identifiers, dollar-quoting) to
// correctly find statement boundaries and produce a token stream that the
// parse package can scan for keywords and identifiers.
package lexer

// Dialect selects the SQL dialect-specific lexing rules.
type Dialect int

const (
	Postgres Dialect = iota
	MySQL
	SQLite
)

// TokenKind classifies a Token.
type TokenKind int

const (
	// TokenWord is an unquoted identifier or keyword.
	TokenWord TokenKind = iota
	// TokenQuotedIdent is a quoted identifier, e.g. "foo", `foo`, [foo].
	TokenQuotedIdent
	// TokenString is a string literal (single-quoted or dollar-quoted).
	TokenString
	// TokenPunct is any other punctuation/operator character(s), e.g. "(", ",", ".", ";".
	TokenPunct
	// TokenComment is a line or block comment.
	TokenComment
)

// Token is a lexical token with its source position (byte offset,
// relative to the start of its statement's Text — i.e. add
// Statement.Start to get the offset into the original source passed to
// Split) and, for identifier-like tokens, the text with surrounding
// quotes stripped (Value) versus the raw source text (Raw).
type Token struct {
	Kind  TokenKind
	Raw   string
	Value string
	Start int
	End   int
}

// Statement is one top-level SQL statement extracted from a file.
type Statement struct {
	// Text is the statement's own text (excluding attached leading
	// comments), trimmed of leading/trailing whitespace. It does not
	// include the trailing semicolon.
	Text string
	// Start is the byte offset of Text[0] in the source text passed to
	// Split. End is the byte offset just past the last byte of Text
	// (i.e. Start+len(Text)), both relative to that same source text.
	Start int
	End   int
	// LeadingComments holds comment text (verbatim, in source order)
	// that immediately precedes the statement with no blank statement
	// in between. Used to look for directives.
	LeadingComments []string
	// LeadingCommentStarts holds the byte offset (relative to the
	// source text passed to Split, like Start/End above) of the first
	// byte of each entry in LeadingComments, in the same order.
	LeadingCommentStarts []int
	// Tokens is the token stream for Text (comments and whitespace
	// excluded), used by the parse package to find keywords. Token
	// Start/End are relative to Text (add Statement.Start to get an
	// offset into the original source).
	Tokens []Token
}
