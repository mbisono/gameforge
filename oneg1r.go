package main

import (
	"cmp"
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/i18n"
	"gameforge/internal/nointro"
)

// One game, one ROM (1G1R): of the versions of a game (USA, Europe, Japan,
// Rev 1…), the one to keep, by the user's order of regions. Versions are
// told by their No-Intro names, found by the ROM's CRC as for Rename… or,
// for ROMs the database does not know, taken from the file name when it
// follows No-Intro's form: "Title (Region) (other flags)". Versions of a
// game that has different titles in different regions ("Pocket Monsters"
// and "Pokemon") are different games here, since No-Intro's lists from
// libretro do not tell which versions belong together.

// defaultRegions is the order of regions when the user has not chosen one.
const defaultRegions = "World, USA, Europe, Brazil, Japan"

// regionNames are the regions No-Intro names use, in lower case.
var regionNames = map[string]bool{}

func init() {
	for _, r := range []string{"World", "USA", "Europe", "Japan", "Brazil", "Korea", "China", "Taiwan",
		"Hong Kong", "Asia", "Australia", "New Zealand", "Canada", "Latin America", "Mexico", "Argentina",
		"France", "Germany", "Italy", "Spain", "Portugal", "Netherlands", "Belgium", "Switzerland", "Austria",
		"Sweden", "Norway", "Denmark", "Finland", "Scandinavia", "UK", "Ireland", "Russia", "Poland",
		"Greece", "Turkey", "India", "South Africa", "Unknown"} {
		regionNames[strings.ToLower(r)] = true
	}
}

