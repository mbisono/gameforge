package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/arcade"
	"gameforge/internal/datfile"
	"gameforge/internal/i18n"
	"gameforge/internal/nointro"
	"gameforge/internal/sevenzip"
)

// The ROMs tab checks arcade sets, the ZIPs and 7z files that emulators
// such as FBNeo load by their short names, against the emulator's list of
// sets: whether all the files are there (in the set, its parent or its
// BIOS) with the right CRCs. Nothing is changed or rebuilt.

// arcadeDB is the local copy of the lists of sets, loaded on first use.
var arcadeDB struct {
	db     *arcade.DB
	loaded bool
}

func (u *ui) arcadePath() string { return cachePath(u.cfg.Path(), arcade.FileName) }

func (u *ui) arcadeLists() *arcade.DB {
	if !arcadeDB.loaded {
		arcadeDB.loaded = true
		db, err := arcade.Load(u.arcadePath())
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			u.logf("arcade.load_error", err)
		}
		arcadeDB.db = db
	}
	return arcadeDB.db
}

// updateArcade downloads again the lists already there.
func (u *ui) updateArcade() {
	if u.busy {
		return
	}
	var ids []string
	for _, e := range arcade.Emulators() {
		if u.arcadeLists().Has(e.ID) {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("arcade.update_none")))
		return
	}
	u.separate()
	u.downloadArcade(ids, func() {})
}

// downloadArcade downloads the lists of the emulators in the background,
// then runs then on the GUI thread.
func (u *ui) downloadArcade(ids []string, then func()) {
	var names []string
	for _, id := range ids {
		if e, ok := arcade.EmulatorByID(id); ok {
			names = append(names, e.Name)
		}
	}
	u.logf("arcade.download_start", strings.Join(names, ", "))
	old := u.arcadeLists()
	u.runDownload(func(ctx context.Context, progress datfile.Progress) func() {
		db, err := arcade.Download(ctx, old, ids, progress)
		if err == nil {
			if serr := db.Save(u.arcadePath()); serr != nil {
				u.logf("arcade.save_error", serr)
			}
			for _, id := range ids {
				e, _ := arcade.EmulatorByID(id)
				if l := db.Lists[id]; l != nil {
					u.logf("arcade.loaded", e.Name, len(l.Sets), l.Version)
				}
			}
		} else if ctx.Err() == nil {
			u.logf("arcade.download_error", err)
		}
		return func() {
			if err == nil {
				arcadeDB.db = db
			}
		}
	}, then)
}

// isArchive reports whether a file is a ZIP or a 7z.
func isArchive(path string) bool { return hasExt(path, []string{".zip", ".7z"}) }

// checkArcade checks the checked ZIPs and 7z files of the ROMs tab as sets
// of an emulator, downloading its list first if the user agrees.
func (t *convTab) checkArcade(u *ui, id string) {
	if u.busy {
		return
	}
	emu, ok := arcade.EmulatorByID(id)
	if !ok {
		return
	}
	var items []*convItem
	for _, it := range t.items {
		if markable(it) && isArchive(it.path) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("arcade.empty")))
		return
	}
	items, ok = t.keepChecked(items)
	if !ok {
		return
	}
	if !u.arcadeLists().Has(id) {
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("arcade.ask_db", emu.Name)), Detail(T("arcade.ask_db.detail", emu.Size, emu.URL())))
		if answer != "yes" {
			return
		}
		u.separate()
		u.downloadArcade([]string{id}, func() {
			if u.arcadeLists().Has(id) {
				u.runArcade(emu, items)
			}
		})
		return
	}
	u.separate()
	u.runArcade(emu, items)
}

// archiveFiles lists the files inside a ZIP or 7z, with their sizes and
// CRCs, from its header.
func archiveFiles(path string) ([]arcade.File, error) {
	var out []arcade.File
	if strings.EqualFold(filepath.Ext(path), ".7z") {
		entries, err := sevenzip.List(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.Dir {
				out = append(out, arcade.File{Name: e.Name, Size: e.Size, CRC: e.CRC})
			}
		}
		return out, nil
	}
	zr, files, err := openZip(path)
	if err != nil {
		return nil, err
	}
	zr.Close()
	for _, f := range files {
		out = append(out, arcade.File{Name: f.Name, Size: int64(f.UncompressedSize64), CRC: f.CRC32})
	}
	return out, nil
}

