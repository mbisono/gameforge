// Package ciso writes and reads compressed ISO images: CSO (deflate) and
// ZSO (LZ4), the formats of PSP and PS2 loaders and emulators.
//
// Both share one layout: a 24-byte header, an index with the position of
// each block (plus one entry for the end), and the blocks. Each block of
// usually 2048 bytes is compressed on its own, so a reader can seek. An
// index entry with the top bit set marks a block stored uncompressed; in
// CSO version 2 the top bit means LZ4 instead, and blocks as large as the
// block size are uncompressed. Positions are stored shifted right by the
// header's index shift, which lets the index address files over 2 GiB.
package ciso

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

	"github.com/klauspost/compress/flate"
	"github.com/pierrec/lz4/v4"
)

// Format is CSO or ZSO.
type Format int

const (
	CSO Format = iota // deflate
	ZSO               // LZ4
)

func (f Format) String() string { return [...]string{"CSO", "ZSO"}[f] }

// Ext is the file extension of a format.
func (f Format) Ext() string { return [...]string{".cso", ".zso"}[f] }

const (
	headerSize = 0x18
	blockSize  = 2048
	plainFlag  = 0x80000000
	batch      = 512 // blocks per compression job
)

// Header describes a compressed image.
type Header struct {
	Format    Format
	Version   int
	Size      int64 // uncompressed size
	BlockSize int
	Shift     uint
}

// Result describes a conversion.
type Result struct {
	InputSize  int64
	OutputSize int64
	SHA1       [sha1.Size]byte // of the uncompressed image
}

type blockResult struct {
	data  [][]byte
	plain []bool
	err   error
}

// Compress writes the 2048-byte sector image at in as a CSO or ZSO (version
// 1) at out, via out.part. progress receives the bytes read and the total.
func Compress(ctx context.Context, in, out string, format Format, progress func(done, total int64)) (*Result, error) {
	src, err := os.Open(in)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size == 0 {
		return nil, errors.New("empty image")
	}
	blocks := int((size + blockSize - 1) / blockSize)
	indexLen := int64(blocks+1) * 4
	// The smallest shift that lets positions fit in 31 bits even if no
	// block compresses.
	var shift uint
	for (headerSize+indexLen+int64(blocks)*(blockSize+(1<<shift)))>>shift >= 1<<31 {
		shift++
	}

	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()

	hdr := make([]byte, headerSize)
	copy(hdr, [...]string{"CISO", "ZISO"}[format])
	binary.LittleEndian.PutUint32(hdr[4:], headerSize)
	binary.LittleEndian.PutUint64(hdr[8:], uint64(size))
	binary.LittleEndian.PutUint32(hdr[16:], blockSize)
	hdr[20], hdr[21] = 1, byte(shift)
	if _, err := f.Write(hdr); err != nil {
		return nil, err
	}
	index := make([]uint32, blocks+1)
	pos := int64(headerSize) + indexLen
	if _, err := f.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type job struct {
		buf []byte
		res chan blockResult
	}
	workers := runtime.NumCPU()
	jobs := make(chan job, workers)
	order := make(chan chan blockResult, workers*4)
	for range workers {
		go func() {
			c := newBlockCompressor(format)
			for j := range jobs {
				var r blockResult
				for off := 0; off < len(j.buf); off += blockSize {
					b := j.buf[off:min(off+blockSize, len(j.buf))]
					data, plain := c.compress(b)
					r.data, r.plain = append(r.data, data), append(r.plain, plain)
				}
				j.res <- r
			}
		}()
	}
	hash := sha1.New()
	go func() {
		defer close(jobs)
		defer close(order)
		for done := 0; done < blocks; done += batch {
			res := make(chan blockResult, 1)
			if ctx.Err() != nil {
				res <- blockResult{err: ctx.Err()}
				order <- res
				return
			}
			n := min(batch, blocks-done)
			buf := make([]byte, n*blockSize)
			got, err := io.ReadFull(src, buf)
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
				res <- blockResult{err: err}
				order <- res
				return
			}
			hash.Write(buf[:got])
			// A partial last block is padded with zeros.
			order <- res
			jobs <- job{buf, res}
		}
	}()

	w := &countWriter{w: f}
	bw := newBufWriter(w)
	i := 0
	var firstErr error
	align := int64(1) << shift
	for res := range order {
		r := <-res
		if firstErr != nil {
			continue
		}
		if r.err != nil {
			firstErr = r.err
			cancel()
			continue
		}
		for k, data := range r.data {
			if rem := pos % align; rem != 0 {
				bw.Write(make([]byte, align-rem))
				pos += align - rem
			}
			index[i] = uint32(pos >> shift)
			if r.plain[k] {
				index[i] |= plainFlag
			}
			bw.Write(data)
			pos += int64(len(data))
			i++
		}
		if progress != nil {
			progress(min(int64(i)*blockSize, size), size)
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if rem := pos % align; rem != 0 {
		bw.Write(make([]byte, align-rem))
		pos += align - rem
	}
	index[blocks] = uint32(pos >> shift)
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	idx := make([]byte, indexLen)
	for k, v := range index {
		binary.LittleEndian.PutUint32(idx[4*k:], v)
	}
	if _, err := f.WriteAt(idx, headerSize); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, out); err != nil {
		return nil, err
	}
	ok = true
	res := &Result{InputSize: size, OutputSize: pos}
	copy(res.SHA1[:], hash.Sum(nil))
	return res, nil
}

