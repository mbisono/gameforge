package merge

import (
	"sort"

	"gameforge/internal/titledb"
)

// OwnedDLCs returns the newest version of every DLC in the game's files,
// counting those inside a merged base game NSP.
func (g *Game) OwnedDLCs() map[uint64]uint32 {
	out := map[uint64]uint32{}
	add := func(id uint64, v uint32) {
		if old, ok := out[id]; !ok || v > old {
			out[id] = v
		}
	}
	for _, d := range g.DLCs {
		add(d.TitleID, d.Version)
	}
	if b := g.Base(); b != nil {
		for _, t := range b.Bundled() {
			if t.Kind() == KindDLC {
				add(t.Meta.TitleID, t.Meta.Version)
			}
		}
	}
	return out
}

// HaveVersion returns the newest update version the game's files provide,
// from an update, a merged base game NSP or a base with an applied update;
// 0 if there is none.
func (g *Game) HaveVersion() uint32 {
	v, _, _ := g.UpdateVersion()
	if b := newest(g.Bases); b != nil && b.Version > v {
		v = b.Version
	}
	return v
}

// OutdatedDLC is a DLC with a newer version in the game database.
type OutdatedDLC struct {
	ID     uint64
	Have   uint32
	Latest titledb.Release
}

// OnlineStatus compares a game with the game database.
type OnlineStatus struct {
	Known bool // false: the database has nothing on the game
	Have  uint32
	// Latest is the newest released update, if HasUpdates.
	Latest     titledb.Release
	HasUpdates bool
	Missing    []uint64 // DLCs listed in the database but not in the files
	Outdated   []OutdatedDLC
}

// NewerUpdate reports whether a newer update than the files' is released.
func (s OnlineStatus) NewerUpdate() bool { return s.HasUpdates && s.Latest.Version > s.Have }

// UpToDate reports whether the game has nothing newer or missing.
func (s OnlineStatus) UpToDate() bool {
	return s.Known && !s.NewerUpdate() && len(s.Missing) == 0 && len(s.Outdated) == 0
}

// Online compares the game's files with db.
func (g *Game) Online(db *titledb.DB) OnlineStatus {
	s := OnlineStatus{Known: db.Known(g.AppID), Have: g.HaveVersion()}
	s.Latest, s.HasUpdates = db.Latest(g.AppID)
	owned := g.OwnedDLCs()
	for _, id := range db.DLCs(g.AppID) {
		if _, ok := owned[id]; !ok {
			s.Missing = append(s.Missing, id)
		}
	}
	for id, v := range owned {
		if r, ok := db.Latest(id); ok && r.Version > v {
			s.Outdated = append(s.Outdated, OutdatedDLC{ID: id, Have: v, Latest: r})
		}
	}
	sort.Slice(s.Outdated, func(i, j int) bool { return s.Outdated[i].ID < s.Outdated[j].ID })
	return s
}
