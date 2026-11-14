package main

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	. "modernc.org/tk9.0"

	"gameforge/internal/chd"
	"gameforge/internal/discinfo"
	"gameforge/internal/i18n"
	"gameforge/internal/redump"
)

// discDB is the local copy of the Redump databases, loaded on first use,
// and the label of the Discs tab that shows its state.
var discDB struct {
	db     *redump.DB
	err    error
	loaded bool
	status *TLabelWidget
}

// A Redump database older than this gets a hint to update it.
const redumpMaxAge = 30 * 24 * time.Hour

func (u *ui) redumpPath() string {
	return filepath.Join(filepath.Dir(u.cfg.Path()), redump.FileName)
}

func (u *ui) redumpDB() *redump.DB {
	if !discDB.loaded {
		discDB.loaded = true
		db, err := redump.Load(u.redumpPath())
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			u.logf("redump.load_error", err)
			discDB.err = err
		}
		discDB.db = db
	}
	return discDB.db
}

// showRedumpStatus shows which consoles the Redump database has and how
// current it is, next to its menu in the Discs tab.
func (u *ui) showRedumpStatus() {
	if discDB.status == nil {
		return
	}
	db := u.redumpDB()
	var have []string
	newest := ""
	if db != nil {
		for _, p := range redump.Platforms {
			if l := db.Lists[p]; l != nil {
				have = append(have, p)
				newest = max(newest, strings.ReplaceAll(l.Version, ".", "-"))
			}
		}
	}
	consoles := strings.Join(have, ", ")
	if len(have) == len(redump.Platforms) {
		consoles = T("redump.all")
	}
	var text string
	switch {
	case len(have) > 0 && time.Since(db.Downloaded) > redumpMaxAge:
		text = T("redump.status_old", consoles, newest, int(time.Since(db.Downloaded).Hours()/24))
	case len(have) > 0:
		text = T("redump.status", consoles, newest)
	case discDB.err != nil:
		text = T("redump.status_error")
	default:
		text = T("redump.status_none")
	}
	discDB.status.Configure(Txt(text))
}

// updateRedump downloads (or checks for changes) the Redump databases of
// all consoles.
func (u *ui) updateRedump() {
	if u.busy {
		return
	}
	u.separate()
	u.downloadRedump(redump.Platforms, func() {})
}

// discRename is a disc image of the Discs tab and its new path.
type discRename struct {
	item *convItem
	to   string
}

// renameDiscs gives the CHD and ISO files of the Discs tab their Redump
// names, downloading the databases of their platforms first if the user
// agrees, and showing the changes before renaming.
func (t *convTab) renameDiscs(u *ui) {
	if u.busy {
		return
	}
	var items []*convItem
	for _, it := range t.items {
		if (isCHD(it.path) || isISO(it.path)) && it.platform != "" {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("drename.empty")))
		return
	}
	db := u.redumpDB()
	var missing []string
	for _, it := range items {
		if _, ok := redump.Files[it.platform]; ok && !db.Has(it.platform) && !slices.Contains(missing, it.platform) {
			missing = append(missing, it.platform)
		}
	}
	if len(missing) > 0 {
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("drename.ask_db", strings.Join(missing, ", "))), Detail(T("drename.ask_db.detail", redump.Source)))
		if answer == "yes" {
			u.downloadRedump(missing, func() { t.planRenames(u, items) })
			return
		}
	}
	t.planRenames(u, items)
}

// downloadRedump downloads the Redump databases of the platforms in the
// background, then runs then on the GUI thread (also after a failure: the
// names that can be found without them are still used).
func (u *ui) downloadRedump(platforms []string, then func()) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	u.logf("redump.download_start", strings.Join(platforms, ", "))
	old := u.redumpDB()
	go func() {
		defer cancel()
		db, err := redump.Download(ctx, old, platforms, func(file string, done, total int64) {
			totalText := "?"
			if total > 0 {
				u.setProgress(float64(done) / float64(total))
				totalText = humanSize(total)
			}
			u.setStatus(T("status.downloading", file, humanSize(done), totalText))
		})
		if err == nil {
			if serr := db.Save(u.redumpPath()); serr != nil {
				u.logf("redump.save_error", serr)
			}
			for _, p := range platforms {
				if l := db.Lists[p]; l != nil {
					u.logf("redump.loaded", p, len(l.Games), l.Version)
				}
			}
		} else if ctx.Err() == nil {
			u.logf("redump.download_error", err)
		}
		u.setProgress(0)
		cancelled := ctx.Err() != nil
		PostEvent(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if err == nil {
				discDB.db, discDB.err = db, nil
			}
			u.showRedumpStatus()
			if cancelled {
				u.logf("log.cancelled")
				u.flush()
				return
			}
			u.flush()
			then()
		}, false)
	}()
}