// regionOrder reads an order of regions such as "USA, europe;Japan".
func regionOrder(s string) []string {
	var out []string
	for _, r := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ';' }) {
		r = strings.ToLower(strings.TrimSpace(r))
		if r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// regionText is the order of regions as the option shows it.
func regionText(s string) string {
	order := regionOrder(cmp.Or(s, defaultRegions))
	if len(order) == 0 {
		return defaultRegions
	}
	var out []string
	for _, r := range order {
		out = append(out, regionLabel(r))
	}
	return strings.Join(out, ", ")
}

// regionLabel spells a region as No-Intro does.
func regionLabel(r string) string {
	switch r {
	case "usa", "uk":
		return strings.ToUpper(r)
	}
	words := strings.Fields(r)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// version is what a No-Intro name tells about a version of a game.
type version struct {
	title   string   // the game's title, in lower case: the name before its flags, with its disc or side ("(disc 2)")
	regions []string // in lower case; none if the name has no region
	rev     int      // revision: 0 for the first release
	bad     bool     // a beta, prototype, demo, sample, kiosk or pirate version, or a bad dump
}

var (
	flagRE = regexp.MustCompile(`\(([^)]*)\)|\[([^\]]*)\]`)
	revRE  = regexp.MustCompile(`^(?:Rev ([0-9]+|[A-Z])|v([0-9]+)(?:\.([0-9]+))?)$`)
	// partRE: one disc, side or part of a game, a separate file to keep.
	partRE = regexp.MustCompile(`^(?:Disc|Disk|Side|Part|Tape) `)
	badRE  = regexp.MustCompile(`^(?:Beta|Proto|Possible Proto|Demo|Sample|Kiosk|Promo|Debug|Pirate|Program)\b`)
)

// parseVersion reads a No-Intro name.
func parseVersion(name string) version {
	v := version{}
	cut := len(name)
	if i := strings.IndexAny(name, "(["); i > 0 {
		cut = i
	}
	v.title = strings.ToLower(strings.TrimSpace(name[:cut]))
	for _, m := range flagRE.FindAllStringSubmatch(name[cut:], -1) {
		if m[2] != "" { // [b], [h], [t]: dumps that are not the original
			v.bad = true
			continue
		}
		flag := strings.TrimSpace(m[1])
		parts := strings.Split(flag, ",")
		regions := true
		for i, p := range parts {
			parts[i] = strings.ToLower(strings.TrimSpace(p))
			regions = regions && regionNames[parts[i]]
		}
		switch {
		case regions && v.regions == nil:
			v.regions = parts
		case badRE.MatchString(flag):
			v.bad = true
		case partRE.MatchString(flag):
			v.title += " (" + strings.ToLower(flag) + ")"
		default:
			if r := revRE.FindStringSubmatch(flag); r != nil {
				switch {
				case r[1] != "" && r[1][0] >= 'A':
					v.rev = int(r[1][0]-'A') + 1
				case r[1] != "":
					v.rev, _ = strconv.Atoi(r[1])
				default:
					major, _ := strconv.Atoi(r[2])
					minor, _ := strconv.Atoi(r[3])
					v.rev = major*100 + minor
				}
			}
		}
	}
	return v
}

// regionRank is the place of a version's best region in order; versions
// with none of the regions in order come after all others.
func regionRank(v version, order []string) int {
	best := len(order)
	for _, r := range v.regions {
		if i := slices.Index(order, r); i >= 0 && i < best {
			best = i
		}
	}
	return best
}

// g1rFile is a file of the 1G1R check.
type g1rFile struct {
	item    *convItem
	name    string // No-Intro name: from the database, or the file's own
	system  string
	version version
	size    int64
}

// g1rGroup is a game and the version kept.
type g1rGroup struct {
	files []g1rFile
	keep  int
}

// g1rBetter tells whether a is a better version to keep than b: not a
// beta or bad dump, then the best region, then the latest revision, then
// the first format in the order of the duplicates option, then the larger
// file.
func g1rBetter(a, b g1rFile, regions, formats []string) bool {
	if a.version.bad != b.version.bad {
		return !a.version.bad
	}
	if ra, rb := regionRank(a.version, regions), regionRank(b.version, regions); ra != rb {
		return ra < rb
	}
	if a.version.rev != b.version.rev {
		return a.version.rev > b.version.rev
	}
	format := func(f g1rFile) int {
		if i := slices.Index(formats, strings.ToLower(filepath.Ext(f.item.path))); i >= 0 {
			return i
		}
		return len(formats)
	}
	if fa, fb := format(a), format(b); fa != fb {
		return fa < fb
	}
	return a.size > b.size
}

// g1rGroups puts the files with a version into games (by console and
// title) and chooses the version kept of each. Files without a region in
// their name are left out.
func g1rGroups(files []g1rFile, regions, formats []string) (groups []g1rGroup, left []g1rFile) {
	byGame := map[string]int{}
	for _, f := range files {
		if f.version.title == "" || len(f.version.regions) == 0 {
			left = append(left, f)
			continue
		}
		key := f.system + "\x00" + f.version.title
		i, ok := byGame[key]
		if !ok {
			i = len(groups)
			byGame[key] = i
			groups = append(groups, g1rGroup{})
		}
		groups[i].files = append(groups[i].files, f)
	}
	for i := range groups {
		g := &groups[i]
		for j, f := range g.files {
			if g1rBetter(f, g.files[g.keep], regions, formats) {
				g.keep = j
			}
		}
	}
	slices.SortFunc(groups, func(a, b g1rGroup) int {
		return strings.Compare(a.files[a.keep].version.title, b.files[b.keep].version.title)
	})
	return groups, left
}

// oneGameOneROM finds the versions of each game among the checked ROMs,
// ZIPs and 7z files of the ROMs tab, chooses one by the order of regions,
// and offers to check the others for Delete checked….
func (t *convTab) oneGameOneROM(u *ui) {
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
	if missing := missingLists(u.noIntroDB(), roms); len(missing) > 0 {
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("rrename.ask_db", strings.Join(missing, ", "))), Detail(T("g1r.ask_db.detail", nointro.Source)))
		if answer == "yes" {
			u.separate()
			u.downloadNoIntro(missing, func() { u.run1G1R(roms) })
			return
		}
	}
	u.run1G1R(roms)
}

