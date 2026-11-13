package host

import (
	"encoding/binary"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// cfHDrop is CF_HDROP. SDL's Windows clipboard writes only text and images, so this goes to Win32 directly.
	cfHDrop = 15
	// gmemMoveable is GMEM_MOVEABLE, which SetClipboardData requires.
	gmemMoveable = 0x0002
	// dropFilesHeaderSize is sizeof(DROPFILES): pFiles, pt.x, pt.y, fNC and fWide.
	dropFilesHeaderSize = 20
	// dropFilesWideOffset is where fWide sits in DROPFILES.
	dropFilesWideOffset = 16
	utf16UnitBytes      = 2
)

var (
	user32             = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard  = user32.NewProc("OpenClipboard")
	procCloseClipboard = user32.NewProc("CloseClipboard")
	procEmptyClipboard = user32.NewProc("EmptyClipboard")
	procSetClipboard   = user32.NewProc("SetClipboardData")

	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGlobalAlloc = kernel32.NewProc("GlobalAlloc")
	procGlobalLock  = kernel32.NewProc("GlobalLock")
	procGlobalUnl   = kernel32.NewProc("GlobalUnlock")
	procGlobalFree  = kernel32.NewProc("GlobalFree")
	procMoveMemory  = kernel32.NewProc("RtlMoveMemory")
)

// CopyFileToClipboard puts path on the clipboard as a real file, as Ctrl+C in Explorer does, and reports success.
func (Desktop) CopyFileToClipboard(window *sdl.Window, path string) bool {
	if path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	drop, err := dropFiles(abs)
	if err != nil {
		return false
	}
	// A NULL owner would make SetClipboardData fail after EmptyClipboard.
	owner := window.Win32Handle()
	if owner == nil {
		return false
	}
	if r, _, _ := procOpenClipboard.Call(uintptr(owner)); r == 0 {
		return false // another program holds the clipboard
	}
	defer procCloseClipboard.Call()
	if r, _, _ := procEmptyClipboard.Call(); r == 0 {
		return false
	}
	return setClipboardDrop(drop)
}

// setClipboardDrop hands a movable copy of drop to the open clipboard, which owns it from then on.
func setClipboardDrop(drop []byte) bool {
	mem, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(drop)))
	if mem == 0 {
		return false
	}
	dst, _, _ := procGlobalLock.Call(mem)
	if dst == 0 {
		procGlobalFree.Call(mem)
		return false
	}
	procMoveMemory.Call(dst, uintptr(unsafe.Pointer(&drop[0])), uintptr(len(drop)))
	procGlobalUnl.Call(mem)
	if r, _, _ := procSetClipboard.Call(cfHDrop, mem); r == 0 {
		procGlobalFree.Call(mem)
		return false
	}
	return true
}

// dropFiles builds a DROPFILES block for one path: the header, then UTF-16 ending in two NUL characters.
func dropFiles(path string) ([]byte, error) {
	wide, err := syscall.UTF16FromString(path) // ends in the path's NUL
	if err != nil {
		return nil, err
	}
	wide = append(wide, 0) // the list's NUL
	block := make([]byte, dropFilesHeaderSize, dropFilesHeaderSize+len(wide)*utf16UnitBytes)
	binary.LittleEndian.PutUint32(block, dropFilesHeaderSize)
	binary.LittleEndian.PutUint32(block[dropFilesWideOffset:], 1)
	for _, unit := range wide {
		block = binary.LittleEndian.AppendUint16(block, unit)
	}
	return block, nil
}
