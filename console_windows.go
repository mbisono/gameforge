//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// When the exe is built without -H windowsgui and started by double-click,
// Windows opens a console window just for it. Close that console, but keep
// it when the program was started from a terminal (which shares its console).
func init() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	var pids [2]uint32
	n, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n == 1 {
		kernel32.NewProc("FreeConsole").Call()
	}
}
