package chd

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
)

// UserDataOffset is where the 2048 bytes of user data start in a sector
// of a CD track type, as stored in a CHD or a BIN file; ok is false for
// audio and other tracks without 2048-byte data sectors.
func UserDataOffset(typ string) (offset int, ok bool) {
	switch typ {
	case "MODE1", "MODE2_FORM1":
		return 0, true
	case "MODE1_RAW":
		return 16, true // sync and header
	case "MODE2_RAW":
		return 24, true // sync, header and subheader (Form 1)
	case "MODE2", "MODE2_FORM_MIX":
		return 8, true // subheader
	}
	return 0, false
}

// DiscTrack is a track of a disc image in a CHD.
type DiscTrack struct {
	Number int
	Type   string // CD track type, e.g. MODE2_RAW or AUDIO
	Frames int    // data frames, without a pregap stored with the track
	Pregap int    // frames of pregap before the data (stored or not)
	Pad    int    // empty frames after the data (GD-ROM: up to the high-density area)
	// Postgap is the frames of postgap after the track (not stored).
	Postgap int
	start   int // first data frame in the CHD
	stored  int // frames of pregap stored in the CHD before start
}

// Disc reads the sectors of a CD, GD-ROM or DVD CHD.
type Disc struct {
	r      *Reader
	Tracks []DiscTrack // empty for a DVD
	GDROM  bool
	DVD    bool

	hunk int // decoded hunk in buf, -1 for none
	buf  []byte
}

// OpenDisc opens a CHD made from a disc image.
func OpenDisc(path string) (*Disc, error) {
	r, err := Open(path)
	if err != nil {
		return nil, err
	}
	d := &Disc{r: r, hunk: -1, buf: make([]byte, r.HunkBytes)}
	for _, m := range r.Meta {
		switch m.tag {
		case tagDVD:
			d.DVD = true
		case tagGDTrack:
			d.GDROM = true
		}
	}
	if d.DVD {
		if r.UnitBytes != DVDSectorSize {
			r.Close()
			return nil, errors.New("chd: unexpected DVD sector size")
		}
		return d, nil
	}
	if r.UnitBytes != FrameSize {
		r.Close()
		return nil, errors.New("chd: not a disc image")
	}
	// Tracks follow each other in the CHD, each padded to a multiple of 4
	// frames; a pregap stored with the track (PGTYPE starting with V)
	// comes before its data.
	chdFrame := 0
	for _, s := range r.CDTracks() {
		f := fields(s)
		n, _ := strconv.Atoi(f["TRACK"])
		frames, err1 := strconv.Atoi(f["FRAMES"])
		pregap, _ := strconv.Atoi(f["PREGAP"])
		if err1 != nil || frames < 0 || pregap < 0 {
			r.Close()
			return nil, fmt.Errorf("chd: invalid track metadata %q", s)
		}
		postgap, _ := strconv.Atoi(f["POSTGAP"])
		t := DiscTrack{Number: n, Type: f["TYPE"], Frames: frames, Pregap: pregap, Postgap: max(postgap, 0), start: chdFrame}
		if strings.HasPrefix(f["PGTYPE"], "V") && pregap <= frames {
			t.start += pregap
			t.Frames -= pregap
			t.stored = pregap
		}
		// On a GD-ROM, empty frames filling the gap up to the
		// high-density area are not part of the track's file.
		if pad, err := strconv.Atoi(f["PAD"]); err == nil && pad > 0 && pad <= t.Frames {
			t.Frames -= pad
			t.Pad = pad
		}
		d.Tracks = append(d.Tracks, t)
		chdFrame += (frames + 3) &^ 3
	}
	if len(d.Tracks) == 0 {
		r.Close()
		return nil, errors.New("chd: no tracks")
	}
	return d, nil
}

// fields splits "KEY:VALUE KEY:VALUE" metadata.
func fields(s string) map[string]string {
	out := map[string]string{}
	for _, w := range strings.Fields(s) {
		if k, v, ok := strings.Cut(w, ":"); ok {
			out[k] = v
		}
	}
	return out
}

func (d *Disc) Close() error { return d.r.Close() }

// Reader returns the underlying CHD reader.
func (d *Disc) Reader() *Reader { return d.r }

