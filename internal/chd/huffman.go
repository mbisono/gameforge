package chd

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// bitReader reads values most significant bit first; past the end it
// returns zero bits and records the overflow.
type bitReader struct {
	data     []byte
	pos      uint // in bits
	overflow bool
}

// peek returns the next n bits (n <= 56) without consuming them.
func (r *bitReader) peek(n uint) uint64 {
	if n == 0 {
		return 0
	}
	i := int(r.pos >> 3)
	var v uint64
	if i+8 <= len(r.data) {
		v = binary.BigEndian.Uint64(r.data[i:])
	} else {
		for k := range 8 {
			v <<= 8
			if i+k < len(r.data) {
				v |= uint64(r.data[i+k])
			}
		}
	}
	return v << (r.pos & 7) >> (64 - n)
}

func (r *bitReader) skip(n uint) {
	r.pos += n
	if r.pos > uint(len(r.data))*8 {
		r.overflow = true
	}
}

func (r *bitReader) read(n uint) uint64 {
	v := r.peek(n)
	r.skip(n)
	return v
}

// readSigned reads an n-bit two's complement value.
func (r *bitReader) readSigned(n uint) int64 {
	if n == 0 {
		return 0
	}
	return int64(r.read(n)<<(64-n)) >> (64 - n)
}

// readUnary counts zero bits up to the next one bit, consuming both.
func (r *bitReader) readUnary() uint {
	var n uint
	for {
		v := uint32(r.peek(32))
		if v != 0 {
			z := uint(bits.LeadingZeros32(v))
			r.skip(z + 1)
			return n + z
		}
		r.skip(32)
		n += 32
		if r.overflow {
			return n
		}
	}
}

// alignByte skips to the next byte boundary.
func (r *bitReader) alignByte() { r.pos = (r.pos + 7) &^ 7 }

// huffDecoder decodes canonical Huffman codes as MAME's huffman.cpp builds
// them, with a lookup table of 2^maxBits entries.
type huffDecoder struct {
	maxBits uint
	lookup  []uint32 // symbol<<5 | length
}

func newHuffDecoder(lengths []uint8, maxBits uint) (*huffDecoder, error) {
	for _, l := range lengths {
		if uint(l) > maxBits {
			return nil, errors.New("chd: invalid Huffman tree")
		}
	}
	codes, err := canonicalCodes(lengths)
	if err != nil {
		return nil, err
	}
	d := &huffDecoder{maxBits: maxBits, lookup: make([]uint32, 1<<maxBits)}
	for s, l := range lengths {
		if l == 0 {
			continue
		}
		shift := maxBits - uint(l)
		first := codes[s] << shift
		for i := range uint32(1) << shift {
			d.lookup[first+i] = uint32(s)<<5 | uint32(l)
		}
	}
	return d, nil
}

func (d *huffDecoder) decode(r *bitReader) int {
	e := d.lookup[r.peek(d.maxBits)]
	r.skip(uint(e & 31))
	return int(e >> 5)
}

// readHuffTree reads the map's tree as huffman_import_tree_rle does: 4 bits
// per length; 1 is an escape for a literal 1 (1,1) or a run (1,len,count-3).
func readHuffTree(r *bitReader) (*huffDecoder, error) {
	lengths := make([]uint8, 0, huffCodes)
	for len(lengths) < huffCodes {
		l := uint8(r.read(4))
		if l != 1 {
			lengths = append(lengths, l)
			continue
		}
		l = uint8(r.read(4))
		if l == 1 {
			lengths = append(lengths, 1)
			continue
		}
		rep := int(r.read(4)) + 3
		if len(lengths)+rep > huffCodes {
			return nil, errors.New("chd: invalid map Huffman tree")
		}
		for range rep {
			lengths = append(lengths, l)
		}
	}
	return newHuffDecoder(lengths, huffMaxBits)
}

// huffDecompress implements the "huff" codec (huffman_8bit_decoder): a
// Huffman tree of 256 byte codes of up to 16 bits, itself stored with a
// small Huffman code (import_tree_huffman), then the coded bytes.
func huffDecompress(src, dst []byte) error {
	r := &bitReader{data: src}

	// The small tree: 24 codes of at most 6 bits.
	small := make([]uint8, 24)
	small[0] = uint8(r.read(3))
	start := int(r.read(3)) + 1
	count := 0
	for i := 1; i < 24; i++ {
		if i < start || count == 7 {
			continue
		}
		count = int(r.read(3))
		if count != 7 {
			small[i] = uint8(count)
		}
	}
	sd, err := newHuffDecoder(small, 6)
	if err != nil {
		return err
	}

	const numCodes = 256
	rleFullBits := uint(bits.Len(numCodes - 9))
	lengths := make([]uint8, 0, numCodes)
	last := uint8(0)
	for len(lengths) < numCodes {
		v := sd.decode(r)
		if v != 0 {
			last = uint8(v - 1)
			lengths = append(lengths, last)
			continue
		}
		n := int(r.read(3)) + 2
		if n == 7+2 {
			n += int(r.read(rleFullBits))
		}
		for ; n > 0 && len(lengths) < numCodes; n-- {
			lengths = append(lengths, last)
		}
	}
	d, err := newHuffDecoder(lengths, 16)
	if err != nil {
		return err
	}
	for i := range dst {
		dst[i] = byte(d.decode(r))
	}
	if r.overflow {
		return errors.New("chd: huff: truncated data")
	}
	return nil
}
