package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/i18n"
	"gameforge/internal/nointro"
)

// Neo Geo games as .neo files (one file per game, as the Geolith core and
// the NeoSD cartridge use them), loose or in a ZIP or 7z, are checked
// against libretro's Neo Geo list by their CRC. The list's names come from
// MAME and often carry the cartridge's product code: NGH for the home
// console (AES), NGM for the arcade board (MVS). A game with both codes
// has the same ROMs on both; most games have no code at all.

// neoKind is the version of a game the list's name tells.
type neoKind struct {
	aes, mvs bool   // NGH, NGM code in the name
	number   string // the product number, such as "2420"; "" if none
}

var neoCode = regexp.MustCompile(`\b(NG[HM])-(\d+)`)

// neoKindOf reads the product codes from a name of the list.
func neoKindOf(name string) neoKind {
	var k neoKind
	for _, m := range neoCode.FindAllStringSubmatch(name, -1) {
		if m[1] == "NGH" {
			k.aes = true
		} else {
			k.mvs = true
		}
		if k.number == "" {
			k.number = strings.TrimLeft(m[2], "0")
		}
	}
	return k
}

// neoFile is a checked file and what the list says it is.
type neoFile struct {
	item *convItem
	name string // the game's name in the list; "" if its CRC is unknown
	kind neoKind
}

// neoMark tells which version of a game checkNeo then checks for
// deleting, when the folder also has the other version.
type neoMark int

const (
	neoMarkNone neoMark = iota
	neoMarkMVS          // arcade versions of games that have a home version
	neoMarkAES          // home versions of games that have an arcade version
)

// checkNeo checks the checked .neo files (loose or in a ZIP or 7z) against
// libretro's Neo Geo list, downloading it first if the user agrees, then
// checks the versions mark asks for.
func (t *convTab) checkNeo(u *ui, mark neoMark) {
	if u.busy {
		return
	}
	var items []*convItem
	for _, it := range t.items {
		if markable(it) && hasExt(it.path, []string{".neo", ".zip", ".7z"}) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("neo.empty")))
		return
	}
	items, ok := t.keepChecked(items)
	if !ok {
		return
	}
	var use []romFile
	for _, it := range items {
		if r := scanRom(it); r.err == nil && r.files == 1 && r.ext() == ".neo" {
			use = append(use, r)
		}
	}
	if len(use) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("neo.empty")))
		return
	}
	if !u.noIntroDB().Has(neoSystem) {
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("rrename.ask_db", neoSystem)), Detail(T("dupes.ask_db.detail", nointro.Source)))
		if answer != "yes" {
			return
		}
		u.separate()
		u.downloadNoIntro([]string{neoSystem}, func() {
			if u.noIntroDB().Has(neoSystem) {
				u.runNeo(use, mark)
			}
		})
		return
	}
	u.separate()
	u.runNeo(use, mark)
}

// runNeo identifies the files in the background (loose .neo files are read
// whole for their CRC) and shows what each is.
func (u *ui) runNeo(roms []romFile, mark neoMark) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	db := u.noIntroDB()
	u.logf("neo.start", len(roms), db.Lists[neoSystem].Version)
	go func() {
		defer cancel()
		var files []neoFile
		known := 0
		for i := range roms {
			if ctx.Err() != nil {
				break
			}
			r := &roms[i]
			base := filepath.Base(r.item.path)
			u.setStatus(T("drename.status", base))
			m, skip := romGame(ctx, u, db, r)
			if ctx.Err() != nil {
				break
			}
			f := neoFile{item: r.item}
			if m.Name != "" && m.System == neoSystem {
				f.name, f.kind = m.Name, neoKindOf(m.Name)
				known++
			}
			if f.name == "" && r.haveCRC {
				skip = i18n.M("neo.unknown", base, fmt.Sprintf("%08X", r.crc))
			}
			status, line := neoResultLine(f, skip)
			u.setItemStatus(r.item, status)
			u.println(line)
			files = append(files, f)
		}
		u.setProgress(0)
		u.setStatus("")
		cancelled := ctx.Err() != nil
		if !cancelled {
			u.logf("neo.summary", known, len(roms))
		}
		onGUI(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			u.flush()
			if !cancelled && mark != neoMarkNone && len(files) > 0 {
				markNeo(u, files, mark)
			}
		})
	}()
}

// neoResultLine is the status of a checked file and its log line.
func neoResultLine(f neoFile, skip fmt.Stringer) (fmt.Stringer, fmt.Stringer) {
	base := filepath.Base(f.item.path)
	if f.name == "" {
		if skip == nil {
			skip = i18n.M("neo.unknown", base, "?")
		}
		return i18n.M("neo.st_unknown"), skip
	}
	key := "neo.kind_none"
	switch k := f.kind; {
	case k.aes && k.mvs:
		key = "neo.kind_both"
	case k.aes:
		key = "neo.kind_aes"
	case k.mvs:
		key = "neo.kind_mvs"
	}
	return i18n.M("neo.st_ok", i18n.M(key)), i18n.M("neo.ok", base, f.name, i18n.M(key))
}

// neoToMark returns the files to check for deleting: of each game (by its
// product number) of which the files include both versions, the ones of
// the version mark names. Files with both codes, which are the same on
// both systems, are never among them.
func neoToMark(files []neoFile, mark neoMark) []neoFile {
	hasAES, hasMVS := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		if f.kind.number == "" {
			continue
		}
		hasAES[f.kind.number] = hasAES[f.kind.number] || f.kind.aes
		hasMVS[f.kind.number] = hasMVS[f.kind.number] || f.kind.mvs
	}
	var out []neoFile
	for _, f := range files {
		k := f.kind
		switch {
		case k.number == "" || k.aes && k.mvs:
		case mark == neoMarkMVS && k.mvs && hasAES[k.number],
			mark == neoMarkAES && k.aes && hasMVS[k.number]:
			out = append(out, f)
		}
	}
	return out
}

// markNeo checks only the files of the version to delete, for Delete
// checked….
func markNeo(u *ui, files []neoFile, mark neoMark) {
	marked := neoToMark(files, mark)
	if len(marked) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("neo.mark_none")))
		return
	}
	t := files[0].item.tab
	for _, it := range t.items {
		t.checked[it.path] = false
	}
	for _, f := range marked {
		t.checked[f.item.path] = true
		f.item.status = i18n.M("neo.st_marked", i18n.M(map[neoMark]string{neoMarkMVS: "neo.kind_mvs", neoMarkAES: "neo.kind_aes"}[mark]))
	}
	u.logf("neo.marked", len(marked))
	t.refresh(u)
}
