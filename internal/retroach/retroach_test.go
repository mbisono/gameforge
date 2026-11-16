package retroach

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func md5hex(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func hashOf(t *testing.T, console int, b []byte) string {
	t.Helper()
	h, err := Hash(console, bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashRules(t *testing.T) {
	rom := bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 0x4000) // 128 KiB
	want := md5hex(rom)
	cases := []struct {
		name    string
		console int
		data    []byte
	}{
		{"whole file", GBC, rom},
		{"iNES header", NES, append([]byte("NES\x1a\x08\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00"), rom...)},
		{"headerless NES", NES, rom},
		{"fwNES header", NES, append([]byte("FDS\x1a\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), rom...)},
		{"Lynx header", Lynx, append(append([]byte("LYNX\x00"), make([]byte, 59)...), rom...)},
		{"7800 header", Atari7800, append(append([]byte("\x01ATARI7800"), make([]byte, 118)...), rom...)},
		{"SNES copier header", SNES, append(make([]byte, 512), rom...)},
		{"PC Engine header", PCEngine, append(make([]byte, 512), rom...)},
	}
	for _, c := range cases {
		if got := hashOf(t, c.console, c.data); got != want {
			t.Errorf("%s: %s, want %s", c.name, got, want)
		}
	}
	// Without a header, the size decides nothing is cut.
	if got := hashOf(t, SNES, rom[:0x2000*3]); got != md5hex(rom[:0x2000*3]) {
		t.Error("SNES without header")
	}

	z64 := append([]byte{0x80, 0x37, 0x12, 0x40}, rom[4:]...)
	v64 := append([]byte(nil), z64...)
	for i := 0; i < len(v64); i += 2 {
		v64[i], v64[i+1] = v64[i+1], v64[i]
	}
	n64 := append([]byte(nil), z64...)
	for i := 0; i < len(n64); i += 4 {
		n64[i], n64[i+1], n64[i+2], n64[i+3] = n64[i+3], n64[i+2], n64[i+1], n64[i]
	}
	for name, b := range map[string][]byte{"z64": z64, "v64": v64, "n64": n64} {
		if got := hashOf(t, N64, b); got != md5hex(z64) {
			t.Errorf("N64 %s: %s", name, got)
		}
	}
}

func TestHashNDS(t *testing.T) {
	rom := make([]byte, 0x8000)
	put := func(at int, v uint32) { binary.LittleEndian.PutUint32(rom[at:], v) }
	put(0x20, 0x4000) // ARM9 at 0x4000, 0x1000 bytes
	put(0x2C, 0x1000)
	put(0x30, 0x5000) // ARM7 at 0x5000, 0x800 bytes
	put(0x3C, 0x800)
	put(0x68, 0x6000) // icon
	for i := 0x4000; i < len(rom); i++ {
		rom[i] = byte(i * 7)
	}
	var want bytes.Buffer
	want.Write(rom[:0x160])
	want.Write(rom[0x4000:0x5000])
	want.Write(rom[0x5000:0x5800])
	want.Write(rom[0x6000:0x6A00])
	if got := hashOf(t, NDS, rom); got != md5hex(want.Bytes()) {
		t.Errorf("NDS: %s", got)
	}
	// Behind a SuperCard header, the same hash.
	sc := make([]byte, 512)
	copy(sc, []byte{0x2E, 0, 0, 0xEA})
	copy(sc[0xB0:], []byte{0x44, 0x46, 0x96, 0})
	if got := hashOf(t, NDS, append(sc, rom...)); got != md5hex(want.Bytes()) {
		t.Errorf("NDS with SuperCard header: %s", got)
	}
}

const sampleList = `[{"Title":"Pokemon Crystal Version","ID":"1234","ConsoleID":6,"ConsoleName":"Game Boy Color","NumAchievements":53,"Hashes":["ABCDEF0123456789ABCDEF0123456789"]},
{"Title":"Tetris DX","ID":5678,"NumAchievements":0,"Hashes":["00112233445566778899aabbccddeeff"]}]`

func TestDownloadFindCache(t *testing.T) {
	const key = "secret-key-123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/API_GetGameList.php" || r.URL.Query().Get("h") != "1" || r.Header.Get("User-Agent") == "" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("y") {
		case key:
			w.Write([]byte(sampleList))
		case "old":
			w.Write([]byte(`{"message":"Unauthenticated."}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	old := BaseURL
	BaseURL = srv.URL + "/"
	defer func() { BaseURL = old }()

	db, err := Download(context.Background(), nil, key, []int{GBC}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := db.Find(GBC, "abcdef0123456789abcdef0123456789")
	if !ok || g.Title != "Pokemon Crystal Version" || g.ID != 1234 || g.Achievements != 53 {
		t.Errorf("find: %+v %v", g, ok)
	}
	if g, ok := db.Find(GameBoy, "00112233445566778899AABBCCDDEEFF"); !ok || g.Title != "Tetris DX" {
		t.Errorf("related console: %+v %v", g, ok)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := db.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !loaded.Has(GBC) || loaded.Has(NES) {
		t.Fatalf("load: %v", err)
	}
	if _, ok := loaded.Find(GBC, "abcdef0123456789abcdef0123456789"); !ok {
		t.Error("after loading")
	}

	for _, bad := range []string{"wrong", "old"} {
		_, err := Download(context.Background(), nil, bad, []int{GBC}, nil)
		if !errors.Is(err, ErrKey) {
			t.Errorf("key %q: %v", bad, err)
		}
	}
	// The key never shows in errors.
	BaseURL = "http://127.0.0.1:1/"
	_, err = Download(context.Background(), nil, key, []int{GBC}, nil)
	if err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("unreachable server: %v", err)
	}
}

func TestFindTitle(t *testing.T) {
	db := New(map[int]*List{
		PSP: {Games: []Game{
			{ID: 1, Title: "God of War: Chains of Olympus", Achievements: 40, Hashes: []string{"799b09290b80129d6d0a880f7bac59b6"}},
			{ID: 2, Title: "~Hack~ God of War: Chains of Olympus", Achievements: 10},
			{ID: 3, Title: "Pocket Monsters | Pokemon", Achievements: 5},
		}},
		GameBoy: {Games: []Game{{ID: 4, Title: "Legend of Zelda, The: Link's Awakening", Achievements: 50}}},
	})
	for _, c := range []struct {
		console int
		name    string
		want    int
	}{
		{PSP, "God of War - Chains of Olympus (USA)", 1},
		{PSP, "god of war chains of olympus [!]", 1},
		{PSP, "Pokemon (Japan)", 3},
		{GBC, "Legend of Zelda, The - Link's Awakening (USA, Europe) (Rev 2)", 4}, // related console
		{PSP, "God of War", 0},
		{PSP, "", 0},
	} {
		g, ok := db.FindTitle(c.console, c.name)
		if got := map[bool]int{true: g.ID}[ok]; got != c.want {
			t.Errorf("%q: game %d, want %d", c.name, got, c.want)
		}
	}
}
