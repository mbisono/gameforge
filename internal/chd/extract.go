package chd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extracting writes the disc image of a CHD back as the files it is
// usually kept in: a CUE sheet with one BIN file per track, named and laid
// out as Redump's (a track's pregap stored in the CHD goes at the start of
// its file, after INDEX 00), a .gdi with its track files for a GD-ROM, or
// an ISO for a DVD and for a CD with one data track.

// ExtractOptions are the settings of an extraction.
type ExtractOptions struct {
	// Progress receives the bytes of the CHD's image read so far.
	Progress func(done, total int64)
	// Verify reads the written files back and compares their SHA-1 with
	// what was written; VerifyProgress receives the bytes read.
	Verify         bool
	VerifyProgress func(done, total int64)
}

// ExtractResult tells what an extraction wrote.
type ExtractResult struct {
	Files      []string // the sheet (or the ISO) first, then the track files
	Tracks     int      // tracks written (1 for an ISO)
	InputSize  int64    // of the CHD
	OutputSize int64    // of all the files written
}

// ExistsError is returned when a file to write is already there; nothing
// is written then.
type ExistsError struct{ Name string }

func (e *ExistsError) Error() string { return e.Name + " already exists" }

// CanISO reports whether the disc can be written as an ISO: a DVD, or a CD
// with only one track, of data. From a CD in raw sectors, an ISO keeps
// only the 2048 bytes of user data of each sector.
func (d *Disc) CanISO() bool {
	if d.DVD {
		return true
	}
	_, data := UserDataOffset(d.Tracks[0].Type)
	return len(d.Tracks) == 1 && !d.GDROM && data
}

// DefaultExt is the extension of the image a disc is extracted to by
// default: ".iso" for a DVD and for a CD made from an ISO (one track of
// 2048-byte sectors), ".gdi" for a GD-ROM and ".cue" for other CDs.
func (d *Disc) DefaultExt() string {
	switch {
	case d.DVD:
		return ".iso"
	case d.GDROM:
		return ".gdi"
	case len(d.Tracks) == 1 && sectorBytes(d.Tracks[0].Type) == DVDSectorSize:
		return ".iso"
	}
	return ".cue"
}

// segment is a run of units (frames, or DVD sectors) of the CHD written
// to a file: size bytes of each, from offset in the unit.
type segment struct {
	file         string
	first, count int
	offset, size int
	swap         bool // audio: stored big endian, written little endian
	w            *bufio.Writer
	f            *os.File
	h            hash.Hash
	sum          []byte
	written      int64 // units
}

// cueModes are the CUE sheet modes of the CHD track types.
var cueModes = map[string]string{
	"MODE1":          "MODE1/2048",
	"MODE1_RAW":      "MODE1/2352",
	"MODE2":          "MODE2/2336",
	"MODE2_FORM1":    "MODE2/2048",
	"MODE2_FORM2":    "MODE2/2324",
	"MODE2_FORM_MIX": "MODE2/2336",
	"MODE2_RAW":      "MODE2/2352",
	"AUDIO":          "AUDIO",
}

// msf writes a number of frames as minutes:seconds:frames.
func msf(frames int) string {
	return fmt.Sprintf("%02d:%02d:%02d", frames/(60*75), frames/75%60, frames%75)
}

// trackFile names the file of a track as Redump does: "Game (Track 1).bin",
// with two digits on discs with 10 tracks or more.
func trackFile(base string, n, tracks int) string {
	if tracks == 1 {
		return base + ".bin"
	}
	if tracks >= 10 {
		return fmt.Sprintf("%s (Track %02d).bin", base, n)
	}
	return fmt.Sprintf("%s (Track %d).bin", base, n)
}

