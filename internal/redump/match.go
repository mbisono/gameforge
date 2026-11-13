package redump

import (
	"fmt"
	"strings"
)

// Track tells the size and CRC of a disc's tracks, for Match. Size is
// cheap; CRC reads the whole track and is used only to tell apart games
// with the same code and size. Track 0 is the whole image (a DVD).
type Track struct {
	Size func(track int) (int64, bool)
	CRC  func(track int) (uint32, error)
	// Regions the disc states (e.g. "USA"), and its revision when the
	// disc tells it (GameCube and Wii: 0 for the first release), or -1.
	Regions []string
	Rev     int
}

// Match finds the Redump name of a disc from its product code. If several
// games share the code, the size and then the CRC of the track the
// database describes decide. It returns "" with the remaining names when
// that is not enough, and "" with none when the code is unknown.
func (db *DB) Match(platform, code string, t Track) (name string, ambiguous []string, err error) {
	games := distinct(db.Lookup(platform, code))
	if len(games) == 0 {
		return "", nil, nil
	}
	if n := names(games); len(n) == 1 {
		return n[0], nil, nil
	}
	// Narrow down by what the disc says, when that leaves any game.
	keep := func(ok func(Game) bool) {
		var same []Game
		for _, g := range games {
			if ok(g) {
				same = append(same, g)
			}
		}
		if len(same) > 0 {
			games = same
		}
	}
	if len(t.Regions) > 0 {
		keep(func(g Game) bool {
			for _, r := range t.Regions {
				if strings.Contains(g.Name, "("+r) || strings.Contains(g.Name, ", "+r) {
					return true
				}
			}
			return false
		})
	}
	if t.Rev >= 0 {
		rev := fmt.Sprintf("(Rev %d)", t.Rev)
		keep(func(g Game) bool {
			if t.Rev == 0 {
				return !strings.Contains(g.Name, "(Rev ")
			}
			return strings.Contains(g.Name, rev)
		})
	}
	if n := names(games); len(n) == 1 {
		return n[0], nil, nil
	}
	if t.Size != nil {
		var same []Game
		for _, g := range games {
			if s, ok := t.Size(g.Track); ok && s == g.Size {
				same = append(same, g)
			}
		}
		if n := names(same); len(n) == 1 {
			return n[0], nil, nil
		}
		if len(same) > 0 {
			games = same
		}
	}
	if t.CRC != nil {
		crcs := map[int]uint32{}
		var same []Game
		for _, g := range games {
			c, ok := crcs[g.Track]
			if !ok {
				if c, err = t.CRC(g.Track); err != nil {
					return "", names(games), err
				}
				crcs[g.Track] = c
			}
			if c == g.CRC {
				same = append(same, g)
			}
		}
		if n := names(same); len(n) == 1 {
			return n[0], nil, nil
		}
		if len(same) > 0 {
			games = same
		}
	}
	return "", names(games), nil
}

// distinct drops repeated entries (the databases list some games twice).
func distinct(games []Game) []Game {
	type key struct {
		name  string
		track int
		size  int64
		crc   uint32
	}
	seen := map[key]bool{}
	var out []Game
	for _, g := range games {
		k := key{g.Name, g.Track, g.Size, g.CRC}
		if !seen[k] {
			seen[k] = true
			out = append(out, g)
		}
	}
	return out
}

// names lists the different names of games, in order.
func names(games []Game) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range games {
		if !seen[g.Name] {
			seen[g.Name] = true
			out = append(out, g.Name)
		}
	}
	return out
}
