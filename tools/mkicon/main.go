// Command mkicon writes the Windows resource objects (rsrc_windows_*.syso)
// that give gameforge.exe its icon in Explorer. The Go linker picks them up
// automatically; run it again only after changing internal/icon:
//
//	go run ./tools/mkicon
package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"log"
	"os"

	"gameforge/internal/icon"
)

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

// Relocation type that adds the image base-relative address of the section,
// per architecture (IMAGE_REL_*_ADDR32NB).
var arches = []struct {
	name    string
	machine uint16
	reloc   uint16
}{
	{"amd64", 0x8664, 3},
	{"386", 0x14c, 7},
	{"arm64", 0xaa64, 2},
}

const (
	rtIcon      = 3
	rtGroupIcon = 14
	langEnUS    = 0x409
)

func main() {
	var icons [][]byte
	for _, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, icon.Render(s)); err != nil {
			log.Fatal(err)
		}
		icons = append(icons, buf.Bytes())
	}
	for _, a := range arches {
		obj := coff(a.machine, a.reloc, resources(icons))
		if err := os.WriteFile("rsrc_windows_"+a.name+".syso", obj, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

// section is the content of a .rsrc section and the offsets of the fields
// that need a relocation.
type section struct {
	data   []byte
	relocs []uint32
}

// resources lays out the resource tree: RT_ICON 1..n with the PNG images and
// RT_GROUP_ICON 1 that lists them.
func resources(icons [][]byte) section {
	group := new(bytes.Buffer)
	le := func(v any) { binary.Write(group, binary.LittleEndian, v) }
	le([3]uint16{0, 1, uint16(len(icons))}) // reserved, type icon, count
	for i, data := range icons {
		dim := uint8(sizes[i]) // 256 is stored as 0
		le([4]uint8{dim, dim, 0, 0})
		le([2]uint16{1, 32}) // planes, bits per pixel
		le(uint32(len(data)))
		le(uint16(i + 1))
	}

	// Resources in tree order; the IDs of each level are ascending.
	type leaf struct {
		typ, id uint32
		data    []byte
	}
	var leaves []leaf
	for i, data := range icons {
		leaves = append(leaves, leaf{rtIcon, uint32(i + 1), data})
	}
	leaves = append(leaves, leaf{rtGroupIcon, 1, group.Bytes()})

	const dirSize, entrySize, dataEntrySize = 16, 8, 16
	types := []uint32{rtIcon, rtGroupIcon}
	idsOf := map[uint32]int{rtIcon: len(icons), rtGroupIcon: 1}

	// Sizes of the directory part: root, one directory per type, one per
	// resource (its language), then the data entries.
	rootSize := dirSize + entrySize*len(types)
	typeDirs := 0
	for _, t := range types {
		typeDirs += dirSize + entrySize*idsOf[t]
	}
	langDirs := len(leaves) * (dirSize + entrySize)
	dataEntries := len(leaves) * dataEntrySize
	dataStart := align(rootSize + typeDirs + langDirs + dataEntries)

	out := new(bytes.Buffer)
	w := func(v any) { binary.Write(out, binary.LittleEndian, v) }
	// Header ending in the counts of named (0) and ID (n) entries, two uint16s.
	dir := func(n int) { w([4]uint32{0, 0, 0, uint32(n) << 16}) }
	const subdir = 0x80000000

	// Root.
	dir(len(types))
	off := rootSize
	for _, t := range types {
		w([2]uint32{t, subdir | uint32(off)})
		off += dirSize + entrySize*idsOf[t]
	}
	// Type directories.
	langOff := rootSize + typeDirs
	i := 0
	for _, t := range types {
		dir(idsOf[t])
		for ; i < len(leaves) && leaves[i].typ == t; i++ {
			w([2]uint32{leaves[i].id, subdir | uint32(langOff+i*(dirSize+entrySize))})
		}
	}
	// Language directories.
	entryOff := rootSize + typeDirs + langDirs
	for i := range leaves {
		dir(1)
		w([2]uint32{langEnUS, uint32(entryOff + i*dataEntrySize)})
	}
	// Data entries; their address field is section-relative and relocated.
	var relocs []uint32
	dataOff := dataStart
	for _, l := range leaves {
		relocs = append(relocs, uint32(out.Len()))
		w([4]uint32{uint32(dataOff), uint32(len(l.data)), 0, 0})
		dataOff = align(dataOff + len(l.data))
	}
	for _, l := range leaves {
		out.Write(make([]byte, align(out.Len())-out.Len()))
		out.Write(l.data)
	}
	out.Write(make([]byte, align(out.Len())-out.Len()))
	return section{out.Bytes(), relocs}
}

func align(n int) int { return (n + 7) &^ 7 }

// coff wraps the section in a COFF object file with one section symbol that
// the relocations refer to.
func coff(machine, relocType uint16, sec section) []byte {
	const headerSize, sectionHeaderSize, relocSize = 20, 40, 10
	rawStart := headerSize + sectionHeaderSize
	relocStart := rawStart + len(sec.data)
	symStart := relocStart + relocSize*len(sec.relocs)

	out := new(bytes.Buffer)
	w := func(v any) { binary.Write(out, binary.LittleEndian, v) }
	name := [8]byte{'.', 'r', 's', 'r', 'c'}

	// File header.
	w(machine)
	w(uint16(1))        // sections
	w(uint32(0))        // timestamp
	w(uint32(symStart)) // symbol table
	w(uint32(1))        // symbols
	w(uint16(0))        // optional header size
	w(uint16(0))        // characteristics
	// Section header.
	w(name)
	w([2]uint32{0, 0}) // virtual size and address
	w(uint32(len(sec.data)))
	w(uint32(rawStart))
	w(uint32(relocStart))
	w(uint32(0)) // line numbers
	w(uint16(len(sec.relocs)))
	w(uint16(0))
	w(uint32(0x40000040)) // initialized data, readable
	// Raw data and relocations.
	out.Write(sec.data)
	for _, r := range sec.relocs {
		w(r)
		w(uint32(0)) // symbol 0: the section
		w(relocType)
	}
	// Symbol table: the section symbol, then an empty string table.
	w(name)
	w(uint32(0)) // value
	w(int16(1))  // section number
	w(uint16(0)) // type
	w(uint8(3))  // storage class static
	w(uint8(0))  // aux symbols
	w(uint32(4))
	return out.Bytes()
}
