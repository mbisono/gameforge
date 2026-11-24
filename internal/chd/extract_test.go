package chd

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// roundTrip converts a sheet or an ISO to a CHD and extracts it again to
// outName in another folder.
func roundTrip(t *testing.T, in string, convert func(context.Context, string, string, Options) (*Result, error), outName string) (string, *ExtractResult) {
	t.Helper()
	chdPath := filepath.Join(t.TempDir(), "disc.chd")
	if _, err := convert(context.Background(), in, chdPath, Options{Workers: 2}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), outName)
	res, err := Extract(context.Background(), chdPath, out, ExtractOptions{Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(out), "*.part")); len(left) > 0 {
		t.Fatalf(".part files left: %v", left)
	}
	return out, res
}

func sameFile(t *testing.T, got, want string) {
	t.Helper()
	a, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("%s differs from %s (%d and %d bytes)", filepath.Base(got), filepath.Base(want), len(a), len(b))
	}
}

func randomBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.IntN(256))
	}
	return b
}

// TestExtractCue gets back a Redump CUE sheet and its BIN files, with an
// audio track whose pregap is in its file.
func TestExtractCue(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(21, 22))
	var data []byte
	for i := range 503 {
		data = append(data, mode1Sector(rng, i+150)...)
	}
	os.WriteFile(filepath.Join(dir, "Game (Track 1).bin"), data, 0o644)
	os.WriteFile(filepath.Join(dir, "Game (Track 2).bin"), randomBytes(rng, 437*SectorSize), 0o644)
	os.WriteFile(filepath.Join(dir, "Game (Track 3).bin"), randomBytes(rng, 151*SectorSize), 0o644)
	cue := "FILE \"Game (Track 1).bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n" +
		"FILE \"Game (Track 2).bin\" BINARY\r\n  TRACK 02 AUDIO\r\n    INDEX 00 00:00:00\r\n    INDEX 01 00:02:00\r\n" +
		"FILE \"Game (Track 3).bin\" BINARY\r\n  TRACK 03 AUDIO\r\n    INDEX 00 00:00:00\r\n    INDEX 01 00:01:00\r\n"
	in := filepath.Join(dir, "Game.cue")
	os.WriteFile(in, []byte(cue), 0o644)

	out, res := roundTrip(t, in, ConvertCue, "Game.cue")
	if res.Tracks != 3 || len(res.Files) != 4 || res.Files[0] != out {
		t.Fatalf("result %+v", res)
	}
	sameFile(t, out, in)
	for _, n := range []string{"Game (Track 1).bin", "Game (Track 2).bin", "Game (Track 3).bin"} {
		sameFile(t, filepath.Join(filepath.Dir(out), n), filepath.Join(dir, n))
	}
	var size int64
	for _, f := range res.Files {
		st, _ := os.Stat(f)
		size += st.Size()
	}
	if size != res.OutputSize {
		t.Errorf("output size %d, files %d", res.OutputSize, size)
	}

	// Again: nothing is written over.
	chdPath := filepath.Join(t.TempDir(), "again.chd")
	ConvertCue(context.Background(), in, chdPath, Options{})
	var exists *ExistsError
	if _, err := Extract(context.Background(), chdPath, out, ExtractOptions{}); !errors.As(err, &exists) || exists.Name != "Game.cue" {
		t.Errorf("existing files: %v", err)
	}
}

