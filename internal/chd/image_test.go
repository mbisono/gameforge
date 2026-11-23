package chd

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// TestEDC checks the CD-ROM EDC against the check value of CRC-32/CD-ROM-EDC.
func TestEDC(t *testing.T) {
	if got := edcUpdate(0, []byte("123456789")); got != 0x6ec2edc4 {
		t.Fatalf("EDC %#x", got)
	}
}

// ecmEncode compresses raw data as ECM does: sectors whose sync, EDC and
// ECC can be computed again become records of their type, the rest stays
// literal.
func ecmEncode(raw []byte) []byte {
	out := []byte("ECM\x00")
	put := func(typ int, n int64) {
		v := uint64(n - 1)
		c := byte(typ) | byte(v&0x1f)<<2
		v >>= 5
		for v != 0 {
			out = append(out, c|0x80)
			c, v = byte(v&0x7f), v>>7
		}
		out = append(out, c)
	}
	var lit []byte
	flush := func() {
		if len(lit) > 0 {
			put(ecmBytes, int64(len(lit)))
			out = append(out, lit...)
			lit = nil
		}
	}
	sector := make([]byte, SectorSize)
	i := 0
	for ; i+SectorSize <= len(raw); i += SectorSize {
		s := raw[i : i+SectorSize]
		if !bytes.Equal(s[:12], syncHeader[:]) {
			lit = append(lit, s...)
			continue
		}
		switch s[15] {
		case 1:
			stored := append(append([]byte{}, s[12:15]...), s[16:0x810]...)
			if decodeECMSector(sector, ecmMode1, stored); bytes.Equal(sector, s) {
				flush()
				put(ecmMode1, 1)
				out = append(out, stored...)
				continue
			}
		case 2:
			for _, typ := range []int{ecmMode2Form1, ecmMode2Form2} {
				stored := s[0x14 : 0x14+ecmStored[typ]]
				if decodeECMSector(sector, typ, stored); bytes.Equal(sector[0x10:], s[0x10:]) {
					lit = append(lit, s[:0x10]...)
					flush()
					put(typ, 1)
					out = append(out, stored...)
					goto next
				}
			}
		}
		lit = append(lit, s...)
	next:
	}
	lit = append(lit, raw[i:]...)
	flush()
	out = append(out, 0xfc, 0xff, 0xff, 0xff, 0x3f)
	return binary.LittleEndian.AppendUint32(out, edcUpdate(0, raw))
}

// mode2Sector makes a Mode 2 sector (Form 1, or Form 2) with valid EDC
// and, in Form 1, ECC.
func mode2Sector(rng *rand.Rand, lba int, form2 bool) []byte {
	s := make([]byte, SectorSize)
	typ, n := ecmMode2Form1, 0x804
	sub := []byte{0, 0, 8, 0}
	if form2 {
		typ, n, sub = ecmMode2Form2, 0x918, []byte{0, 0, 0x20, 0}
	}
	stored := append(sub, randomBytes(rng, n-4)...)
	decodeECMSector(s, typ, stored)
	copy(s, syncHeader[:])
	m := lba + 150
	s[12], s[13], s[14], s[15] = toBCD(m/75/60), toBCD(m/75%60), toBCD(m%75), 2
	return s
}

func toBCD(v int) byte { return byte(v/10<<4 | v%10) }

// music makes little-endian 16-bit stereo audio: two slow sine waves.
func music(frames int) []byte {
	b := make([]byte, frames*SectorSize)
	for i := 0; i < len(b)/4; i++ {
		l := int16(8000 * math.Sin(float64(i)/40))
		r := int16(6000 * math.Sin(float64(i)/27))
		binary.LittleEndian.PutUint16(b[4*i:], uint16(l))
		binary.LittleEndian.PutUint16(b[4*i+2:], uint16(r))
	}
	return b
}

