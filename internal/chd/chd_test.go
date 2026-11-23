package chd

import (
	"bytes"
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mode1Sector(rng *rand.Rand, lba int) []byte {
	s := make([]byte, SectorSize)
	copy(s, syncHeader[:])
	s[12], s[13], s[14], s[15] = byte(lba/4500), byte(lba/75%60), byte(lba%75), 1
	for i := 16; i < 2064; i++ {
		s[i] = byte(rng.IntN(4)) // compressible
	}
	// EDC is not checked by the codec; leave it zero.
	eccGenerate(s)
	return s
}

func TestECC(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	s := mode1Sector(rng, 1234)
	if !eccVerify(s) {
		t.Fatal("generated parity does not verify")
	}
	c := bytes.Clone(s)
	clear(c[:12])
	eccClear(c)
	copy(c, syncHeader[:])
	eccGenerate(c)
	if !bytes.Equal(c, s) {
		t.Fatal("regenerated sector differs")
	}
	s[100] ^= 1
	if eccVerify(s) {
		t.Fatal("corrupted sector verifies")
	}
}

func TestECCMode2(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 14))
	s := make([]byte, SectorSize)
	copy(s, syncHeader[:])
	s[12], s[13], s[14], s[15] = 0x00, 0x02, 0x16, 2
	for i := 16; i < 2072; i++ {
		s[i] = byte(rng.IntN(256))
	}
	eccGenerate(s)
	if !eccVerify(s) {
		t.Fatal("Mode 2 parity does not verify")
	}
	// The header does not count: changing it keeps the parity valid.
	c := bytes.Clone(s)
	c[12], c[13], c[14] = 0x55, 0x66, 0x77
	if !eccVerify(c) {
		t.Fatal("Mode 2 parity depends on the header")
	}
	c[2000] ^= 1
	if eccVerify(c) {
		t.Fatal("corrupted Mode 2 sector verifies")
	}
}

func TestMapRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{1, 2, 5, 40, 1000, 5000} {
		var entries []mapEntry
		pos := uint64(500)
		for i := range n {
			var e mapEntry
			switch r := rng.IntN(10); {
			case i > 0 && r < 3:
				e = mapEntry{typ: compSelf, offset: uint64(rng.IntN(i))}
			case i > 0 && r < 4 && entries[i-1].typ == compSelf && entries[i-1].offset+1 < uint64(i):
				e = mapEntry{typ: compSelf, offset: entries[i-1].offset + 1}
			case r < 5:
				e = mapEntry{typ: compNone, length: 4096, offset: pos, crc: uint16(rng.Uint32())}
			default:
				e = mapEntry{typ: uint8(rng.IntN(2)), length: uint32(1 + rng.IntN(4000)), offset: pos, crc: uint16(rng.Uint32())}
			}
			if i > 10 && i < 60 { // a long run
				e = mapEntry{typ: compType1, length: 77, offset: pos, crc: 9}
			}
			if e.typ != compSelf {
				e.offset = pos
				pos += uint64(e.length)
			}
			entries = append(entries, e)
		}
		enc := encodeMap(entries, 500)
		dec, err := decodeMap(enc[:mapHeaderSize], enc[mapHeaderSize:], n, 4096)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for i := range entries {
			if dec[i] != entries[i] {
				t.Fatalf("n=%d hunk %d: got %+v want %+v", n, i, dec[i], entries[i])
			}
		}
	}
}

