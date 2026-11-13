package chd

import (
	"errors"
	"fmt"
	"math/bits"
)

// FLAC as used by the CHD "flac" and "cdfl" codecs: 16-bit stereo 44.1 kHz
// frames of a fixed block size, without the "fLaC" marker and STREAMINFO
// block (the decoder supplies them). Each hunk is a new stream whose frames
// are numbered from 0.

var flacCRC8Table, flacCRC16Table = func() (t8 [256]uint8, t16 [256]uint16) {
	for i := range 256 {
		c8 := uint8(i)
		c16 := uint16(i) << 8
		for range 8 {
			if c8&0x80 != 0 {
				c8 = c8<<1 ^ 0x07
			} else {
				c8 <<= 1
			}
			if c16&0x8000 != 0 {
				c16 = c16<<1 ^ 0x8005
			} else {
				c16 <<= 1
			}
		}
		t8[i], t16[i] = c8, c16
	}
	return
}()

func flacCRC8(b []byte) uint8 {
	var c uint8
	for _, v := range b {
		c = flacCRC8Table[c^v]
	}
	return c
}

func flacCRC16(b []byte) uint16 {
	var c uint16
	for _, v := range b {
		c = c<<8 ^ flacCRC16Table[byte(c>>8)^v]
	}
	return c
}

// flacBlockSize is the samples per FLAC frame CHD uses for a buffer of n
// bytes: n/4 halved until at most limit (2048 for "flac", 2352 for "cdfl").
func flacBlockSize(n, limit int) int {
	b := n / 4
	for b > limit {
		b /= 2
	}
	return b
}

var errFLAC = errors.New("chd: invalid FLAC data")

// flacDecode decodes FLAC frames from src until left and right hold
// len(left) samples each. It returns the number of bytes used.
func flacDecode(src []byte, left, right []int32) (int, error) {
	r := &bitReader{data: src}
	ch := [2][]int32{left, right}
	for done := 0; done < len(left); {
		n, err := flacFrame(r, ch[0][done:], ch[1][done:])
		if err != nil {
			return 0, err
		}
		done += n
	}
	return int(r.pos / 8), nil
}

var fixedCoefs = [5][]int64{{}, {1}, {2, -1}, {3, -3, 1}, {4, -6, 4, -1}}

func flacFrame(r *bitReader, left, right []int32) (int, error) {
	start := r.pos / 8
	if sync := r.read(15); sync != 0x7ffc {
		return 0, fmt.Errorf("chd: FLAC frame sync not found")
	}
	r.read(1) // blocking strategy
	bsCode := r.read(4)
	srCode := r.read(4)
	chans := r.read(4)
	bpsCode := r.read(3)
	r.read(1)
	// Frame or sample number, UTF-8 coded.
	first := r.read(8)
	for n := bits.LeadingZeros8(^uint8(first)); n > 1; n-- {
		r.read(8)
	}
	var blockSize int
	switch {
	case bsCode == 1:
		blockSize = 192
	case bsCode >= 2 && bsCode <= 5:
		blockSize = 576 << (bsCode - 2)
	case bsCode == 6:
		blockSize = int(r.read(8)) + 1
	case bsCode == 7:
		blockSize = int(r.read(16)) + 1
	case bsCode >= 8:
		blockSize = 256 << (bsCode - 8)
	default:
		return 0, errFLAC
	}
	switch srCode {
	case 12:
		r.read(8)
	case 13, 14:
		r.read(16)
	}
	if bpsCode != 0 && bpsCode != 4 {
		return 0, fmt.Errorf("chd: FLAC: only 16-bit audio is supported")
	}
	hdrEnd := r.pos / 8
	if crc := uint8(r.read(8)); r.overflow || crc != flacCRC8(r.data[start:hdrEnd]) {
		return 0, fmt.Errorf("chd: FLAC frame header CRC mismatch")
	}
	if blockSize > len(left) {
		return 0, errFLAC
	}
	left, right = left[:blockSize], right[:blockSize]

	sideBps := [2]uint{16, 16}
	switch chans {
	case 1: // left, right
	case 8: // left, side
		sideBps[1] = 17
	case 9: // side, right
		sideBps[0] = 17
	case 10: // mid, side
		sideBps[1] = 17
	default:
		return 0, fmt.Errorf("chd: FLAC: only stereo audio is supported")
	}
	for c, out := range [2][]int32{left, right} {
		if err := flacSubframe(r, out, sideBps[c]); err != nil {
			return 0, err
		}
	}
	switch chans {
	case 8:
		for i := range left {
			right[i] = left[i] - right[i]
		}
	case 9:
		for i := range left {
			left[i] += right[i]
		}
	case 10:
		for i := range left {
			mid := left[i]<<1 | right[i]&1
			side := right[i]
			left[i], right[i] = (mid+side)>>1, (mid-side)>>1
		}
	}
	r.alignByte()
	end := r.pos / 8
	if crc := uint16(r.read(16)); r.overflow || crc != flacCRC16(r.data[start:end]) {
		return 0, fmt.Errorf("chd: FLAC frame CRC mismatch")
	}
	return blockSize, nil
}

