package chd

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"sort"
)

// metadata is one metadata entry.
type metadata struct {
	tag   uint32
	flags byte
	data  []byte
}

// layout describes the CHD to write.
type layout struct {
	hunkBytes    int
	unitBytes    int
	logicalBytes int64
	codecs       []uint32 // up to 4
	meta         []metadata
}

func (l *layout) hunks() int {
	return int((l.logicalBytes + int64(l.hunkBytes) - 1) / int64(l.hunkBytes))
}

// Options controls writing a CHD.
type Options struct {
	Workers int // compression goroutines; 0 means one per CPU
	// Progress receives the number of image bytes processed and the total.
	Progress func(done, total int64)
}

// writeCHD writes a CHD to path. fill must fill each hunk buffer with the
// next bytes of the image, in order; bytes past the logical size must be
// left zero. The file is written as path.part and renamed when complete.
func writeCHD(ctx context.Context, path string, l layout, fill func([]byte) error, opt Options) (err error) {
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	w := bufio.NewWriterSize(f, 1<<20)

	// Header placeholder, then the metadata chain.
	pos := int64(headerSize)
	w.Write(make([]byte, headerSize))
	var metaOffset int64
	if len(l.meta) > 0 {
		metaOffset = pos
	}
	for i, m := range l.meta {
		next := int64(0)
		if i+1 < len(l.meta) {
			next = pos + metaHeaderSize + int64(len(m.data))
		}
		var h [metaHeaderSize]byte
		binary.BigEndian.PutUint32(h[0:], m.tag)
		binary.BigEndian.PutUint32(h[4:], uint32(len(m.data)))
		h[4] = m.flags
		binary.BigEndian.PutUint64(h[8:], uint64(next))
		w.Write(h[:])
		w.Write(m.data)
		pos += metaHeaderSize + int64(len(m.data))
	}
	firstOffs := pos

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		data []byte
		typ  uint8
		ref  uint64 // referenced hunk for compSelf
		crc  uint16
		err  error
	}
	type job struct {
		buf []byte
		res chan result
	}
	jobs := make(chan job, workers)
	order := make(chan chan result, workers*4)
	for range workers {
		go func() {
			c := &compressor{codecs: l.codecs, hunkBytes: l.hunkBytes}
			for j := range jobs {
				data, typ, err := c.compress(j.buf)
				j.res <- result{data: data, typ: typ, crc: crc16(j.buf), err: err}
			}
		}()
	}

	// Reader: fills the hunks in order, hashes them and finds repeated ones.
	nHunks := l.hunks()
	rawSHA := sha1.New()
	go func() {
		defer close(jobs)
		defer close(order)
		seen := map[[sha1.Size]byte]uint64{}
		left := l.logicalBytes
		for i := range nHunks {
			res := make(chan result, 1)
			if ctx.Err() != nil {
				res <- result{err: ctx.Err()}
				order <- res
				return
			}
			buf := make([]byte, l.hunkBytes)
			if err := fill(buf); err != nil {
				res <- result{err: err}
				order <- res
				return
			}
			rawSHA.Write(buf[:min(int64(len(buf)), left)])
			left -= int64(len(buf))
			sum := sha1.Sum(buf)
			if ref, ok := seen[sum]; ok {
				res <- result{typ: compSelf, ref: ref}
				order <- res
				continue
			}
			seen[sum] = uint64(i)
			order <- res
			jobs <- job{buf, res}
		}
	}()

	entries := make([]mapEntry, 0, nHunks)
	var firstErr error
	for res := range order {
		r := <-res
		if firstErr != nil {
			continue // drain so that all goroutines finish
		}
		if r.err != nil {
			firstErr = r.err
			cancel()
			continue
		}
		e := mapEntry{typ: r.typ, crc: r.crc}
		switch r.typ {
		case compSelf:
			e.offset, e.crc = r.ref, 0
		default:
			e.offset, e.length = uint64(pos), uint32(len(r.data))
			if _, err := w.Write(r.data); err != nil {
				firstErr = err
				cancel()
				continue
			}
			pos += int64(len(r.data))
		}
		entries = append(entries, e)
		if opt.Progress != nil {
			opt.Progress(min(int64(len(entries))*int64(l.hunkBytes), l.logicalBytes), l.logicalBytes)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if len(entries) != nHunks {
		return errors.New("chd: incomplete image")
	}

	mapOffset := pos
	if _, err := w.Write(encodeMap(entries, uint64(firstOffs))); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}

	var raw [sha1.Size]byte
	copy(raw[:], rawSHA.Sum(nil))
	h := make([]byte, headerSize)
	copy(h, "MComprHD")
	binary.BigEndian.PutUint32(h[8:], headerSize)
	binary.BigEndian.PutUint32(h[12:], 5)
	for i, c := range l.codecs {
		binary.BigEndian.PutUint32(h[16+4*i:], c)
	}
	binary.BigEndian.PutUint64(h[32:], uint64(l.logicalBytes))
	binary.BigEndian.PutUint64(h[40:], uint64(mapOffset))
	binary.BigEndian.PutUint64(h[48:], uint64(metaOffset))
	binary.BigEndian.PutUint32(h[56:], uint32(l.hunkBytes))
	binary.BigEndian.PutUint32(h[60:], uint32(l.unitBytes))
	copy(h[64:], raw[:])
	overall := overallSHA1(raw, l.meta)
	copy(h[84:], overall[:])
	if _, err := f.WriteAt(h, 0); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// overallSHA1 is the SHA-1 of the raw data SHA-1 followed by the tag and
// SHA-1 of each checksummed metadata entry, sorted.
func overallSHA1(raw [sha1.Size]byte, meta []metadata) [sha1.Size]byte {
	var items [][]byte
	for _, m := range meta {
		if m.flags&metaChecksum == 0 {
			continue
		}
		item := make([]byte, 4, 4+sha1.Size)
		binary.BigEndian.PutUint32(item, m.tag)
		s := sha1.Sum(m.data)
		items = append(items, append(item, s[:]...))
	}
	sort.Slice(items, func(i, j int) bool { return string(items[i]) < string(items[j]) })
	h := sha1.New()
	h.Write(raw[:])
	for _, it := range items {
		h.Write(it)
	}
	var out [sha1.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}
