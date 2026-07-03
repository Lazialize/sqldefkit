package lsp

import (
	"unicode/utf16"
	"unicode/utf8"
)

// byteColToUTF16 converts a 1-based byte column (as stored in
// pos.Position.Col) within lineText to a 0-based UTF-16 code-unit
// character offset, as required by LSP Position.character.
//
// lineText is the content of the line only (no trailing newline). byteCol
// is clamped into [1, len(lineText)+1] so a column pointing at
// end-of-line (one past the last byte, as produced when scanning to the
// end of an identifier at EOL) is handled without panicking.
func byteColToUTF16(lineText string, byteCol int) int {
	byteOffset := byteCol - 1
	if byteOffset < 0 {
		byteOffset = 0
	}
	if byteOffset > len(lineText) {
		byteOffset = len(lineText)
	}

	units := 0
	i := 0
	for i < byteOffset {
		r, size := utf8.DecodeRuneInString(lineText[i:])
		units += utf16RuneLen(r)
		i += size
	}
	return units
}

// utf16RuneLen returns how many UTF-16 code units r encodes to: 1 for
// runes in the basic multilingual plane, 2 for supplementary-plane runes
// (surrogate pairs, e.g. most emoji). utf16.RuneLen returns -1 for
// utf8.RuneError-class invalid runes; treat those as a single unit (as a
// lone replacement character would encode).
func utf16RuneLen(r rune) int {
	if n := utf16.RuneLen(r); n > 0 {
		return n
	}
	return 1
}

// utf16ColToByteCol converts a 0-based UTF-16 character offset (as
// received in LSP Position.character) within lineText to a 1-based byte
// column matching pos.Position.Col's convention.
//
// If utf16Col lands beyond the end of the line (client points past the
// last character), the result is clamped to len(lineText)+1 (one past the
// last byte), matching LSP's tolerance for positions at end-of-line.
func utf16ColToByteCol(lineText string, utf16Col int) int {
	if utf16Col < 0 {
		utf16Col = 0
	}

	units := 0
	i := 0
	for i < len(lineText) {
		if units >= utf16Col {
			break
		}
		r, size := utf8.DecodeRuneInString(lineText[i:])
		units += utf16RuneLen(r)
		i += size
	}
	return i + 1
}
