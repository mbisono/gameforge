package merge

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gameforge/internal/i18n"
	"gameforge/internal/nx"
)

// Game groups all NSPs that belong to one base application. Every file is
// kept, including duplicates and older versions, so the user can choose.
type Game struct {
	AppID   uint64
	Bases   []*NSP // sorted by version, newest last
	Updates []*NSP // sorted by version, newest last
	DLCs    []*NSP // sorted by title ID, then version
}

func newest(l []*NSP) *NSP {
	if len(l) == 0 {
		return nil
	}
	return l[len(l)-1]
}

// Base returns the original (v0) base game file if there is one, else the
// newest base with an applied update, or nil.
func (g *Game) Base() *NSP {
	if len(g.Bases) > 0 && !g.Bases[0].Patched() {
		return g.Bases[0]
	}
	return newest(g.Bases)
}

// LatestUpdate returns the newest update, or nil.
func (g *Game) LatestUpdate() *NSP { return newest(g.Updates) }

// Files returns every file of the game: bases, updates, then DLCs.
func (g *Game) Files() []*NSP {
	out := append(append([]*NSP{}, g.Bases...), g.Updates...)
	return append(out, g.DLCs...)
}

// DefaultSelection returns what is normally merged: the base, the newest
// update (updates are cumulative) and the newest version of each DLC. A base
// with an applied update takes no update, and files that a merged base game
// NSP already contains in the same or a newer version are left out.
func (g *Game) DefaultSelection() []*NSP {
	var out []*NSP
	b := g.Base()
	if b != nil {
		out = append(out, b)
	}
	if u := g.LatestUpdate(); u != nil && (b == nil || !b.Patched()) && !bundles(b, u) {
		out = append(out, u)
	}
	for i, d := range g.DLCs {
		if (i+1 == len(g.DLCs) || g.DLCs[i+1].TitleID != d.TitleID) && !bundles(b, d) {
			out = append(out, d)
		}
	}
	return out
}

// fingerprint identifies the content of n: the sorted names of its NCAs,
// which are their content hashes, an NCZ counting as the NCA it holds. It is
// "" for a file without NCAs.
func (n *NSP) fingerprint() string {
	var names []string
	for _, e := range n.Entries {
		name := strings.ToLower(e.Name)
		if strings.HasSuffix(name, ".ncz") {
			name = strings.TrimSuffix(name, ".ncz") + ".nca"
		}
		if strings.HasSuffix(name, ".nca") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, "/")
}

// DuplicateOf returns the file of the game that n is an identical copy of
// (same NCAs, even if one is compressed), or nil. Of several copies, the one
// the default selection picks, or else the first, counts as the original.
func (g *Game) DuplicateOf(n *NSP) *NSP {
	fp := n.fingerprint()
	if fp == "" {
		return nil
	}
	def := map[*NSP]bool{}
	for _, d := range g.DefaultSelection() {
		def[d] = true
	}
	var orig *NSP
	for _, f := range g.Files() {
		if f.fingerprint() == fp && (orig == nil || def[f] && !def[orig]) {
			orig = f
		}
	}
	if orig == n {
		return nil
	}
	return orig
}

// bundles reports whether the merged NSP b contains n's title in the same or
// a newer version.
func bundles(b, n *NSP) bool {
	if b == nil {
		return false
	}
	for _, t := range b.Bundled() {
		if t.Meta.TitleID == n.TitleID && t.Meta.Version >= n.Version {
			return true
		}
	}
	return false
}

// UpdateVersion returns the newest update version of the game, counting the
// update inside a merged base game NSP, and its title when it was read from
// a CNMT; ok is false if there is no update.
func (g *Game) UpdateVersion() (v uint32, t *Title, ok bool) {
	if u := g.LatestUpdate(); u != nil {
		v, t, ok = u.Version, u.Own(), true
	}
	if b := g.Base(); b != nil {
		if bt := b.BundledUpdate(); bt != nil && (!ok || bt.Meta.Version > v) {
			v, t, ok = bt.Meta.Version, bt, true
		}
	}
	return v, t, ok
}

// Firmware returns the system version the game needs with its newest
// update, or "" if unknown.
func (g *Game) Firmware() string {
	if _, t, ok := g.UpdateVersion(); ok {
		if t != nil {
			return t.Firmware()
		}
		return ""
	}
	if b := g.Base(); b != nil && b.Own() != nil {
		return b.Own().Firmware()
	}
	return ""
}

