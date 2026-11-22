package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/i18n"
	"gameforge/internal/nointro"
)

// Duplicates in the ROMs tab: files that hold the same game, such as a
// Neo Geo game as a .neo, inside a .7z and as MAME's .zip set. Two files
// are the same game when they share any of these keys:
//   - the game's name in the databases, found by the ROM's CRC or, for
//     MAME's Neo Geo sets, by the set's short name (blazstar.zip);
//   - the ROM's CRC and size, for ROMs the databases do not know;
//   - the file name without its extension, for ROMs of the same console
//     (or archives of several files), in case neither of the above helps.

// dupeFile is a file of the check and what it was found to be.
type dupeFile struct {
	item *convItem
	keys []string
	game string // its name in the databases, if found
	size int64  // on disk
}

// findDupes looks for duplicates among the checked ROMs, ZIPs and 7z
// files of the ROMs tab, downloading the databases it needs first if the
// user agrees. With mark, it then checks the extra copies without asking.
func (t *convTab) findDupes(u *ui, mark bool) {
	if u.busy {
		return
	}
	items, ok := t.romItems()
	if !ok {
		return
	}
	var roms []romFile
	for _, it := range items {
		roms = append(roms, scanRom(it))
	}
	db := u.noIntroDB()
	missing := missingLists(db, roms)
	for _, s := range shortListsNeeded(db, roms) {
		if !slices.Contains(missing, s) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("rrename.ask_db", strings.Join(missing, ", "))), Detail(T("dupes.ask_db.detail", nointro.Source)))
		if answer == "yes" {
			u.separate()
			u.downloadNoIntro(missing, func() { u.runDupes(roms, mark) })
			return
		}
	}
	u.runDupes(roms, mark)
}

// markDupes checks only the extra copies that the last duplicate check
// found, or runs the check first if there was none or the list changed
// since.
func (t *convTab) markDupes(u *ui) {
	if u.busy {
		return
	}
	if len(t.dupes) > 0 && t.dupesListed() {
		markGroups(u, t.dupes)
		return
	}
	t.findDupes(u, true)
}

// dupesListed reports whether every file of the last duplicate check is
// still in the list.
func (t *convTab) dupesListed() bool {
	listed := map[*convItem]bool{}
	for _, it := range t.items {
		listed[it] = true
	}
	for _, g := range t.dupes {
		for _, f := range g.files {
			if !listed[f.item] {
				return false
			}
		}
	}
	return true
}

// shortListsNeeded returns the consoles whose short names would tell what
// the archives of several files are (MAME's Neo Geo sets), when their
// lists are missing or were saved without them. They are only asked for
// in a folder that has such games or whose list is already there.
func shortListsNeeded(db *nointro.DB, roms []romFile) []string {
	if !slices.ContainsFunc(roms, func(r romFile) bool { return r.err == nil && r.files > 1 }) {
		return nil
	}
	var out []string
	for _, s := range nointro.ShortNames() {
		used := db.Has(s) || slices.ContainsFunc(roms, func(r romFile) bool { return nointro.SystemOf(r.ext()) == s })
		if used && !db.HasShort(s) {
			out = append(out, s)
		}
	}
	return out
}

// runDupes identifies the files in the background (loose ROMs are read
// whole for their CRC), then shows the groups.
func (u *ui) runDupes(roms []romFile, mark bool) {
	orderText := keepText(u.cfg.DupesKeep)
	order := keepOrder(orderText)
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	db := u.noIntroDB()
	u.separate()
	u.logf("dupes.start", len(roms))
	go func() {
		defer cancel()
		var files []dupeFile
		for i := range roms {
			if ctx.Err() != nil {
				break
			}
			u.setStatus(T("drename.status", filepath.Base(roms[i].item.path)))
			f := dupeKeys(ctx, u, db, &roms[i])
			if roms[i].err != nil {
				u.println(i18n.M("drename.skip_error", filepath.Base(roms[i].item.path), roms[i].err))
			}
			files = append(files, f)
		}
		u.setProgress(0)
		u.setStatus("")
		var groups []dupeGroup
		if ctx.Err() == nil {
			groups = reportDupes(u, files, order)
		}
		cancelled := ctx.Err() != nil
		onGUI(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			u.flush()
			if cancelled {
				return
			}
			roms[0].item.tab.dupes = groups
			if mark {
				markGroups(u, groups)
			} else {
				markExtras(u, groups, orderText)
			}
		})
	}()
}

