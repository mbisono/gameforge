package main

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"gameforge/internal/redump"
	"gameforge/internal/rvz"
)

const gcDat = `clrmamepro (
	version "2026"
)
game (
	name "Super Smash Bros. Melee (USA) (En,Ja) (Rev 1)"
	serial "DL-DOL-GALE-USA"
	rom ( name "1.iso" size 1459978240 crc 11111111 )
)
game (
	name "Super Smash Bros. Melee (USA) (En,Ja) (Rev 2)"
	serial "DL-DOL-GALE-USA"
	rom ( name "2.iso" size 1459978240 crc 22222222 )
)
`

func TestRVZRename(t *testing.T) {
	dir := t.TempDir()
	img := make([]byte, 4<<20)
	copy(img, "GALE01")
	img[7] = 2 // revision 2
	binary.BigEndian.PutUint32(img[0x1c:], 0xc2339f3d)
	copy(img[0x20:], "Super Smash Bros. Melee")
	iso := filepath.Join(dir, "melee.iso")
	os.WriteFile(iso, img, 0o644)
	out := filepath.Join(dir, "melee.rvz")
	if _, err := rvz.Compress(context.Background(), iso, out, rvz.Options{Level: zstd.SpeedDefault}, nil); err != nil {
		t.Skipf("synthetic disc not accepted: %v", err)
	}
	os.Remove(iso)

	l, err := redump.Parse("GameCube", strings.NewReader(gcDat))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, redump.FileName)
	if err := (&redump.DB{Lists: map[string]*redump.List{"GameCube": l}}).Save(path); err != nil {
		t.Fatal(err)
	}
	db, err := redump.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	it := &convItem{path: out}
	describeRVZ(it)
	if !it.infoOnly || it.platform != "GameCube" || !strings.Contains(it.detail.String(), "GALE01") {
		t.Fatalf("described: %+v %v", it, it.detail)
	}
	name, skip := discName(context.Background(), &ui{}, db, it)
	if name != "Super Smash Bros. Melee (USA) (En,Ja) (Rev 2).rvz" {
		t.Errorf("name %q (%v)", name, skip)
	}
}