// read copies n bytes from the CHD's data at offset into dst.
func (d *Disc) read(dst []byte, offset int64) error {
	for len(dst) > 0 {
		if offset >= d.r.LogicalBytes {
			return errors.New("chd: read beyond the end")
		}
		h := int(offset / int64(d.r.HunkBytes))
		if h != d.hunk {
			if err := d.r.ReadHunk(h, d.buf); err != nil {
				d.hunk = -1
				return err
			}
			d.hunk = h
		}
		n := copy(dst, d.buf[offset%int64(d.r.HunkBytes):])
		dst, offset = dst[n:], offset+int64(n)
	}
	return nil
}

// ReadSector returns the 2048 bytes of user data of sector lba (counted
// from the start of the track's data) of the track at index t in Tracks.
// For a DVD, t is ignored.
func (d *Disc) ReadSector(t, lba int) ([]byte, error) {
	b := make([]byte, DVDSectorSize)
	if d.DVD {
		return b, d.read(b, int64(lba)*DVDSectorSize)
	}
	if t < 0 || t >= len(d.Tracks) {
		return nil, errors.New("chd: no such track")
	}
	tr := d.Tracks[t]
	skip, ok := UserDataOffset(tr.Type)
	if !ok {
		return nil, fmt.Errorf("chd: track %d has no data sectors", tr.Number)
	}
	if lba < 0 || lba >= tr.Frames {
		return nil, fmt.Errorf("chd: sector %d is outside track %d", lba, tr.Number)
	}
	return b, d.read(b, int64(tr.start+lba)*FrameSize+int64(skip))
}

// sectorBytes is the size of a sector of a track type in a BIN file.
func sectorBytes(typ string) int {
	switch typ {
	case "MODE1", "MODE2_FORM1":
		return 2048
	case "MODE2", "MODE2_FORM_MIX":
		return 2336
	case "MODE2_FORM2":
		return 2324
	}
	return SectorSize // raw data and audio
}

// track finds a track by number; 0 stands for the whole image, which on a
// CD is its only track.
func (d *Disc) track(n int) (int, bool) {
	if n == 0 && len(d.Tracks) == 1 {
		return 0, true
	}
	for i, t := range d.Tracks {
		if t.Number == n {
			return i, true
		}
	}
	return 0, false
}

// Size returns the size track n would have as a BIN file (0: the whole
// image, an ISO for a DVD); ok is false if there is no such track.
func (d *Disc) Size(n int) (int64, bool) {
	if d.DVD {
		return d.r.LogicalBytes, n == 0
	}
	i, ok := d.track(n)
	if !ok {
		return 0, false
	}
	t := d.Tracks[i]
	return int64(t.Frames) * int64(sectorBytes(t.Type)), true
}

// CRC computes the CRC-32 of track n as a BIN file (0: the whole image),
// as disc databases list it. It reads the whole track.
func (d *Disc) CRC(ctx context.Context, n int, progress func(done, total int64)) (uint32, error) {
	h := crc32.NewIEEE()
	if d.DVD {
		if n != 0 {
			return 0, errors.New("chd: no such track")
		}
		buf := make([]byte, d.r.HunkBytes)
		for off := int64(0); off < d.r.LogicalBytes; off += int64(len(buf)) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			b := buf[:min(int64(len(buf)), d.r.LogicalBytes-off)]
			if err := d.read(b, off); err != nil {
				return 0, err
			}
			h.Write(b)
			if progress != nil {
				progress(off+int64(len(b)), d.r.LogicalBytes)
			}
		}
		return h.Sum32(), nil
	}
	i, ok := d.track(n)
	if !ok {
		return 0, errors.New("chd: no such track")
	}
	t := d.Tracks[i]
	size := sectorBytes(t.Type)
	b := make([]byte, size)
	for f := 0; f < t.Frames; f++ {
		if f%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if progress != nil {
				progress(int64(f), int64(t.Frames))
			}
		}
		if err := d.read(b, int64(t.start+f)*FrameSize); err != nil {
			return 0, err
		}
		if t.Type == "AUDIO" { // stored big-endian; BIN files are little-endian
			for k := 0; k+1 < len(b); k += 2 {
				b[k], b[k+1] = b[k+1], b[k]
			}
		}
		h.Write(b)
	}
	return h.Sum32(), nil
}
