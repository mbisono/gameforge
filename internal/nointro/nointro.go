// Package nointro keeps a local copy of the No-Intro cartridge databases
// published by libretro (github.com/libretro/libretro-database): for each
// ROM, its No-Intro name, size and CRC-32. Only those fields are stored,
// in a small cache file; the network is used only when Download is called.
package nointro

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gameforge/internal/datfile"
)

// FileName is the name of the cache file.
const FileName = "nointro.cache"

// Source is where the databases come from, for the user.
const Source = "github.com/libretro/libretro-database (No-Intro)"

// System is a console with a database, and the extensions of its ROMs.
type System struct {
	Name string // as the app shows it
	File string // database file, without ".dat"
	Exts []string
}

// Systems lists the consoles with a database, in display order.
var Systems = []System{
	{"Game Boy", "Nintendo - Game Boy", []string{".gb"}},
	{"Game Boy Color", "Nintendo - Game Boy Color", []string{".gbc"}},
	{"Game Boy Advance", "Nintendo - Game Boy Advance", []string{".gba"}},
	{"NES", "Nintendo - Nintendo Entertainment System", []string{".nes", ".unh", ".unf", ".unif"}},
	{"Famicom Disk", "Nintendo - Family Computer Disk System", []string{".fds", ".qd"}},
	{"SNES", "Nintendo - Super Nintendo Entertainment System", []string{".sfc", ".smc", ".swc", ".fig"}},
	{"Nintendo 64", "Nintendo - Nintendo 64", []string{".z64", ".v64", ".n64"}},
	{"Nintendo DS", "Nintendo - Nintendo DS", []string{".nds"}},
	{"Virtual Boy", "Nintendo - Virtual Boy", []string{".vb", ".vboy"}},
	{"Pokémon Mini", "Nintendo - Pokemon Mini", []string{".min"}},
	{"Mega Drive", "Sega - Mega Drive - Genesis", []string{".md", ".gen"}},
	{"32X", "Sega - 32X", []string{".32x"}},
	{"Master System", "Sega - Master System - Mark III", []string{".sms"}},
	{"Game Gear", "Sega - Game Gear", []string{".gg"}},
	{"SG-1000", "Sega - SG-1000", []string{".sg"}},
	{"PC Engine", "NEC - PC Engine - TurboGrafx 16", []string{".pce"}},
	{"SuperGrafx", "NEC - PC Engine SuperGrafx", []string{".sgx"}},
	{"WonderSwan", "Bandai - WonderSwan", []string{".ws"}},
	{"WonderSwan Color", "Bandai - WonderSwan Color", []string{".wsc"}},
	{"Neo Geo Pocket", "SNK - Neo Geo Pocket", []string{".ngp"}},
	{"Neo Geo Pocket Color", "SNK - Neo Geo Pocket Color", []string{".ngc"}},
	// The single-file .neo format of the NeoSD, the MiSTer and Geolith,
	// whose names do not matter to the emulator (unlike MAME's zip sets).
	{"Neo Geo", "SNK - Neo Geo", []string{".neo"}},
	{"Lynx", "Atari - Lynx", []string{".lnx", ".lyx"}},
	{"Atari 2600", "Atari - 2600", []string{".a26"}},
	{"Atari 5200", "Atari - 5200", []string{".a52"}},
	{"Atari 7800", "Atari - 7800", []string{".a78"}},
	{"Jaguar", "Atari - Jaguar", []string{".j64", ".jag"}},
	{"ColecoVision", "Coleco - ColecoVision", []string{".col"}},
	{"Intellivision", "Mattel - Intellivision", []string{".int"}},
	{"Vectrex", "GCE - Vectrex", []string{".vec"}},
}

// SystemOf returns the console of a ROM extension (e.g. ".gbc"), or "".
// Extensions shared by several consoles, such as ".bin", have none.
func SystemOf(ext string) string {
	ext = strings.ToLower(ext)
	for _, s := range Systems {
		for _, e := range s.Exts {
			if e == ext {
				return s.Name
			}
		}
	}
	return ""
}

// libretroLists are the consoles whose database is not No-Intro's but one
// libretro keeps itself, in its dat folder. Their files have the
// emulator's short names ("mslug.neo"), so the ROMs take the games' names.
var libretroLists = map[string]bool{"Neo Geo": true}

func systemOf(name string) (System, bool) {
	for _, s := range Systems {
		if s.Name == name {
			return s, true
		}
	}
	return System{}, false
}

func fileOf(system string) string {
	s, _ := systemOf(system)
	return s.File
}

// Game is a ROM in a database.
type Game struct {
	Name string // No-Intro name, without extension, e.g. "Tetris (World) (Rev 1)"
	Size int64
	CRC  uint32
}

// List is the database of one console.
type List struct {
	Version string // date of the database, as it states it
	ETag    string
	Games   []Game
}

