package chd

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
)

// Reader reads a CHD v5 file without a parent.
type Reader struct {
	f            *os.File
	Codecs       [4]uint32
	LogicalBytes int64
	HunkBytes    int
	UnitBytes    int
	RawSHA1      [sha1.Size]byte
	SHA1         [sha1.Size]byte
	Meta         []metadata
	entries      []mapEntry
	fileSize     int64
}

// Open reads the header, metadata and map of a CHD.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f}
	if err := r.init(); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

func (r *Reader) Close() error { return r.f.Close() }

func (r *Reader) init() error {
	h := make([]byte, headerSize)
	if _, err := r.f.ReadAt(h, 0); err != nil {
		return fmt.Errorf("chd: reading header: %w", err)
	}
	if string(h[:8]) != "MComprHD" {
		return errors.New("chd: not a CHD file")
	}
	if v := binary.BigEndian.Uint32(h[12:]); v != 5 {
		return fmt.Errorf("chd: version %d not supported", v)
	}
	if !bytes.Equal(h[104:124], make([]byte, 20)) {
		return errors.New("chd: CHDs with a parent are not supported")
	}
	for i := range r.Codecs {
		r.Codecs[i] = binary.BigEndian.Uint32(h[16+4*i:])
	}
	r.LogicalBytes = int64(binary.BigEndian.Uint64(h[32:]))
	mapOffset := int64(binary.BigEndian.Uint64(h[40:]))
	metaOffset := int64(binary.BigEndian.Uint64(h[48:]))
	r.HunkBytes = int(binary.BigEndian.Uint32(h[56:]))
	r.UnitBytes = int(binary.BigEndian.Uint32(h[60:]))
	copy(r.RawSHA1[:], h[64:])
	copy(r.SHA1[:], h[84:])
	if r.HunkBytes <= 0 || r.HunkBytes > 1<<24 {
		return errors.New("chd: invalid hunk size")
	}
	// Sizes from a damaged header must not decide allocations: at most
	// 2^26 hunks (a 1 TiB disc at the smallest CD hunk), and no map or
	// metadata larger than the file.
	st, err := r.f.Stat()
	if err != nil {
		return err
	}
	r.fileSize = st.Size()
	if r.LogicalBytes <= 0 || (r.LogicalBytes+int64(r.HunkBytes)-1)/int64(r.HunkBytes) > 1<<26 {
		return errors.New("chd: invalid size")
	}

	for off, n := metaOffset, 0; off != 0; n++ {
		if n > 1<<16 {
			return errors.New("chd: metadata loop")
		}
		var mh [metaHeaderSize]byte
		if _, err := r.f.ReadAt(mh[:], off); err != nil {
			return fmt.Errorf("chd: reading metadata: %w", err)
		}
		length := binary.BigEndian.Uint32(mh[4:]) & 0xffffff
		if int64(length) > st.Size() {
			return errors.New("chd: invalid metadata")
		}
		data := make([]byte, length)
		if _, err := r.f.ReadAt(data, off+metaHeaderSize); err != nil {
			return fmt.Errorf("chd: reading metadata: %w", err)
		}
		r.Meta = append(r.Meta, metadata{binary.BigEndian.Uint32(mh[0:]), mh[4], data})
		off = int64(binary.BigEndian.Uint64(mh[8:]))
	}

	var mh [mapHeaderSize]byte
	if _, err := r.f.ReadAt(mh[:], mapOffset); err != nil {
		return fmt.Errorf("chd: reading map: %w", err)
	}
	mapLen := int64(binary.BigEndian.Uint32(mh[0:]))
	if mapLen > st.Size() {
		return errors.New("chd: invalid map")
	}
	data := make([]byte, mapLen)
	if _, err := r.f.ReadAt(data, mapOffset+mapHeaderSize); err != nil {
		return fmt.Errorf("chd: reading map: %w", err)
	}
	hunks := int((r.LogicalBytes + int64(r.HunkBytes) - 1) / int64(r.HunkBytes))
	r.entries, err = decodeMap(mh[:], data, hunks, uint32(r.HunkBytes))
	return err
}

// Hunks returns the number of hunks.
func (r *Reader) Hunks() int { return len(r.entries) }

