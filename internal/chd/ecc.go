package chd

// CD-ROM error correction (ECMA-130 P and Q parity) of Mode 1 and Mode 2
// Form 1 sectors, as MAME's cdrom.cpp computes it. The CD codecs drop the
// sync header and the parity of sectors whose parity is intact, and the
// reader regenerates them.

const (
	eccPOffset   = 0x81c
	eccPNumBytes = 86
	eccPComp     = 24
	eccQOffset   = eccPOffset + 2*eccPNumBytes
	eccQNumBytes = 52
	eccQComp     = 43
)

var syncHeader = [12]byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

var (
	eccLow, eccHigh [256]byte
	pOffsets        [eccPNumBytes][eccPComp]uint16
	qOffsets        [eccQNumBytes][eccQComp]uint16
)

func init() {
	// eccLow multiplies by 2 in GF(2^8) (polynomial 0x11d); eccHigh divides
	// by 3, the inverse of x -> 2x ^ x.
	for i := range 256 {
		v := i << 1
		if i&0x80 != 0 {
			v ^= 0x11d
		}
		eccLow[i] = byte(v)
		eccHigh[byte(v)^byte(i)] = byte(i)
	}
	// The sector bytes from offset 12 are 16-bit words split into an MSB and
	// an LSB plane; byte b of a parity vector belongs to plane b%2.
	for b := range eccPNumBytes {
		for c := range eccPComp {
			pOffsets[b][c] = uint16(b + 86*c)
		}
	}
	for b := range eccQNumBytes {
		for c := range eccQComp {
			qOffsets[b][c] = uint16(((b/2)*43+c*44)%1118*2 + b%2)
		}
	}
}

func eccBytes(sector []byte, row []uint16) (byte, byte) {
	// In Mode 2 sectors (PS1, CD-i, ...) the 4-byte header is counted as
	// zero, as ECMA-130 specifies and MAME's ecc_source_byte does.
	mode2 := sector[15] == 2
	var v1, v2 byte
	for _, off := range row {
		v := sector[int(off)+12]
		if mode2 && off < 4 {
			v = 0
		}
		v1 ^= v
		v2 ^= v
		v1 = eccLow[v1]
	}
	v1 = eccHigh[eccLow[v1]^v2]
	return v1, v2 ^ v1
}

// eccVerify reports whether the P and Q parity of a raw 2352-byte sector
// are correct.
func eccVerify(sector []byte) bool {
	for b := range eccPNumBytes {
		v1, v2 := eccBytes(sector, pOffsets[b][:])
		if sector[eccPOffset+b] != v1 || sector[eccPOffset+eccPNumBytes+b] != v2 {
			return false
		}
	}
	for b := range eccQNumBytes {
		v1, v2 := eccBytes(sector, qOffsets[b][:])
		if sector[eccQOffset+b] != v1 || sector[eccQOffset+eccQNumBytes+b] != v2 {
			return false
		}
	}
	return true
}

// eccGenerate computes the P and Q parity of a raw sector in place.
func eccGenerate(sector []byte) {
	for b := range eccPNumBytes {
		sector[eccPOffset+b], sector[eccPOffset+eccPNumBytes+b] = eccBytes(sector, pOffsets[b][:])
	}
	for b := range eccQNumBytes {
		sector[eccQOffset+b], sector[eccQOffset+eccQNumBytes+b] = eccBytes(sector, qOffsets[b][:])
	}
}

// eccClear zeroes the parity bytes.
func eccClear(sector []byte) {
	clear(sector[eccPOffset : eccPOffset+2*eccPNumBytes])
	clear(sector[eccQOffset : eccQOffset+2*eccQNumBytes])
}
