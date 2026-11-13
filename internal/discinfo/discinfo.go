// Package discinfo tells which console a disc image is for, from the
// marks each platform leaves on its discs: the Sega and 3DO headers in the
// first sector, and the ISO 9660 files of PlayStation, PSP and Neo Geo CD
// discs.
package discinfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"

	"gameforge/internal/chd"
)

// Platforms.
const (
	PS1       = "PS1"
	PS2       = "PS2"
	PSP       = "PSP"
	Saturn    = "Saturn"
	SegaCD    = "Sega CD"
	Dreamcast = "Dreamcast"
	PCEngine  = "PC Engine CD"
	NeoGeoCD  = "Neo Geo CD"
	ThreeDO   = "3DO"
	GameCube  = "GameCube"
	Wii       = "Wii"
	AudioCD   = "Audio CD"
)

// MaxCDSize is the size of an 80-minute CD of 2048-byte sectors: ISO
// images up to this size are CD games, larger ones DVDs.
const MaxCDSize = 360000 * 2048

// sectorReader returns the 2048 user data bytes of a sector.
type sectorReader func(lba int) ([]byte, error)

// FromISO detects the platform of a 2048-byte sector image. cd tells
// whether it should become a CD CHD (CD games) rather than a DVD CHD.
func FromISO(path string) (platform string, cd bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	head := make([]byte, 0x20)
	if _, err := f.ReadAt(head, 0); err == nil {
		switch {
		case binary.BigEndian.Uint32(head[0x1c:]) == 0xc2339f3d:
			return GameCube, false, nil
		case binary.BigEndian.Uint32(head[0x18:]) == 0x5d1c9ea3:
			return Wii, false, nil
		}
	}
	read := func(lba int) ([]byte, error) {
		b := make([]byte, 2048)
		_, err := f.ReadAt(b, int64(lba)*2048)
		return b, err
	}
	platform = detect(read)
	switch platform {
	case PSP:
		return platform, false, nil
	case PS1:
		return platform, true, nil
	}
	return platform, st.Size() <= MaxCDSize, nil
}

// FromCue detects the platform of the disc of a CUE sheet.
func FromCue(path string) (string, error) {
	tracks, err := chd.ParseCue(path)
	if err != nil {
		return "", err
	}
	if chd.IsGDROM(tracks) {
		return Dreamcast, nil
	}
	for _, t := range tracks {
		if t.Type == "AUDIO" {
			continue
		}
		f, err := os.Open(t.File)
		if err != nil {
			return "", err
		}
		defer f.Close()
		// Where the user data starts in a sector of the file.
		skip := map[string]int{"MODE1_RAW": 16, "MODE2_RAW": 24, "MODE2": 8, "MODE2_FORM_MIX": 8}[t.Type]
		start := t.Offset
		if t.PregapInFile {
			start += int64(t.Pregap) * int64(t.DataSize)
		}
		read := func(lba int) ([]byte, error) {
			b := make([]byte, 2048)
			_, err := f.ReadAt(b, start+int64(lba)*int64(t.DataSize)+int64(skip))
			return b, err
		}
		if p := detect(read); p != "" {
			return p, nil
		}
		// PC Engine CDs keep their boot record in the first data track,
		// after an audio track.
		if b, err := read(1); err == nil && bytes.Contains(b[:256], []byte("PC Engine CD-ROM SYSTEM")) {
			return PCEngine, nil
		}
		return "", nil
	}
	return AudioCD, nil
}

// Detect tells the platform of a disc from its 2048-byte sectors, read by
// read (for example from a compressed image).
func Detect(read func(lba int) ([]byte, error)) string { return detect(read) }

func detect(read sectorReader) string {
	if s0, err := read(0); err == nil {
		switch {
		case bytes.HasPrefix(s0, []byte("SEGADISCSYSTEM")), bytes.HasPrefix(s0, []byte("SEGABOOTDISC")):
			return SegaCD
		case bytes.HasPrefix(s0, []byte("SEGA SEGASATURN")):
			return Saturn
		case bytes.HasPrefix(s0, []byte("SEGA SEGAKATANA")):
			return Dreamcast
		case s0[0] == 0x01 && bytes.Equal(s0[1:6], []byte{0x5a, 0x5a, 0x5a, 0x5a, 0x5a}) && s0[6] == 0x01:
			return ThreeDO
		}
	}
	root, err := rootDir(read)
	if err != nil {
		return ""
	}
	if e, ok := root["SYSTEM.CNF"]; ok {
		cnf, err := readFile(read, e)
		if err == nil {
			txt := strings.ToUpper(string(cnf))
			if strings.Contains(txt, "BOOT2") {
				return PS2
			}
			if strings.Contains(txt, "BOOT") {
				return PS1
			}
		}
	}
	if _, ok := root["UMD_DATA.BIN"]; ok {
		return PSP
	}
	if _, ok := root["PSP_GAME"]; ok {
		return PSP
	}
	if _, ok := root["IPL.TXT"]; ok {
		return NeoGeoCD
	}
	return ""
}

type extent struct{ lba, size int }

// rootDir lists the root directory of an ISO 9660 volume, by upper-case
// name without version.
func rootDir(read sectorReader) (map[string]extent, error) {
	pvd, err := read(16)
	if err != nil {
		return nil, err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return nil, errors.New("no ISO 9660 volume")
	}
	rec := pvd[156:]
	root := extent{int(binary.LittleEndian.Uint32(rec[2:])), int(binary.LittleEndian.Uint32(rec[10:]))}
	if root.size <= 0 || root.size > 1<<20 {
		return nil, errors.New("invalid root directory")
	}
	out := map[string]extent{}
	for s := 0; s*2048 < root.size; s++ {
		b, err := read(root.lba + s)
		if err != nil {
			return out, nil
		}
		for p := 0; p < len(b); {
			n := int(b[p])
			if n == 0 || p+n > len(b) || n < 34 {
				break
			}
			nameLen := int(b[p+32])
			if 33+nameLen <= n {
				name := strings.ToUpper(string(b[p+33 : p+33+nameLen]))
				if i := strings.IndexByte(name, ';'); i >= 0 {
					name = name[:i]
				}
				out[name] = extent{int(binary.LittleEndian.Uint32(b[p+2:])), int(binary.LittleEndian.Uint32(b[p+10:]))}
			}
			p += n
		}
	}
	return out, nil
}

func readFile(read sectorReader, e extent) ([]byte, error) {
	if e.size > 64<<10 {
		e.size = 64 << 10
	}
	var out []byte
	for s := 0; len(out) < e.size; s++ {
		b, err := read(e.lba + s)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		out = append(out, b...)
	}
	return out[:e.size], nil
}
