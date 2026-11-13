//go:build !windows

package main

// enableDrop does nothing: Tk 9.0 has no drag and drop, and on X11 it would
// need the XDND protocol. Files are added with the buttons instead.
func enableDrop(func([]string)) {}