// plan works out the files to write for outPath (its extension tells the
// format) and the text of the sheet, if any.
func (d *Disc) plan(outPath string) (segs []*segment, sheet []byte, err error) {
	ext := strings.ToLower(filepath.Ext(outPath))
	dir := filepath.Dir(outPath)
	base := strings.TrimSuffix(filepath.Base(outPath), filepath.Ext(outPath))
	switch ext {
	case ".iso":
		if !d.CanISO() {
			return nil, nil, errors.New("chd: this disc has more than one track or no data track; it cannot be an ISO")
		}
		if d.DVD {
			n := int(d.r.LogicalBytes / DVDSectorSize)
			return []*segment{{file: outPath, count: n, size: DVDSectorSize}}, nil, nil
		}
		t := d.Tracks[0]
		skip, _ := UserDataOffset(t.Type)
		return []*segment{{file: outPath, first: t.start, count: t.Frames, offset: skip, size: DVDSectorSize}}, nil, nil
	case ".cue":
		if d.DVD {
			return nil, nil, errors.New("chd: a DVD is extracted to an ISO, not a CUE sheet")
		}
		if d.GDROM {
			return nil, nil, errors.New("chd: a GD-ROM is extracted to a .gdi, not a CUE sheet")
		}
		var b bytes.Buffer
		for _, t := range d.Tracks {
			mode, ok := cueModes[t.Type]
			if !ok {
				return nil, nil, fmt.Errorf("chd: track %d has the unknown type %s", t.Number, t.Type)
			}
			name := trackFile(base, t.Number, len(d.Tracks))
			segs = append(segs, &segment{file: filepath.Join(dir, name), first: t.start - t.stored,
				count: t.stored + t.Frames, size: sectorBytes(t.Type), swap: t.Type == "AUDIO"})
			fmt.Fprintf(&b, "FILE \"%s\" BINARY\r\n  TRACK %02d %s\r\n", name, t.Number, mode)
			switch {
			case t.stored > 0:
				fmt.Fprintf(&b, "    INDEX 00 00:00:00\r\n    INDEX 01 %s\r\n", msf(t.stored))
			case t.Pregap > 0:
				fmt.Fprintf(&b, "    PREGAP %s\r\n    INDEX 01 00:00:00\r\n", msf(t.Pregap))
			default:
				b.WriteString("    INDEX 01 00:00:00\r\n")
			}
			if t.Postgap > 0 {
				fmt.Fprintf(&b, "    POSTGAP %s\r\n", msf(t.Postgap))
			}
		}
		return segs, b.Bytes(), nil
	case ".gdi":
		if !d.GDROM {
			return nil, nil, errors.New("chd: only a GD-ROM is extracted to a .gdi")
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "%d\r\n", len(d.Tracks))
		lba := 0
		for _, t := range d.Tracks {
			var kind int
			switch t.Type {
			case "MODE1_RAW", "MODE1":
				kind = 4
			case "AUDIO":
				kind = 0
			default:
				return nil, nil, fmt.Errorf("chd: GD-ROM track %d has the type %s, which a .gdi cannot hold", t.Number, t.Type)
			}
			if t.stored == 0 {
				lba += t.Pregap
			}
			size := sectorBytes(t.Type)
			name := trackFile(base, t.Number, len(d.Tracks))
			segs = append(segs, &segment{file: filepath.Join(dir, name), first: t.start - t.stored,
				count: t.stored + t.Frames, size: size, swap: t.Type == "AUDIO"})
			fmt.Fprintf(&b, "%d %d %d %d \"%s\" 0\r\n", t.Number, lba, kind, size, name)
			lba += t.stored + t.Frames + t.Pad
		}
		return segs, b.Bytes(), nil
	}
	return nil, nil, fmt.Errorf("chd: cannot extract to %s files", ext)
}

