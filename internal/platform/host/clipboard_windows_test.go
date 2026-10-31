package host

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func TestDropFilesLayout(t *testing.T) {
	const path = `C:\exports\ゲーム.gif`
	block, err := dropFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(block); got != dropFilesHeaderSize {
		t.Errorf("pFiles = %d, want %d", got, dropFilesHeaderSize)
	}
	if got := binary.LittleEndian.Uint32(block[dropFilesWideOffset:]); got != 1 {
		t.Errorf("fWide = %d, want 1", got)
	}
	var want bytes.Buffer
	for _, unit := range utf16.Encode([]rune(path + "\x00\x00")) {
		_ = binary.Write(&want, binary.LittleEndian, unit)
	}
	if got := block[dropFilesHeaderSize:]; !bytes.Equal(got, want.Bytes()) {
		t.Errorf("path bytes = %x, want %x", got, want.Bytes())
	}
}

func TestDropFilesRefusesANULInThePath(t *testing.T) {
	if _, err := dropFiles("a\x00b"); err == nil {
		t.Error("a NUL would have cut the path short")
	}
}

func TestCopyFileToClipboardRefusesWhatItCannotCopy(t *testing.T) {
	if (Desktop{}).CopyFileToClipboard(nil, "") {
		t.Error("an empty path was accepted")
	}
	if (Desktop{}).CopyFileToClipboard(nil, "a\x00b") {
		t.Error("an invalid path was accepted")
	}
}
