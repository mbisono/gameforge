package chd

import (
	"errors"
	"fmt"
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
	start  int    // first data frame in the CHD
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
		t := DiscTrack{Number: n, Type: f["TYPE"], Frames: frames, start: chdFrame}
		if strings.HasPrefix(f["PGTYPE"], "V") && pregap <= frames {
			t.start += pregap
			t.Frames -= pregap
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
