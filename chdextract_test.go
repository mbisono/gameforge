package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gameforge/internal/chd"
	"gameforge/internal/i18n"
)

// TestExtractCHD lists a CHD of each kind, offers the formats it can be
// extracted to and extracts it with the Discs tab's back conversion.
func TestExtractCHD(t *testing.T) {
	i18n.Set("en")
	dir := t.TempDir()
	iso := bytes.Repeat([]byte("GameForge"), 2048*100/9+1)[:2048*100]
	in := filepath.Join(dir, "game.iso")
	os.WriteFile(in, iso, 0o644)
	dvd, cd := filepath.Join(dir, "dvd.chd"), filepath.Join(dir, "cd.chd")
	if _, err := chd.ConvertISO(context.Background(), in, dvd, chd.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := chd.ConvertISOAsCD(context.Background(), in, cd, chd.Options{}); err != nil {
		t.Fatal(err)
	}
	back := chdConverter.back
	for _, c := range []struct {
		path, kind string
		choices    []string
	}{{dvd, "iso", []string{"iso"}}, {cd, "iso", []string{"cue", "iso"}}} {
		it := &convItem{path: c.path}
		describeDisc(it)
		if it.kind != c.kind || !back.accept(it) || !slices.Equal(extractChoices(c.path), c.choices) {
			t.Errorf("%s: kind %q, choices %v", filepath.Base(c.path), it.kind, extractChoices(c.path))
		}
	}

	u := &ui{}
	it := &convItem{path: cd, mode: "cue"}
	describeDisc(it)
	if got := back.outName(it); got != "cd.cue" {
		t.Fatalf("name %q", got)
	}
	out := filepath.Join(t.TempDir(), back.outName(it))
	inSize, outSize, err := back.run(context.Background(), u, it, out, true, func(bool, int64, int64) {})
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := os.ReadFile(filepath.Join(filepath.Dir(out), "cd.bin"))
	if inSize == 0 || outSize != int64(len(bin))+fileSize(out) || !bytes.Equal(bin, iso) {
		t.Errorf("sizes %d, %d; bin %d bytes", inSize, outSize, len(bin))
	}
	if _, _, err := back.run(context.Background(), u, it, out, false, func(bool, int64, int64) {}); err == nil ||
		err.Error() != "cd.cue already exists; nothing was written" {
		t.Errorf("again: %v", err)
	}
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// TestDiscsCSO converts an ISO to CSO and ZSO in the Discs tab, lists them
// and extracts them back to the ISO.
func TestDiscsCSO(t *testing.T) {
	i18n.Set("en")
	defer func() { isoTarget = "" }()
	dir := t.TempDir()
	iso := bytes.Repeat([]byte("GameForge CSO "), 2048*300/14+1)[:2048*300]
	in := filepath.Join(dir, "game.iso")
	os.WriteFile(in, iso, 0o644)
	u, back := &ui{}, chdConverter.back
	for _, target := range []string{"cso", "zso"} {
		isoTarget = target
		src := &convItem{path: in}
		describeDisc(src)
		if !src.ok || chdConverter.outName(src) != "game."+target || chdConverter.menu(u, nil, src) != nil {
			t.Fatalf("%s: ISO listed as %v, %q", target, src.ok, chdConverter.outName(src))
		}
		out := filepath.Join(dir, chdConverter.outName(src))
		if _, _, err := chdConverter.run(context.Background(), u, src, out, true, func(bool, int64, int64) {}); err != nil {
			t.Fatal(err)
		}
		it := &convItem{path: out}
		describeDisc(it)
		if it.ok || !it.infoOnly || !back.accept(it) || back.outName(it) != "game.iso" {
			t.Fatalf("%s listed: %+v", target, it)
		}
		again := filepath.Join(t.TempDir(), "game.iso")
		if _, _, err := back.run(context.Background(), u, it, again, true, func(bool, int64, int64) {}); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(again); !bytes.Equal(got, iso) {
			t.Errorf("%s: ISO differs", target)
		}
	}
}

// TestXboxNames names the XISO of an Xbox image and tells XISO names.
func TestXboxNames(t *testing.T) {
	for in, want := range map[string]string{
		`D:\x\Halo.iso`:            "Halo.xiso.iso",
		`D:\x\Halo.xiso.iso`:       "Halo.xiso.iso",
		`D:\x\Halo (USA).XISO.ISO`: "Halo (USA).xiso.iso",
	} {
		if got := xisoName(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	if !isXISOName("a/Halo.Xiso.iso") || isXISOName("a/Halo.iso") {
		t.Error("isXISOName")
	}
	if !chdConverter.accept("no-such.iso") || !isXboxPlatform("Xbox 360") || isXboxPlatform("PS2") {
		t.Error("accept")
	}
}
