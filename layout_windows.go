//go:build windows

package main

import "unsafe"

var monitorFromPoint = user32.NewProc("MonitorFromPoint")

// onScreen reports whether the point is on a connected monitor, so that a
// window saved on a monitor that is gone is not restored out of sight.
func onScreen(x, y int) bool {
	const defaultToNull = 0
	var r uintptr
	if unsafe.Sizeof(uintptr(0)) == 8 { // POINT is passed in one register
		r, _, _ = monitorFromPoint.Call(uintptr(uint32(int32(x)))|uintptr(uint32(int32(y)))<<32, defaultToNull)
	} else {
		r, _, _ = monitorFromPoint.Call(uintptr(int32(x)), uintptr(int32(y)), defaultToNull)
	}
	return r != 0
}