// Control returns the Control data of the base game, or else of the newest
// update, or nil.
func (g *Game) Control() *nx.Control {
	if b := g.Base(); b != nil && b.Control() != nil {
		return b.Control()
	}
	if _, t, _ := g.UpdateVersion(); t != nil {
		return t.Control
	}
	return nil
}

// DLCCount returns the number of distinct DLCs, counting those inside a
// merged base game NSP.
func (g *Game) DLCCount() int { return len(g.OwnedDLCs()) }

// Title returns the game's name from its Control data, or else one derived
// from the base game's file name.
func (g *Game) Title() string {
	if c := g.Control(); c != nil && c.Name != "" {
		return c.Name
	}
	src := g.Base()
	if src == nil {
		src = g.LatestUpdate()
	}
	if src == nil && len(g.DLCs) > 0 {
		src = g.DLCs[0]
	}
	if src == nil {
		return fmt.Sprintf("%016X", g.AppID)
	}
	return CleanName(src.Name())
}

var reTags = regexp.MustCompile(`\s*[\[(][^\])]*[\])]`)

// CleanName strips extension and [..]/(..) tags from a file name.
func CleanName(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = strings.TrimSpace(reTags.ReplaceAllString(name, ""))
	if name == "" {
		return i18n.T("name.fallback")
	}
	return name
}

// OutputName builds the merged file name for a selection; ext is ".nsp" or
// ".nsz".
func (g *Game) OutputName(sel []*NSP, ext string) string {
	return mergedName(g.Title(), g.AppID, sel, ext)
}

func mergedName(title string, appID uint64, sel []*NSP, ext string) string {
	// Count what a merged base game NSP brings along as well.
	var ver uint32
	dlcs := map[uint64]bool{}
	add := func(kind Kind, id uint64, v uint32) {
		switch kind {
		case KindUpdate, KindBase: // a base game with an update applied has its version
			ver = max(ver, v)
		case KindDLC:
			dlcs[id] = true
		}
	}
	for _, n := range sel {
		add(n.Kind, n.TitleID, n.Version)
		for _, t := range n.Bundled() {
			add(t.Kind(), t.Meta.TitleID, t.Meta.Version)
		}
	}
	name := fmt.Sprintf("%s [%016X][v%d]", title, appID, ver)
	if len(dlcs) > 0 {
		name += fmt.Sprintf("[%d DLC]", len(dlcs))
	}
	return sanitize(name) + ext
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, s)
}

// Group sorts analysed NSPs into games. NSPs whose title could not be
// determined are returned separately.
func Group(nsps []*NSP) (games []*Game, unknown []*NSP) {
	byApp := map[uint64]*Game{}
	for _, n := range nsps {
		if n.Kind == KindUnknown || n.AppID == 0 {
			unknown = append(unknown, n)
			continue
		}
		g := byApp[n.AppID]
		if g == nil {
			g = &Game{AppID: n.AppID}
			byApp[n.AppID] = g
			games = append(games, g)
		}
		switch n.Kind {
		case KindBase:
			g.Bases = append(g.Bases, n)
		case KindUpdate:
			g.Updates = append(g.Updates, n)
		case KindDLC:
			g.DLCs = append(g.DLCs, n)
		}
	}
	byVersion := func(l []*NSP) func(i, j int) bool {
		return func(i, j int) bool {
			if l[i].TitleID != l[j].TitleID {
				return l[i].TitleID < l[j].TitleID
			}
			return l[i].Version < l[j].Version
		}
	}
	for _, g := range games {
		sort.SliceStable(g.Bases, byVersion(g.Bases))
		sort.SliceStable(g.Updates, byVersion(g.Updates))
		sort.SliceStable(g.DLCs, byVersion(g.DLCs))
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Title() < games[j].Title() })
	return games, unknown
}

// Plan lists the PFS0 entries of the merged file, skipping duplicates
// (identical NCAs, tickets and certificates share the same name).
func Plan(sel []*NSP) []nx.PFS0Source {
	seen := map[string]bool{}
	var files []nx.PFS0Source
	for _, n := range sel {
		for _, e := range n.Entries {
			key := strings.ToLower(e.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			files = append(files, nx.PFS0Source{Name: e.Name, Path: n.Path, Offset: e.Offset, Size: e.Size})
		}
	}
	return files
}

// TotalSize returns the size of the merged file for a plan.
func TotalSize(files []nx.PFS0Source) int64 {
	total := nx.PFS0HeaderSize(files)
	for _, f := range files {
		total += f.Size
	}
	return total
}