// blockCompressor compresses blocks with one format, reusing its state.
type blockCompressor struct {
	format Format
	buf    bytes.Buffer
	fw     *flate.Writer
	hc     lz4.CompressorHC
	lzBuf  []byte
}

func newBlockCompressor(format Format) *blockCompressor {
	c := &blockCompressor{format: format}
	if format == CSO {
		c.fw, _ = flate.NewWriter(&c.buf, flate.BestCompression)
	} else {
		c.lzBuf = make([]byte, lz4.CompressBlockBound(blockSize))
	}
	return c
}

// compress returns the stored form of a block and whether it is stored
// uncompressed (when compression does not make it smaller).
func (c *blockCompressor) compress(b []byte) ([]byte, bool) {
	if len(b) < blockSize {
		b = append(append([]byte(nil), b...), make([]byte, blockSize-len(b))...)
	}
	var out []byte
	if c.format == CSO {
		c.buf.Reset()
		c.fw.Reset(&c.buf)
		c.fw.Write(b)
		c.fw.Close()
		out = c.buf.Bytes()
	} else if n, err := c.hc.CompressBlock(b, c.lzBuf); err == nil && n > 0 {
		out = c.lzBuf[:n]
	}
	if out == nil || len(out) >= blockSize {
		return b, true
	}
	return bytes.Clone(out), false
}

// Reader reads a CSO (version 1 or 2) or ZSO image.
type Reader struct {
	f      *os.File
	Header Header
	index  []uint32
}

// Open reads the header and index of a CSO or ZSO.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f}
	if err := r.init(); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

func (r *Reader) Close() error { return r.f.Close() }

func (r *Reader) init() error {
	hdr := make([]byte, headerSize)
	if _, err := r.f.ReadAt(hdr, 0); err != nil {
		return fmt.Errorf("reading header: %w", err)
	}
	h := &r.Header
	switch string(hdr[:4]) {
	case "CISO":
		h.Format = CSO
	case "ZISO":
		h.Format = ZSO
	default:
		return errors.New("not a CSO or ZSO image")
	}
	h.Size = int64(binary.LittleEndian.Uint64(hdr[8:]))
	h.BlockSize = int(binary.LittleEndian.Uint32(hdr[16:]))
	h.Version, h.Shift = int(hdr[20]), uint(hdr[21])
	if h.Version == 0 {
		h.Version = 1
	}
	if h.BlockSize < 512 || h.BlockSize > 1<<20 || h.BlockSize&(h.BlockSize-1) != 0 || h.Size <= 0 ||
		h.Shift > 16 || h.Version > 2 {
		return errors.New("invalid CSO/ZSO header")
	}
	blocks := (h.Size + int64(h.BlockSize) - 1) / int64(h.BlockSize)
	if blocks > 1<<28 {
		return errors.New("invalid CSO/ZSO header")
	}
	idx := make([]byte, (blocks+1)*4)
	if _, err := r.f.ReadAt(idx, headerSize); err != nil {
		return fmt.Errorf("reading index: %w", err)
	}
	r.index = make([]uint32, blocks+1)
	for i := range r.index {
		r.index[i] = binary.LittleEndian.Uint32(idx[4*i:])
	}
	return nil
}

// Blocks returns the number of blocks.
func (r *Reader) Blocks() int { return len(r.index) - 1 }

// ReadBlock decodes block n into dst (BlockSize bytes).
func (r *Reader) ReadBlock(n int, dst []byte) error {
	h := r.Header
	start := int64(r.index[n]&^plainFlag) << h.Shift
	end := int64(r.index[n+1]&^plainFlag) << h.Shift
	if end < start || end-start > int64(2*h.BlockSize+(1<<h.Shift)) {
		return fmt.Errorf("block %d: invalid index", n)
	}
	src := make([]byte, end-start)
	if _, err := r.f.ReadAt(src, start); err != nil && !(errors.Is(err, io.EOF) && n == r.Blocks()-1) {
		return fmt.Errorf("block %d: %w", n, err)
	}
	flag := r.index[n]&plainFlag != 0
	plain := flag
	lz := h.Format == ZSO
	if h.Version == 2 {
		plain = len(src) >= h.BlockSize
		lz = flag
	}
	switch {
	case plain:
		if len(src) < h.BlockSize && n != r.Blocks()-1 {
			return fmt.Errorf("block %d: truncated", n)
		}
		clear(dst)
		copy(dst, src)
	case lz:
		if err := lz4Decode(src, dst); err != nil {
			return fmt.Errorf("block %d: %w", n, err)
		}
	default:
		fr := flate.NewReader(bytes.NewReader(src))
		_, err := io.ReadFull(fr, dst)
		fr.Close()
		if err != nil {
			return fmt.Errorf("block %d: deflate: %w", n, err)
		}
	}
	return nil
}

