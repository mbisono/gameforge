package rvz

import "encoding/binary"

// The pseudorandom padding of GameCube and Wii discs comes from a lagged
// Fibonacci generator (xor, j = 32, k = 521). This is a port of Dolphin's
// LaggedFibonacciGenerator (CC0), including recovering the seed from the
// generator's output, which lets RVZ store padding as a 68-byte seed.
//
// The buffer holds words in output form: their big-endian bytes are the
// output (after the "shift by 18 instead of 16" of the original code).

const (
	lfgK       = 521
	lfgJ       = 32
	seedWords  = 17
	SeedSize   = seedWords * 4 // bytes of seed stored in an RVZ file
	blockBytes = 0x8000        // the generator restarts every 32 KiB
)

type lfg struct {
	buf [lfgK]uint32
	pos int // in bytes
}

func (g *lfg) setSeed(seed []byte) {
	g.pos = 0
	for i := range seedWords {
		g.buf[i] = binary.BigEndian.Uint32(seed[4*i:])
	}
	g.initialize(false)
}

func (g *lfg) initialize(check bool) bool {
	b := &g.buf
	for i := seedWords; i < lfgK; i++ {
		calc := b[i-17]<<23 ^ b[i-16]>>9 ^ b[i-1]
		if check {
			actual := b[i]&0xff00ffff | b[i]<<2&0x00fc0000
			if calc&0xfffcffff != actual {
				return false
			}
		}
		b[i] = calc
	}
	for i, x := range b {
		b[i] = x&0xff00ffff | x>>2&0x00ff0000
	}
	for range 4 {
		g.forward()
	}
	return true
}

func (g *lfg) forward() {
	b := &g.buf
	for i := range lfgJ {
		b[i] ^= b[i+lfgK-lfgJ]
	}
	for i := lfgJ; i < lfgK; i++ {
		b[i] ^= b[i-lfgJ]
	}
}

func (g *lfg) backward(start, end int) {
	b := &g.buf
	for i := min(end, lfgK); i > max(lfgJ, start); i-- {
		b[i-1] ^= b[i-1-lfgJ]
	}
	for i := min(end, lfgJ); i > start; i-- {
		b[i-1] ^= b[i-1+lfgK-lfgJ]
	}
}

func (g *lfg) reinitialize(seed *[seedWords]uint32) bool {
	for range 4 {
		g.backward(0, lfgK)
	}
	b := &g.buf
	// Rebuild the bits lost by the output transform.
	for i := range seedWords {
		b[i] = b[i]&0xff00ffff | b[i]<<2&0x00fc0000 | (b[i+16]^b[i+15])<<9&0x00030000
		seed[i] = b[i]
	}
	return g.initialize(true)
}

func (g *lfg) byteAt(p int) byte { return byte(g.buf[p/4] >> (24 - 8*(p%4))) }

func (g *lfg) getByte() byte {
	v := g.byteAt(g.pos)
	g.pos++
	if g.pos == lfgK*4 {
		g.forward()
		g.pos = 0
	}
	return v
}

func (g *lfg) getBytes(out []byte) {
	for i := range out {
		out[i] = g.getByte()
	}
}

// skip advances the output by n bytes.
func (g *lfg) skip(n int) {
	g.pos += n
	for g.pos >= lfgK*4 {
		g.forward()
		g.pos -= lfgK * 4
	}
}

// seedFromWords recovers the generator state from at least 521 output
// words starting at word offset off of the generator's output.
func (g *lfg) seedFromWords(words []uint32, off int, seed *[seedWords]uint32) bool {
	if len(words) < lfgK {
		return false
	}
	for _, x := range words[:lfgK] {
		if x&0x00c00000 != x>>2&0x00c00000 {
			return false // cannot be generator output
		}
	}
	mod, div := off%lfgK, off/lfgK
	copy(g.buf[mod:], words[:lfgK-mod])
	copy(g.buf[:mod], words[lfgK-mod:lfgK])
	g.backward(0, mod)
	for range div {
		g.backward(0, lfgK)
	}
	if !g.reinitialize(seed) {
		return false
	}
	for range div {
		g.forward()
	}
	return true
}

// recoverSeed tries to explain data, found at byte offset off of a
// generator's output, as that output. It returns the seed and how many
// bytes from the start of data the generator reproduces (0 if none).
func recoverSeed(data []byte, off int) ([SeedSize]byte, int) {
	var out [SeedSize]byte
	skip := (off+3)&^3 - off
	if len(data) < skip {
		return out, 0
	}
	n := (len(data) - skip) / 4
	if n < lfgK {
		return out, 0
	}
	words := make([]uint32, n)
	for i := range words {
		words[i] = binary.BigEndian.Uint32(data[skip+4*i:])
	}
	var g lfg
	var seed [seedWords]uint32
	if !g.seedFromWords(words, (off+skip)/4, &seed) {
		return out, 0
	}
	g.pos = off % (lfgK * 4)
	count := 0
	for count < len(data) && g.getByte() == data[count] {
		count++
	}
	for i, w := range seed {
		binary.BigEndian.PutUint32(out[4*i:], w)
	}
	return out, count
}

// junk writes n bytes of generator output for seed, starting at a disc (or
// partition data) offset; the generator restarts at every 32 KiB.
func junk(seed []byte, offset int64, out []byte) {
	var g lfg
	g.setSeed(seed)
	g.skip(int(offset % blockBytes))
	g.getBytes(out)
}