// DB holds the downloaded databases.
type DB struct {
	Downloaded time.Time
	Lists      map[string]*List // by console

	index map[uint32][]ref
}

type ref struct {
	system string
	game   int
}

// Load reads the cache file; the error wraps os.ErrNotExist if there is none.
func Load(path string) (*DB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	db := &DB{}
	if err := gob.NewDecoder(zr).Decode(db); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	db.build()
	return db, nil
}

// Save writes the cache file, replacing an old one only once complete.
func (db *DB) Save(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), FileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	zw := gzip.NewWriter(tmp)
	err = gob.NewEncoder(zw).Encode(db)
	if err == nil {
		err = zw.Close()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// New makes a database of lists by console (for tests and tools).
func New(lists map[string]*List) *DB {
	db := &DB{Lists: lists, Downloaded: time.Now()}
	db.build()
	return db
}

func (db *DB) build() {
	db.index = map[uint32][]ref{}
	for s, l := range db.Lists {
		for i, g := range l.Games {
			db.index[g.CRC] = append(db.index[g.CRC], ref{s, i})
		}
	}
}

// Has reports whether the database of a console is there.
func (db *DB) Has(system string) bool {
	return db != nil && db.Lists[system] != nil
}

// Match is a ROM found in a database.
type Match struct {
	System string
	Name   string
}

// Find returns the ROMs of all databases with this CRC and size, each
// name once; those of the console system (if any) come first.
func (db *DB) Find(crc uint32, size int64, system string) []Match {
	if db == nil {
		return nil
	}
	var first, rest []Match
	seen := map[string]bool{}
	for _, r := range db.index[crc] {
		g := db.Lists[r.system].Games[r.game]
		if g.Size != size || seen[g.Name] {
			continue
		}
		seen[g.Name] = true
		if r.system == system {
			first = append(first, Match{r.system, g.Name})
		} else {
			rest = append(rest, Match{r.system, g.Name})
		}
	}
	return append(first, rest...)
}

// Download fetches the databases of the consoles and returns them merged
// with old (which may be nil). Files that did not change since old are
// not downloaded again.
func Download(ctx context.Context, old *DB, systems []string, progress datfile.Progress) (*DB, error) {
	db := &DB{Lists: map[string]*List{}}
	if old != nil {
		for s, l := range old.Lists {
			db.Lists[s] = l
		}
	}
	for _, s := range systems {
		sys, ok := systemOf(s)
		if !ok {
			continue
		}
		file, dir := sys.File, "metadat/no-intro/"
		if libretroLists[s] {
			dir = "dat/"
		}
		etag := ""
		if l := db.Lists[s]; l != nil {
			etag = l.ETag
		}
		var list *List
		newTag, err := datfile.Fetch(ctx, datfile.BaseURL+dir+url.PathEscape(file)+".dat", file+".dat", etag, progress, func(r io.Reader) error {
			var err error
			list, err = parse(r, libretroLists[s])
			return err
		})
		if err != nil {
			return nil, err
		}
		if newTag == "" { // not modified
			continue
		}
		list.ETag = newTag
		db.Lists[s] = list
	}
	db.Downloaded = time.Now()
	db.build()
	return db, nil
}

// Parse reads a clrmamepro database: every ROM, with the name of its file
// (the full No-Intro name; the game's name may leave out flags such as
// "(Unl)") without the extension.
func Parse(r io.Reader) (*List, error) { return parse(r, false) }

// parse reads a database as Parse does; with gameNames, each ROM gets the
// name of its game instead.
func parse(r io.Reader, gameNames bool) (*List, error) {
	l := &List{}
	game := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	inGame := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "game (":
			inGame, game = true, ""
		case inGame && strings.HasPrefix(line, "name "):
			game = datfile.Unquote(line[len("name "):])
		case line == ")":
			inGame = false
		case strings.HasPrefix(line, "version ") && !inGame:
			l.Version = datfile.Unquote(line[len("version "):])
		case inGame && strings.HasPrefix(line, "rom ("):
			f := datfile.RomFields(line)
			size, err := strconv.ParseInt(f["size"], 10, 64)
			if err != nil {
				continue
			}
			crc, err := strconv.ParseUint(f["crc"], 16, 32)
			if err != nil {
				continue
			}
			name := f["name"]
			if ext := filepath.Ext(name); len(ext) > 1 && len(ext) <= 6 && !strings.Contains(ext, " ") {
				name = strings.TrimSuffix(name, ext)
			}
			if gameNames {
				// Japanese and Western titles are joined by " / ", which
				// some entries already show as " _ ".
				name = strings.NewReplacer(" / ", " - ", " _ ", " - ").Replace(game)
			}
			if name != "" {
				l.Games = append(l.Games, Game{Name: name, Size: size, CRC: uint32(crc)})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(l.Games) == 0 {
		return nil, fmt.Errorf("no games in the database")
	}
	return l, nil
}
