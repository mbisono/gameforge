// Package retroach tells whether games are versions that RetroAchievements
// recognizes: it computes the hash RetroAchievements identifies a game by
// (the rules of rcheevos' rc_hash, MIT, rewritten here for cartridges) and
// looks it up in the game lists of the RetroAchievements web API, which
// needs the user's own API key.
package retroach

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Console numbers of RetroAchievements.
const (
	MegaDrive     = 1
	N64           = 2
	SNES          = 3
	GameBoy       = 4
	GBA           = 5
	GBC           = 6
	NES           = 7
	PCEngine      = 8
	Sega32X       = 10
	MasterSystem  = 11
	Lynx          = 13
	NeoGeoPocket  = 14
	GameGear      = 15
	Jaguar        = 17
	NDS           = 18
	PokemonMini   = 24
	Atari2600     = 25
	VirtualBoy    = 28
	SG1000        = 33
	ColecoVision  = 44
	Intellivision = 45
	Vectrex       = 46
	Atari5200     = 50
	Atari7800     = 51
	WonderSwan    = 53
)

// Consoles maps the console names the app uses (those of the No-Intro
// databases) to RetroAchievements console numbers.
var Consoles = map[string]int{
	"Game Boy": GameBoy, "Game Boy Color": GBC, "Game Boy Advance": GBA,
	"NES": NES, "Famicom Disk": NES, "SNES": SNES, "Nintendo 64": N64,
	"Nintendo DS": NDS, "Virtual Boy": VirtualBoy, "Pokémon Mini": PokemonMini,
	"Mega Drive": MegaDrive, "32X": Sega32X, "Master System": MasterSystem,
	"Game Gear": GameGear, "SG-1000": SG1000,
	"PC Engine": PCEngine, "SuperGrafx": PCEngine,
	"WonderSwan": WonderSwan, "WonderSwan Color": WonderSwan,
	"Neo Geo Pocket": NeoGeoPocket, "Neo Geo Pocket Color": NeoGeoPocket,
	"Lynx": Lynx, "Atari 2600": Atari2600, "Atari 5200": Atari5200,
	"Atari 7800": Atari7800, "Jaguar": Jaguar,
	"ColecoVision": ColecoVision, "Intellivision": Intellivision, "Vectrex": Vectrex,
}

// Related lists other consoles whose lists may hold a game, e.g. a Game
// Boy Color game in a .gb file.
var Related = map[int][]int{GameBoy: {GBC}, GBC: {GameBoy}}

// maxBuffer is how much of a file rcheevos hashes for most consoles.
const maxBuffer = 64 << 20

// ErrNotSupported is returned for files whose hash cannot be computed.
var ErrNotSupported = errors.New("not supported")

// Hash computes the RetroAchievements hash (MD5, in lower-case hex) of a
// cartridge ROM of a console, read from r, which has size bytes.
func Hash(console int, r io.ReaderAt, size int64) (string, error) {
	if console == NDS {
		return hashNDS(r, size)
	}
	n := min(size, maxBuffer)
	if console == N64 {
		n = size // read whole, to put it in big-endian order
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, 0); err != nil && !(errors.Is(err, io.EOF) && int64(len(buf)) == n) {
		return "", err
	}
	switch console {
	case NES:
		if bytes.HasPrefix(buf, []byte("NES\x1a")) || bytes.HasPrefix(buf, []byte("FDS\x1a")) {
			buf = buf[min(16, len(buf)):]
		}
	case Lynx:
		if bytes.HasPrefix(buf, []byte("LYNX\x00")) {
			buf = buf[min(64, len(buf)):]
		}
	case Atari7800:
		if len(buf) > 128 && string(buf[1:10]) == "ATARI7800" {
			buf = buf[128:]
		}
	case SNES:
		if size%0x2000 == 512 {
			buf = buf[512:]
		}
	case PCEngine:
		if size-size/0x20000*0x20000 == 512 {
			buf = buf[512:]
		}
	case N64:
		if len(buf) > 0 {
			switch buf[0] {
			case 0x37: // .v64: byte pairs swapped
				for i := 0; i+1 < len(buf); i += 2 {
					buf[i], buf[i+1] = buf[i+1], buf[i]
				}
			case 0x40: // .n64: little-endian words
				for i := 0; i+3 < len(buf); i += 4 {
					buf[i], buf[i+1], buf[i+2], buf[i+3] = buf[i+3], buf[i+2], buf[i+1], buf[i]
				}
			}
		}
	}
	sum := md5.Sum(buf)
	return hex.EncodeToString(sum[:]), nil
}

// hashNDS hashes the header, the ARM9 and ARM7 code and the icon of a
// Nintendo DS ROM, as rcheevos does.
func hashNDS(r io.ReaderAt, size int64) (string, error) {
	read := func(off, n int64) ([]byte, error) {
		b := make([]byte, n)
		if off >= size {
			return b, nil
		}
		m, err := r.ReadAt(b, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		_ = m
		return b, nil
	}
	header, err := read(0, 512)
	if err != nil {
		return "", err
	}
	var offset int64
	if header[0] == 0x2E && header[1] == 0 && header[2] == 0 && header[3] == 0xEA &&
		header[0xB0] == 0x44 && header[0xB1] == 0x46 && header[0xB2] == 0x96 && header[0xB3] == 0 {
		offset = 512 // SuperCard header
		if header, err = read(offset, 512); err != nil {
			return "", err
		}
	}
	u32 := func(at int) int64 { return int64(binary.LittleEndian.Uint32(header[at:])) }
	arm9Addr, arm9Size := u32(0x20), u32(0x2C)
	arm7Addr, arm7Size := u32(0x30), u32(0x3C)
	iconAddr := u32(0x68)
	if arm9Size+arm7Size > 16<<20 {
		return "", fmt.Errorf("%w: code too large", ErrNotSupported)
	}
	h := md5.New()
	h.Write(header[:0x160])
	for _, part := range [][2]int64{{arm9Addr, arm9Size}, {arm7Addr, arm7Size}, {iconAddr, 0xA00}} {
		b, err := read(offset+part[0], part[1])
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
