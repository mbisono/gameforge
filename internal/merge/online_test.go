package merge

import (
	"slices"
	"testing"

	"gameforge/internal/titledb"
)

func TestOnline(t *testing.T) {
	const app, dlc1, dlc2, dlc3 = 0x0100EBA004726000, 0x0100EBA004727001, 0x0100EBA004727002, 0x0100EBA004727003
	db := titledb.New(map[uint64][]titledb.Release{
		app:  {{Version: 65536, Date: "2020-01-01"}, {Version: 131072, Date: "2020-06-01"}},
		dlc1: {{Version: 65536, Date: "2020-03-01"}},
	}, map[uint64]string{app: "Game", dlc1: "One", dlc2: "Two", dlc3: "Three"})

	nsp := func(kind Kind, id uint64, v uint32) *NSP {
		return &NSP{Path: "x", Kind: kind, TitleID: id, AppID: app, Version: v}
	}
	g := &Game{AppID: app,
		Bases:   []*NSP{nsp(KindBase, app, 0)},
		Updates: []*NSP{nsp(KindUpdate, app|0x800, 65536)},
		DLCs:    []*NSP{nsp(KindDLC, dlc1, 0), nsp(KindDLC, dlc3, 0)},
	}
	s := g.Online(db)
	if !s.Known || !s.NewerUpdate() || s.Have != 65536 || s.Latest.Version != 131072 {
		t.Errorf("update status %+v", s)
	}
	if !slices.Equal(s.Missing, []uint64{dlc2}) {
		t.Errorf("missing %X", s.Missing)
	}
	if len(s.Outdated) != 1 || s.Outdated[0].ID != dlc1 || s.Outdated[0].Latest.Version != 65536 {
		t.Errorf("outdated %+v", s.Outdated)
	}
	if s.UpToDate() {
		t.Error("reported up to date")
	}

	// A base with the newest update applied, and all DLCs current.
	g = &Game{AppID: app,
		Bases: []*NSP{nsp(KindBase, app, 131072)},
		DLCs:  []*NSP{nsp(KindDLC, dlc1, 65536), nsp(KindDLC, dlc2, 0), nsp(KindDLC, dlc3, 0)},
	}
	if s := g.Online(db); !s.UpToDate() || s.Have != 131072 {
		t.Errorf("patched base: %+v", s)
	}

	if s := (&Game{AppID: 0x0100000000010000}).Online(db); s.Known || s.UpToDate() {
		t.Errorf("unknown game: %+v", s)
	}
}

// UpToDate reports whether the game has nothing newer or missing.
func (s OnlineStatus) UpToDate() bool {
	return s.Known && !s.NewerUpdate() && len(s.Missing) == 0 && len(s.Outdated) == 0
}
