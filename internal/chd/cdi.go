package chd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// DiscJuggler's CDI images keep every sector of the disc from 00:00:00,
// track after track, and at the end a description of the sessions and
// tracks. The last 8 bytes hold the version (0x80000004, 0x80000005 or
// 0x80000006) and where the description starts: as an offset from the
// start of the file, or, in the newest version, as its size counted back
// from the end. All numbers are little endian. Each track's block starts
// with the same 12 bytes and holds the lengths of its indices (pregap and
// the rest), its mode, its position on the disc, its length and its
// sector size.

var cdiMark = []byte{0xff, 0xff, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff}

// cdiSectorSizes are the bytes per sector of the read modes.
var cdiSectorSizes = []int{2048, 2336, 2352, 2368, 2448}

// ParseCDI reads the tracks of a CDI image.
func ParseCDI(path string) ([]*Track, error) {
	img, err := OpenImage(path)
	if err != nil {
		return nil, err
	}
	defer img.Close()
	name := filepath.Base(path)
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%s: %s", name, fmt.Sprintf(format, a...))
	}
	size := img.Size()
	if size < 8 {
		return nil, bad("not a CDI image")
	}
	tail := make([]byte, 8)
	if _, err := img.ReadAt(tail, size-8); err != nil {
		return nil, err
	}
	version := binary.LittleEndian.Uint32(tail)
	at := int64(binary.LittleEndian.Uint32(tail[4:]))
	switch version {
	case 0x80000004, 0x80000005:
	case 0x80000006:
		at = size - at
	default:
		return nil, bad("not a CDI image (unknown version %#x)", version)
	}
	if at <= 0 || at >= size-8 {
		return nil, bad("invalid CDI header offset")
	}
	d := make([]byte, size-at)
	if _, err := img.ReadAt(d, at); err != nil {
		return nil, err
	}

	u16 := func(p int) int { return int(binary.LittleEndian.Uint16(d[p:])) }
	u32 := func(p int) int { return int(binary.LittleEndian.Uint32(d[p:])) }
	var tracks []*Track
	var starts []int
	total := -1
	var offset int64
	for p := 0; ; {
		i := bytes.Index(d[p:], cdiMark)
		if i < 0 {
			break
		}
		p += i
		if p+0x11 > len(d) {
			break
		}
		if total < 0 {
			total = int(d[p+0x0f])
		}
		if len(tracks) == total {
			break // the disc's block, after the tracks
		}
		f := int(d[p+0x10])
		q := p + 0x30 + f
		if q+2 > len(d) {
			return nil, bad("track block %d is cut off", len(tracks)+1)
		}
		nIdx := u16(q)
		idx := q + 2
		q = idx + 4*nIdx
		if q+4 > len(d) {
			return nil, bad("track block %d is cut off", len(tracks)+1)
		}
		nText := u32(q)
		q += 4
		for range nText * 18 { // CD-Text: 18 strings per block, each with its length first
			if q >= len(d) {
				return nil, bad("track block %d is cut off", len(tracks)+1)
			}
			q += 1 + int(d[q])
		}
		base := q - 0x36 // the fields below are at fixed places after the CD-Text
		if base+0x64 > len(d) {
			return nil, bad("track block %d is cut off", len(tracks)+1)
		}
		mode := int(d[base+0x38])
		start := u32(base + 0x48)
		length := u32(base + 0x4c)
		readMode := u32(base + 0x60)
		if mode > 2 || readMode >= len(cdiSectorSizes) || length <= 0 {
			return nil, bad("unsupported track %d (mode %d, read mode %d)", len(tracks)+1, mode, readMode)
		}
		secSize := cdiSectorSizes[readMode]
		t := &Track{Number: len(tracks) + 1, File: path, Offset: offset, Frames: length}
		if nIdx >= 2 {
			t.Pregap = u32(idx)
			t.PregapInFile = t.Pregap > 0
		}
		if offset+int64(length)*int64(secSize) > at {
			return nil, bad("track %d goes past the end of the sectors", t.Number)
		}
		if err := setTrackType(t, img, mode == 0, mode == 2, secSize, offset+int64(t.Pregap)*int64(secSize)); err != nil {
			return nil, bad("track %d: %v", t.Number, err)
		}
		tracks = append(tracks, t)
		starts = append(starts, start)
		offset += int64(length) * int64(secSize)
		p = base + 0x64
	}
	if len(tracks) == 0 || len(tracks) != total {
		return nil, bad("found %d of %d tracks", len(tracks), max(total, 0))
	}
	// The gaps between sessions, which the image does not store (on
	// Dreamcast discs the data session starts at sector 11700 or so), keep
	// the tracks at their places on the disc.
	for i := 1; i < len(tracks); i++ {
		addGap(tracks[i], starts[i]-(starts[i-1]+tracks[i-1].Frames))
	}
	return tracks, nil
}
