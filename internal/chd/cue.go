package chd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Track is a track of a CUE sheet, laid out as chdman createcd does.
type Track struct {
	Number   int
	Type     string // MODE1, MODE1_RAW, MODE2, MODE2_FORM1, MODE2_FORM2, MODE2_FORM_MIX, MODE2_RAW, AUDIO
	DataSize int    // bytes per sector in the file
	File     string
	Offset   int64 // of the track's first sector in File
	Frames   int   // sectors in the file, including a pregap stored there
	Pregap   int
	// PregapInFile tells whether the pregap sectors are part of Frames
	// (INDEX 00) or not stored at all (PREGAP command).
	PregapInFile bool
	Postgap      int
	// Pad is, on a GD-ROM, the number of empty frames at the end of Frames
	// that fill the gap up to the high-density area (not in the file).
	Pad int
	// Split is, on a GD-ROM multi-cue, the number of frames at the end of
	// Frames read from the start of the next track's file (its pregap).
	Split int
	// LBA is, in a GDI file, the track's first sector on the disc.
	LBA   int
	swap  bool // audio stored little endian: CHD stores it big endian
	gdrom bool // part of a GD-ROM (Dreamcast): stored with GD-ROM metadata
	area  int  // GD-ROM area in a Redump multi-cue: 1 single, 2 high density

	idx0, idx1 int
}

// GD-ROM areas; the high-density area starts at sector 45000.
const (
	singleDensity   = 1
	highDensity     = 2
	highDensityArea = 45000
)

// cueTypes maps CUE track modes to CHD track types and sector sizes.
var cueTypes = map[string]struct {
	typ  string
	size int
}{
	"MODE1":      {"MODE1", 2048},
	"MODE1/2048": {"MODE1", 2048},
	"MODE1/2352": {"MODE1_RAW", 2352},
	"MODE2":      {"MODE2", 2336},
	"MODE2/2048": {"MODE2_FORM1", 2048},
	"MODE2/2324": {"MODE2_FORM2", 2324},
	"MODE2/2336": {"MODE2", 2336},
	"MODE2/2352": {"MODE2_RAW", 2352},
	"AUDIO":      {"AUDIO", 2352},
}

// cueFields splits a CUE line into words, keeping quoted strings together.
func cueFields(line string) []string {
	var out []string
	for line = strings.TrimSpace(line); line != ""; line = strings.TrimSpace(line) {
		if line[0] == '"' {
			end := strings.IndexByte(line[1:], '"')
			if end < 0 {
				out = append(out, line[1:])
				break
			}
			out = append(out, line[1:end+1])
			line = line[end+2:]
			continue
		}
		end := strings.IndexAny(line, " \t")
		if end < 0 {
			out = append(out, line)
			break
		}
		out = append(out, line[:end])
		line = line[end:]
	}
	return out
}

func parseMSF(s string) (int, error) {
	var m, sec, f int
	if _, err := fmt.Sscanf(s, "%d:%d:%d", &m, &sec, &f); err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return (m*60+sec)*75 + f, nil
}

