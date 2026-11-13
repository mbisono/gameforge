//go:build !windows

package main

import "strconv"

// systemDark is false: there is no common way to ask X11 desktops for their
// color scheme, so "auto" means light here.
func systemDark() bool { return false }

// setTitleBarDark does nothing: the window manager draws the title bar.
func setTitleBarDark(bool) {}

// onScreen checks the point against the screen; window managers usually
// move windows back into view themselves.
func onScreen(x, y int) bool {
	w, _ := strconv.Atoi(tcl("winfo", "vrootwidth", "."))
	h, _ := strconv.Atoi(tcl("winfo", "vrootheight", "."))
	return x >= 0 && y >= 0 && (w == 0 || x < w) && (h == 0 || y < h)
}
