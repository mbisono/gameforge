//go:build windows

package main

import (
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	. "modernc.org/tk9.0"
)

// Tk 9.0 has no drag and drop, so the main window accepts dropped files
// through the Win32 API: WM_DROPFILES is caught by subclassing the window.

const wmDropFiles = 0x0233

var (
	shell32      = syscall.NewLazyDLL("shell32.dll")
	comctl32     = syscall.NewLazyDLL("comctl32.dll")
	dragAccept   = shell32.NewProc("DragAcceptFiles")
	dragQuery    = shell32.NewProc("DragQueryFileW")
	dragFinish   = shell32.NewProc("DragFinish")
	setSubclass  = comctl32.NewProc("SetWindowSubclass")
	defSubclass  = comctl32.NewProc("DefSubclassProc")
	dropOnce     sync.Once
	dropCallback uintptr
	onDrop       func([]string)
)

// enableDrop calls handler, on the GUI thread, with the paths of the files
// and folders dropped onto the main window.
func enableDrop(handler func([]string)) {
	onDrop = handler
	// "wm frame" is the decorated top-level window that Windows drops onto;
	// drops on child widgets go up to it.
	hwnd, err := strconv.ParseUint(strings.TrimPrefix(tcl("wm", "frame", "."), "0x"), 16, 64)
	if err != nil || hwnd == 0 {
		return
	}
	dropOnce.Do(func() { dropCallback = syscall.NewCallback(subclassProc) })
	setSubclass.Call(uintptr(hwnd), dropCallback, 1, 0)
	dragAccept.Call(uintptr(hwnd), 1)
}

func subclassProc(hwnd, msg, wparam, lparam, id, data uintptr) uintptr {
	if msg != wmDropFiles {
		r, _, _ := defSubclass.Call(hwnd, msg, wparam, lparam)
		return r
	}
	n, _, _ := dragQuery.Call(wparam, 0xFFFFFFFF, 0, 0)
	var paths []string
	for i := uintptr(0); i < n; i++ {
		size, _, _ := dragQuery.Call(wparam, i, 0, 0)
		buf := make([]uint16, size+1)
		dragQuery.Call(wparam, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	dragFinish.Call(wparam)
	// Handled after the window procedure returns, not inside it.
	if len(paths) > 0 {
		go PostEvent(func() { onDrop(paths) }, false)
	}
	return 0
}
