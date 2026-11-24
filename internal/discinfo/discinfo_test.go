package discinfo

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// isoWith builds a small ISO 9660 image whose root holds the given files.
func isoWith(files map[string]string, sectors int) []byte {
	img := make([]byte, sectors*2048)
	pvd := img[16*2048:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	rootLBA, dataLBA := 20, 21
	var dir []byte
	rec := func(name string, lba, size int) []byte {
		r := make([]byte, 33+len(name)+(1-len(name)%2))
		r[0] = byte(len(r))
		binary.LittleEndian.PutUint32(r[2:], uint32(lba))
		binary.LittleEndian.PutUint32(r[10:], uint32(size))
		r[32] = byte(len(name))
		copy(r[33:], name)
		return r
	}
	dir = append(dir, rec("\x00", rootLBA, 2048)...)
	for name, content := range files {
		copy(img[dataLBA*2048:], content)
		dir = append(dir, rec(name+";1", dataLBA, len(content))...)
		dataLBA++
	}
	copy(img[rootLBA*2048:], dir)
	root := rec("\x00", rootLBA, 2048)
	copy(pvd[156:], root)
	return img
}

func TestFromISO(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, b, 0o644)
		return p
	}
	saturn := make([]byte, 40*2048)
	copy(saturn, "SEGA SEGASATURN ")
	gc := make([]byte, 40*2048)
	binary.BigEndian.PutUint32(gc[0x1c:], 0xc2339f3d)

	for _, c := range []struct {
		name     string
		img      []byte
		platform string
		cd       bool
	}{
		{"ps2.iso", isoWith(map[string]string{"SYSTEM.CNF": "BOOT2 = cdrom0:\\SLUS_200.62;1\r\nVER = 1.00\r\n"}, 40), PS2, true},
		{"ps1.iso", isoWith(map[string]string{"SYSTEM.CNF": "BOOT = cdrom:\\SCUS_944.55;1\r\n"}, 40), PS1, true},
		{"psp.iso", isoWith(map[string]string{"UMD_DATA.BIN": "ULUS-10041|"}, 40), PSP, false},
		{"neogeo.iso", isoWith(map[string]string{"IPL.TXT": "x"}, 40), NeoGeoCD, true},
		{"saturn.iso", saturn, Saturn, true},
		{"gc.iso", gc, GameCube, false},
		{"other.iso", isoWith(nil, 40), "", true},
	} {
		p, cd, err := FromISO(write(c.name, c.img))
		if err != nil || p != c.platform || cd != c.cd {
			t.Errorf("%s: got %q cd=%v err=%v, want %q cd=%v", c.name, p, cd, err, c.platform, c.cd)
		}
	}
}

func TestInfoFromCuePlatform(t *testing.T) {
	dir := t.TempDir()
	// A raw Mode 2 data track: 24 bytes before the user data of each sector.
	iso := isoWith(map[string]string{"SYSTEM.CNF": "BOOT = cdrom:\\SLUS_005.94;1\r\n"}, 40)
	var raw []byte
	for s := 0; s < 40; s++ {
		sec := make([]byte, 2352)
		sec[15] = 2
		copy(sec[24:], iso[s*2048:(s+1)*2048])
		raw = append(raw, sec...)
	}
	os.WriteFile(filepath.Join(dir, "g.bin"), raw, 0o644)
	cue := filepath.Join(dir, "g.cue")
	os.WriteFile(cue, []byte("FILE \"g.bin\" BINARY\n TRACK 01 MODE2/2352\n  INDEX 01 00:00:00\n"), 0o644)
	if info, err := InfoFromCue(cue); err != nil || info.Platform != PS1 {
		t.Fatalf("got %+v %v", info, err)
	}
}

// A real disc, when present.
func TestRealCue(t *testing.T) {
	p := os.Getenv("DISCINFO_CUE")
	if p == "" {
		t.Skip()
	}
	got, err := InfoFromCue(p)
	t.Logf("%s: %+v %v", filepath.Base(p), got, err)
}