// lz4Decode decodes an LZ4 block until dst is full; bytes after it (block
// padding) are ignored.
func lz4Decode(src, dst []byte) error {
	errBad := errors.New("invalid LZ4 data")
	si, di := 0, 0
	length := func(n int) (int, bool) {
		if n != 15 {
			return n, true
		}
		for si < len(src) {
			b := src[si]
			si++
			n += int(b)
			if b != 255 {
				return n, true
			}
		}
		return 0, false
	}
	for di < len(dst) {
		if si >= len(src) {
			return errBad
		}
		token := src[si]
		si++
		lit, ok := length(int(token >> 4))
		if !ok || si+lit > len(src) {
			return errBad
		}
		lit = min(lit, len(dst)-di)
		copy(dst[di:], src[si:si+lit])
		si, di = si+lit, di+lit
		if di >= len(dst) {
			return nil
		}
		if si+2 > len(src) {
			return errBad
		}
		off := int(src[si]) | int(src[si+1])<<8
		si += 2
		ml, ok := length(int(token & 15))
		if !ok || off == 0 || off > di {
			return errBad
		}
		ml += 4
		for k := 0; k < ml && di < len(dst); k++ {
			dst[di] = dst[di-off]
			di++
		}
	}
	return nil
}

// Hash decodes every block of a CSO or ZSO and returns the SHA-1 of the
// uncompressed image. progress receives the bytes decoded and the total.
func Hash(ctx context.Context, path string, progress func(done, total int64)) ([sha1.Size]byte, error) {
	var sum [sha1.Size]byte
	err := decode(ctx, path, io.Discard, &sum, progress)
	return sum, err
}

// Decompress writes the uncompressed image of a CSO or ZSO to out, via
// out.part.
func Decompress(ctx context.Context, in, out string, progress func(done, total int64)) (*Result, error) {
	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	bw := newBufWriter(f)
	err = decode(ctx, in, bw, &res.SHA1, progress)
	if err == nil {
		err = bw.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, out)
	}
	if err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if st, err := os.Stat(in); err == nil {
		res.InputSize = st.Size()
	}
	if st, err := os.Stat(out); err == nil {
		res.OutputSize = st.Size()
	}
	return res, nil
}

// decode writes the uncompressed image to w, decoding blocks in parallel.
func decode(ctx context.Context, path string, w io.Writer, sum *[sha1.Size]byte, progress func(done, total int64)) error {
	r, err := Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		buf []byte
		err error
	}
	type job struct {
		first, n int
		res      chan result
	}
	bs := r.Header.BlockSize
	workers := runtime.NumCPU()
	jobs := make(chan job, workers)
	order := make(chan chan result, workers*4)
	for range workers {
		go func() {
			for j := range jobs {
				buf := make([]byte, j.n*bs)
				var err error
				for k := 0; k < j.n && err == nil; k++ {
					err = r.ReadBlock(j.first+k, buf[k*bs:(k+1)*bs])
				}
				j.res <- result{buf, err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		defer close(order)
		for first := 0; first < r.Blocks(); first += batch {
			if ctx.Err() != nil {
				return
			}
			res := make(chan result, 1)
			order <- res
			jobs <- job{first, min(batch, r.Blocks()-first), res}
		}
	}()
	h := sha1.New()
	left := r.Header.Size
	var firstErr error
	for res := range order {
		v := <-res
		if firstErr != nil {
			continue
		}
		if v.err != nil {
			firstErr = v.err
			cancel()
			continue
		}
		data := v.buf[:min(int64(len(v.buf)), left)]
		left -= int64(len(data))
		h.Write(data)
		if _, err := w.Write(data); err != nil {
			firstErr = err
			cancel()
			continue
		}
		if progress != nil {
			progress(r.Header.Size-left, r.Header.Size)
		}
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	if firstErr != nil {
		return firstErr
	}
	if left != 0 {
		return errors.New("image shorter than its header says")
	}
	copy(sum[:], h.Sum(nil))
	return nil
}

// HashFile returns the SHA-1 of a file.
func HashFile(ctx context.Context, path string, progress func(done, total int64)) ([sha1.Size]byte, error) {
	var sum [sha1.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return sum, err
	}
	h := sha1.New()
	buf := make([]byte, 4<<20)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		done += int64(n)
		if progress != nil {
			progress(done, st.Size())
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return sum, err
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
