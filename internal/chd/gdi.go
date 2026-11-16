package chd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseGDI reads a Dreamcast GD-ROM rip (.gdi), laid out as chdman's
// parse_gdi does. Each line is: track, start sector, type (4 data, 0
// audio), sector size, file and an unused offset. The gap between a track
// and the next one (e.g. up to the high-density area at sector 45000) is
// added to the track as empty frames.
func ParseGDI(path string) ([]*Track, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	dir := filepath.Dir(path)
	sc := bufio.NewScanner(strings.NewReader(decodeCueText(b)))
	count := -1
	byNum := map[int]*Track{}
	phys := map[int]int{}
	for lineNo := 1; sc.Scan(); lineNo++ {
		f := cueFields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if count < 0 {
			n, err := strconv.Atoi(f[0])
			if err != nil || n <= 0 || n > 99 {
				return nil, fmt.Errorf("%s:%d: invalid track count %q", name, lineNo, f[0])
			}
			count = n
			continue
		}
		if len(f) != 6 {
			return nil, fmt.Errorf("%s:%d: a track line has 6 fields, found %d", name, lineNo, len(f))
		}
		num, err1 := strconv.Atoi(f[0])
		lba, err2 := strconv.Atoi(f[1])
		typ, err3 := strconv.Atoi(f[2])
		size, err4 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || num < 1 || num > count {
			return nil, fmt.Errorf("%s:%d: invalid track line", name, lineNo)
		}
		t := &Track{Number: num, File: f[4], gdrom: true, LBA: lba}
		if !filepath.IsAbs(t.File) {
			t.File = filepath.Join(dir, t.File)
		}
		switch {
		case typ == 4 && size == 2352:
			t.Type, t.DataSize = "MODE1_RAW", 2352
		case typ == 4 && size == 2048:
			t.Type, t.DataSize = "MODE1", 2048
		case typ == 0:
			t.Type, t.DataSize, t.swap = "AUDIO", 2352, true
		default:
			return nil, fmt.Errorf("%s:%d: unknown track type %d with sector size %d", name, lineNo, typ, size)
		}
		st, err := os.Stat(t.File)
		if err != nil {
			return nil, err
		}
		t.Frames = int(st.Size() / int64(size))
		byNum[num], phys[num] = t, lba
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("%s: no track count", name)
	}
	tracks := make([]*Track, count)
	for i := range tracks {
		t, ok := byNum[i+1]
		if !ok {
			return nil, fmt.Errorf("%s: track %d is missing", name, i+1)
		}
		tracks[i] = t
	}
	// The gap up to the next track's start sector pads the track with empty
	// frames, as chdman 0.289 does. (Later MAME sources turn gaps other
	// than the one before the high-density area into pregaps; released
	// chdman versions and the emulators reading their CHDs use padding.)
	for i := 1; i < count; i++ {
		prev := tracks[i-1]
		dif := phys[i+1] - (prev.Frames + phys[i])
		if dif < 0 {
			return nil, fmt.Errorf("%s: track %d starts inside track %d", name, i+1, i)
		}
		prev.Frames += dif
		prev.Pad = dif
	}
	for _, t := range tracks {
		if t.Frames <= 0 {
			return nil, fmt.Errorf("%s: track %d is empty", name, t.Number)
		}
	}
	return tracks, nil
}

// ConvertGDI writes a Dreamcast GD-ROM rip as a CHD with GD-ROM metadata,
// as chdman createcd does with a .gdi.
func ConvertGDI(ctx context.Context, gdiPath, outPath string, opt Options) (*Result, error) {
	tracks, err := ParseGDI(gdiPath)
	if err != nil {
		return nil, err
	}
	return convertTracks(ctx, tracks, outPath, opt)
}