// ReadHunk decodes hunk n into dst (HunkBytes long) and checks its CRC.
func (r *Reader) ReadHunk(n int, dst []byte) error {
	for depth := 0; ; depth++ {
		e := r.entries[n]
		if e.typ != compSelf {
			break
		}
		if e.offset >= uint64(n) || depth > len(r.entries) {
			return fmt.Errorf("chd: hunk %d: invalid reference", n)
		}
		n = int(e.offset)
	}
	e := r.entries[n]
	// A damaged map must not decide the allocation: the data lies inside
	// the file, and compressed data is never larger than the hunk.
	if int64(e.offset)+int64(e.length) > r.fileSize || int64(e.length) > 2*int64(r.HunkBytes) {
		return fmt.Errorf("chd: hunk %d: invalid map entry", n)
	}
	src := make([]byte, e.length)
	if _, err := r.f.ReadAt(src, int64(e.offset)); err != nil && !(err == io.EOF && len(src) == 0) {
		return fmt.Errorf("chd: hunk %d: %w", n, err)
	}
	if e.typ == compNone {
		copy(dst, src)
	} else if err := decompress(r.Codecs[e.typ], src, dst); err != nil {
		return fmt.Errorf("chd: hunk %d: %w", n, err)
	}
	if crc16(dst) != e.crc {
		return fmt.Errorf("chd: hunk %d: CRC mismatch", n)
	}
	return nil
}

// CDTracks returns the track metadata strings of a CD image.
func (r *Reader) CDTracks() []string {
	var out []string
	for _, m := range r.Meta {
		if m.tag == tagCDTrack || m.tag == tagGDTrack {
			out = append(out, string(bytes.TrimRight(m.data, "\x00")))
		}
	}
	return out
}

// HeaderSHA1Error reports a CHD whose hunks all decode and pass their CRC,
// but whose data SHA-1 in the header differs from the SHA-1 of that data.
// chdman verify reports such files the same way. Since every hunk matches
// the CRC it was stored with, the data is what the writing tool compressed;
// the header was written for other data, or the hunks were changed
// afterwards. Such files turn up in real collections, with tracks that
// still extract to the Redump dump.
type HeaderSHA1Error struct {
	Hunks          int
	Stored, Actual [sha1.Size]byte
}

func (e *HeaderSHA1Error) Error() string {
	return fmt.Sprintf("chd: data SHA-1 mismatch (header %x, data %x)", e.Stored, e.Actual)
}

// Verify decodes every hunk and checks the data and overall SHA-1 of the
// header. progress receives the bytes checked so far. It returns the number
// of hunks.
func Verify(ctx context.Context, path string, progress func(done, total int64)) (int, error) {
	r, err := Open(path)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	h := sha1.New()
	left := r.LogicalBytes
	err = r.eachHunk(ctx, func(_ int, buf []byte) error {
		h.Write(buf[:min(int64(len(buf)), left)])
		left -= int64(len(buf))
		if progress != nil {
			progress(r.LogicalBytes-max(left, 0), r.LogicalBytes)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	var raw [sha1.Size]byte
	copy(raw[:], h.Sum(nil))
	if raw != r.RawSHA1 {
		return 0, &HeaderSHA1Error{Hunks: r.Hunks(), Stored: r.RawSHA1, Actual: raw}
	}
	if overallSHA1(raw, r.Meta) != r.SHA1 {
		return 0, errors.New("chd: overall SHA-1 mismatch")
	}
	return r.Hunks(), nil
}

// eachHunk decodes the hunks, several at a time, and hands them to fn in
// order; it stops at the first error, of a hunk or of fn.
func (r *Reader) eachHunk(ctx context.Context, fn func(n int, buf []byte) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		buf []byte
		err error
	}
	type job struct {
		n   int
		res chan result
	}
	workers := runtime.NumCPU()
	jobs := make(chan job, workers)
	order := make(chan chan result, workers*4)
	for range workers {
		go func() {
			for j := range jobs {
				buf := make([]byte, r.HunkBytes)
				j.res <- result{buf, r.ReadHunk(j.n, buf)}
			}
		}()
	}
	go func() {
		defer close(jobs)
		defer close(order)
		for n := range r.Hunks() {
			if ctx.Err() != nil {
				return
			}
			res := make(chan result, 1)
			order <- res
			jobs <- job{n, res}
		}
	}()
	var firstErr error
	n := 0
	for res := range order {
		v := <-res
		if firstErr != nil {
			continue
		}
		if v.err == nil {
			v.err = fn(n, v.buf)
		}
		if v.err != nil {
			firstErr = v.err
			cancel()
		}
		n++
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return firstErr
}
