package lsp

import "testing"

func TestByteColToUTF16_ASCII(t *testing.T) {
	line := "CREATE TABLE users ("
	// byte col 8 (1-based) is the 'T' of TABLE, offset 7 -> UTF-16 char 7.
	got := byteColToUTF16(line, 8)
	if got != 7 {
		t.Errorf("byteColToUTF16 = %d, want 7", got)
	}
}

func TestByteColToUTF16_Japanese(t *testing.T) {
	// "あ" is a 3-byte UTF-8 rune, 1 UTF-16 code unit.
	line := "-- あコメント\nCREATE TABLE t"
	line = "-- あ users"
	// bytes: '-','-',' ','あ'(3 bytes),' ','u','s','e','r','s'
	// byte col for 'u' (1-based): 1,2,3 = "-- ", then "あ" occupies bytes 4-6,
	// then space is byte 7, 'u' starts at byte 8 (1-based col 8).
	gotBeforeRune := byteColToUTF16(line, 4) // right before あ
	if gotBeforeRune != 3 {
		t.Errorf("byteColToUTF16 before rune = %d, want 3", gotBeforeRune)
	}
	gotAfterRune := byteColToUTF16(line, 8) // 'u' of users, after あ (3 bytes -> 1 utf16 unit)
	if gotAfterRune != 5 {
		t.Errorf("byteColToUTF16 after rune (col 8) = %d, want 5", gotAfterRune)
	}
	// Byte offset differs from UTF-16 offset: byte col 8 means byte offset 7,
	// but UTF-16 offset is 5 -- confirms they diverge for multibyte runes.
	if gotAfterRune == 8-1 {
		t.Errorf("expected UTF-16 offset to differ from byte offset for multibyte content")
	}
}

func TestByteColToUTF16_Emoji(t *testing.T) {
	// An emoji outside the BMP is 4 bytes in UTF-8 and 2 code units (a
	// surrogate pair) in UTF-16.
	line := "-- 🎉 party"
	// bytes: '-','-',' ' (3 bytes, cols 1-3), then 🎉 is 4 bytes (cols 4-7),
	// then ' ' col 8, 'p' col 9.
	beforeEmoji := byteColToUTF16(line, 4)
	if beforeEmoji != 3 {
		t.Errorf("byteColToUTF16 before emoji = %d, want 3", beforeEmoji)
	}
	afterEmoji := byteColToUTF16(line, 8) // the space right after emoji
	// 3 ascii units + 2 surrogate units = 5
	if afterEmoji != 5 {
		t.Errorf("byteColToUTF16 after emoji = %d, want 5", afterEmoji)
	}
}

func TestUTF16ColToByteCol_RoundTrip_ASCII(t *testing.T) {
	line := "CREATE TABLE users ("
	for byteCol := 1; byteCol <= len(line)+1; byteCol++ {
		u := byteColToUTF16(line, byteCol)
		gotByteCol := utf16ColToByteCol(line, u)
		if gotByteCol != byteCol {
			t.Errorf("round trip byteCol=%d -> utf16=%d -> byteCol=%d", byteCol, u, gotByteCol)
		}
	}
}

func TestUTF16ColToByteCol_Japanese(t *testing.T) {
	line := "-- あ users"
	// UTF-16 char 3 is right before あ; byte col should be 4.
	got := utf16ColToByteCol(line, 3)
	if got != 4 {
		t.Errorf("utf16ColToByteCol(3) = %d, want 4", got)
	}
	// UTF-16 char 4 is right after あ (1 utf16 unit); byte col should be 7.
	got = utf16ColToByteCol(line, 4)
	if got != 7 {
		t.Errorf("utf16ColToByteCol(4) = %d, want 7", got)
	}
}

func TestUTF16ColToByteCol_Emoji(t *testing.T) {
	line := "-- 🎉 party"
	// utf16 char 3 is right before the emoji.
	got := utf16ColToByteCol(line, 3)
	if got != 4 {
		t.Errorf("utf16ColToByteCol(3) = %d, want 4", got)
	}
	// utf16 char 5 is right after the emoji (2 units consumed).
	got = utf16ColToByteCol(line, 5)
	if got != 8 {
		t.Errorf("utf16ColToByteCol(5) = %d, want 8", got)
	}
}

func TestUTF16ColToByteCol_ClampsPastEndOfLine(t *testing.T) {
	line := "abc"
	got := utf16ColToByteCol(line, 100)
	if got != len(line)+1 {
		t.Errorf("utf16ColToByteCol past end = %d, want %d", got, len(line)+1)
	}
}

func TestByteColToUTF16_ClampsPastEndOfLine(t *testing.T) {
	line := "abc"
	got := byteColToUTF16(line, 100)
	if got != len(line) {
		t.Errorf("byteColToUTF16 past end = %d, want %d", got, len(line))
	}
}

func TestByteColToUTF16_EmptyLine(t *testing.T) {
	if got := byteColToUTF16("", 1); got != 0 {
		t.Errorf("byteColToUTF16 on empty line = %d, want 0", got)
	}
}