// planRenames finds the new names in the background (telling versions
// apart may read whole tracks), then shows them and renames.
func (t *convTab) planRenames(u *ui, items []*convItem) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	db := u.redumpDB()
	u.separate()
	u.logf("drename.start", len(items))
	go func() {
		defer cancel()
		var plan []discRename
		for _, it := range items {
			if ctx.Err() != nil {
				break
			}
			name := filepath.Base(it.path)
			u.setStatus(T("drename.status", name))
			newName, skip := discName(ctx, u, db, it)
			switch {
			case skip != nil:
				u.println(skip)
			case newName != "" && newName != name:
				plan = append(plan, discRename{it, filepath.Join(filepath.Dir(it.path), newName)})
			}
		}
		u.setProgress(0)
		u.setStatus("")
		cancelled := ctx.Err() != nil
		PostEvent(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			u.flush()
			if cancelled {
				u.logf("log.cancelled")
				u.flush()
				return
			}
			t.applyRenames(u, plan)
		}, false)
	}()
}

// discName returns the new file name of a disc image, or why it keeps its
// name.
func discName(ctx context.Context, u *ui, db *redump.DB, it *convItem) (string, fmt.Stringer) {
	base := filepath.Base(it.path)
	ext := strings.ToLower(filepath.Ext(it.path))
	var info discinfo.Info
	var err error
	if isCHD(it.path) {
		info, err = discinfo.InfoFromCHD(it.path)
	} else {
		info, err = discinfo.InfoFromISO(it.path)
	}
	if err != nil {
		return "", i18n.M("drename.skip_error", base, err)
	}
	if info.Serial == "" && info.Title == "" {
		return "", i18n.M("drename.skip_nocode", base)
	}
	var ambiguous []string
	if _, listed := redump.Files[info.Platform]; listed && db.Has(info.Platform) && info.Serial != "" {
		track, closeTrack := discTracks(ctx, u, it, base)
		track.Regions, track.Rev = info.Regions, -1
		if info.Platform == discinfo.GameCube || info.Platform == discinfo.Wii {
			if v, err := strconv.ParseFloat(info.Version, 64); err == nil {
				track.Rev = int((v-1)*100 + 0.5)
			}
		}
		name, amb, err := db.Match(info.Platform, info.Serial, track)
		closeTrack()
		if err != nil {
			if ctx.Err() != nil {
				return "", nil
			}
			return "", i18n.M("drename.skip_error", base, err)
		}
		if name != "" {
			return fileSafe(name) + ext, nil
		}
		ambiguous = amb
	}
	// Without a database entry, discs that carry their title in mixed case
	// get it with their region.
	switch info.Platform {
	case discinfo.PSP, discinfo.GameCube, discinfo.Wii:
		if info.Title != "" && len(ambiguous) == 0 {
			name := info.Title
			if len(info.Regions) > 0 {
				name += " (" + strings.Join(info.Regions, ", ") + ")"
			}
			if info.Discs > 1 || info.Disc > 1 {
				name += fmt.Sprintf(" (Disc %d)", info.Disc)
			}
			return fileSafe(name) + ext, nil
		}
	}
	switch {
	case len(ambiguous) > 0:
		return "", i18n.M("drename.skip_ambiguous", base, info.Serial, strings.Join(ambiguous, " / "))
	case !db.Has(info.Platform):
		return "", i18n.M("drename.skip_nodb", base, info.Platform)
	case info.Serial == "":
		return "", i18n.M("drename.skip_nocode", base)
	}
	return "", i18n.M("drename.skip_unknown", base, info.Serial)
}

