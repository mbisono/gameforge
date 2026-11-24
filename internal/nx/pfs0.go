// Package nx implements the small subset of Nintendo Switch container
// formats needed to inspect and merge NSP files (PFS0, NCA, CNMT, tickets).
package nx

import (
	"bytes"
	"encoding/binary"
	"io"

	"gameforge/internal/i18n"
)

// PFS0Entry is a file stored inside a PFS0 container. Offset is absolute,
// relative to the start of the reader the container was read from.
type PFS0Entry struct {
	Name   string
	Offset int64
	Size   int64
}

const pfs0EntrySize = 0x18

// ReadPFS0 parses a PFS0 header located at base inside r.
func ReadPFS0(r io.ReaderAt, base int64) ([]PFS0Entry, error) {
	var hdr [0x10]byte
	if _, err := r.ReadAt(hdr[:], base); err != nil {
		return nil, i18n.Errorf("err.pfs0.read_header", err)
	}
	if string(hdr[:4]) != "PFS0" {
		return nil, i18n.Errorf("err.pfs0.magic")
	}
	n := binary.LittleEndian.Uint32(hdr[4:])
	strSize := binary.LittleEndian.Uint32(hdr[8:])
	if n > 100000 || strSize > 16<<20 {
		return nil, i18n.Errorf("err.pfs0.corrupt")
	}

	meta := make([]byte, int(n)*pfs0EntrySize+int(strSize))
	if _, err := r.ReadAt(meta, base+0x10); err != nil {
		return nil, i18n.Errorf("err.pfs0.read_table", err)
	}
	strTable := meta[int(n)*pfs0EntrySize:]
	dataStart := base + 0x10 + int64(len(meta))

	entries := make([]PFS0Entry, n)
	for i := range entries {
		e := meta[i*pfs0EntrySize:]
		off := binary.LittleEndian.Uint64(e[0:])
		size := binary.LittleEndian.Uint64(e[8:])
		nameOff := binary.LittleEndian.Uint32(e[16:])
		if int(nameOff) >= len(strTable) {
			return nil, i18n.Errorf("err.pfs0.name")
		}
		name := strTable[nameOff:]
		if end := bytes.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		}
		entries[i] = PFS0Entry{Name: string(name), Offset: dataStart + int64(off), Size: int64(size)}
	}
	return entries, nil
}

// PFS0Source describes one file to be written into a new PFS0: Size bytes
// read from Path starting at Offset.
type PFS0Source struct {
	Name   string
	Path   string
	Offset int64
	Size   int64
}

// PFS0HeaderSize returns the size of the header BuildPFS0Header makes.
func PFS0HeaderSize(files []PFS0Source) int64 {
	return int64(len(BuildPFS0Header(files)))
}

// BuildPFS0Header returns the PFS0 header for files. Its length depends only
// on the names, so it can be written first and rewritten once sizes are known.
func BuildPFS0Header(files []PFS0Source) []byte {
	var strTable []byte
	nameOffs := make([]uint32, len(files))
	for i, f := range files {
		nameOffs[i] = uint32(len(strTable))
		strTable = append(strTable, f.Name...)
		strTable = append(strTable, 0)
	}
	// Pad so the data section starts on a 0x20 boundary, like other tools do.
	headerLen := 0x10 + len(files)*pfs0EntrySize + len(strTable)
	if pad := (0x20 - headerLen%0x20) % 0x20; pad > 0 {
		strTable = append(strTable, make([]byte, pad)...)
	}

	buf := make([]byte, 0x10, 0x10+len(files)*pfs0EntrySize+len(strTable))
	copy(buf, "PFS0")
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(files)))
	binary.LittleEndian.PutUint32(buf[8:], uint32(len(strTable)))

	var dataOff uint64
	for i, f := range files {
		var e [pfs0EntrySize]byte
		binary.LittleEndian.PutUint64(e[0:], dataOff)
		binary.LittleEndian.PutUint64(e[8:], uint64(f.Size))
		binary.LittleEndian.PutUint32(e[16:], nameOffs[i])
		buf = append(buf, e[:]...)
		dataOff += uint64(f.Size)
	}
	return append(buf, strTable...)
}
