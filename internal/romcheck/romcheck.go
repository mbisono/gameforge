// Package romcheck checks the checksums that cartridge ROMs carry in their
// header (Nintendo 64, SNES, Mega Drive, Nintendo DS), which tell whether
// a ROM is as it was released or was changed (a hack, a translation, a
// bad dump), and decodes Mega Drive ROMs in the interleaved .smd format of
// the Super Magic Drive copier.
package romcheck

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
	"strings"
)

// Result is the outcome of a check.
type Result struct {
	System           string // "Nintendo 64", "SNES", "Mega Drive", "Nintendo DS"
	OK               bool
	Stored, Computed uint32
	Width            int // digits of the checksums in hex
}

// Need is how many bytes from the start of a ROM a check of this
// extension reads: the whole ROM (-1) or a part; 0 if there is no check.
func Need(ext string) int64 {
	switch strings.ToLower(ext) {
	case ".z64", ".v64", ".n64":
		return 0x101000
	case ".nds", ".dsi":
		return 0x160
	case ".sfc", ".smc", ".swc", ".fig", ".md", ".gen", ".smd", ".bin":
		return -1
	}
	return 0
}

// Check checks the ROM b, of the console its extension tells; ok is false
// if the ROM has no checksum the package knows.
func Check(ext string, b []byte) (Result, bool) {
	switch strings.ToLower(ext) {
	case ".z64", ".v64", ".n64":
		return N64(b)
	case ".nds", ".dsi":
		return DS(b)
	case ".sfc", ".smc", ".swc", ".fig":
		return SNES(b)
	case ".md", ".gen", ".smd", ".bin":
		return MegaDrive(b)
	}
	return Result{}, false
}

// --- Nintendo 64 ---

// The N64 header holds two checksums (CRC1 and CRC2, at 0x10 and 0x14) of
// the 1 MiB after the boot code (from 0x1000), computed with a seed that
// depends on the cartridge's lockout chip (CIC), which is known by the CRC
// of the boot code (0x40 to 0x1000).
var n64CICs = map[uint32]int{
	0x6170a4a1: 6101,
	0x90bb6cb5: 6102,
	0x0b050ee0: 6103,
	0x98bc2c86: 6105,
	0xacc8580a: 6106,
	0x009e9ea3: 7102,
}

// N64 checks the CRC1 and CRC2 of a ROM's header; b holds at least the
// first 0x101000 bytes, in any byte order.
func N64(b []byte) (Result, bool) {
	if len(b) < 0x101000 {
		return Result{}, false
	}
	b = toZ64(b[:0x101000])
	if b == nil {
		return Result{}, false
	}
	cic := n64CICs[crc32.ChecksumIEEE(b[0x40:0x1000])]
	var seed uint32
	switch cic {
	case 6101, 6102, 7102:
		seed = 0xf8ca4ddc
	case 6103:
		seed = 0xa3886759
	case 6105:
		seed = 0xdf26f436
	case 6106:
		seed = 0x1fea617a
	default:
		return Result{}, false // unknown boot code (homebrew, a modified ROM)
	}
	t1, t2, t3, t4, t5, t6 := seed, seed, seed, seed, seed, seed
	for i := 0x1000; i < 0x101000; i += 4 {
		d := binary.BigEndian.Uint32(b[i:])
		if t6+d < t6 {
			t4++
		}
		t6 += d
		t3 ^= d
		r := bits.RotateLeft32(d, int(d&0x1f))
		t5 += r
		if t2 > d {
			t2 ^= r
		} else {
			t2 ^= t6 ^ d
		}
		if cic == 6105 {
			t1 += binary.BigEndian.Uint32(b[0x0750+(i&0xff):]) ^ d
		} else {
			t1 += t5 ^ d
		}
	}
	var c1, c2 uint32
	switch cic {
	case 6103:
		c1, c2 = (t6^t4)+t3, (t5^t2)+t1
	case 6106:
		c1, c2 = t6*t4+t3, t5*t2+t1
	default:
		c1, c2 = t6^t4^t3, t5^t2^t1
	}
	s1, s2 := binary.BigEndian.Uint32(b[0x10:]), binary.BigEndian.Uint32(b[0x14:])
	return Result{System: "Nintendo 64", OK: c1 == s1 && c2 == s2, Stored: s1, Computed: c1, Width: 8}, true
}

// toZ64 returns b in the cartridge's (big-endian) byte order, or nil if it
// is not an N64 ROM.
func toZ64(b []byte) []byte {
	switch {
	case len(b) < 4:
		return nil
	case b[0] == 0x80 && b[1] == 0x37 && b[2] == 0x12 && b[3] == 0x40:
		return b
	case b[0] == 0x37 && b[1] == 0x80 && b[2] == 0x40 && b[3] == 0x12:
		out := make([]byte, len(b))
		for i := 0; i+1 < len(b); i += 2 {
			out[i], out[i+1] = b[i+1], b[i]
		}
		return out
	case b[0] == 0x40 && b[1] == 0x12 && b[2] == 0x37 && b[3] == 0x80:
		out := make([]byte, len(b))
		for i := 0; i+3 < len(b); i += 4 {
			out[i], out[i+1], out[i+2], out[i+3] = b[i+3], b[i+2], b[i+1], b[i]
		}
		return out
	}
	return nil
}

// --- Nintendo DS ---

