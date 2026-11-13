package nx

import (
	"bytes"
	"encoding/binary"
	"io"

	"gameforge/internal/i18n"
)

// A game card image (XCI) holds a root HFS0 partition table whose "secure"
// partition is another HFS0 with the game's NCAs (and those of any update
// or DLC on the card). An NSP is the same NCAs in a PFS0, so the secure
// partition can be used as if it were an NSP.

// ReadXCI returns the files of the secure partition of an XCI. Offsets are
// absolute in r, like those of ReadPFS0.
func ReadXCI(r io.ReaderAt) ([]PFS0Entry, error) {
	// Dumps with the card's key area start with 0x1000 extra bytes.
	var base int64 = -1
	for _, b := range []int64{0, 0x1000} {
		var magic [4]byte
		if _, err := r.ReadAt(magic[:], b+0x100); err == nil && string(magic[:]) == "HEAD" {
			base = b
			break
		}
	}
	if base < 0 {
		return nil, i18n.Errorf("err.xci.format", "HEAD")
	}
	var off [8]byte
	if _, err := r.ReadAt(off[:], base+0x130); err != nil {
		return nil, i18n.Errorf("err.xci.format", err)
	}
	root, err := readHFS0(r, base+int64(binary.LittleEndian.Uint64(off[:])))
	if err != nil {
		return nil, err
	}
	for _, p := range root {
		if p.Name == "secure" {
			return readHFS0(r, p.Offset)
		}
	}
	return nil, i18n.Errorf("err.xci.format", "secure")
}

// readHFS0 parses an HFS0 header at base: like PFS0, with 0x40-byte entries
// that also hold a hash of each file's start.
func readHFS0(r io.ReaderAt, base int64) ([]PFS0Entry, error) {
	const entrySize = 0x40
	var hdr [0x10]byte
	if _, err := r.ReadAt(hdr[:], base); err != nil {
		return nil, i18n.Errorf("err.xci.format", err)
	}
	if string(hdr[:4]) != "HFS0" {
		return nil, i18n.Errorf("err.xci.format", "HFS0")
	}
	n := binary.LittleEndian.Uint32(hdr[4:])
	strSize := binary.LittleEndian.Uint32(hdr[8:])
	if n > 100000 || strSize > 16<<20 {
		return nil, i18n.Errorf("err.xci.format", "HFS0")
	}
	meta := make([]byte, int(n)*entrySize+int(strSize))
	if _, err := r.ReadAt(meta, base+0x10); err != nil {
		return nil, i18n.Errorf("err.xci.format", err)
	}
	strTable := meta[int(n)*entrySize:]
	dataStart := base + 0x10 + int64(len(meta))
	entries := make([]PFS0Entry, n)
	for i := range entries {
		e := meta[i*entrySize:]
		nameOff := binary.LittleEndian.Uint32(e[16:])
		if int(nameOff) >= len(strTable) {
			return nil, i18n.Errorf("err.xci.format", "HFS0")
		}
		name := strTable[nameOff:]
		if end := bytes.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		}
		entries[i] = PFS0Entry{
			Name:   string(name),
			Offset: dataStart + int64(binary.LittleEndian.Uint64(e[0:])),
			Size:   int64(binary.LittleEndian.Uint64(e[8:])),
		}
	}
	return entries, nil
}
