package main

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		want version
	}{
		{"Legend of Zelda, The - Ocarina of Time (USA) (Rev 2)", version{title: "legend of zelda, the - ocarina of time", regions: []string{"usa"}, rev: 2}},
		{"Pokemon - Crystal Version (USA, Europe) (Rev A)", version{title: "pokemon - crystal version", regions: []string{"usa", "europe"}, rev: 1}},
		{"Super Game (Europe) (En,Fr,De) (v1.1)", version{title: "super game", regions: []string{"europe"}, rev: 101}},
		{"Super Game (Japan) (Beta 2)", version{title: "super game", regions: []string{"japan"}, bad: true}},
		{"Super Game (USA) [b]", version{title: "super game", regions: []string{"usa"}, bad: true}},
		{"Super Game (Brazil) (Proto)", version{title: "super game", regions: []string{"brazil"}, bad: true}},
		{"super game", version{title: "super game"}},
	} {
		got := parseVersion(tc.name)
		if got.title != tc.want.title || !slices.Equal(got.regions, tc.want.regions) || got.rev != tc.want.rev || got.bad != tc.want.bad {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestG1RGroups(t *testing.T) {
	file := func(path string) g1rFile {
		name := filepath.Base(path)
		name = name[:len(name)-len(filepath.Ext(name))]
		return g1rFile{item: &convItem{path: path}, name: name, system: "SNES", version: parseVersion(name), size: 1}
	}
	files := []g1rFile{
		file("Game A (Japan).sfc"),
		file("Game A (Europe).sfc"),
		file("Game A (USA) (Beta).sfc"),
		file("Game B (Japan).sfc"),
		file("Game C (USA).sfc"),
		file("Game C (USA) (Rev 1).zip"),
		file("Game C (USA) (Rev 1).7z"),
		file("Game D (Brazil).sfc"),
		file("Game D (USA, Europe).sfc"),
		file("random.sfc"),
	}
	regions := regionOrder(defaultRegions)
	groups, left := g1rGroups(files, regions, keepOrder(defaultKeep))
	kept := map[string]string{}
	for _, g := range groups {
		kept[g.files[0].version.title] = filepath.Base(g.files[g.keep].item.path)
	}
	want := map[string]string{
		"game a": "Game A (Europe).sfc",      // the USA one is a beta
		"game b": "Game B (Japan).sfc",       // the only version
		"game c": "Game C (USA) (Rev 1).7z",  // latest revision, then the 7z
		"game d": "Game D (USA, Europe).sfc", // USA comes before Brazil
	}
	if len(kept) != len(want) {
		t.Fatalf("groups %v", kept)
	}
	for k, v := range want {
		if kept[k] != v {
			t.Errorf("%s: kept %s, want %s", k, kept[k], v)
		}
	}
	if len(left) != 1 || filepath.Base(left[0].item.path) != "random.sfc" {
		t.Errorf("left %v", left)
	}
	// Brazil first.
	groups, _ = g1rGroups(files, regionOrder("Brazil, USA"), nil)
	for _, g := range groups {
		if g.files[0].version.title == "game d" && filepath.Base(g.files[g.keep].item.path) != "Game D (Brazil).sfc" {
			t.Errorf("kept %s", g.files[g.keep].item.path)
		}
	}
	if regionText("usa;  hong kong ,Europe") != "USA, Hong Kong, Europe" {
		t.Errorf("regionText %q", regionText("usa;  hong kong ,Europe"))
	}
}