// dupeKeys finds what a file is: the keys it is compared by.
func dupeKeys(ctx context.Context, u *ui, db *nointro.DB, r *romFile) dupeFile {
	f := dupeFile{item: r.item}
	if st, err := os.Stat(r.item.path); err == nil {
		f.size = st.Size()
	}
	base := filepath.Base(r.item.path)
	stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	if r.err != nil {
		f.keys = append(f.keys, "name\x00\x00"+stem)
		return f
	}
	kind, system := "", ""
	if r.files == 1 {
		system = nointro.SystemOf(r.ext())
		kind = cmp.Or(system, r.ext())
	}
	f.keys = append(f.keys, "name\x00"+kind+"\x00"+stem)
	if r.files == 1 {
		m, _ := romGame(ctx, u, db, r)
		if r.haveCRC {
			f.keys = append(f.keys, fmt.Sprintf("crc\x00%08x\x00%d", r.crc, r.size))
		}
		if m.Name != "" {
			f.game = m.Name
			f.keys = append(f.keys, "game\x00"+m.System+"\x00"+m.Name)
		}
	}
	if f.game == "" && (r.files > 1 || slices.Contains(nointro.ShortNames(), system)) {
		for _, s := range nointro.ShortNames() {
			if name := db.ByShort(s, stem); name != "" && (system == "" || system == s) {
				f.game = name
				f.keys = append(f.keys, "game\x00"+s+"\x00"+name)
				break
			}
		}
	}
	return f
}

// dupeGroups returns the groups of two or more files that share a key,
// sorted by name, each in the order of the list.
func dupeGroups(files []dupeFile) [][]dupeFile {
	parent := make([]int, len(files))
	for i := range parent {
		parent[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		if parent[i] != i {
			parent[i] = root(parent[i])
		}
		return parent[i]
	}
	first := map[string]int{}
	for i, f := range files {
		for _, k := range f.keys {
			if j, ok := first[k]; ok {
				parent[root(i)] = root(j)
			} else {
				first[k] = i
			}
		}
	}
	byRoot := map[int][]dupeFile{}
	var roots []int
	for i, f := range files {
		r := root(i)
		if byRoot[r] == nil {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], f)
	}
	var groups [][]dupeFile
	for _, r := range roots {
		if len(byRoot[r]) > 1 {
			groups = append(groups, byRoot[r])
		}
	}
	slices.SortFunc(groups, func(a, b []dupeFile) int {
		return strings.Compare(strings.ToLower(dupeLabel(a)), strings.ToLower(dupeLabel(b)))
	})
	return groups
}

