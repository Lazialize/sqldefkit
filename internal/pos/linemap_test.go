package pos

import "testing"

func TestLineMap_Basic(t *testing.T) {
	src := "abc\ndef\nghi"
	m := NewLineMap(src)
	cases := []struct {
		offset   int
		wantLine int
		wantCol  int
	}{
		{0, 1, 1},
		{1, 1, 2},
		{3, 1, 4}, // the '\n' itself
		{4, 2, 1}, // 'd'
		{5, 2, 2},
		{8, 3, 1}, // 'g'
		{10, 3, 3},
	}
	for _, c := range cases {
		p := m.Pos("f.sql", c.offset)
		if p.Line != c.wantLine || p.Col != c.wantCol {
			t.Errorf("offset=%d: got line=%d col=%d, want line=%d col=%d", c.offset, p.Line, p.Col, c.wantLine, c.wantCol)
		}
		if p.File != "f.sql" || p.Offset != c.offset {
			t.Errorf("offset=%d: file/offset mismatch: %+v", c.offset, p)
		}
	}
}

func TestLineMap_EmptySource(t *testing.T) {
	m := NewLineMap("")
	p := m.Pos("f.sql", 0)
	if p.Line != 1 || p.Col != 1 {
		t.Errorf("got %+v, want line=1 col=1", p)
	}
}