func TestECM(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	var raw []byte
	for i := range 40 {
		switch i % 4 {
		case 0:
			raw = append(raw, mode1Sector(rng, i)...)
		case 1:
			raw = append(raw, mode2Sector(rng, i, false)...)
		case 2:
			raw = append(raw, mode2Sector(rng, i, true)...)
		default:
			raw = append(raw, randomBytes(rng, SectorSize)...) // stays literal
		}
	}
	raw = append(raw, []byte("trailing bytes")...)
	enc := ecmEncode(raw)
	if len(enc) >= len(raw) {
		t.Fatalf("ECM did not shrink the data: %d bytes of %d", len(enc), len(raw))
	}
	path := filepath.Join(t.TempDir(), "disc.bin.ecm")
	os.WriteFile(path, enc, 0o644)
	img, err := OpenImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if img.Size() != int64(len(raw)) {
		t.Fatalf("size %d, want %d", img.Size(), len(raw))
	}
	// Random reads.
	for range 200 {
		off := rng.IntN(len(raw))
		n := min(rng.IntN(5000), len(raw)-off)
		got := make([]byte, n)
		if _, err := img.ReadAt(got, int64(off)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, raw[off:off+n]) {
			t.Fatalf("data at %d differs", off)
		}
	}
	// The checksum, once read in order.
	img2, _ := OpenImage(path)
	defer img2.Close()
	all := make([]byte, len(raw))
	for off := 0; off < len(raw); off += 3000 {
		img2.ReadAt(all[off:min(off+3000, len(raw))], int64(off))
	}
	if err := img2.CheckECM(); err != nil || !bytes.Equal(all, raw) {
		t.Fatalf("read in order: %v", err)
	}
	enc[len(enc)-1] ^= 1
	os.WriteFile(path, enc, 0o644)
	img3, _ := OpenImage(path)
	defer img3.Close()
	img3.ReadAt(all, 0)
	if img3.CheckECM() == nil {
		t.Fatal("a wrong checksum went unnoticed")
	}
}

// discFiles writes a disc of a Mode 1 data track and an audio track with
// a 2-second pregap as one BIN file with its CUE sheet, and returns the
// BIN's data and the tracks' lengths.
func discFiles(t *testing.T, dir string) (bin []byte, dataFrames, audioFrames int) {
	rng := rand.New(rand.NewPCG(7, 8))
	dataFrames, audioFrames = 300, 400
	for i := range dataFrames {
		bin = append(bin, mode1Sector(rng, i)...)
	}
	bin = append(bin, music(audioFrames)...)
	cue := "FILE \"Game.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n" +
		"  TRACK 02 AUDIO\r\n    INDEX 00 00:04:00\r\n    INDEX 01 00:06:00\r\n"
	os.WriteFile(filepath.Join(dir, "Game.bin"), bin, 0o644)
	os.WriteFile(filepath.Join(dir, "Game.cue"), []byte(cue), 0o644)
	return bin, dataFrames, audioFrames
}

