package pos

import "sort"

// LineMap converts byte offsets within a fixed source text into 1-based
// (Line, Col) pairs, where Col is a byte offset within the line (see
// Position doc comment). Construct with NewLineMap once per file text and
// reuse it for every offset in that text.
type LineMap struct {
	// lineStarts[i] is the byte offset of the first byte of line i+1
	// (0-based slice, 1-based line numbers).
	lineStarts []int
}

// NewLineMap builds a LineMap for src.
func NewLineMap(src string) *LineMap {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &LineMap{lineStarts: starts}
}

// Pos returns the Position for the given byte offset, with File set to
// file. Offsets outside [0, len(src)] are clamped to the nearest valid
// line so callers never panic on slightly-off-by-construction input
// (e.g. a synthetic line map covering less text than the offset was
// originally computed against).
func (m *LineMap) Pos(file string, offset int) Position {
	if offset < 0 {
		offset = 0
	}
	// Find the last line start <= offset.
	i := sort.Search(len(m.lineStarts), func(i int) bool {
		return m.lineStarts[i] > offset
	})
	if i < 1 {
		i = 1
	}
	line := i // lineStarts[i-1] <= offset < lineStarts[i]; line is 1-based already since i counts from 1
	lineStart := m.lineStarts[line-1]
	return Position{
		File:   file,
		Offset: offset,
		Line:   line,
		Col:    offset - lineStart + 1,
	}
}