// dupeLabel names a group: the game's name if any of its files was
// identified, else the first file's name.
func dupeLabel(g []dupeFile) string {
	for _, f := range g {
		if f.game != "" {
			return f.game
		}
	}
	base := filepath.Base(g[0].item.path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// defaultKeep is the order of the formats kept of a game's duplicates
// when the user has not chosen one: a 7z (with a .neo inside, for Neo Geo),
// then a loose .neo, then a ZIP (such as MAME's set), then the rest.
const defaultKeep = "7z, neo, zip"

// keepOrder reads an order of formats such as "7z, .neo zip" into
// extensions (".7z", ".neo", ".zip").
func keepOrder(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if f = "." + strings.TrimLeft(f, "."); f != "." && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// keepText is the order of formats as the option shows it.
func keepText(s string) string {
	var out []string
	for _, e := range keepOrder(cmp.Or(s, defaultKeep)) {
		out = append(out, e[1:])
	}
	if len(out) == 0 {
		return keepText(defaultKeep)
	}
	return strings.Join(out, ", ")
}

// keepIndex chooses the file kept of a group: the first format in order;
// among files of the same format, one that already has the game's name,
// then the largest, then the first in the list.
func keepIndex(g []dupeFile, order []string) int {
	rank := func(f dupeFile) int {
		if i := slices.Index(order, strings.ToLower(filepath.Ext(f.item.path))); i >= 0 {
			return i
		}
		return len(order)
	}
	named := func(f dupeFile) bool {
		base := filepath.Base(f.item.path)
		return f.game != "" && strings.EqualFold(strings.TrimSuffix(base, filepath.Ext(base)), fileSafe(f.game))
	}
	best := 0
	for i, f := range g[1:] {
		b := g[best]
		switch {
		case rank(f) != rank(b):
			if rank(f) < rank(b) {
				best = i + 1
			}
		case named(f) != named(b):
			if named(f) {
				best = i + 1
			}
		case f.size > b.size:
			best = i + 1
		}
	}
	return best
}

// dupeGroup is a game found in several files and the one to keep.
type dupeGroup struct {
	files []dupeFile
	keep  int
}

// reportDupes shows the groups in the status column and the log, and
// returns them.
func reportDupes(u *ui, files []dupeFile, order []string) []dupeGroup {
	var groups []dupeGroup
	inGroup := map[*convItem]bool{}
	extra, extraSize := 0, int64(0)
	for _, g := range dupeGroups(files) {
		label := dupeLabel(g)
		keep := keepIndex(g, order)
		groups = append(groups, dupeGroup{g, keep})
		kept := filepath.Base(g[keep].item.path)
		u.logf("dupes.group", label, len(g))
		for i, f := range g {
			inGroup[f.item] = true
			if i == keep {
				u.setItemStatus(f.item, i18n.M("dupes.st_keep", label, len(g)))
				u.logf("dupes.file_keep", filepath.Base(f.item.path), humanSize(f.size))
				continue
			}
			u.setItemStatus(f.item, i18n.M("dupes.st_extra", kept))
			u.logf("dupes.file_extra", filepath.Base(f.item.path), humanSize(f.size))
			extra++
			extraSize += f.size
		}
	}
	for _, f := range files {
		if !inGroup[f.item] {
			u.setItemStatus(f.item, i18n.M("dupes.st_single"))
		}
	}
	if len(groups) == 0 {
		u.logf("dupes.none", len(files))
	} else {
		u.logf("dupes.summary", len(groups), extra, humanSize(extraSize))
	}
	return groups
}

// markExtras offers to check only the extra copies of each game, ready to
// be deleted.
func markExtras(u *ui, groups []dupeGroup, order string) {
	if len(groups) == 0 {
		return
	}
	n := 0
	for _, g := range groups {
		n += len(g.files) - 1
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("dupes.ask_mark", n)), Detail(T("dupes.ask_mark.detail", order)))
	if answer == "yes" {
		markGroups(u, groups)
	}
}

// markGroups checks the extra copies of each game, the file kept chosen by
// the current order of formats, and unchecks all the other files.
func markGroups(u *ui, groups []dupeGroup) {
	if len(groups) == 0 {
		return
	}
	order := keepOrder(keepText(u.cfg.DupesKeep))
	t := groups[0].files[0].item.tab
	for _, it := range t.items {
		t.checked[it.path] = false
	}
	n := 0
	for i := range groups {
		g := &groups[i]
		g.keep = keepIndex(g.files, order)
		label, kept := dupeLabel(g.files), filepath.Base(g.files[g.keep].item.path)
		for j, f := range g.files {
			if j == g.keep {
				f.item.status = i18n.M("dupes.st_keep", label, len(g.files))
				continue
			}
			f.item.status = i18n.M("dupes.st_extra", kept)
			t.checked[f.item.path] = true
			n++
		}
	}
	u.logf("dupes.marked", n)
	t.refresh(u)
}

// deleteChecked deletes the checked files of a tab, to the Recycle Bin
// where there is one, after showing them.
func (t *convTab) deleteChecked(u *ui) {
	if u.busy {
		return
	}
	var items []*convItem
	var size int64
	var lines []string
	for _, it := range t.items {
		if !t.marked(it) {
			continue
		}
		items = append(items, it)
		if st, err := os.Stat(it.path); err == nil {
			size += st.Size()
		}
		if len(lines) < 15 {
			lines = append(lines, filepath.Base(it.path))
		}
	}
	if len(items) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("del.none")))
		return
	}
	if len(items) > len(lines) {
		lines = append(lines, T("rename.more", len(items)-len(lines)))
	}
	where := T("del.permanent")
	if usesTrash {
		where = T("del.trash")
	}
	answer := MessageBox(Parent(App), Icon("warning"), Type("yesno"), Default("no"), Title(T("app.title")),
		Msg(T("del.confirm", len(items), humanSize(size))), Detail(strings.Join(lines, "\n")+"\n\n"+where))
	if answer != "yes" {
		return
	}
	u.separate()
	gone := map[string]bool{}
	var freed int64
	for _, it := range items {
		st, _ := os.Stat(it.path)
		if err := removeFile(it.path); err != nil {
			u.logf("del.error", filepath.Base(it.path), err)
			continue
		}
		gone[it.path] = true
		if st != nil {
			freed += st.Size()
		}
		u.logf("del.done", filepath.Base(it.path))
	}
	t.items = slices.DeleteFunc(t.items, func(it *convItem) bool { return gone[it.path] })
	t.pool = slices.DeleteFunc(t.pool, func(p string) bool { return gone[p] })
	for p := range gone {
		delete(t.checked, p)
	}
	u.logf("del.summary", len(gone), len(items), humanSize(freed))
	t.refresh(u)
}

// keepBox is the option of which format of a game's duplicates is kept.
func keepBox(u *ui, parent *TFrameWidget) *TFrameWidget {
	f := parent.TFrame()
	label := f.TLabel(Txt(T("dupes.keep")))
	entry := f.TEntry(Textvariable(keepText(u.cfg.DupesKeep)), Width(20))
	save := func() {
		text := keepText(entry.Textvariable())
		entry.Configure(Textvariable(text))
		if text == keepText(defaultKeep) {
			text = ""
		}
		if text != u.cfg.DupesKeep {
			u.cfg.DupesKeep = text
			u.saveConfig()
		}
	}
	Bind(entry, "<FocusOut>", Command(save))
	Bind(entry, "<Return>", Command(save))
	setTooltip(label, T("dupes.keep.tip"))
	setTooltip(entry, T("dupes.keep.tip"))
	Grid(label, Row(0), Column(0), Sticky("w"))
	Grid(entry, Row(0), Column(1), Sticky("w"), Padx("2m 0"))
	return f
}
