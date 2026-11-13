//go:build windows && (amd64 || arm64)

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// shFileOp is SHFILEOPSTRUCTW with the natural alignment of 64-bit
// Windows (32-bit Windows packs it, hence the build constraint).
type shFileOp struct {
	hwnd    uintptr
	op      uint32
	from    *uint16
	to      *uint16
	flags   uint16
	aborted int32
	maps    uintptr
	title   *uint16
}

var shFileOperation = syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")

// removeOriginal sends a file to the Recycle Bin. Where the drive has no
// Recycle Bin (some USB and network drives), Windows deletes it.
func removeOriginal(path string) error {
	from, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0) // the list ends with an empty name
	const (
		foDelete          = 3
		fofSilent         = 0x0004
		fofNoConfirm      = 0x0010
		fofAllowUndo      = 0x0040
		fofNoErrorUI      = 0x0400
		fofNoConfirmMkdir = 0x0200
	)
	op := shFileOp{op: foDelete, from: &from[0], flags: fofSilent | fofNoConfirm | fofAllowUndo | fofNoErrorUI | fofNoConfirmMkdir}
	r, _, _ := shFileOperation.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("SHFileOperation error %#x", r)
	}
	if op.aborted != 0 {
		return errors.New("cancelled")
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("the file is still there")
	}
	return nil
}

// usesTrash says whether removed files go to the Recycle Bin.
const usesTrash = true