// setArchive finds the archive of a set in a folder: name.zip or name.7z.
func setArchive(dir, name string) string {
	for _, ext := range []string{".zip", ".7z", ".ZIP", ".7Z"} {
		p := filepath.Join(dir, name+ext)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// runArcade checks the archives in the background and shows the results.
func (u *ui) runArcade(emu arcade.Emulator, items []*convItem) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	list := u.arcadeLists().Lists[emu.ID]
	u.logf("arcade.start", len(items), emu.Name, list.Version)
	neo := u.noIntroDB() // for .neo files, which are not arcade sets
	go func() {
		defer cancel()
		read := map[string][]arcade.File{} // other sets' archives, read once
		complete := 0
		for i, it := range items {
			if ctx.Err() != nil {
				break
			}
			base := filepath.Base(it.path)
			u.setProgress(float64(i) / float64(len(items)))
			u.setStatus(T("drename.status", base))
			files, err := archiveFiles(it.path)
			if err != nil {
				u.setItemStatus(it, i18n.M("arcade.st_error", emu.Name))
				u.println(i18n.M("drename.skip_error", base, err))
				continue
			}
			dir := filepath.Dir(it.path)
			other := func(set string) ([]arcade.File, bool) {
				p := setArchive(dir, set)
				if p == "" {
					return nil, false
				}
				if fs, ok := read[p]; ok {
					return fs, fs != nil
				}
				fs, err := archiveFiles(p)
				if err != nil {
					read[p] = nil
					return nil, false
				}
				read[p] = fs
				return fs, true
			}
			r := list.Audit(strings.TrimSuffix(base, filepath.Ext(base)), files, other)
			chds := missingChds(dir, r.Set, list)
			status, line := arcadeResult(emu, base, r, chds)
			if r.Set == nil && r.Like == nil {
				if st, l, ok := neoResult(emu, base, files, neo); ok {
					status, line = st, l
				}
			}
			if r.OK() && len(chds) == 0 {
				complete++
			}
			u.setItemStatus(it, status)
			u.println(line)
			if r.Set != nil && len(r.Bios) > 0 {
				u.logf("arcade.bios_note", base, strings.Join(r.Bios, ".zip, ")+".zip")
			}
			if r.Set != nil && len(r.Set.Chds) == 0 && r.Set.Disks > 0 {
				u.logf("arcade.disks", base, r.Set.Disks) // a list saved before the CHDs had names
			}
		}
		u.setProgress(0)
		u.setStatus("")
		cancelled := ctx.Err() != nil
		if !cancelled {
			u.logf("arcade.summary", complete, len(items), emu.Name)
		}
		onGUI(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			u.flush()
		})
	}()
}

// missingChds returns the CHDs of a set not in the folder, where the
// emulators look for them: name.chd in a folder named after the set (or
// after its parent, for a clone).
func missingChds(dir string, s *arcade.Set, list *arcade.List) []string {
	if s == nil {
		return nil
	}
	var missing []string
	for _, c := range s.Chds {
		found := false
		for p, seen := s, 0; p != nil && seen < 4 && !found; seen++ {
			found = fileExists(filepath.Join(dir, p.Name, c+".chd"))
			p = list.Sets[strings.ToLower(p.CloneOf)]
		}
		if !found {
			missing = append(missing, s.Name+"/"+c+".chd")
		}
	}
	return missing
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// joined shows several messages one after another, in the current language.
type joined []fmt.Stringer

func (j joined) String() string {
	var parts []string
	for _, s := range j {
		parts = append(parts, s.String())
	}
	return strings.Join(parts, ", ")
}

// neoResult tells, for an archive that holds a .neo file (Neo Geo games in
// one file, as Geolith and the NeoSD use them), that it is not an arcade
// set, and whether libretro's Neo Geo list, if downloaded, knows its CRC.
func neoResult(emu arcade.Emulator, base string, files []arcade.File, db *nointro.DB) (fmt.Stringer, fmt.Stringer, bool) {
	for _, f := range files {
		if !strings.EqualFold(filepath.Ext(f.Name), ".neo") {
			continue
		}
		var why fmt.Stringer = i18n.M("arcade.neo_nolist")
		if db.Has(neoSystem) {
			why = i18n.M("arcade.neo_unknown")
			for _, m := range db.Find(f.CRC, f.Size, neoSystem) {
				if m.System == neoSystem {
					why = i18n.M("arcade.neo_known", m.Name)
					break
				}
			}
		}
		return i18n.M("arcade.st_neo", emu.Name), i18n.M("arcade.neo", base, emu.Name, why), true
	}
	return nil, nil, false
}

const neoSystem = "Neo Geo"

// arcadeResult is the status of a checked archive and its log line.
func arcadeResult(emu arcade.Emulator, base string, r arcade.Result, chds []string) (fmt.Stringer, fmt.Stringer) {
	switch {
	case r.Set == nil && r.Like != nil:
		return i18n.M("arcade.st_like", emu.Name, r.Like.Name),
			i18n.M("arcade.like", base, emu.Name, r.Like.Name+filepath.Ext(base), r.Like.Title)
	case r.Set == nil:
		return i18n.M("arcade.st_unknown", emu.Name), i18n.M("arcade.unknown", base, emu.Name)
	}
	title := r.Set.Title
	if r.OK() && len(chds) == 0 {
		uses := ""
		if len(r.Uses) > 0 {
			uses = " (+ " + strings.Join(r.Uses, ".zip, ") + ".zip)"
		}
		if !r.Set.Works {
			return i18n.M("arcade.st_ok_bad", emu.Name, title), i18n.M("arcade.ok_bad", base, title+uses, emu.Name)
		}
		return i18n.M("arcade.st_ok", emu.Name, title), i18n.M("arcade.ok", base, title+uses)
	}
	var why joined
	if r.Missing > 0 {
		why = append(why, i18n.M("arcade.missing", r.Missing))
	}
	if r.Wrong > 0 {
		why = append(why, i18n.M("arcade.wrong", r.Wrong))
	}
	if len(r.Needs) > 0 {
		why = append(why, i18n.M("arcade.needs", strings.Join(r.Needs, ".zip, ")+".zip"))
	}
	if len(chds) > 0 {
		why = append(why, i18n.M("arcade.needs_chd", strings.Join(chds, ", ")))
	}
	return i18n.M("arcade.st_bad", emu.Name, why), i18n.M("arcade.bad", base, title, why)
}
