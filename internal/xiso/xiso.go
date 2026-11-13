// Package xiso reads Xbox and Xbox 360 game discs (the XDVDFS file system)
// and rebuilds them as compact XISO images, like "extract-xiso -r".
//
// A disc dump (redump) holds a video partition followed by the game
// partition; the game partition is an XDVDFS volume whose unused sectors
// are filled with random data. Rebuilding keeps only the game partition's
// files and directory tables, laid out one after the other, which is what
// xemu, Xenia and modded consoles load.
package xiso

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	SectorSize   = 2048
	headerOffset = 0x10000 // volume descriptor, from the partition start
	rootSector   = 0x108   // where extract-xiso places the root directory
	fileModulus  = 0x10000 // images are padded to a multiple of this
	attrDir      = 0x10
	padByte      = 0xff
	entryHeader  = 14 // left, right, sector, size, attributes, name length
)

var magic = []byte("MICROSOFT*XBOX*MEDIA")

// Kind is where the game partition starts in the image.
type Kind int

const (
	KindXISO Kind = iota // already a game partition (XISO)
	KindXGD1             // Xbox disc dump
	KindXGD2             // Xbox 360 disc dump
	KindXGD3             // Xbox 360 disc dump, newer layout
)

// Partition offsets in disc dumps, tried in this order as extract-xiso does.
var kinds = []struct {
	kind   Kind
	offset int64
}{{KindXISO, 0}, {KindXGD2, 0x0fd90000}, {KindXGD3, 0x02080000}, {KindXGD1, 0x18300000}}

func (k Kind) String() string {
	return [...]string{"XISO", "XGD1", "XGD2", "XGD3"}[k]
}

// Entry is a file or directory.
type Entry struct {
	Name   string
	Attr   byte
	Sector uint32
	Size   uint32
	off    int       // position of the entry in its directory table
	dir    *dirTable // for directories with content
}

func (e *Entry) IsDir() bool { return e.Attr&attrDir != 0 }

// dirTable is a directory: its table, kept as stored, and its entries in
// table order.
type dirTable struct {
	raw     []byte
	entries []*Entry
}

// Image is an opened Xbox disc image.
type Image struct {
	f        *os.File
	Kind     Kind
	base     int64 // offset of the game partition
	size     int64 // file size
	filetime [8]byte
	root     *dirTable
	Files    int
	Bytes    int64 // total size of the files
	Xbox360  bool  // has default.xex rather than default.xbe
}

// Open reads the volume descriptor and the directory tree of an image.
func Open(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	im := &Image{f: f}
	if err := im.init(); err != nil {
		f.Close()
		return nil, err
	}
	return im, nil
}

func (im *Image) Close() error { return im.f.Close() }

func (im *Image) init() error {
	st, err := im.f.Stat()
	if err != nil {
		return err
	}
	im.size = st.Size()
	vd := make([]byte, SectorSize)
	found := false
	for _, k := range kinds {
		if _, err := im.f.ReadAt(vd, k.offset+headerOffset); err != nil {
			continue
		}
		if bytes.Equal(vd[:20], magic) {
			im.Kind, im.base, found = k.kind, k.offset, true
			break
		}
	}
	if !found {
		return errors.New("not an Xbox or Xbox 360 game disc image (no XDVDFS volume found)")
	}
	if !bytes.Equal(vd[0x7ec:0x800], magic) {
		return errors.New("damaged XDVDFS volume descriptor")
	}
	rootSec := binary.LittleEndian.Uint32(vd[20:])
	rootSize := binary.LittleEndian.Uint32(vd[24:])
	copy(im.filetime[:], vd[28:36])
	if rootSize == 0 {
		return errors.New("the image contains no files")
	}
	seen := map[uint32]bool{}
	im.root, err = im.readDir(rootSec, rootSize, seen, 0)
	if err != nil {
		return err
	}
	for _, e := range im.root.entries {
		switch strings.ToLower(e.Name) {
		case "default.xex":
			im.Xbox360 = true
		}
	}
	if im.Kind == KindXGD2 || im.Kind == KindXGD3 {
		im.Xbox360 = true
	}
	return nil
}

func (im *Image) readDir(sector, size uint32, seen map[uint32]bool, depth int) (*dirTable, error) {
	if depth > 64 || seen[sector] {
		return nil, errors.New("directory loop in the image")
	}
	seen[sector] = true
	off := im.base + int64(sector)*SectorSize
	if size > 64<<20 || off+int64(size) > im.size {
		return nil, fmt.Errorf("directory at sector %d is outside the image", sector)
	}
	t := &dirTable{raw: make([]byte, size)}
	if _, err := im.f.ReadAt(t.raw, off); err != nil {
		return nil, err
	}
	// An empty directory is one sector of padding.
	if size < entryHeader || binary.LittleEndian.Uint16(t.raw) == 0xffff {
		return t, nil
	}
	visited := map[int]bool{}
	var walk func(o int) error
	walk = func(o int) error {
		if visited[o] {
			return errors.New("loop in a directory table")
		}
		visited[o] = true
		if o+entryHeader > len(t.raw) {
			return errors.New("directory entry outside its table")
		}
		b := t.raw[o:]
		e := &Entry{
			Sector: binary.LittleEndian.Uint32(b[4:]),
			Size:   binary.LittleEndian.Uint32(b[8:]),
			Attr:   b[12],
			off:    o,
		}
		n := int(b[13])
		if o+entryHeader+n > len(t.raw) {
			return errors.New("directory entry name outside its table")
		}
		e.Name = string(b[entryHeader : entryHeader+n])
		t.entries = append(t.entries, e)
		for _, child := range []uint16{binary.LittleEndian.Uint16(b), binary.LittleEndian.Uint16(b[2:])} {
			if child != 0 && child != 0xffff {
				if err := walk(int(child) * 4); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return nil, err
	}
	sort.Slice(t.entries, func(i, j int) bool { return t.entries[i].off < t.entries[j].off })
	for _, e := range t.entries {
		if e.IsDir() {
			if e.Size == 0 {
				continue
			}
			sub, err := im.readDir(e.Sector, e.Size, seen, depth+1)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name, err)
			}
			e.dir = sub
			continue
		}
		if im.base+int64(e.Sector)*SectorSize+int64(e.Size) > im.size {
			return nil, fmt.Errorf("%s: file outside the image (truncated dump?)", e.Name)
		}
		im.Files++
		im.Bytes += int64(e.Size)
	}
	return t, nil
}

// file returns a reader of a file's data.
func (im *Image) file(e *Entry) io.Reader {
	return io.NewSectionReader(im.f, im.base+int64(e.Sector)*SectorSize, int64(e.Size))
}

// walk calls fn for every file, with its path, directories first in table
// order.
func (im *Image) walk(fn func(path string, e *Entry) error) error {
	var rec func(t *dirTable, prefix string) error
	rec = func(t *dirTable, prefix string) error {
		for _, e := range t.entries {
			p := prefix + e.Name
			if e.IsDir() {
				if e.dir != nil {
					if err := rec(e.dir, p+"/"); err != nil {
						return err
					}
				}
				continue
			}
			if err := fn(p, e); err != nil {
				return err
			}
		}
		return nil
	}
	return rec(im.root, "")
}

// IsXbox reports whether a file is an Xbox or Xbox 360 game disc image,
// reading only the volume descriptor.
func IsXbox(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, len(magic))
	for _, k := range kinds {
		if _, err := f.ReadAt(b, k.offset+headerOffset); err == nil && bytes.Equal(b, magic) {
			return true
		}
	}
	return false
}
