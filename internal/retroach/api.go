package retroach

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FileName is the name of the cache file.
const FileName = "retroachievements.cache"

// BaseURL is the RetroAchievements web API.
var BaseURL = "https://retroachievements.org/API/"

// UserAgent identifies the app to the API, as RetroAchievements asks.
var UserAgent = "GameForge"

// Game is a game of RetroAchievements and the hashes of its versions.
type Game struct {
	ID           int
	Title        string
	Achievements int
	Hashes       []string // lower case
}

// List is the game list of one console.
type List struct {
	Downloaded time.Time
	Games      []Game
}

// DB holds the downloaded lists.
type DB struct {
	Lists map[int]*List // by console

	index  map[int]map[string]int // console → hash → game
	titles map[int]map[string]int // console → normalized title → game
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
func New(lists map[int]*List) *DB {
	db := &DB{Lists: lists}
	db.build()
	return db
}

func (db *DB) build() {
	db.index = map[int]map[string]int{}
	db.titles = map[int]map[string]int{}
	for c, l := range db.Lists {
		m, t := map[string]int{}, map[string]int{}
		for i, g := range l.Games {
			for _, h := range g.Hashes {
				m[h] = i
			}
			// Hacks, homebrew, demos and the like ("~Hack~ …") are other
			// games; alternative titles are separated by " | ".
			if strings.Contains(g.Title, "~") {
				continue
			}
			for _, part := range strings.Split(g.Title, "|") {
				if k := TitleKey(part); k != "" {
					if _, dup := t[k]; !dup {
						t[k] = i
					}
				}
			}
		}
		db.index[c], db.titles[c] = m, t
	}
}

// TitleKey reduces a game's name to compare names from different sources:
// "God of War - Chains of Olympus (USA)" and "God of War: Chains of
// Olympus" give the same key. Parentheses and brackets (regions,
// languages, revisions), punctuation, case and the word "the" are left out.
func TitleKey(name string) string {
	var b strings.Builder
	depth := 0
	for _, r := range strings.ToLower(name) {
		switch {
		case r == '(' || r == '[':
			depth++
		case r == ')' || r == ']':
			depth = max(depth-1, 0)
		case depth > 0:
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127:
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	var words []string
	for _, w := range strings.Fields(b.String()) {
		if w != "the" {
			words = append(words, w)
		}
	}
	return strings.Join(words, "")
}

// FindTitle returns the game of a console (or a related one) whose title
// matches one of the names, for files whose version is not recognized.
func (db *DB) FindTitle(console int, names ...string) (Game, bool) {
	if db == nil {
		return Game{}, false
	}
	for _, name := range names {
		k := TitleKey(name)
		if k == "" {
			continue
		}
		for _, c := range append([]int{console}, Related[console]...) {
			if i, ok := db.titles[c][k]; ok {
				return db.Lists[c].Games[i], true
			}
		}
	}
	return Game{}, false
}

// Has reports whether the list of a console is there.
func (db *DB) Has(console int) bool {
	return db != nil && db.Lists[console] != nil
}

// Find returns the game with this hash in the list of a console, or of a
// related one already downloaded.
func (db *DB) Find(console int, hash string) (Game, bool) {
	if db == nil {
		return Game{}, false
	}
	hash = strings.ToLower(hash)
	for _, c := range append([]int{console}, Related[console]...) {
		if i, ok := db.index[c][hash]; ok {
			return db.Lists[c].Games[i], true
		}
	}
	return Game{}, false
}

// ErrKey is returned when the API refuses the key.
var ErrKey = errors.New("the RetroAchievements API refused the key")

var client = &http.Client{Timeout: 2 * time.Minute}

// Download fetches the game lists of the consoles, with the hashes of
// every game, and returns them merged with old (which may be nil). The
// key is sent only to the API and never appears in errors.
func Download(ctx context.Context, old *DB, key string, consoles []int, progress func(console int)) (*DB, error) {
	db := &DB{Lists: map[int]*List{}}
	if old != nil {
		for c, l := range old.Lists {
			db.Lists[c] = l
		}
	}
	for _, c := range consoles {
		if progress != nil {
			progress(c)
		}
		games, err := fetchList(ctx, key, c)
		if err != nil {
			return nil, err
		}
		db.Lists[c] = &List{Downloaded: time.Now(), Games: games}
	}
	db.build()
	return db, nil
}

func fetchList(ctx context.Context, key string, console int) ([]Game, error) {
	q := url.Values{"i": {strconv.Itoa(console)}, "h": {"1"}, "y": {key}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+"API_GetGameList.php?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("invalid request")
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		// url.Error holds the address, with the key: keep only the cause.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("RetroAchievements: %s", redact(err.Error(), key))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrKey
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("RetroAchievements: %s", resp.Status)
	}
	var raw []struct {
		ID              json.Number
		Title           string
		NumAchievements json.Number
		Hashes          []string
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("RetroAchievements: %s", redact(err.Error(), key))
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		// Errors come as an object, e.g. {"message": "Unauthenticated."}.
		var obj map[string]any
		if json.Unmarshal(body, &obj) == nil {
			text := fmt.Sprint(obj["message"], " ", obj["error"])
			if strings.Contains(strings.ToLower(text), "auth") || strings.Contains(strings.ToLower(text), "key") {
				return nil, ErrKey
			}
			return nil, fmt.Errorf("RetroAchievements: %s", redact(strings.TrimSpace(strings.ReplaceAll(text, "<nil>", "")), key))
		}
		return nil, fmt.Errorf("RetroAchievements: unexpected answer (%s)", redact(err.Error(), key))
	}
	games := make([]Game, 0, len(raw))
	for _, r := range raw {
		id, _ := r.ID.Int64()
		n, _ := r.NumAchievements.Int64()
		g := Game{ID: int(id), Title: r.Title, Achievements: int(n)}
		for _, h := range r.Hashes {
			g.Hashes = append(g.Hashes, strings.ToLower(h))
		}
		games = append(games, g)
	}
	return games, nil
}

func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "…")
}

// HashesURL is the page listing the versions a game supports.
func HashesURL(id int) string { return GameURL(id) + "/hashes" }

// GameURL is the page of a game on RetroAchievements.
func GameURL(id int) string {
	return "https://retroachievements.org/game/" + strconv.Itoa(id)
}