func convertTo(t *testing.T, path string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "disc.chd")
	if _, err := ConvertImage(context.Background(), path, out, Options{Workers: 2}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestImagesSameCHD writes the same disc as CUE/BIN, as CUE with an ECM
// file, as NRG and as CDI, and expects the same CHD from each.
func TestImagesSameCHD(t *testing.T) {
	dir := t.TempDir()
	bin, dataFrames, audioFrames := discFiles(t, dir)
	want := convertTo(t, filepath.Join(dir, "Game.cue"))

	// A CUE sheet naming Game.bin when only Game.bin.ecm is there.
	ecmDir := t.TempDir()
	os.WriteFile(filepath.Join(ecmDir, "Game.bin.ecm"), ecmEncode(bin), 0o644)
	cue, _ := os.ReadFile(filepath.Join(dir, "Game.cue"))
	os.WriteFile(filepath.Join(ecmDir, "Game.cue"), cue, 0o644)
	if got := convertTo(t, filepath.Join(ecmDir, "Game.cue")); !bytes.Equal(got, want) {
		t.Error("CUE + ECM: CHD differs")
	}

	// NRG (Nero 5.5 and later): DAOX with each track's pregap, start and end.
	nrg := filepath.Join(t.TempDir(), "Game.nrg")
	os.WriteFile(nrg, makeNRG(bin, dataFrames, audioFrames), 0o644)
	if got := convertTo(t, nrg); !bytes.Equal(got, want) {
		t.Error("NRG: CHD differs")
	}

	// CDI (DiscJuggler, newest version).
	cdi := filepath.Join(t.TempDir(), "Game.cdi")
	os.WriteFile(cdi, makeCDI(bin, []cdiTrack{{0, 2, 0, dataFrames, 0}, {0, 0, 150, audioFrames - 150, dataFrames}}), 0o644)
	if got := convertTo(t, cdi); !bytes.Equal(got, want) {
		t.Error("CDI: CHD differs")
	}
	// The same CDI compressed with ECM.
	cdiECM := cdi + ".ecm"
	data, _ := os.ReadFile(cdi)
	os.WriteFile(cdiECM, ecmEncode(data), 0o644)
	if got := convertTo(t, cdiECM); !bytes.Equal(got, want) {
		t.Error("CDI + ECM: CHD differs")
	}
}

func makeNRG(bin []byte, dataFrames, audioFrames int) []byte {
	out := append([]byte{}, bin...)
	at := len(out)
	chunk := func(id string, d []byte) {
		out = append(out, id...)
		out = binary.BigEndian.AppendUint32(out, uint32(len(d)))
		out = append(out, d...)
	}
	var cuex []byte
	cue := func(mode, track, index byte, lba int32) {
		cuex = append(cuex, mode, track, index, 0)
		cuex = binary.BigEndian.AppendUint32(cuex, uint32(lba))
	}
	cue(0x41, 0, 0, -150)
	cue(0x41, 1, 0, -150)
	cue(0x41, 1, 1, 0)
	cue(0x01, 2, 0, int32(dataFrames))
	cue(0x01, 2, 1, int32(dataFrames+150))
	cue(0x01, 0xaa, 1, int32(dataFrames+audioFrames))
	chunk("CUEX", cuex)
	dao := make([]byte, 22)
	dao[20], dao[21] = 1, 2
	track := func(secSize int, mode byte, i0, i1, end int64) {
		e := make([]byte, 42)
		binary.BigEndian.PutUint16(e[12:], uint16(secSize))
		e[14] = mode
		binary.BigEndian.PutUint16(e[16:], 1)
		binary.BigEndian.PutUint64(e[18:], uint64(i0))
		binary.BigEndian.PutUint64(e[26:], uint64(i1))
		binary.BigEndian.PutUint64(e[34:], uint64(end))
		dao = append(dao, e...)
	}
	d := int64(dataFrames * SectorSize)
	track(SectorSize, 0x05, 0, 0, d)
	track(SectorSize, 0x07, d, d+150*SectorSize, int64(len(bin)))
	binary.BigEndian.PutUint32(dao, uint32(len(dao)))
	chunk("DAOX", dao)
	chunk("SINF", []byte{0, 0, 0, 2})
	chunk("MTYP", []byte{0, 0, 0, 1})
	chunk("END!", nil)
	out = append(out, "NER5"...)
	return binary.BigEndian.AppendUint64(out, uint64(at))
}

type cdiTrack struct {
	session, mode, pregap, frames, start int // frames after the pregap; start of the pregap on the disc
}

// makeCDI writes a CDI image of the newest version, laid out as the
// PSX-SPX documentation describes it, with tracks of raw 2352-byte
// sectors.
func makeCDI(bin []byte, tracks []cdiTrack) []byte {
	out := append([]byte{}, bin...)
	at := len(out)
	header := func(total int) {
		out = append(out, cdiMark...)
		out = append(out, 0xda, 0x0a, 0xd5, byte(total))
		name := "C:\\game.cdi"
		out = append(out, byte(len(name)))
		out = append(out, name...)
		out = append(out, make([]byte, 11)...)
		out = append(out, 2)
		out = append(out, make([]byte, 10)...)
		out = append(out, 0x80)
		out = binary.LittleEndian.AppendUint32(out, 360000)
		out = append(out, 0, 0, 0x98, 0)
	}
	sessions := 1
	for _, tr := range tracks {
		sessions = max(sessions, tr.session+1)
	}
	out = append(out, byte(sessions))
	for s := range sessions {
		var mine []cdiTrack
		for _, tr := range tracks {
			if tr.session == s {
				mine = append(mine, tr)
			}
		}
		block := make([]byte, 15)
		block[1], block[9], block[13], block[14] = byte(len(mine)), 1, 0xff, 0xff
		out = append(out, block...)
		for i, tr := range mine {
			header(len(tracks))
			out = binary.LittleEndian.AppendUint16(out, 2)
			out = binary.LittleEndian.AppendUint32(out, uint32(tr.pregap))
			out = binary.LittleEndian.AppendUint32(out, uint32(tr.frames))
			out = binary.LittleEndian.AppendUint32(out, 0) // no CD-Text
			f := make([]byte, 0xe4-0x36)                   // the fields from 36h, relative to 36h
			f[0x38-0x36] = byte(tr.mode)
			binary.LittleEndian.PutUint32(f[0x40-0x36:], uint32(s))
			binary.LittleEndian.PutUint32(f[0x48-0x36:], uint32(tr.start))
			binary.LittleEndian.PutUint32(f[0x4c-0x36:], uint32(tr.pregap+tr.frames))
			binary.LittleEndian.PutUint32(f[0x60-0x36:], 2) // 2352-byte sectors
			binary.LittleEndian.PutUint32(f[0x69-0x36:], uint32(tr.pregap+tr.frames))
			if i < len(mine)-1 {
				f[0xde-0x36] = 1
			}
			out = append(out, f...)
		}
	}
	out = append(out, make([]byte, 15)...) // no more sessions
	header(len(tracks))
	out = append(out, make([]byte, 0x5f-0x30)...)
	out = binary.LittleEndian.AppendUint32(out, 0x80000006)
	return binary.LittleEndian.AppendUint32(out, uint32(len(out)+4-at))
}

// TestCDISessions keeps a second session at its place on the disc.
func TestCDISessions(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	audio := music(300)
	var data []byte
	for i := range 200 {
		data = append(data, mode2Sector(rng, 11700+150+i, false)...)
	}
	bin := append(append([]byte{}, audio...), data...)
	path := filepath.Join(t.TempDir(), "Game.cdi")
	os.WriteFile(path, makeCDI(bin, []cdiTrack{{0, 0, 150, 150, 0}, {1, 2, 150, 50, 11700}}), 0o644)
	tracks, err := ParseCDI(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].Type != "AUDIO" || !tracks[0].swap || tracks[1].Type != "MODE2_RAW" {
		t.Fatalf("tracks %+v %+v", tracks[0], tracks[1])
	}
	// The second session starts at sector 11700: the gap after the first
	// track (300 frames) is added before the second one's pregap.
	if tr := tracks[1]; tr.Frames != 200+11400 || tr.Pregap != 150+11400 || tr.DataStart() != int64(len(audio)+150*SectorSize) {
		t.Fatalf("second track: frames %d, pregap %d, data at %d", tr.Frames, tr.Pregap, tr.DataStart())
	}
	if _, err := ConvertImage(context.Background(), path, filepath.Join(t.TempDir(), "disc.chd"), Options{Workers: 2}); err != nil {
		t.Fatal(err)
	}
}

// TestRawECM reads a .bin.ecm without a CUE sheet as one data track.
func TestRawECM(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	var bin []byte
	for i := range 100 {
		bin = append(bin, mode2Sector(rng, i, i%3 == 0)...)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Game.bin"), bin, 0o644)
	os.WriteFile(filepath.Join(dir, "Game.cue"), []byte("FILE \"Game.bin\" BINARY\n  TRACK 01 MODE2/2352\n    INDEX 01 00:00:00\n"), 0o644)
	want := convertTo(t, filepath.Join(dir, "Game.cue"))
	ecm := filepath.Join(t.TempDir(), "Game.bin.ecm")
	os.WriteFile(ecm, ecmEncode(bin), 0o644)
	if got := convertTo(t, ecm); !bytes.Equal(got, want) {
		t.Error("CHD of the .bin.ecm differs from the CUE/BIN's")
	}
}
