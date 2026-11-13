package discinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"gameforge/internal/chd"
)

// rawMode1 wraps 2048-byte sectors in raw Mode 1 sectors.
func rawMode1(data []byte) []byte {
	var out []byte
	for s := 0; s*2048 < len(data); s++ {
		sec := make([]byte, chd.SectorSize)
		for i := 1; i < 11; i++ {
			sec[i] = 0xFF
		}
		sec[15] = 1
		copy(sec[16:], data[s*2048:min(len(data), (s+1)*2048)])
		out = append(out, sec...)
	}
	return out
}

// gdromFiles writes a Dreamcast disc as a .gdi and as a Redump multi-cue,
// with IP.BIN at the start of the high-density area.
func gdromFiles(t *testing.T, dir string) (gdi, cue string) {
	ip := make([]byte, 500*2048)
	put(ip, 0, "SEGA SEGAKATANA", 16)
	put(ip, 0x20, "5D4B GD-ROM1/1", 16)
	put(ip, 0x30, "JUE", 8)
	put(ip, 0x40, "MK-51000", 10)
	put(ip, 0x4A, "V1.005", 6)
	put(ip, 0x50, "19990909", 16)
	put(ip, 0x80, "SONIC ADVENTURE", 128)
	gd1, gd3 := rawMode1(make([]byte, 300*2048)), rawMode1(ip)
	audio := func(n int) []byte { return make([]byte, n*chd.SectorSize) }
	pregap := audio(150)
	files := map[string][]byte{
		"gd1.bin": gd1, "gd2.raw": audio(75), "gd 3.bin": gd3, "gd4.raw": audio(100),
		"rd1.bin": gd1, "rd2.bin": append(append([]byte(nil), pregap...), audio(75)...),
		"rd3.bin": gd3, "rd4.bin": append(append([]byte(nil), pregap...), audio(100)...),
	}
	for name, b := range files {
		os.WriteFile(filepath.Join(dir, name), b, 0o644)
	}
	gdi = filepath.Join(dir, "disc.gdi")
	os.WriteFile(gdi, []byte("4\r\n1 0 4 2352 gd1.bin 0\r\n2 450 0 2352 gd2.raw 0\r\n"+
		"3 45000 4 2352 \"gd 3.bin\" 0\r\n4 45650 0 2352 gd4.raw 0\r\n"), 0o644)
	cue = filepath.Join(dir, "gdrom.cue")
	os.WriteFile(cue, []byte(`REM SINGLE-DENSITY AREA
FILE "rd1.bin" BINARY
  TRACK 01 MODE1/2352
    INDEX 01 00:00:00
FILE "rd2.bin" BINARY
  TRACK 02 AUDIO
    INDEX 00 00:00:00
    INDEX 01 00:02:00
REM HIGH-DENSITY AREA
FILE "rd3.bin" BINARY
  TRACK 03 MODE1/2352
    INDEX 01 00:00:00
FILE "rd4.bin" BINARY
  TRACK 04 AUDIO
    INDEX 00 00:00:00
    INDEX 01 00:02:00
`), 0o644)
	return gdi, cue
}

var sonic = Info{Platform: Dreamcast, Serial: "MK-51000", Title: "SONIC ADVENTURE", Version: "1.005", Regions: []string{"Japan", "USA", "Europe"}, Date: "1999-09-09"}

func TestGDROMInfo(t *testing.T) {
	dir := t.TempDir()
	gdi, cue := gdromFiles(t, dir)
	for name, in := range map[string]string{"gdi": gdi, "cue": cue} {
		if got, err := InfoFromCue(in); err != nil || !reflect.DeepEqual(got, sonic) {
			t.Errorf("%s: %+v (%v)", name, got, err)
		}
		out := filepath.Join(dir, name+".chd")
		convert := chd.ConvertCue
		if name == "gdi" {
			convert = chd.ConvertGDI
		}
		if _, err := convert(context.Background(), in, out, chd.Options{}); err != nil {
			t.Fatal(err)
		}
		if got, err := InfoFromCHD(out); err != nil || !reflect.DeepEqual(got, sonic) {
			t.Errorf("%s CHD: %+v (%v)", name, got, err)
		}
	}
}

// TestChdmanInfo reads CHDs made by MAME's chdman; it runs only when
// CHDMAN points to chdman.exe.
func TestChdmanInfo(t *testing.T) {
	exe := os.Getenv("CHDMAN")
	if exe == "" {
		t.Skip("CHDMAN not set")
	}
	dir := t.TempDir()
	gdi, cue := gdromFiles(t, dir)
	ps2 := filepath.Join(dir, "ps2.iso")
	os.WriteFile(ps2, isoTree(map[string][]byte{"SYSTEM.CNF": []byte("BOOT2 = cdrom0:\\SLUS_214.09;1\r\nVER = 1.01\r\n")}, 300), 0o644)
	ps2Info := Info{Platform: PS2, Serial: "SLUS-21409", Version: "1.01", Regions: []string{"USA"}}
	for _, c := range []struct {
		name, in, cmd string
		want          Info
	}{
		{"gdi", gdi, "createcd", sonic},
		{"cue", cue, "createcd", sonic},
		{"dvd", ps2, "createdvd", ps2Info},
		{"cd", ps2, "createcd", ps2Info},
	} {
		out := filepath.Join(dir, c.name+"-chdman.chd")
		if b, err := exec.Command(exe, c.cmd, "-i", c.in, "-o", out).CombinedOutput(); err != nil {
			t.Fatalf("chdman %s: %v\n%s", c.name, err, b)
		}
		if got, err := InfoFromCHD(out); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v (%v)", c.name, got, err)
		}
	}
}
