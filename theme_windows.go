//go:build windows

package main

import (
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32     = syscall.NewLazyDLL("advapi32.dll")
	dwmapi       = syscall.NewLazyDLL("dwmapi.dll")
	user32       = syscall.NewLazyDLL("user32.dll")
	regGetValue  = advapi32.NewProc("RegGetValueW")
	dwmSetAttr   = dwmapi.NewProc("DwmSetWindowAttribute")
	setWindowPos = user32.NewProc("SetWindowPos")
)

// systemDark reports whether Windows is set to dark mode for apps.
func systemDark() bool {
	const hkcu, rrfDword = 0x80000001, 0x00000010
	key, _ := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	name, _ := syscall.UTF16PtrFromString("AppsUseLightTheme")
	var v, size uint32 = 1, 4
	r, _, _ := regGetValue.Call(hkcu, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(name)), rrfDword,
		0, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&size)))
	return r == 0 && v == 0
}

// setTitleBarDark draws the main window's title bar dark or light.
func setTitleBarDark(dark bool) {
	hwnd, err := strconv.ParseUint(strings.TrimPrefix(tcl("wm", "frame", "."), "0x"), 16, 64)
	if err != nil || hwnd == 0 {
		return
	}
	const immersiveDarkMode = 20
	var on uint32
	if dark {
		on = 1
	}
	dwmSetAttr.Call(uintptr(hwnd), immersiveDarkMode, uintptr(unsafe.Pointer(&on)), 4)
	// Redraw the frame now, not at the next activation.
	const swpFrameChanged = 0x0001 | 0x0002 | 0x0004 | 0x0020 // no size, move, z-order
	setWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, swpFrameChanged)
}
