package chd

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// Nero's NRG images keep the sectors first and, at the end, a chain of
// chunks describing the tracks, found through a footer: "NER5" and a
// 64-bit offset in the last 12 bytes, or "NERO" and a 32-bit offset in
// the last 8 (images of Nero before 5.5). All numbers are big endian.
// Disc-at-once sessions are described by DAOX (DAOI in old images), with
// each track's pregap, start and end as offsets in the file; track-at-once
// sessions by ETN2 (ETNF). CUEX (CUES) gives the tracks' positions on the
// disc, which tell the gaps between sessions.

// ParseNRG reads the tracks of an NRG image.
func ParseNRG(path string) ([]*Track, error) {
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
	if size < 12 {
		return nil, bad("not an NRG image")
	}
	foot := make([]byte, 12)
	if _, err := img.ReadAt(foot, size-12); err != nil {
		return nil, err
	}
	var start, end int64
	switch {
	case string(foot[0:4]) == "NER5":
		start, end = int64(binary.BigEndian.Uint64(foot[4:])), size-12
	case string(foot[4:8]) == "NERO":
		start, end = int64(binary.BigEndian.Uint32(foot[8:])), size-8
	default:
		return nil, bad("not an NRG image (no NERO or NER5 footer)")
	}
	if start < 0 || start >= end {
		return nil, bad("invalid NRG footer")
	}
	chain := make([]byte, end-start)
	if _, err := img.ReadAt(chain, start); err != nil {
		return nil, err
	}

	cues := map[int][]cuePoint{} // by track number
	var tracks []*Track
	for p := 0; p+8 <= len(chain); {
		id := string(chain[p : p+4])
		n := int(binary.BigEndian.Uint32(chain[p+4:]))
		p += 8
		if id == "END!" {
			break
		}
		if n < 0 || p+n > len(chain) {
			return nil, bad("chunk %s is cut off", id)
		}
		d := chain[p : p+n]
		p += n
		switch id {
		case "CUEX", "CUES":
			for i := 0; i+8 <= len(d); i += 8 {
				track := bcd(d[i+1])
				if d[i+1] == 0xaa || track == 0 {
					continue // lead-in, lead-out
				}
				var lba int
				if id == "CUEX" {
					lba = int(int32(binary.BigEndian.Uint32(d[i+4:])))
				} else { // minutes, seconds and frames, counted from 00:00:00
					lba = (bcd(d[i+5])*60+bcd(d[i+6]))*75 + bcd(d[i+7]) - 150
				}
				cues[track] = append(cues[track], cuePoint{bcd(d[i+2]), lba})
			}
		case "DAOX", "DAOI":
			// Session header: size, UPC, TOC type, first and last track.
			head, per := 22, 42
			if id == "DAOI" {
				head, per = 22, 30
			}
			if len(d) < head {
				return nil, bad("chunk %s is too short", id)
			}
			first := int(d[head-2])
			for i, num := head, first; i+per <= len(d); i, num = i+per, num+1 {
				e := d[i+12:] // after the ISRC
				secSize := int(binary.BigEndian.Uint16(e[0:]))
				code := int(e[2])
				var idx0, idx1, endOff int64
				if id == "DAOX" {
					idx0 = int64(binary.BigEndian.Uint64(e[6:]))
					idx1 = int64(binary.BigEndian.Uint64(e[14:]))
					endOff = int64(binary.BigEndian.Uint64(e[22:]))
				} else {
					idx0 = int64(binary.BigEndian.Uint32(e[6:]))
					idx1 = int64(binary.BigEndian.Uint32(e[10:]))
					endOff = int64(binary.BigEndian.Uint32(e[14:]))
				}
				t, err := nrgTrack(img, num, code, secSize, idx0, idx1, endOff)
				if err != nil {
					return nil, bad("%v", err)
				}
				tracks = append(tracks, t)
			}
		case "ETN2", "ETNF":
			per := 32
			if id == "ETNF" {
				per = 20
			}
			for i := 0; i+per <= len(d); i += per {
				var off, length int64
				var code int
				if id == "ETN2" {
					off = int64(binary.BigEndian.Uint64(d[i:]))
					length = int64(binary.BigEndian.Uint64(d[i+8:]))
					code = int(binary.BigEndian.Uint32(d[i+16:]))
				} else {
					off = int64(binary.BigEndian.Uint32(d[i:]))
					length = int64(binary.BigEndian.Uint32(d[i+4:]))
					code = int(binary.BigEndian.Uint32(d[i+8:]))
				}
				t, err := nrgTrack(img, len(tracks)+1, code, nrgSectorSize(code), off, off, off+length)
				if err != nil {
					return nil, bad("%v", err)
				}
				tracks = append(tracks, t)
			}
		}
	}
	if len(tracks) == 0 {
		return nil, bad("no tracks found")
	}
	for _, t := range tracks {
		t.File = path
	}
	// The gaps between sessions, which the image does not store, keep the
	// tracks at their places on the disc.
	for i := 1; i < len(tracks); i++ {
		prev, t := tracks[i-1], tracks[i]
		p1, ok1 := index1(cues[prev.Number])
		t1, ok2 := index1(cues[t.Number])
		if !ok1 || !ok2 {
			continue
		}
		// Where each track's data in the file starts on the disc.
		addGap(t, (t1-t.Pregap)-(p1-prev.Pregap+prev.Frames))
	}
	return tracks, nil
}

