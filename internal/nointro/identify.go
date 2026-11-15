package nointro

import (
	"bytes"
	"context"
	"hash/crc32"
	"io"
	"strings"
)

// Fix turns a ROM as dumped by other tools into the data No-Intro
// describes: without a header that is not part of the cartridge, or in
// the byte order of the database.
type Fix struct {
	Skip int64 // bytes of header to leave out
	Swap int   // 2: swap byte pairs (.v64), 4: reverse 4-byte words (.n64)
	What string
}

// Fixes returns the ways the ROM whose first bytes are head may differ
// from the database: copier headers (SNES), emulator headers (iNES, fwNES,
// Lynx, Atari 7800) and Nintendo 64 byte orders. The databases list most
// headered ROMs too, so these are tried only when the ROM as it is is not
// found.
func Fixes(head []byte, size int64, ext string) []Fix {
	ext = strings.ToLower(ext)
	var out []Fix
	switch {
	case bytes.HasPrefix(head, []byte("NES\x1a")) && size > 16:
		out = append(out, Fix{Skip: 16, What: "iNES"})
	case bytes.HasPrefix(head, []byte("FDS\x1a")) && size > 16:
		out = append(out, Fix{Skip: 16, What: "fwNES"})
	case bytes.HasPrefix(head, []byte("LYNX")) && size > 64:
		out = append(out, Fix{Skip: 64, What: "Lynx"})
	case len(head) >= 10 && string(head[1:10]) == "ATARI7800" && size > 128:
		out = append(out, Fix{Skip: 128, What: "Atari 7800"})
	case bytes.HasPrefix(head, []byte{0x37, 0x80, 0x40, 0x12}) && size%2 == 0:
		out = append(out, Fix{Swap: 2, What: "byte-swapped"})
	case bytes.HasPrefix(head, []byte{0x40, 0x12, 0x37, 0x80}) && size%4 == 0:
		out = append(out, Fix{Swap: 4, What: "little-endian"})
	}
	if size%1024 == 512 && size > 512 && SystemOf(ext) == "SNES" {
		out = append(out, Fix{Skip: 512, What: "copier"})
	}
	return out
}

// CRC computes the CRC-32 and size of a ROM read from r, with a fix
// applied; progress, if set, gets the bytes read.
func CRC(ctx context.Context, r io.Reader, fix Fix, progress func(done int64)) (uint32, int64, error) {
	if fix.Skip > 0 {
		if _, err := io.CopyN(io.Discard, r, fix.Skip); err != nil {
			return 0, 0, err
		}
	}
	h := crc32.NewIEEE()
	buf := make([]byte, 1<<20)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		n, err := io.ReadFull(r, buf)
		if fix.Swap > 1 {
			swap(buf[:n-n%fix.Swap], fix.Swap)
		}
		h.Write(buf[:n])
		size += int64(n)
		if progress != nil {
			progress(size + fix.Skip)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return h.Sum32(), size, nil
		}
		if err != nil {
			return 0, 0, err
		}
	}
}

// swap puts the bytes of b in big-endian order, from byte pairs (2) or
// little-endian 4-byte words (4).
func swap(b []byte, n int) {
	for i := 0; i+n <= len(b); i += n {
		if n == 2 {
			b[i], b[i+1] = b[i+1], b[i]
		} else {
			b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
		}
	}
}

// Identify finds a ROM in the databases. crc and size are those of the
// file as it is (known from an archive's header, or computed with CRC);
// open reads it again from the start when a fix must be tried. system is
// the console the extension tells, if any. It returns the matches and the
// fix that found them (a zero Fix when the ROM was found as it is).
func (db *DB) Identify(ctx context.Context, crc uint32, size int64, ext string, open func() (io.ReadCloser, error), progress func(done int64)) ([]Match, Fix, error) {
	system := SystemOf(ext)
	if m := db.Find(crc, size, system); len(m) > 0 {
		return m, Fix{}, nil
	}
	if open == nil {
		return nil, Fix{}, nil
	}
	rc, err := open()
	if err != nil {
		return nil, Fix{}, err
	}
	head := make([]byte, 128)
	n, _ := io.ReadFull(rc, head)
	rc.Close()
	for _, fix := range Fixes(head[:n], size, ext) {
		rc, err := open()
		if err != nil {
			return nil, Fix{}, err
		}
		c, s, err := CRC(ctx, rc, fix, progress)
		rc.Close()
		if err != nil {
			return nil, Fix{}, err
		}
		if m := db.Find(c, s, system); len(m) > 0 {
			return m, fix, nil
		}
	}
	return nil, Fix{}, nil
}
