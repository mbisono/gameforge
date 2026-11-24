package chd

import (
	"bytes"
	"context"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The CHD in testdata/chdman.chd was made by chdman (MAME 0.289) from
// smallDisc, so that the default tests read a CHD this package did not
// write: a bug the writer and the reader share would pass Verify, but not
// this. To make it again, run with CHDMAN set and GEN_CHD_TESTDATA=1.
const chdmanTestdata = "testdata/chdman.chd"

// smallDisc is a CD of synthetic data: a Mode 1 data track (with valid
// ECC, which the cdlz/cdzl codecs leave out and rebuild) and an audio
// track (which goes to cdfl, FLAC).
func smallDisc() (data, audio []byte) {
	rng := rand.New(rand.NewPCG(2026, 11))
	for i := range 64 {
		data = append(data, mode1Sector(rng, i+150)...)
	}
	audio = make([]byte, 75*SectorSize)
	v := 0
	for i := 0; i < len(audio); i += 2 {
		v += rng.IntN(64) - 32
		audio[i], audio[i+1] = byte(v), byte(v>>8)
	}
	return data, audio
}

func writeSmallDisc(t *testing.T, dir string) string {
	t.Helper()
	data, audio := smallDisc()
	os.WriteFile(filepath.Join(dir, "small (Track 1).bin"), data, 0o644)
	os.WriteFile(filepath.Join(dir, "small (Track 2).bin"), audio, 0o644)
	cue := filepath.Join(dir, "small.cue")
	os.WriteFile(cue, []byte("FILE \"small (Track 1).bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n"+
		"FILE \"small (Track 2).bin\" BINARY\r\n  TRACK 02 AUDIO\r\n    INDEX 01 00:00:00\r\n"), 0o644)
	return cue
}

// TestGenerateChdmanTestdata writes testdata/chdman.chd with chdman.
func TestGenerateChdmanTestdata(t *testing.T) {
	tool := os.Getenv("CHDMAN")
	if tool == "" || os.Getenv("GEN_CHD_TESTDATA") == "" {
		t.Skip("set CHDMAN and GEN_CHD_TESTDATA=1 to write the test data")
	}
	cue := writeSmallDisc(t, t.TempDir())
	os.MkdirAll("testdata", 0o755)
	os.Remove(chdmanTestdata)
	if out, err := exec.Command(tool, "createcd", "-i", cue, "-o", chdmanTestdata).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestChdmanTestdata reads the CHD made by chdman: the header SHA-1s and
// every hunk check out, and the tracks extracted are the original ones.
func TestChdmanTestdata(t *testing.T) {
	if _, err := Verify(context.Background(), chdmanTestdata, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "small.cue")
	if _, err := Extract(context.Background(), chdmanTestdata, out, ExtractOptions{Verify: true}); err != nil {
		t.Fatal(err)
	}
	data, audio := smallDisc()
	for name, want := range map[string][]byte{"small (Track 1).bin": data, "small (Track 2).bin": audio} {
		got, err := os.ReadFile(filepath.Join(filepath.Dir(out), name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the original (%d and %d bytes)", name, len(got), len(want))
		}
	}
}
