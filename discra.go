package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gameforge/internal/chd"
	"gameforge/internal/retroach"
)

// gdHighDensity is where a GD-ROM's high-density area starts, and
// gdEnd where it ends.
const (
	gdHighDensity = 45000
	gdEnd         = 549150
)

// discReader opens a disc image of the Discs tab for the RetroAchievements
// hashes: sectors by their address on the disc. done closes it.
func discReader(path string) (retroach.Disc, func(), error) {
	switch {
	case isCHD(path):
		return chdReader(path)
	case isISO(path):
		f, err := os.Open(path)
		if err != nil {
			return retroach.Disc{}, nil, err
		}
		read := func(lba int64) ([]byte, error) {
			b := make([]byte, 2048)
			_, err := f.ReadAt(b, lba*2048)
			return b, err
		}
		return retroach.Disc{Read: read}, func() { f.Close() }, nil
	case strings.EqualFold(filepath.Ext(path), ".gdi"):
		return gdiReader(path)
	}
	return cueReader(path)
}

// span is a data track's place on the disc, for reading sectors by
// their address.
type span struct {
	start, frames int64
	read          func(rel int64) ([]byte, error)
}

func readSpans(spans []span, start int64) retroach.Disc {
	return retroach.Disc{Start: start, Read: func(lba int64) ([]byte, error) {
		for _, s := range spans {
			if lba >= s.start && lba < s.start+s.frames {
				return s.read(lba - s.start)
			}
		}
		return nil, fmt.Errorf("sector %d is not in a data track", lba)
	}}
}

func chdReader(path string) (retroach.Disc, func(), error) {
	d, err := chd.OpenDisc(path)
	if err != nil {
		return retroach.Disc{}, nil, err
	}
	done := func() { d.Close() }
	if d.DVD {
		return retroach.Disc{Read: func(lba int64) ([]byte, error) { return d.ReadSector(0, int(lba)) }}, done, nil
	}
	data := func(i int) bool { _, ok := chd.UserDataOffset(d.Tracks[i].Type); return ok }
	if !d.GDROM {
		// The first data track holds the file system from sector 0.
		for i := range d.Tracks {
			if data(i) {
				return retroach.Disc{Read: func(lba int64) ([]byte, error) { return d.ReadSector(i, int(lba)) }}, done, nil
			}
		}
		done()
		return retroach.Disc{}, nil, errors.New("no data track")
	}
	// GD-ROM: the high-density area starts with track 3 at sector 45000
	// and the tracks follow each other; the last ends at 549150.
	if len(d.Tracks) < 3 {
		done()
		return retroach.Disc{}, nil, errors.New("GD-ROM without track 3")
	}
	var spans []span
	pos := int64(gdHighDensity)
	for i := 2; i < len(d.Tracks); i++ {
		t := d.Tracks[i]
		if i > 2 {
			pos += int64(t.Pregap)
		}
		if data(i) {
			spans = append(spans, span{pos, int64(t.Frames), func(rel int64) ([]byte, error) { return d.ReadSector(i, int(rel)) }})
		}
		pos += int64(t.Frames + t.Pad)
	}
	if last := len(d.Tracks) - 1; data(last) && pos != gdEnd && len(spans) > 1 {
		spans[len(spans)-1].start = gdEnd - int64(d.Tracks[last].Frames)
	}
	return readSpans(spans, gdHighDensity), done, nil
}

func gdiReader(path string) (retroach.Disc, func(), error) {
	tracks, err := chd.ParseGDI(path)
	if err != nil {
		return retroach.Disc{}, nil, err
	}
	var files []*os.File
	done := func() {
		for _, f := range files {
			f.Close()
		}
	}
	var spans []span
	for _, t := range tracks {
		skip, ok := chd.UserDataOffset(t.Type)
		if !ok || t.LBA < gdHighDensity {
			continue
		}
		f, err := os.Open(t.File)
		if err != nil {
			done()
			return retroach.Disc{}, nil, err
		}
		files = append(files, f)
		size := int64(t.DataSize)
		spans = append(spans, span{int64(t.LBA), int64(t.Frames), func(rel int64) ([]byte, error) {
			b := make([]byte, 2048)
			_, err := f.ReadAt(b, rel*size+int64(skip))
			return b, err
		}})
	}
	return readSpans(spans, gdHighDensity), done, nil
}

func cueReader(path string) (retroach.Disc, func(), error) {
	tracks, err := chd.ParseCue(path)
	if err != nil {
		return retroach.Disc{}, nil, err
	}
	if chd.IsGDROM(tracks) {
		return retroach.Disc{}, nil, errors.New(T("ra.gdrom_cue"))
	}
	for _, t := range tracks {
		skip, ok := chd.UserDataOffset(t.Type)
		if !ok {
			continue
		}
		f, err := os.Open(t.File)
		if err != nil {
			return retroach.Disc{}, nil, err
		}
		start := t.Offset
		if t.PregapInFile {
			start += int64(t.Pregap) * int64(t.DataSize)
		}
		size := int64(t.DataSize)
		read := func(lba int64) ([]byte, error) {
			b := make([]byte, 2048)
			_, err := f.ReadAt(b, start+lba*size+int64(skip))
			return b, err
		}
		return retroach.Disc{Read: read}, func() { f.Close() }, nil
	}
	return retroach.Disc{}, nil, errors.New("no data track")
}

// raDiscJobs are the disc images of the Discs tab to check, with their
// consoles.
func raDiscJobs(t *convTab) []raJob {
	var jobs []raJob
	for _, it := range t.items {
		if !(isCHD(it.path) || isISO(it.path) || hasExt(it.path, []string{".cue", ".gdi"})) {
			continue
		}
		it := it
		console := retroach.DiscConsoles[it.platform]
		jobs = append(jobs, raJob{item: it, console: console, system: it.platform,
			run: func(ctx context.Context, db *retroach.DB) (fmt.Stringer, fmt.Stringer, bool) {
				return raDiscCheck(db, it, console)
			}})
	}
	return jobs
}

// raDiscCheck checks one disc image: the status for its row, a line for
// the log and whether RetroAchievements recognizes it.
func raDiscCheck(db *retroach.DB, it *convItem, console int) (fmt.Stringer, fmt.Stringer, bool) {
	base := filepath.Base(it.path)
	switch {
	case console == 0 && it.platform == "":
		return msg("ra.st_console"), msg("ra.skip_disc", base), false
	case console == 0:
		return msg("ra.st_console"), msg("ra.skip_platform", base, it.platform), false
	case !db.Has(console):
		return msg("ra.st_nolist"), msg("ra.skip_nolist", base, it.platform), false
	}
	d, done, err := discReader(it.path)
	if err != nil {
		return msg("ra.st_error"), msg("drename.skip_error", base, err), false
	}
	hash, err := retroach.HashDisc(console, d)
	done()
	if err != nil {
		return msg("ra.st_error"), msg("drename.skip_error", base, err), false
	}
	return raResult(db, console, it.platform, base, hash, strings.TrimSuffix(base, filepath.Ext(base)))
}

// raConsoleName names a RetroAchievements console with the app's name.
func raConsoleName(c int) string {
	var names []string
	for name, n := range retroach.Consoles {
		if n == c {
			names = append(names, name)
		}
	}
	for name, n := range retroach.DiscConsoles {
		if n == c {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int { return len(a) - len(b) })
	if len(names) == 0 {
		return fmt.Sprint(c)
	}
	return names[0]
}
