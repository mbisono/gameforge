// Package redump keeps a local copy of the Redump disc databases published
// by libretro (github.com/libretro/libretro-database, CC BY-SA 4.0): for
// each game, its Redump name, product codes and the size and CRC of its
// main data track. Only those fields are stored, in a small cache file;
// the network is used only when Download is called.
package redump

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
	"regexp"
	"strconv"
	"strings"
	"time"

	"gameforge/internal/datfile"
)

// FileName is the name of the cache file.
const FileName = "redump.cache"

// Source is where the databases come from, for the user.
const Source = "github.com/libretro/libretro-database (Redump, CC BY-SA 4.0)"

// Platforms lists the platforms with a database, in display order.
var Platforms = []string{"PS1", "PS2", "PSP", "Saturn", "Sega CD", "Dreamcast", "GameCube", "Wii"}

// Files names the database of each platform (as discinfo names them).
var Files = map[string]string{
	"PS1":       "Sony - PlayStation",
	"PS2":       "Sony - PlayStation 2",
	"PSP":       "Sony - PlayStation Portable",
	"Dreamcast": "Sega - Dreamcast",
	"Saturn":    "Sega - Saturn",
	"Sega CD":   "Sega - Mega-CD - Sega CD",
	"GameCube":  "Nintendo - GameCube",
	"Wii":       "Nintendo - Wii",
}

// Game is a disc in a database.
type Game struct {
	Name  string   // Redump name, e.g. "Ridge Racer (Europe)"
	Codes []string // product codes, normalized (see Code)
	Track int      // track the size and CRC are of; 0 for a single image file
	Size  int64
	CRC   uint32
}

// List is the database of one platform.
type List struct {
	Version string // date of the database, as it states it
	ETag    string
	Games   []Game
}

// DB holds the downloaded databases.
type DB struct {
	Downloaded time.Time
	Lists      map[string]*List // by platform

	index map[string]map[string][]int // platform → code → games
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

func (db *DB) build() {
	db.index = map[string]map[string][]int{}
	for p, l := range db.Lists {
		m := map[string][]int{}
		for i, g := range l.Games {
			for _, c := range g.Codes {
				m[c] = append(m[c], i)
			}
		}
		db.index[p] = m
	}
}

// Has reports whether the database of a platform is there.
func (db *DB) Has(platform string) bool {
	return db != nil && db.Lists[platform] != nil
}

var (
	sonyCode     = regexp.MustCompile(`^([A-Z]{4})[-_ ]?(\d{3})\.?(\d{2})`)
	nintendoCode = regexp.MustCompile(`(?:DOL|RVL)-([A-Z0-9]{4})`)
	nonAlnum     = regexp.MustCompile(`[^A-Z0-9]+`)
)

// Code normalizes a product code for comparison: Sony codes become their
// letters and five digits (SLES-01324/P → SLES01324), Nintendo codes the
// four-letter game ID (DL-DOL-GALE-USA or GALE01 → GALE), others their
// letters and digits (T-46603D-50 → T46603D50).
func Code(platform, s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch platform {
	case "PS1", "PS2", "PSP":
		if m := sonyCode.FindStringSubmatch(s); m != nil {
			return m[1] + m[2] + m[3]
		}
	case "GameCube", "Wii":
		if m := nintendoCode.FindStringSubmatch(s); m != nil {
			return m[1]
		}
		if len(s) == 6 || len(s) == 4 {
			return s[:4]
		}
	}
	return nonAlnum.ReplaceAllString(s, "")
}

// Lookup returns the games of a platform with a product code. Codes that
// differ only by a suffix (a Sega code with or without "-50") also match.
func (db *DB) Lookup(platform, code string) []Game {
	if !db.Has(platform) {
		return nil
	}
	key := Code(platform, code)
	if key == "" {
		return nil
	}
	l := db.Lists[platform]
	var out []Game
	for _, i := range db.index[platform][key] {
		out = append(out, l.Games[i])
	}
	if len(out) > 0 || len(key) < 5 {
		return out
	}
	for c, idx := range db.index[platform] {
		if len(c) >= 5 && (strings.HasPrefix(c, key) || strings.HasPrefix(key, c)) {
			for _, i := range idx {
				out = append(out, l.Games[i])
			}
		}
	}
	return out
}

// Progress reports the download of a database file; total is -1 if the
// server did not state it.
type Progress = datfile.Progress

// Download fetches the databases of the platforms and returns them merged
// with old (which may be nil). Files that did not change since old are
// not downloaded again.
func Download(ctx context.Context, old *DB, platforms []string, progress Progress) (*DB, error) {
	db := &DB{Lists: map[string]*List{}}
	if old != nil {
		for p, l := range old.Lists {
			db.Lists[p] = l
		}
	}
	for _, p := range platforms {
		file, ok := Files[p]
		if !ok {
			continue
		}
		etag := ""
		if l := db.Lists[p]; l != nil {
			etag = l.ETag
		}
		var list *List
		newTag, err := datfile.Fetch(ctx, datfile.BaseURL+"metadat/redump/"+url.PathEscape(file)+".dat", file+".dat", etag, progress, func(r io.Reader) error {
			var err error
			list, err = Parse(p, r)
			return err
		})
		if err != nil {
			return nil, err
		}
		if newTag == "" { // not modified
			continue
		}
		list.ETag = newTag
		db.Lists[p] = list
	}
	db.Downloaded = time.Now()
	db.build()
	return db, nil
}

var trackNum = regexp.MustCompile(`\(Track (\d+)\)\.\w+$`)

// Parse reads a clrmamepro database: per game, the name, the product
// codes and the first file (track 1 of a CD, track 3 of a GD-ROM, or the
// image of a DVD).
func Parse(platform string, r io.Reader) (*List, error) {
	l := &List{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var g *Game
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "game (":
			g = &Game{}
		case line == ")" && g != nil:
			if g.Name != "" {
				l.Games = append(l.Games, *g)
			}
			g = nil
		case strings.HasPrefix(line, "version ") && g == nil:
			l.Version = datfile.Unquote(line[len("version "):])
		case g == nil:
		case strings.HasPrefix(line, "name "):
			g.Name = datfile.Unquote(line[len("name "):])
		case strings.HasPrefix(line, "serial "):
			addCodes(g, platform, datfile.Unquote(line[len("serial "):]))
		case strings.HasPrefix(line, "rom (") && g.Size == 0:
			f := datfile.RomFields(line)
			g.Size, _ = strconv.ParseInt(f["size"], 10, 64)
			if c, err := strconv.ParseUint(f["crc"], 16, 32); err == nil {
				g.CRC = uint32(c)
			}
			if m := trackNum.FindStringSubmatch(f["name"]); m != nil {
				g.Track, _ = strconv.Atoi(m[1])
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

func addCodes(g *Game, platform, s string) {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSuffix(strings.TrimSpace(part), "*")
		c := Code(platform, part)
		if c == "" {
			continue
		}
		dup := false
		for _, have := range g.Codes {
			dup = dup || have == c
		}
		if !dup {
			g.Codes = append(g.Codes, c)
		}
	}
}