// run1G1R identifies the files in the background (loose ROMs are read
// whole for their CRC), then shows the games and the versions kept.
func (u *ui) run1G1R(roms []romFile) {
	regions := regionOrder(regionText(u.cfg.RegionOrder))
	formats := keepOrder(keepText(u.cfg.DupesKeep))
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	db := u.noIntroDB()
	u.separate()
	u.logf("g1r.start", len(roms), regionText(u.cfg.RegionOrder))
	go func() {
		defer u.recoverPanic()
		defer cancel()
		var files []g1rFile
		for i := range roms {
			if ctx.Err() != nil {
				break
			}
			r := &roms[i]
			base := filepath.Base(r.item.path)
			u.setStatus(T("drename.status", base))
			f := g1rFile{item: r.item, size: r.size}
			if r.err != nil || r.files != 1 {
				files = append(files, f) // arcade sets and unreadable files: no version
				continue
			}
			m, _ := romGame(ctx, u, db, r)
			f.name, f.system = m.Name, m.System
			if f.name == "" {
				f.name = strings.TrimSuffix(base, filepath.Ext(base))
				f.system = cmp.Or(nointro.SystemOf(r.ext()), r.ext())
			}
			f.version = parseVersion(f.name)
			files = append(files, f)
		}
		u.setProgress(0)
		u.setStatus("")
		var groups []g1rGroup
		if ctx.Err() == nil {
			groups = report1G1R(u, files, regions, formats)
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
			if !cancelled {
				mark1G1R(u, groups)
			}
		})
	}()
}

// report1G1R shows the games in the status column and the log, and
// returns them.
func report1G1R(u *ui, files []g1rFile, regions, formats []string) []g1rGroup {
	groups, left := g1rGroups(files, regions, formats)
	others, othersSize := 0, int64(0)
	for _, g := range groups {
		kept := g.files[g.keep]
		if len(g.files) == 1 {
			u.setItemStatus(kept.item, i18n.M("g1r.st_only"))
			continue
		}
		u.logf("g1r.group", kept.name, len(g.files))
		for i, f := range g.files {
			if i == g.keep {
				u.setItemStatus(f.item, i18n.M("g1r.st_keep", len(g.files)))
				u.logf("g1r.file_keep", filepath.Base(f.item.path))
				continue
			}
			u.setItemStatus(f.item, i18n.M("g1r.st_other", filepath.Base(kept.item.path)))
			u.logf("g1r.file_other", filepath.Base(f.item.path), humanSize(f.size))
			others++
			othersSize += f.size
		}
	}
	for _, f := range left {
		u.setItemStatus(f.item, i18n.M("g1r.st_none"))
	}
	if len(left) > 0 {
		u.logf("g1r.left", len(left))
	}
	u.logf("g1r.summary", len(groups), others, humanSize(othersSize))
	return groups
}

// mark1G1R offers to check only the versions not kept, ready to be
// deleted, unchecking all other files.
func mark1G1R(u *ui, groups []g1rGroup) {
	n := 0
	for _, g := range groups {
		n += len(g.files) - 1
	}
	if n == 0 {
		return
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("g1r.ask_mark", n)), Detail(T("g1r.ask_mark.detail")))
	if answer != "yes" {
		return
	}
	t := groups[0].files[0].item.tab
	for _, it := range t.items {
		t.checked[it.path] = false
	}
	for _, g := range groups {
		for i, f := range g.files {
			if i != g.keep {
				t.checked[f.item.path] = true
			}
		}
	}
	u.logf("dupes.marked", n)
	t.refresh(u)
}

// regionBox is the option of the order of regions for 1G1R.
func regionBox(u *ui, parent *TFrameWidget) *TFrameWidget {
	f := parent.TFrame()
	label := f.TLabel(Txt(T("g1r.regions")))
	entry := f.TEntry(Textvariable(regionText(u.cfg.RegionOrder)), Width(32))
	save := func() {
		text := regionText(entry.Textvariable())
		entry.Configure(Textvariable(text))
		if text == regionText(defaultRegions) {
			text = ""
		}
		if text != u.cfg.RegionOrder {
			u.cfg.RegionOrder = text
			u.saveConfig()
		}
	}
	Bind(entry, "<FocusOut>", Command(save))
	Bind(entry, "<Return>", Command(save))
	setTooltip(label, T("g1r.regions.tip"))
	setTooltip(entry, T("g1r.regions.tip"))
	Grid(label, Row(0), Column(0), Sticky("w"))
	Grid(entry, Row(0), Column(1), Sticky("w"), Padx("2m 0"))
	return f
}