// Extract writes the disc image of the CHD at chdPath as outPath, whose
// extension (.cue, .gdi or .iso) tells the format, and the track files the
// sheet names, next to it. None of the files may exist yet. On an error,
// the files written so far are removed.
func Extract(ctx context.Context, chdPath, outPath string, opt ExtractOptions) (res *ExtractResult, err error) {
	d, err := OpenDisc(chdPath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	segs, sheet, err := d.plan(outPath)
	if err != nil {
		return nil, err
	}
	files := []string{}
	if sheet != nil {
		files = append(files, outPath)
	}
	for _, s := range segs {
		files = append(files, s.file)
	}
	seen := map[string]bool{}
	for _, f := range files {
		key := strings.ToLower(filepath.Clean(f))
		if seen[key] {
			return nil, fmt.Errorf("chd: %s would be written twice", filepath.Base(f))
		}
		seen[key] = true
		if _, err := os.Lstat(f); err == nil {
			return nil, &ExistsError{filepath.Base(f)}
		}
	}

	var created []string
	defer func() {
		for _, s := range segs {
			if s.f != nil {
				s.f.Close()
			}
		}
		if err != nil {
			for _, f := range created {
				os.Remove(f)
			}
		}
	}()
	create := func(path string) (*os.File, error) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			created = append(created, path)
		}
		return f, err
	}
	var sheetSum []byte
	if sheet != nil {
		f, err := create(outPath)
		if err != nil {
			return nil, err
		}
		_, err = f.Write(sheet)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		s := sha1.Sum(sheet)
		sheetSum = s[:]
	}

	unit := d.r.UnitBytes
	if d.r.HunkBytes%unit != 0 {
		return nil, errors.New("chd: hunks are not made of whole frames")
	}
	perHunk := d.r.HunkBytes / unit
	units := int(d.r.LogicalBytes / int64(unit))
	cur := 0
	tmp := make([]byte, SectorSize)
	err = d.r.eachHunk(ctx, func(n int, buf []byte) error {
		for k := range perHunk {
			u := n*perHunk + k
			if u >= units {
				break
			}
			for cur < len(segs) && u >= segs[cur].first+segs[cur].count {
				if err := segs[cur].finish(); err != nil {
					return err
				}
				cur++
			}
			if cur == len(segs) {
				return nil
			}
			s := segs[cur]
			if u < s.first {
				continue
			}
			if s.f == nil {
				f, err := create(s.file)
				if err != nil {
					return err
				}
				s.f, s.h = f, sha1.New()
				s.w = bufio.NewWriterSize(io.MultiWriter(f, s.h), 1<<20)
			}
			b := buf[k*unit+s.offset : k*unit+s.offset+s.size]
			if s.swap {
				b = tmp[:len(b)]
				copy(b, buf[k*unit+s.offset:])
				for i := 0; i+1 < len(b); i += 2 {
					b[i], b[i+1] = b[i+1], b[i]
				}
			}
			if _, err := s.w.Write(b); err != nil {
				return err
			}
			s.written++
		}
		if opt.Progress != nil {
			opt.Progress(min(int64(n+1)*int64(d.r.HunkBytes), d.r.LogicalBytes), d.r.LogicalBytes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for ; cur < len(segs); cur++ {
		if err := segs[cur].finish(); err != nil {
			return nil, err
		}
	}

	res = &ExtractResult{Files: files, Tracks: len(segs)}
	if st, err := os.Stat(chdPath); err == nil {
		res.InputSize = st.Size()
	}
	var total int64
	for _, s := range segs {
		if s.written != int64(s.count) {
			return nil, fmt.Errorf("chd: the image ends inside %s", filepath.Base(s.file))
		}
		total += s.written * int64(s.size)
	}
	res.OutputSize = total + int64(len(sheet))

	if opt.Verify {
		var done int64
		check := func(path string, want []byte) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			h := sha1.New()
			buf := make([]byte, 1<<20)
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				n, err := f.Read(buf)
				h.Write(buf[:n])
				done += int64(n)
				if opt.VerifyProgress != nil {
					opt.VerifyProgress(done, total)
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
			if !bytes.Equal(h.Sum(nil), want) {
				return fmt.Errorf("chd: %s differs from what was written", filepath.Base(path))
			}
			return nil
		}
		if sheet != nil {
			if err = check(outPath, sheetSum); err != nil {
				return nil, err
			}
			done = 0
		}
		for _, s := range segs {
			if err = check(s.file, s.sum); err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}

// finish flushes and closes a segment's file and keeps its SHA-1.
func (s *segment) finish() error {
	if s.f == nil {
		return nil
	}
	err := s.w.Flush()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	s.f = nil
	s.sum = s.h.Sum(nil)
	return err
}