// TestExtractPregap writes a pregap that is not stored as a PREGAP command,
// and a track of a single BIN file in a file of its own.
func TestExtractPregap(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(23, 24))
	img := randomBytes(rng, (300+200)*SectorSize)
	os.WriteFile(filepath.Join(dir, "disc.bin"), img, 0o644)
	cue := "FILE \"disc.bin\" BINARY\r\n TRACK 01 MODE1/2352\r\n  INDEX 01 00:00:00\r\n" +
		" TRACK 02 AUDIO\r\n  PREGAP 00:02:00\r\n  INDEX 01 00:04:00\r\n"
	in := filepath.Join(dir, "disc.cue")
	os.WriteFile(in, []byte(cue), 0o644)
	out, _ := roundTrip(t, in, ConvertCue, "disc.cue")
	got, _ := os.ReadFile(out)
	want := "FILE \"disc (Track 1).bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n" +
		"FILE \"disc (Track 2).bin\" BINARY\r\n  TRACK 02 AUDIO\r\n    PREGAP 00:02:00\r\n    INDEX 01 00:00:00\r\n"
	if string(got) != want {
		t.Errorf("sheet:\n%s", got)
	}
	t1, _ := os.ReadFile(filepath.Join(filepath.Dir(out), "disc (Track 1).bin"))
	t2, _ := os.ReadFile(filepath.Join(filepath.Dir(out), "disc (Track 2).bin"))
	if !bytes.Equal(append(t1, t2...), img) {
		t.Error("tracks differ from the image")
	}
}

// TestExtractGDI gets back a .gdi and its tracks, with the gaps between
// the tracks.
func TestExtractGDI(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(25, 26))
	var data []byte
	for i := range 300 {
		data = append(data, mode1Sector(rng, i+150)...)
	}
	names := []string{"Game (Track 1).bin", "Game (Track 2).bin", "Game (Track 3).bin"}
	os.WriteFile(filepath.Join(dir, names[0]), data, 0o644)
	os.WriteFile(filepath.Join(dir, names[1]), randomBytes(rng, 210*SectorSize), 0o644)
	os.WriteFile(filepath.Join(dir, names[2]), data[:100*SectorSize], 0o644)
	gdi := "3\r\n1 0 4 2352 \"Game (Track 1).bin\" 0\r\n2 450 0 2352 \"Game (Track 2).bin\" 0\r\n" +
		"3 2000 4 2352 \"Game (Track 3).bin\" 0\r\n"
	in := filepath.Join(dir, "Game.gdi")
	os.WriteFile(in, []byte(gdi), 0o644)
	out, res := roundTrip(t, in, ConvertGDI, "Game.gdi")
	if res.Tracks != 3 {
		t.Fatalf("result %+v", res)
	}
	sameFile(t, out, in)
	for _, n := range names {
		sameFile(t, filepath.Join(filepath.Dir(out), n), filepath.Join(dir, n))
	}
}

// TestExtractISO gets back an ISO from a DVD CHD and from a CD CHD made
// from an ISO.
func TestExtractISO(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(27, 28))
	in := filepath.Join(dir, "game.iso")
	os.WriteFile(in, randomBytes(rng, 2049*DVDSectorSize), 0o644)
	for _, conv := range []func(context.Context, string, string, Options) (*Result, error){ConvertISO, ConvertISOAsCD} {
		out, res := roundTrip(t, in, conv, "game.iso")
		if res.Tracks != 1 || len(res.Files) != 1 {
			t.Errorf("result %+v", res)
		}
		sameFile(t, out, in)
	}
	chdPath := filepath.Join(t.TempDir(), "cd.chd")
	ConvertISOAsCD(context.Background(), in, chdPath, Options{})
	d, err := OpenDisc(chdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.DefaultExt() != ".iso" || !d.CanISO() {
		t.Errorf("CD from an ISO: %s", d.DefaultExt())
	}
	if _, err := Extract(context.Background(), chdPath, filepath.Join(t.TempDir(), "x.cue"), ExtractOptions{}); err != nil {
		t.Errorf("as a CUE sheet: %v", err)
	}
}

// TestExtractRawISO keeps only the user data of a raw data track.
func TestExtractRawISO(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(29, 30))
	var img, user []byte
	for i := range 200 {
		s := mode1Sector(rng, i+150)
		img = append(img, s...)
		user = append(user, s[16:16+DVDSectorSize]...)
	}
	os.WriteFile(filepath.Join(dir, "g.bin"), img, 0o644)
	in := filepath.Join(dir, "g.cue")
	os.WriteFile(in, []byte("FILE \"g.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n"), 0o644)
	out, _ := roundTrip(t, in, ConvertCue, "g.iso")
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, user) {
		t.Error("ISO differs from the user data")
	}
}
