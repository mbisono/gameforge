package main

import (
	"slices"
	"testing"
)

// TestNeoKind reads the product codes from names of libretro's Neo Geo list.
func TestNeoKind(t *testing.T) {
	for name, want := range map[string]neoKind{
		"3 Count Bout / Fire Suplex (NGM-043)(NGH-043)":                    {aes: true, mvs: true, number: "43"},
		"The King of Fighters '98 - The Slugfest (NGH-2420)":               {aes: true, number: "2420"},
		"The King of Fighters '98 - Dream Match Never Ends (NGM-2420)":     {mvs: true, number: "2420"},
		"The King of Fighters '95 (NGH-084, alternate board)":              {aes: true, number: "84"},
		"Aero Fighters 2 / Sonic Wings 2":                                  {},
		"The King of Fighters '98 - dream match never ends (Korean board)": {},
	} {
		if got := neoKindOf(name); got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

// TestNeoToMark checks one version of the games the folder has in both.
func TestNeoToMark(t *testing.T) {
	file := func(path, name string) neoFile {
		return neoFile{item: &convItem{path: path}, name: name, kind: neoKindOf(name)}
	}
	files := []neoFile{
		file("kof98 aes", "KOF '98 (NGH-2420)"),
		file("kof98 mvs", "KOF '98 (NGM-2420)"),
		file("kof98 mvs alt", "KOF '98 (NGM-2420, alt board)"),
		file("kof98 korea", "KOF '98 (Korean board)"),
		file("3countb", "3 Count Bout (NGM-043)(NGH-043)"),
		file("aof2 mvs", "Art of Fighting 2 (NGM-056)"), // no AES version here
		file("kof95 aes", "KOF '95 (NGH-084)"),          // no MVS version here
		file("sonicwi2", "Aero Fighters 2 / Sonic Wings 2"),
		{item: &convItem{path: "unknown"}},
	}
	paths := func(fs []neoFile) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.item.path)
		}
		return out
	}
	if got := paths(neoToMark(files, neoMarkMVS)); !slices.Equal(got, []string{"kof98 mvs", "kof98 mvs alt"}) {
		t.Errorf("MVS: %v", got)
	}
	if got := paths(neoToMark(files, neoMarkAES)); !slices.Equal(got, []string{"kof98 aes"}) {
		t.Errorf("AES: %v", got)
	}
	if got := neoToMark(files, neoMarkNone); len(got) != 0 {
		t.Errorf("none: %v", paths(got))
	}
}
