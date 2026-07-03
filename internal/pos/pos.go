// Package pos defines a small shared source-position type used by the
// lexer, parse, and bundle packages so that a future LSP server can map
// symbols and diagnostics back to exact locations in the original source
// files.
package pos

import "fmt"

// Position identifies a single point in a source file.
//
// Offset is the byte offset into the file's content as lexed (see note
// below about CRLF normalization). Line and Col are both 1-based; Col is
// a byte offset within the line (column 1 is the first byte of the
// line), not a rune or UTF-16 code-unit count. Converting to UTF-16
// code-unit columns (as the Language Server Protocol requires) is left
// to a future LSP layer, which can re-derive it from Offset/the file
// text; byte offsets make that conversion possible without re-lexing.
//
// Note: bundle normalizes CRLF line endings to LF before lexing, and
// positions are computed against that normalized text. For files that
// already use LF line endings (the common case, including on the wire
// in most editors and in git-checked-out repos with core.autocrlf off)
// this matches the original file exactly. For CRLF source files, byte
// offsets/line numbers are computed as if the '\r' bytes were absent;
// this is a known, documented approximation.
type Position struct {
	File   string
	Offset int
	Line   int
	Col    int
}

// String renders the position as "file:line:col".
func (p Position) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// IsValid reports whether p carries real position information (as
// opposed to a zero-value placeholder).
func (p Position) IsValid() bool {
	return p.Line > 0
}