// discTracks gives Match the sizes and CRCs of a CHD's tracks or of an ISO
// file; computing a CRC shows its progress.
func discTracks(ctx context.Context, u *ui, it *convItem, name string) (redump.Track, func()) {
	progress := func(done, total int64) {
		u.setProgress(float64(done) / float64(max(total, 1)))
		u.setStatus(T("drename.crc", name, int(done*100/max(total, 1))))
	}
	if isCHD(it.path) {
		d, err := chd.OpenDisc(it.path)
		if err != nil {
			return redump.Track{}, func() {}
		}
		return redump.Track{
			Size: d.Size,
			CRC:  func(n int) (uint32, error) { return d.CRC(ctx, n, progress) },
		}, func() { d.Close() }
	}
	return redump.Track{
		Size: func(n int) (int64, bool) {
			st, err := os.Stat(it.path)
			if err != nil || n != 0 {
				return 0, false
			}
			return st.Size(), true
		},
		CRC: func(n int) (uint32, error) { return fileCRC(ctx, it.path, progress) },
	}, func() {}
}

// fileCRC computes the CRC-32 of a file.
func fileCRC(ctx context.Context, path string, progress func(done, total int64)) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	h := crc32.NewIEEE()
	buf := make([]byte, 4<<20)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		done += int64(n)
		progress(done, st.Size())
		if err == io.EOF {
			return h.Sum32(), nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// fileSafe replaces what Windows does not allow in file names.
func fileSafe(name string) string {
	name = strings.ReplaceAll(name, ": ", " - ")
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, name)
	return strings.TrimRight(name, ". ")
}

// applyRenames shows the planned names, renames after confirmation and
// updates the .m3u playlists that list renamed files.
func (t *convTab) applyRenames(u *ui, plan []discRename) {
	if len(plan) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("drename.none")))
		return
	}
	var lines []string
	for i, r := range plan {
		if i == 15 {
			lines = append(lines, T("rename.more", len(plan)-i))
			break
		}
		lines = append(lines, filepath.Base(r.item.path)+"\n   → "+filepath.Base(r.to))
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("rename.confirm", len(plan))), Detail(strings.Join(lines, "\n")))
	if answer != "yes" {
		return
	}
	done := 0
	renamed := map[string]string{} // old path → new path
	for _, r := range plan {
		from := r.item.path
		if err := renameFile(from, r.to); err != nil {
			u.logf("log.rename_error", filepath.Base(from), err)
			continue
		}
		u.logf("log.renamed", filepath.Base(from), filepath.Base(r.to))
		renamed[from] = r.to
		r.item.path = r.to
		if v, ok := t.checked[from]; ok {
			delete(t.checked, from)
			t.checked[r.to] = v
		}
		done++
	}
	for _, p := range updatePlaylists(renamed) {
		u.logf("drename.m3u", filepath.Base(p))
	}
	u.logf("log.rename_done", done, len(plan))
	u.flush()
	t.refresh(u)
}

// updatePlaylists replaces the old names of renamed files in the .m3u
// playlists of their folders, and returns the playlists it changed.
func updatePlaylists(renamed map[string]string) []string {
	dirs := map[string]map[string]string{} // folder → old name (lower case) → new name
	for from, to := range renamed {
		d := filepath.Dir(from)
		if dirs[d] == nil {
			dirs[d] = map[string]string{}
		}
		dirs[d][strings.ToLower(filepath.Base(from))] = filepath.Base(to)
	}
	var changed []string
	for d, names := range dirs {
		lists, _ := filepath.Glob(filepath.Join(d, "*.m3u"))
		for _, p := range lists {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			lines := strings.Split(string(b), "\n")
			edit := false
			for i, l := range lines {
				entry := strings.TrimRight(l, "\r")
				if to, ok := names[strings.ToLower(entry)]; ok {
					lines[i] = to + l[len(entry):]
					edit = true
				}
			}
			if edit && os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644) == nil {
				changed = append(changed, p)
			}
		}
	}
	slices.Sort(changed)
	return changed
}
