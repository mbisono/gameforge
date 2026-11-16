package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"

	. "modernc.org/tk9.0"
)

// appVersion is the version of the app: 1.x was nspmerge (Switch only),
// 2.0.0 the first GameForge, with the disc image tabs, 2.1.0 added LPC to
// the FLAC of CD audio in CHD, 2.2.0 the highest Zstandard level and a
// block size option for RVZ, 2.2.1 moved the game database state from the
// log to the Switch tab, 2.3.0 added renaming Switch files, 2.3.1 made the
// log easier to read (file names only, a blank line between files) and
// added the conversion time to the file status, 2.4.0 added the 7z tab,
// 2.5.0 shows what disc images and CHDs say about their games, 2.6.0
// renames disc images with their Redump names, 2.7.0 shows the icons of
// PSP games, has a menu to update the Redump database, calls the CHD tab
// "Discs", hides hidden files in Tk's file dialogs, evens out the tabs of
// the clam theme and keeps one output folder per tab, 2.7.1 renames the
// RetroArch saves, states and thumbnails of renamed disc images, keeps
// Tk's message boxes from being resized and has an About window with a
// link to the project's page, 2.8.0 renames ROMs with their No-Intro names,
// 2.9.0 checks ROMs against RetroAchievements and keeps the downloaded
// databases in a cache folder.
const appVersion = "2.9.0"

// repoURL is the project's page, with the source code and the releases.
const repoURL = "https://github.com/tschertel/gameforge"

// aboutWin is the open About window, if any.
var aboutWin *ToplevelWidget

// showAbout shows the version, what the app does, what it builds on and a
// link to the project's page, in a window of fixed size over the main one
// (a message box cannot hold a link).
func (u *ui) showAbout() {
	if aboutWin != nil && tcl("winfo", "exists", aboutWin.String()) == "1" {
		tcl("raise", aboutWin.String())
		return
	}
	top := Toplevel()
	aboutWin = top
	path := top.String()
	tcl("wm", "withdraw", path)
	top.WmTitle(T("about.title"))
	tcl("wm", "transient", path, ".")
	tcl("wm", "resizable", path, "0", "0")
	tcl(path, "configure", "-background", tcl(".", "cget", "-background"))

	f := top.TFrame(Padding("5m 4m"))
	Grid(f, Row(0), Column(0), Sticky("news"))
	icon := f.TLabel()
	tcl("catch", icon.String()+" configure -image ::tk::icons::information")
	Grid(icon, Row(0), Column(0), Rowspan(3), Sticky("n"), Padx("0 4m"))
	tcl("catch", "font create GameForgeTitle {*}[font configure TkDefaultFont] -weight bold")
	Grid(f.TLabel(Txt(T("app.title")+" "+appVersion), Font("GameForgeTitle")), Row(0), Column(1), Sticky("w"))
	text := T("about.text", runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
	Grid(f.TLabel(Txt(text), Wraplength("110m"), Justify("left")), Row(1), Column(1), Sticky("w"), Pady("3m 0"))

	tcl("catch", "font create GameForgeLink {*}[font configure TkDefaultFont] -underline 1")
	color := "#0b5cad"
	if darkTheme {
		color = "#6cb6ff"
	}
	link := f.TLabel(Txt(repoURL), Foreground(color), Font("GameForgeLink"), Cursor("hand2"))
	Bind(link, "<Button-1>", Command(func() { openURL(repoURL) }))
	Grid(link, Row(2), Column(1), Sticky("w"), Pady("3m 0"))

	closeWin := func() {
		tcl("grab", "release", path)
		Destroy(top)
		aboutWin = nil
	}
	ok := f.TButton(Txt("OK"), Default("active"), Command(closeWin))
	Grid(ok, Row(3), Column(0), Columnspan(2), Pady("5m 0"))
	Bind(top, "<Escape>", Command(closeWin))
	Bind(top, "<Return>", Command(closeWin))
	WmProtocol(top.Window, WM_DELETE_WINDOW, Command(closeWin))

	// Centered over the main window, then shown and kept in front of it.
	tcl("update", "idletasks")
	num := func(words ...string) int { n, _ := strconv.Atoi(tcl(words...)); return n }
	x := num("winfo", "rootx", ".") + (num("winfo", "width", ".")-num("winfo", "reqwidth", path))/2
	y := num("winfo", "rooty", ".") + (num("winfo", "height", ".")-num("winfo", "reqheight", path))/3
	tcl("wm", "geometry", path, fmt.Sprintf("+%d+%d", max(x, 0), max(y, 0)))
	tcl("wm", "deiconify", path)
	tcl("focus", ok.String())
	tcl("catch", "grab set "+path)
}

// openURL opens a web page in the user's browser.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
