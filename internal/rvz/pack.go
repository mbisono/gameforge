package rvz

import (
	"encoding/binary"
	"sort"
)

// junkRun is a stretch of padding the generator reproduces.
type junkRun struct {
	start, end int
	seed       [SeedSize]byte
}

// scan finds the padding in data found at offset off (of the disc, or of
// the decrypted partition data). The search restarts at every 32 KiB of
// offset and after the end of each file, as Dolphin's RVZPack does. The
// runs are in increasing order of end.
func scan(in []byte, off int64, files []file) []junkRun {
	var runs []junkRun
	pos, dataOff := 0, off
	for pos < len(in) {
		// Skip the zeros that typically follow a file.
		for pos < len(in) && in[pos] == 0 {
			pos++
			dataOff++
		}
		if pos >= len(in) {
			break
		}
		toRead := min(int((dataOff/blockBytes+1)*blockBytes-dataOff), len(in)-pos)
		seed, n := recoverSeed(in[pos:pos+toRead], int(dataOff%blockBytes))
		if n > 0 {
			runs = append(runs, junkRun{pos, pos + n, seed})
		}
		// If a file ends in this block, continue after it.
		if end := fileEnd(files, dataOff+int64(n)); end >= 0 && end < dataOff+int64(toRead) {
			pos += int(end - dataOff)
			dataOff = end
			continue
		}
		pos += toRead
		dataOff += int64(toRead)
	}
	return runs
}

// emit encodes in[start:end] (one chunk) with RVZ packing using the runs
// found by scan: plain runs, and padding stored as its generator seed. It
// returns nil when the chunk has no padding worth packing.
func emit(in []byte, runs []junkRun, start, end int) []byte {
	var out []byte
	cur := start
	first := true
	for cur < end {
		junkStart, junkEnd := end, end
		var seed *[SeedSize]byte
		if end-cur > SeedSize {
			i := sort.Search(len(runs), func(i int) bool { return runs[i].end > cur+SeedSize })
			if i < len(runs) && runs[i].start+SeedSize < end {
				junkStart = max(cur, runs[i].start)
				junkEnd = min(end, runs[i].end)
				seed = &runs[i].seed
			}
		}
		if first {
			if junkStart == end {
				return nil
			}
			first = false
		}
		if n := junkStart - cur; n > 0 {
			out = binary.BigEndian.AppendUint32(out, uint32(n))
			out = append(out, in[cur:junkStart]...)
			cur = junkStart
		}
		if n := junkEnd - cur; n > 0 {
			out = binary.BigEndian.AppendUint32(out, uint32(n)|0x80000000)
			out = append(out, seed[:]...)
			cur = junkEnd
		}
	}
	return out
}

// pack encodes one chunk at offset off with RVZ packing (nil if no padding).
func pack(in []byte, off int64, files []file) []byte {
	return emit(in, scan(in, off, files), 0, len(in))
}

// unpack decodes RVZ-packed data of a chunk at offset off into dst.
func unpack(src []byte, off int64, dst []byte) error {
	di := 0
	for len(src) > 0 {
		if len(src) < 4 {
			return errBadPack
		}
		n := binary.BigEndian.Uint32(src)
		src = src[4:]
		size := int(n &^ 0x80000000)
		if di+size > len(dst) {
			return errBadPack
		}
		if n&0x80000000 == 0 {
			if len(src) < size {
				return errBadPack
			}
			copy(dst[di:], src[:size])
			src = src[size:]
		} else {
			if len(src) < SeedSize {
				return errBadPack
			}
			junk(src[:SeedSize], off+int64(di), dst[di:di+size])
			src = src[SeedSize:]
		}
		di += size
	}
	if di != len(dst) {
		return errBadPack
	}
	return nil
}
