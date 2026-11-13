package xiso

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// node is a file (data != nil or empty) or directory (children) of a test
// image.
type node struct {
	name     string
	data     []byte
	dir      bool
	children []*node
}

// builder writes an XDVDFS volume at base with random filler between
// everything, like a disc dump.
type builder struct {
	img  []byte
	base int
	next int // next free sector
	rng  *rand.Rand
}

func (b *builder) alloc(n int) int {
	s := b.next + 1 + b.rng.IntN(3) // leave filler sectors between items
	b.next = s + max((n+SectorSize-1)/SectorSize, 1)
	if need := b.base + b.next*SectorSize; need > len(b.img) {
		grow := make([]byte, need-len(b.img))
		for i := range grow {
			grow[i] = byte(b.rng.Uint32())
		}
		b.img = append(b.img, grow...)
	}
	return s
}

func (b *builder) put(sector int, data []byte) {
	copy(b.img[b.base+sector*SectorSize:], data)
}

// table writes a directory and returns its sector and size. Entries are
// chained through their right pointers (a valid, if unbalanced, tree).
func (b *builder) table(children []*node) (int, int) {
	sort.Slice(children, func(i, j int) bool { return strings.ToUpper(children[i].name) < strings.ToUpper(children[j].name) })
	var raw []byte
	var offs []int
	for _, c := range children {
		l := (entryHeader + len(c.name) + 3) &^ 3
		if len(raw)/SectorSize != (len(raw)+l-1)/SectorSize { // entries do not cross sectors
			raw = append(raw, bytes.Repeat([]byte{padByte}, SectorSize-len(raw)%SectorSize)...)
		}
		offs = append(offs, len(raw))
		raw = append(raw, make([]byte, l)...)
	}
	sectors := make([]int, len(children))
	sizes := make([]int, len(children))
	for i, c := range children {
		switch {
		case c.dir && len(c.children) == 0:
			sectors[i], sizes[i] = 0, 0
		case c.dir:
			sectors[i], sizes[i] = b.table(c.children)
		default:
			sectors[i], sizes[i] = b.alloc(len(c.data)), len(c.data)
			b.put(sectors[i], c.data)
		}
	}
	for i, c := range children {
		e := raw[offs[i]:]
		for k := range len(e[:(entryHeader+len(c.name)+3)&^3]) {
			e[k] = padByte
		}
		binary.LittleEndian.PutUint16(e, 0)
		right := uint16(0)
		if i+1 < len(children) {
			right = uint16(offs[i+1] / 4)
		}
		binary.LittleEndian.PutUint16(e[2:], right)
		binary.LittleEndian.PutUint32(e[4:], uint32(sectors[i]))
		binary.LittleEndian.PutUint32(e[8:], uint32(sizes[i]))
		e[12] = 0x20
		if c.dir {
			e[12] = attrDir
		}
		e[13] = byte(len(c.name))
		copy(e[entryHeader:], c.name)
	}
	s := b.alloc(len(raw))
	b.put(s, raw)
	return s, len(raw)
}

func buildImage(t *testing.T, base int, root []*node, rng *rand.Rand) []byte {
	b := &builder{base: base, next: 40, rng: rng}
	b.img = make([]byte, base+64*SectorSize)
	for i := range b.img {
		b.img[i] = byte(rng.Uint32())
	}
	rs, rsize := b.table(root)
	vd := b.img[base+headerOffset : base+headerOffset+SectorSize]
	copy(vd, magic)
	binary.LittleEndian.PutUint32(vd[20:], uint32(rs))
	binary.LittleEndian.PutUint32(vd[24:], uint32(rsize))
	copy(vd[0x7ec:], magic)
	return b.img
}

func testTree(rng *rand.Rand) []*node {
	file := func(name string, n int) *node {
		d := make([]byte, n)
		for i := range d {
			d[i] = byte(rng.IntN(7))
		}
		return &node{name: name, data: d}
	}
	var many []*node
	for i := range 150 { // a directory table longer than one sector
		many = append(many, file("level_"+strings.Repeat("x", i%40)+string(rune('a'+i%26))+string(rune('0'+i/26)), rng.IntN(5000)))
	}
	return []*node{
		file("default.xbe", 300000),
		file("empty.bin", 0),
		file("exact.bin", 2*SectorSize),
		{name: "media", dir: true, children: []*node{
			file("movie.xmv", 1234567),
			{name: "sub", dir: true, children: []*node{file("deep.txt", 10)}},
		}},
		{name: "nothing", dir: true},
		{name: "levels", dir: true, children: many},
	}
}

// collect returns path -> content of every file of the image.
func collect(t *testing.T, path string) map[string][]byte {
	im, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()
	out := map[string][]byte{}
	err = im.walk(func(p string, e *Entry) error {
		b := make([]byte, e.Size)
		_, err := im.f.ReadAt(b, im.base+int64(e.Sector)*SectorSize)
		out[p] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRebuild(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		base int
	}{{KindXISO, 0}, {KindXGD3, 0x02080000}} {
		t.Run(c.kind.String(), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1, uint64(c.base)))
			dir := t.TempDir()
			in := filepath.Join(dir, "game.iso")
			os.WriteFile(in, buildImage(t, c.base, testTree(rng), rng), 0o644)
			want := collect(t, in)

			out := filepath.Join(dir, "game.xiso.iso")
			res, hashes, err := Rebuild(context.Background(), in, out, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Kind != c.kind || res.Files != len(want) {
				t.Fatalf("result %+v, %d files expected", res, len(want))
			}
			if err := Verify(context.Background(), out, hashes, nil); err != nil {
				t.Fatal(err)
			}
			got := collect(t, out)
			if len(got) != len(want) {
				t.Fatalf("%d files, want %d", len(got), len(want))
			}
			for p, d := range want {
				if !bytes.Equal(got[p], d) {
					t.Fatalf("%s differs", p)
				}
			}
			st, _ := os.Stat(out)
			if st.Size()%fileModulus != 0 || st.Size() >= res.InputSize {
				t.Errorf("output size %d (input %d)", st.Size(), res.InputSize)
			}
			im, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer im.Close()
			if im.Kind != KindXISO {
				t.Errorf("output kind %v", im.Kind)
			}
			// The empty directory is one sector of padding.
			for _, e := range im.root.entries {
				if e.Name == "nothing" && (e.Size != SectorSize || e.dir == nil || len(e.dir.entries) != 0) {
					t.Errorf("empty directory: %+v", e)
				}
			}
			// A rebuilt image rebuilds to the same bytes.
			out2 := filepath.Join(dir, "again.iso")
			if _, _, err := Rebuild(context.Background(), out, out2, nil); err != nil {
				t.Fatal(err)
			}
			a, _ := os.ReadFile(out)
			b, _ := os.ReadFile(out2)
			if !bytes.Equal(a, b) {
				t.Error("rebuilding an XISO changes it")
			}
		})
	}
}

func TestNotXbox(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.iso")
	os.WriteFile(p, make([]byte, 1<<20), 0o644)
	if _, err := Open(p); err == nil {
		t.Fatal("opened a non-Xbox image")
	}
}
