// Package titledb keeps a local copy of the public titledb game database
// (github.com/blawar/titledb): the update versions of every game and the
// names of games and DLCs. Only the fields gameforge uses are stored, in a
// small cache file; the network is used only when Download is called.
package titledb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"gameforge/internal/cachefile"
	"gameforge/internal/datfile"
)

// FileName is the name of the cache file.
const FileName = "titledb.cache"

// Sources, in download order: the update versions (small) and the English
// catalog with names (large).
var sources = [2]string{
	"https://raw.githubusercontent.com/blawar/titledb/master/versions.json",
	"https://raw.githubusercontent.com/blawar/titledb/master/US.en.json",
}

// Release is one published version of a title.
type Release struct {
	Version uint32
	Date    string // "2006-01-02", as titledb states it
}

// DB is the local game database.
type DB struct {
	Downloaded time.Time // last successful download or check
	ETags      [2]string // of sources, to skip unchanged files
	// Versions lists the releases of base games (their updates) and DLCs
	// by title ID, oldest first.
	Versions map[uint64][]Release
	// Names of base games and DLCs by title ID.
	Names map[uint64]string

	dlcs map[uint64][]uint64 // DLC title IDs by base game, sorted
}

// Load reads the cache file; the error wraps os.ErrNotExist if there is none.
func Load(path string) (*DB, error) {
	db := &DB{}
	if err := cachefile.Load(path, db); err != nil {
		return nil, err
	}
	db.index()
	return db, nil
}

// Save writes the cache file, replacing an old one only once complete.
func (db *DB) Save(path string) error { return cachefile.Save(path, db) }

// New returns a database with the given data, as if downloaded now.
func New(versions map[uint64][]Release, names map[uint64]string) *DB {
	db := &DB{Downloaded: time.Now(), Versions: versions, Names: names}
	db.index()
	return db
}

func (db *DB) index() {
	db.dlcs = map[uint64][]uint64{}
	for id := range db.Names {
		if app, ok := dlcApp(id); ok {
			db.dlcs[app] = append(db.dlcs[app], id)
		}
	}
	for _, l := range db.dlcs {
		sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	}
}

// dlcApp returns the base game of a DLC title ID; ok is false for base
// games and updates.
func dlcApp(id uint64) (app uint64, ok bool) {
	if low := id & 0xFFF; low == 0 || low == 0x800 {
		return 0, false
	}
	return (id - 0x1000) &^ 0xFFF, true
}

// Known reports whether the database has any data on the base game app.
func (db *DB) Known(app uint64) bool {
	_, v := db.Versions[app]
	_, n := db.Names[app]
	return v || n
}

// Latest returns the newest release of a base game (its newest update) or
// of a DLC; ok is false if none is listed.
func (db *DB) Latest(id uint64) (r Release, ok bool) {
	l := db.Versions[id]
	if len(l) == 0 {
		return r, false
	}
	return l[len(l)-1], true
}

// DLCs returns the title IDs of all DLCs listed for the base game app.
func (db *DB) DLCs(app uint64) []uint64 { return db.dlcs[app] }

// Games returns the number of base games with a name.
func (db *DB) Games() int {
	n := 0
	for id := range db.Names {
		if id&0xFFF == 0 {
			n++
		}
	}
	return n
}

// Name returns the English name of a title, or "".
func (db *DB) Name(id uint64) string { return db.Names[id] }

// DataDate returns the newest release date in the database, which shows
// how current it is, or "".
func (db *DB) DataDate() string {
	newest := ""
	for _, l := range db.Versions {
		for _, r := range l {
			if r.Date > newest {
				newest = r.Date
			}
		}
	}
	return newest
}

// Progress reports download progress of one source file; total is -1 if
// the server did not state it.
type Progress func(file string, done, total int64)

// Download fetches the database. With old set, files that did not change
// since are not downloaded again and their data is taken from old.
func Download(ctx context.Context, old *DB, progress Progress) (*DB, error) {
	db := &DB{Versions: map[uint64][]Release{}, Names: map[uint64]string{}}
	parsers := [2]func(io.Reader) error{db.parseVersions, db.parseCatalog}
	for i, url := range sources {
		etag := ""
		if old != nil {
			etag = old.ETags[i]
		}
		newTag, err := datfile.Fetch(ctx, url, url[strings.LastIndex(url, "/")+1:], etag, datfile.Progress(progress), parsers[i])
		if err != nil {
			return nil, err
		}
		if newTag == "" { // not modified
			db.ETags[i] = etag
			if i == 0 {
				db.Versions = old.Versions
			} else {
				db.Names = old.Names
			}
			continue
		}
		db.ETags[i] = newTag
	}
	db.Downloaded = time.Now()
	db.index()
	return db, nil
}

func parseID(s string) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(s), 16, 64)
	return id, err == nil && id != 0
}

// parseVersions reads versions.json: {"<title id>": {"<version>": "<date>"}}.
func (db *DB) parseVersions(r io.Reader) error {
	var raw map[string]map[string]string
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return err
	}
	for k, vs := range raw {
		id, ok := parseID(k)
		if !ok {
			continue
		}
		var l []Release
		for v, date := range vs {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				continue
			}
			l = append(l, Release{Version: uint32(n), Date: date})
		}
		if len(l) == 0 {
			continue
		}
		sort.Slice(l, func(i, j int) bool { return l[i].Version < l[j].Version })
		db.Versions[id] = l
	}
	if len(db.Versions) == 0 {
		return errors.New("no versions")
	}
	return nil
}

// parseCatalog reads a titledb region file, {"<nsuId>": {"id": ..., "name":
// ..., ...}}, entry by entry, keeping only title IDs and names.
func (db *DB) parseCatalog(r io.Reader) error {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil {
		return err
	} else if t != json.Delim('{') {
		return errors.New("not a JSON object")
	}
	for dec.More() {
		if _, err := dec.Token(); err != nil { // the key
			return err
		}
		var e struct {
			ID     *string `json:"id"`
			Name   string  `json:"name"`
			IsDemo bool    `json:"isDemo"`
		}
		if err := dec.Decode(&e); err != nil {
			return err
		}
		if e.ID == nil || e.Name == "" || e.IsDemo {
			continue
		}
		if id, ok := parseID(*e.ID); ok {
			if _, dup := db.Names[id]; !dup {
				db.Names[id] = strings.TrimSpace(e.Name)
			}
		}
	}
	if len(db.Names) == 0 {
		return errors.New("no titles")
	}
	return nil
}