// DS checks the CRC-16 of a DS ROM's header (at 0x15E, of the first 0x15E
// bytes).
func DS(b []byte) (Result, bool) {
	if len(b) < 0x160 {
		return Result{}, false
	}
	c := uint32(crc16(b[:0x15e]))
	s := uint32(binary.LittleEndian.Uint16(b[0x15e:]))
	// A file whose header is not a DS header at all is not checked.
	if c != s && crc16(b[0xc0:0x15c]) != binary.LittleEndian.Uint16(b[0x15c:]) {
		return Result{}, false
	}
	return Result{System: "Nintendo DS", OK: c == s, Stored: s, Computed: c, Width: 4}, true
}

// crc16 is the CRC-16 (MODBUS) of DS headers.
func crc16(b []byte) uint16 {
	crc := uint16(0xffff)
	for _, c := range b {
		crc ^= uint16(c)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xa001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// --- SNES ---

// SNES checks the checksum of a SNES ROM's internal header: the 16-bit sum
// of all bytes, with the part after the largest power of two repeated up
// to twice that size, as on the cartridge. The header is at 0x7FC0
// (LoROM), 0xFFC0 (HiROM) or 0x40FFC0 (ExHiROM), and holds the checksum
// and its complement at +0x1E and +0x1C. A copier header (512 bytes before
// the ROM) is left out.
func SNES(b []byte) (Result, bool) {
	if len(b)%1024 == 512 {
		b = b[512:]
	}
	best := -1
	for _, at := range []int{0x7fc0, 0xffc0, 0x40ffc0} {
		if at+0x20 > len(b) {
			continue
		}
		comp := binary.LittleEndian.Uint16(b[at+0x1c:])
		sum := binary.LittleEndian.Uint16(b[at+0x1e:])
		if comp^sum == 0xffff {
			best = at
			if at != 0x7fc0 {
				break // a HiROM header wins: a LoROM one may be data
			}
		}
	}
	if best < 0 {
		return Result{}, false
	}
	stored := uint32(binary.LittleEndian.Uint16(b[best+0x1e:]))
	computed, ok := mirroredSum(b)
	if !ok {
		return Result{}, false
	}
	computed &= 0xffff
	return Result{System: "SNES", OK: computed == stored, Stored: stored, Computed: computed, Width: 4}, true
}

// mirroredSum is the sum of the bytes of a ROM whose size is a power of
// two, or a power of two and a smaller power of two that the cartridge
// repeats (3 MiB: 2 MiB, then 1 MiB twice).
func mirroredSum(b []byte) (uint32, bool) {
	n := len(b)
	if n == 0 {
		return 0, false
	}
	sum := func(p []byte) uint32 {
		var s uint32
		for _, c := range p {
			s += uint32(c)
		}
		return s
	}
	main := 1 << (bits.Len(uint(n)) - 1)
	if main == n {
		return sum(b), true
	}
	rest := n - main
	if rest&(rest-1) != 0 {
		return 0, false
	}
	return sum(b[:main]) + sum(b[main:])*uint32(main/rest), true
}

// --- Mega Drive ---

// MegaDrive checks the checksum of a Mega Drive ROM's header (at 0x18E):
// the 16-bit sum of the big-endian words from 0x200 to the end. An .smd
// file is decoded first. Some early games were released with a wrong
// checksum.
func MegaDrive(b []byte) (Result, bool) {
	if IsSMD(b, int64(len(b))) {
		b = DecodeSMD(b[512:])
	}
	if len(b) < 0x202 || (string(b[0x100:0x104]) != "SEGA" && string(b[0x101:0x105]) != "SEGA") {
		return Result{}, false
	}
	var s uint16
	for i := 0x200; i+1 < len(b); i += 2 {
		s += binary.BigEndian.Uint16(b[i:])
	}
	stored := uint32(binary.BigEndian.Uint16(b[0x18e:]))
	return Result{System: "Mega Drive", OK: uint32(s) == stored, Stored: stored, Computed: uint32(s), Width: 4}, true
}

// --- .smd ---

// SMDBlock is the size of the blocks of an .smd file: each holds 8 KiB of
// the ROM's odd bytes, then 8 KiB of its even bytes.
const SMDBlock = 16 << 10

// IsSMD tells, from a file's first bytes and its size, whether it is an
// interleaved .smd file: a 512-byte header with 0xAA 0xBB at offset 8,
// followed by whole 16 KiB blocks.
func IsSMD(head []byte, size int64) bool {
	return len(head) >= 10 && head[8] == 0xaa && head[9] == 0xbb && size > 512 && (size-512)%SMDBlock == 0
}

// DecodeSMD turns the blocks of an .smd file (without its header) into the
// ROM. len(b) must be a multiple of SMDBlock.
func DecodeSMD(b []byte) []byte {
	out := make([]byte, len(b))
	DecodeSMDBlocks(out, b)
	return out
}

// DecodeSMDBlocks decodes whole .smd blocks from src into dst.
func DecodeSMDBlocks(dst, src []byte) {
	const half = SMDBlock / 2
	for at := 0; at+SMDBlock <= len(src); at += SMDBlock {
		blk, o := src[at:at+SMDBlock], dst[at:at+SMDBlock]
		for i := range half {
			o[2*i+1] = blk[i]
			o[2*i] = blk[half+i]
		}
	}
}