type cuePoint struct{ index, lba int }

// index1 is the position on the disc of a track's INDEX 01.
func index1(points []cuePoint) (int, bool) {
	for _, p := range points {
		if p.index == 1 {
			return p.lba, true
		}
	}
	return 0, false
}

// nrgSectorSize is the bytes per sector of an NRG mode code.
func nrgSectorSize(code int) int {
	switch code {
	case 0x00:
		return 2048
	case 0x02, 0x03:
		return 2336
	case 0x0f, 0x10, 0x11:
		return 2448
	}
	return 2352
}

// nrgTrack makes the track of an NRG mode code and its offsets in the file.
func nrgTrack(img *Image, num, code, secSize int, idx0, idx1, end int64) (*Track, error) {
	if secSize <= 0 {
		secSize = nrgSectorSize(code)
	}
	if idx0 > idx1 || idx1 >= end || end > img.Size() {
		return nil, fmt.Errorf("track %d: invalid offsets", num)
	}
	t := &Track{Number: num, File: "", Offset: idx0, Frames: int((end - idx0) / int64(secSize))}
	t.Pregap = int((idx1 - idx0) / int64(secSize))
	t.PregapInFile = t.Pregap > 0
	audio := code == 0x07 || code == 0x10
	if err := setTrackType(t, img, audio, code == 0x03, secSize, idx1); err != nil {
		return nil, fmt.Errorf("track %d: %w", num, err)
	}
	return t, nil
}

// setTrackType sets the type, sector size and stride of a track of an NRG
// or CDI image from its sector size in the file, reading its first sector
// (at start) to tell Mode 1 from Mode 2 in raw tracks.
func setTrackType(t *Track, img *Image, audio, mode2 bool, secSize int, start int64) error {
	switch {
	case audio:
		t.Type, t.DataSize = "AUDIO", SectorSize
		if secSize > SectorSize {
			t.Stride = secSize
		}
		t.swap = !bigEndianAudio(img, start, secSize, t.Frames-t.Pregap)
	case secSize == 2048:
		t.Type, t.DataSize = "MODE1", 2048
		if mode2 {
			t.Type = "MODE2_FORM1"
		}
	case secSize == 2336:
		t.Type, t.DataSize = "MODE2", 2336
	case secSize >= SectorSize:
		head := make([]byte, 16)
		if _, err := img.ReadAt(head, start); err != nil {
			return err
		}
		t.Type, t.DataSize = "MODE1_RAW", SectorSize
		if head[15] == 2 {
			t.Type = "MODE2_RAW"
		}
		if secSize > SectorSize {
			t.Stride = secSize
		}
	default:
		return fmt.Errorf("sectors of %d bytes are not supported", secSize)
	}
	return nil
}

// bigEndianAudio tells whether the audio of a track is stored big endian
// (CHD's order) rather than little endian (as in BIN files): music changes
// little from one sample to the next, so the order in which neighbouring
// samples differ less is the right one. It looks at up to 64 sectors from
// the middle of the track.
func bigEndianAudio(img *Image, start int64, stride, frames int) bool {
	if frames <= 0 {
		return false
	}
	n := min(frames, 64)
	from := start + int64(frames/2-n/2)*int64(stride)
	var le, be uint64
	buf := make([]byte, SectorSize)
	for i := range n {
		if _, err := img.ReadAt(buf, from+int64(i)*int64(stride)); err != nil {
			break
		}
		var pl, pb [2]int
		for j := 0; j+3 < len(buf); j += 4 {
			for c := range 2 {
				vl := int(int16(binary.LittleEndian.Uint16(buf[j+2*c:])))
				vb := int(int16(binary.BigEndian.Uint16(buf[j+2*c:])))
				le += uint64(abs(vl - pl[c]))
				be += uint64(abs(vb - pb[c]))
				pl[c], pb[c] = vl, vb
			}
		}
	}
	return be < le
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// bcd decodes a BCD byte.
func bcd(b byte) int { return int(b>>4)*10 + int(b&0x0f) }
