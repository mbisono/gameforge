package main

import (
	"runtime"

	. "modernc.org/tk9.0"
)

// appVersion is the version of the app: 1.x was nspmerge (Switch only),
// 2.0.0 the first GameForge, with the disc image tabs, 2.1.0 added LPC to
// the FLAC of CD audio in CHD, 2.2.0 the highest Zstandard level and a
// block size option for RVZ, 2.2.1 moved the game database state from the
// log to the Switch tab, 2.3.0 added renaming Switch files, 2.3.1 made the
// log easier to read (file names only, a blank line between files) and
// added the conversion time to the file status, 2.4.0 added the 7z tab.
const appVersion = "2.4.0"

// showAbout shows the version, what the app does and what it builds on.
func (u *ui) showAbout() {
	MessageBox(Parent(App), Icon("info"), Title(T("about.title")),
		Msg(T("app.title")+" "+appVersion),
		Detail(T("about.text", runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)))
}