func flacSubframe(r *bitReader, out []int32, bps uint) error {
	if r.read(1) != 0 {
		return errFLAC
	}
	typ := r.read(6)
	wasted := uint(0)
	if r.read(1) == 1 {
		wasted = r.readUnary() + 1
		if wasted >= bps {
			return errFLAC
		}
		bps -= wasted
	}
	n := len(out)
	switch {
	case typ == 0: // constant
		v := int32(r.readSigned(bps))
		for i := range out {
			out[i] = v
		}
	case typ == 1: // verbatim
		for i := range out {
			out[i] = int32(r.readSigned(bps))
		}
	case typ >= 8 && typ <= 12: // fixed predictor
		order := int(typ - 8)
		if order > n {
			return errFLAC
		}
		for i := range order {
			out[i] = int32(r.readSigned(bps))
		}
		if err := flacResidual(r, out, order); err != nil {
			return err
		}
		coefs := fixedCoefs[order]
		for i := order; i < n; i++ {
			var p int64
			for j, c := range coefs {
				p += c * int64(out[i-1-j])
			}
			out[i] += int32(p)
		}
	case typ >= 32: // LPC
		order := int(typ-32) + 1
		if order > n {
			return errFLAC
		}
		for i := range order {
			out[i] = int32(r.readSigned(bps))
		}
		prec := uint(r.read(4)) + 1
		if prec == 16 {
			return errFLAC
		}
		shift := r.readSigned(5)
		if shift < 0 {
			return errFLAC
		}
		coefs := make([]int64, order)
		for i := range coefs {
			coefs[i] = r.readSigned(prec)
		}
		if err := flacResidual(r, out, order); err != nil {
			return err
		}
		for i := order; i < n; i++ {
			var p int64
			for j, c := range coefs {
				p += c * int64(out[i-1-j])
			}
			out[i] += int32(p >> shift)
		}
	default:
		return errFLAC
	}
	if wasted > 0 {
		for i := range out {
			out[i] <<= wasted
		}
	}
	if r.overflow {
		return errFLAC
	}
	return nil
}

// flacResidual reads the partitioned Rice residual into out[order:].
func flacResidual(r *bitReader, out []int32, order int) error {
	method := r.read(2)
	if method > 1 {
		return errFLAC
	}
	paramBits, escape := uint(4), uint64(15)
	if method == 1 {
		paramBits, escape = 5, 31
	}
	partOrder := uint(r.read(4))
	parts := 1 << partOrder
	if len(out)%parts != 0 || len(out)>>partOrder < order {
		return errFLAC
	}
	i := order
	for p := range parts {
		end := (p + 1) * (len(out) >> partOrder)
		k := r.read(paramBits)
		if k == escape {
			nb := uint(r.read(5))
			for ; i < end; i++ {
				out[i] = int32(r.readSigned(nb))
			}
			continue
		}
		for ; i < end; i++ {
			u := uint64(r.readUnary())<<k | r.read(uint(k))
			out[i] = int32(u>>1) ^ -int32(u&1)
			if r.overflow {
				return errFLAC
			}
		}
	}
	return nil
}