// decodeCueText accepts UTF-8 (with or without BOM) and, for older sheets,
// Latin-1 file names.
func decodeCueText(b []byte) string {
	s := strings.TrimPrefix(string(b), string(rune(0xfeff)))
	if utf8.ValidString(s) {
		return s
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// ParseCue reads a CUE sheet and works out where each track's sectors are.
func ParseCue(path string) ([]*Track, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	var tracks []*Track
	var file string
	swap := true
	area, areas := 0, 0
	cur := func() *Track {
		if len(tracks) == 0 {
			return nil
		}
		return tracks[len(tracks)-1]
	}
	sc := bufio.NewScanner(strings.NewReader(decodeCueText(b)))
	for lineNo := 1; sc.Scan(); lineNo++ {
		f := cueFields(sc.Text())
		if len(f) == 0 {
			continue
		}
		bad := func(format string, args ...any) error {
			return fmt.Errorf("%s:%d: %s", filepath.Base(path), lineNo, fmt.Sprintf(format, args...))
		}
		switch strings.ToUpper(f[0]) {
		case "REM":
			// Redump stores Dreamcast discs as one cue with both areas.
			rem := strings.ToUpper(strings.Join(f[1:], " "))
			switch {
			case strings.HasPrefix(rem, "SINGLE-DENSITY AREA"):
				area, areas = singleDensity, areas|1
			case strings.HasPrefix(rem, "HIGH-DENSITY AREA"):
				area, areas = highDensity, areas|2
			}
		case "FILE":
			if len(f) < 2 {
				return nil, bad("FILE without a name")
			}
			file = f[1]
			if !filepath.IsAbs(file) {
				file = filepath.Join(dir, file)
			}
			kind := "BINARY"
			if len(f) > 2 {
				kind = strings.ToUpper(f[2])
			}
			switch kind {
			case "BINARY":
				swap = true
			case "MOTOROLA":
				swap = false // audio already big endian
			default:
				return nil, bad("file type %s is not supported (only BINARY)", kind)
			}
		case "TRACK":
			if len(f) < 3 {
				return nil, bad("incomplete TRACK")
			}
			if file == "" {
				return nil, bad("TRACK before FILE")
			}
			n, err := strconv.Atoi(f[1])
			if err != nil {
				return nil, bad("invalid track number %q", f[1])
			}
			t, ok := cueTypes[strings.ToUpper(f[2])]
			if !ok {
				return nil, bad("track type %s is not supported", f[2])
			}
			tracks = append(tracks, &Track{Number: n, Type: t.typ, DataSize: t.size, File: file,
				swap: swap && t.typ == "AUDIO", area: area, idx0: -1, idx1: -1})
		case "INDEX":
			t := cur()
			if t == nil || len(f) < 3 {
				return nil, bad("INDEX outside a track")
			}
			idx, err := strconv.Atoi(f[1])
			if err != nil {
				return nil, bad("invalid index %q", f[1])
			}
			pos, err := parseMSF(f[2])
			if err != nil {
				return nil, bad("%v", err)
			}
			switch idx {
			case 0:
				t.idx0 = pos
			case 1:
				t.idx1 = pos
				if t.Pregap == 0 && t.idx0 >= 0 {
					t.Pregap = pos - t.idx0
					t.PregapInFile = true // chdman marks it even when empty
				} else {
					t.idx0 = pos
				}
			}
		case "PREGAP", "POSTGAP":
			t := cur()
			if t == nil || len(f) < 2 {
				return nil, bad("%s outside a track", f[0])
			}
			n, err := parseMSF(f[1])
			if err != nil {
				return nil, bad("%v", err)
			}
			if strings.EqualFold(f[0], "PREGAP") {
				t.Pregap = n
			} else {
				t.Postgap = n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("%s: no tracks", filepath.Base(path))
	}

	// Track lengths: up to the next track in the same file, or to the end
	// of the file. Tracks of a file follow each other in it.
	var offset int64
	for i, t := range tracks {
		if t.idx1 < 0 {
			return nil, fmt.Errorf("%s: track %d has no INDEX 01", filepath.Base(path), t.Number)
		}
		if i == 0 || tracks[i-1].File != t.File {
			offset = 0
		}
		t.Offset = offset
		if i+1 < len(tracks) && tracks[i+1].File == t.File {
			t.Frames = tracks[i+1].idx0 - t.idx0
		} else {
			st, err := os.Stat(t.File)
			if err != nil {
				return nil, err
			}
			t.Frames = int((st.Size() - offset) / int64(t.DataSize))
		}
		if t.Frames <= 0 {
			return nil, fmt.Errorf("%s: track %d is empty", filepath.Base(path), t.Number)
		}
		offset += int64(t.Frames) * int64(t.DataSize)
	}
	if areas == 3 {
		for _, t := range tracks {
			t.gdrom = true
		}
		if err := adjustHighDensity(tracks); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
	}
	return tracks, nil
}

// adjustHighDensity gives a Redump GD-ROM multi-cue the layout of a .gdi,
// as chdman 0.289 does: each track's pregap moves to the end of the track
// before it, and the last track of the single-density area is padded so
// that the high-density area starts at sector 45000.
func adjustHighDensity(tracks []*Track) error {
	for i := 1; i < len(tracks); i++ {
		prev, t := tracks[i-1], tracks[i]
		pg := t.Pregap
		prev.Frames += pg
		prev.Split += pg
		t.Offset += int64(pg) * int64(t.DataSize)
		t.Frames -= pg
		t.Pregap, t.PregapInFile = 0, false
		if t.Frames <= 0 {
			return fmt.Errorf("track %d is empty", t.Number)
		}
	}
	phys := 0
	for i := 1; i < len(tracks); i++ {
		prev, t := tracks[i-1], tracks[i]
		if t.area == highDensity && prev.area == singleDensity {
			dif := highDensityArea - (prev.Frames + phys)
			if dif < 0 {
				return fmt.Errorf("the single-density area is larger than %d sectors", highDensityArea)
			}
			prev.Frames += dif
			prev.Pad = dif
			phys = highDensityArea
			continue
		}
		phys += prev.Frames
	}
	return nil
}

// IsGDROM reports whether tracks are a GD-ROM (Dreamcast) disc.
func IsGDROM(tracks []*Track) bool { return len(tracks) > 0 && tracks[0].gdrom }

// extraFrames is the number of empty frames added after a track, to a
// multiple of 4 frames.
func (t *Track) extraFrames() int {
	return (t.Frames+trackPadding-1)/trackPadding*trackPadding - t.Frames
}

// metadata is the track's CHT2 entry (CHGD on a GD-ROM), as chdman writes
// it.
func (t *Track) metadata() string {
	pgType := "MODE1"
	if t.PregapInFile {
		pgType = "V" + t.Type
	}
	if t.gdrom {
		return fmt.Sprintf("TRACK:%d TYPE:%s SUBTYPE:NONE FRAMES:%d PAD:%d PREGAP:%d PGTYPE:%s PGSUB:NONE POSTGAP:%d",
			t.Number, t.Type, t.Frames, t.Pad, t.Pregap, pgType, t.Postgap)
	}
	return fmt.Sprintf("TRACK:%d TYPE:%s SUBTYPE:NONE FRAMES:%d PREGAP:%d PGTYPE:%s PGSUB:NONE POSTGAP:%d",
		t.Number, t.Type, t.Frames, t.Pregap, pgType, t.Postgap)
}