// readAll decodes every hunk of a CHD, up to its logical size.
func readAll(t *testing.T, path string) (*Reader, []byte) {
	t.Helper()
	if _, err := Verify(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	var out []byte
	buf := make([]byte, r.HunkBytes)
	for n := range r.Hunks() {
		if err := r.ReadHunk(n, buf); err != nil {
			t.Fatal(err)
		}
		out = append(out, buf...)
	}
	return r, out[:r.LogicalBytes]
}

func TestConvertCue(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(5, 6))

	// Track 1: data, mostly Mode 1 sectors with valid parity, some damaged.
	var data []byte
	const dataFrames = 1003
	for i := range dataFrames {
		s := mode1Sector(rng, i+150)
		if i%50 == 7 {
			s[2000] ^= 0xff
		}
		if i >= 900 {
			clear(s) // repeated hunks
		}
		data = append(data, s...)
	}
	// Track 2: audio with a 2-second pregap in the file.
	audio := make([]byte, 437*SectorSize)
	for i := range audio {
		audio[i] = byte(rng.IntN(256))
	}
	os.WriteFile(filepath.Join(dir, "game (Track 1).bin"), data, 0o644)
	os.WriteFile(filepath.Join(dir, "game (Track 2).bin"), audio, 0o644)
	cue := `FILE "game (Track 1).bin" BINARY
  TRACK 01 MODE2/2352
    INDEX 01 00:00:00
FILE "game (Track 2).bin" BINARY
  TRACK 02 AUDIO
    INDEX 00 00:00:00
    INDEX 01 00:02:00
`
	cuePath := filepath.Join(dir, "game.cue")
	os.WriteFile(cuePath, []byte(cue), 0o644)

	out := filepath.Join(dir, "game.chd")
	res, err := ConvertCue(context.Background(), cuePath, out, Options{Workers: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.OutputSize >= res.InputSize {
		t.Errorf("not compressed: %d -> %d", res.InputSize, res.OutputSize)
	}
	r, got := readAll(t, out)
	tracks := r.CDTracks()
	want := []string{
		"TRACK:1 TYPE:MODE2_RAW SUBTYPE:NONE FRAMES:1003 PREGAP:0 PGTYPE:MODE1 PGSUB:NONE POSTGAP:0",
		"TRACK:2 TYPE:AUDIO SUBTYPE:NONE FRAMES:437 PREGAP:150 PGTYPE:VAUDIO PGSUB:NONE POSTGAP:0",
	}
	if strings.Join(tracks, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tracks:\n%s", strings.Join(tracks, "\n"))
	}

	// Expected frames: data, padding to 4 frames, byte-swapped audio, padding.
	var exp []byte
	frame := func(sector []byte, swap bool) {
		f := make([]byte, FrameSize)
		copy(f, sector)
		if swap {
			for i := 0; i < SectorSize; i += 2 {
				f[i], f[i+1] = f[i+1], f[i]
			}
		}
		exp = append(exp, f...)
	}
	for i := range dataFrames {
		frame(data[i*SectorSize:(i+1)*SectorSize], false)
	}
	exp = append(exp, make([]byte, 1*FrameSize)...) // 1003 -> 1004
	for i := range 437 {
		frame(audio[i*SectorSize:(i+1)*SectorSize], true)
	}
	exp = append(exp, make([]byte, 3*FrameSize)...) // 437 -> 440
	if !bytes.Equal(got, exp) {
		t.Fatalf("image differs (got %d bytes, want %d)", len(got), len(exp))
	}
}

func TestCueSingleFile(t *testing.T) {
	dir := t.TempDir()
	img := make([]byte, (300+200)*SectorSize)
	for i := range img {
		img[i] = byte(i * 7)
	}
	os.WriteFile(filepath.Join(dir, "disc.bin"), img, 0o644)
	cue := "FILE \"disc.bin\" BINARY\r\n TRACK 01 MODE1/2352\r\n  INDEX 01 00:00:00\r\n" +
		" TRACK 02 AUDIO\r\n  PREGAP 00:02:00\r\n  INDEX 01 00:04:00\r\n"
	os.WriteFile(filepath.Join(dir, "disc.cue"), []byte(cue), 0o644)
	tracks, err := ParseCue(filepath.Join(dir, "disc.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].Frames != 300 || tracks[1].Frames != 200 || tracks[1].Offset != 300*SectorSize ||
		tracks[1].Pregap != 150 || tracks[1].PregapInFile {
		t.Fatalf("tracks: %+v %+v", *tracks[0], *tracks[1])
	}
	if got := tracks[1].metadata(); got != "TRACK:2 TYPE:AUDIO SUBTYPE:NONE FRAMES:200 PREGAP:150 PGTYPE:MODE1 PGSUB:NONE POSTGAP:0" {
		t.Fatal(got)
	}
	out := filepath.Join(dir, "disc.chd")
	if _, err := ConvertCue(context.Background(), filepath.Join(dir, "disc.cue"), out, Options{}); err != nil {
		t.Fatal(err)
	}
	readAll(t, out)
}

func TestConvertISO(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(7, 8))
	iso := make([]byte, 2049*DVDSectorSize) // odd number of sectors: half a hunk at the end
	for i := 0; i < len(iso)/2; i++ {
		iso[i] = byte(rng.IntN(16))
	}
	for i := len(iso) / 2; i < len(iso)*3/4; i++ {
		iso[i] = byte(rng.Uint32()) // incompressible
	}
	in := filepath.Join(dir, "game.iso")
	os.WriteFile(in, iso, 0o644)
	out := filepath.Join(dir, "game.chd")
	if _, err := ConvertISO(context.Background(), in, out, Options{}); err != nil {
		t.Fatal(err)
	}
	r, got := readAll(t, out)
	if r.HunkBytes != 4096 || r.UnitBytes != 2048 || r.Codecs[0] != codecLZMA {
		t.Fatalf("header: %+v", r)
	}
	if !bytes.Equal(got, iso) {
		t.Fatal("image differs")
	}
}

func TestFLACRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	for _, kind := range []string{"silence", "sine", "noise", "mono", "loud"} {
		n := 4704
		left, right := make([]int32, n), make([]int32, n)
		v := 0.0
		for i := range n {
			switch kind {
			case "sine":
				v = 12000 * math.Sin(float64(i)/20)
				left[i], right[i] = int32(v), int32(v*0.7)+int32(rng.IntN(9))-4
			case "noise":
				left[i], right[i] = int32(int16(rng.Uint32())), int32(int16(rng.Uint32()))
			case "mono":
				left[i] = int32(rng.IntN(2000)) - 1000
				right[i] = left[i]
			case "loud":
				left[i], right[i] = 32767, -32768
				if i%2 == 1 {
					left[i], right[i] = -32768, 32767
				}
			}
		}
		var e flacEncoder
		enc := e.encode(left, right, 2352)
		gl, gr := make([]int32, n), make([]int32, n)
		used, err := flacDecode(enc, gl, gr)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if used != len(enc) {
			t.Errorf("%s: used %d of %d bytes", kind, used, len(enc))
		}
		for i := range n {
			if gl[i] != left[i] || gr[i] != right[i] {
				t.Fatalf("%s: sample %d: got %d,%d want %d,%d", kind, i, gl[i], gr[i], left[i], right[i])
			}
		}
		t.Logf("%s: %d -> %d bytes", kind, n*4, len(enc))
	}
}

// A CHD whose hunks are intact but whose header holds the SHA-1 of other
// data (found in real collections; chdman verify fails them too) gets a
// HeaderSHA1Error with both SHA-1s.
func TestVerifyHeaderSHA1(t *testing.T) {
	dir := t.TempDir()
	iso := make([]byte, 5*DVDSectorSize)
	for i := range iso {
		iso[i] = byte(i * 7)
	}
	in := filepath.Join(dir, "game.iso")
	os.WriteFile(in, iso, 0o644)
	out := filepath.Join(dir, "game.chd")
	if _, err := ConvertISO(context.Background(), in, out, Options{}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(out, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw [20]byte
	f.ReadAt(raw[:], 64)
	if _, err := f.WriteAt([]byte{raw[0] ^ 1}, 64); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = Verify(context.Background(), out, nil)
	var e *HeaderSHA1Error
	if !errors.As(err, &e) {
		t.Fatalf("got %v, want a HeaderSHA1Error", err)
	}
	want := raw
	want[0] ^= 1
	if e.Actual != raw || e.Stored != want || e.Hunks != 3 {
		t.Fatalf("got %+v", e)
	}
}
