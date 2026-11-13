package nsz

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"runtime"

	"github.com/klauspost/compress/zstd"
)

// Options controls NCZ compression.
type Options struct {
	Level        zstd.EncoderLevel
	BlockSizeExp uint8 // block size is 2^BlockSizeExp; nsz default 20 (1 MiB)
	Workers      int
}

func (o Options) withDefaults() Options {
	if o.Level == 0 {
		o.Level = zstd.SpeedBetterCompression
	}
	if o.BlockSizeExp == 0 {
		o.BlockSizeExp = 20
	}
	if o.Workers <= 0 {
		o.Workers = runtime.NumCPU()
	}
	return o
}

type blockResult struct {
	data []byte
	raw  int // uncompressed length
	err  error
}

// Compress writes the NCZ form of the NCA stored at [ncaOff, ncaOff+ncaSize)
// in r to w at offset pos. It returns the number of bytes written and the
// SHA-256 of the original NCA. progress receives the number of NCA bytes
// processed so far.
func Compress(ctx context.Context, w io.WriterAt, pos int64, r io.ReaderAt, ncaOff, ncaSize int64,
	sections []Section, opt Options, progress func(int64)) (int64, [32]byte, error) {
	var sum [32]byte
	opt = opt.withDefaults()
	hash := sha256.New()
	start := pos

	head := make([]byte, HeaderSize)
	if _, err := r.ReadAt(head, ncaOff); err != nil {
		return 0, sum, err
	}
	hash.Write(head)

	blockSize := int64(1) << opt.BlockSizeExp
	total := ncaSize - HeaderSize
	nBlocks := (total + blockSize - 1) / blockSize

	hdr := append(head, encodeSections(sections)...)
	blk := make([]byte, 24+4*nBlocks)
	copy(blk, "NCZBLOCK")
	blk[8], blk[9], blk[11] = 2, 1, opt.BlockSizeExp
	binary.LittleEndian.PutUint32(blk[12:], uint32(nBlocks))
	binary.LittleEndian.PutUint64(blk[16:], uint64(total))
	listPos := pos + int64(len(hdr)) + 24
	if _, err := w.WriteAt(append(hdr, blk...), pos); err != nil {
		return 0, sum, err
	}
	pos += int64(len(hdr) + len(blk))
	if progress != nil {
		progress(HeaderSize)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type job struct {
		idx int64
		buf []byte
		res chan blockResult
	}
	jobs := make(chan job, opt.Workers)
	order := make(chan chan blockResult, opt.Workers*2)

	for range opt.Workers {
		go func() {
			enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(opt.Level), zstd.WithEncoderConcurrency(1))
			for j := range jobs {
				if err != nil {
					j.res <- blockResult{err: err}
					continue
				}
				applyCTR(sections, HeaderSize+j.idx*blockSize, j.buf)
				c := enc.EncodeAll(j.buf, make([]byte, 0, len(j.buf)))
				if len(c) >= len(j.buf) {
					c = j.buf // not compressible: stored as plain text
				}
				j.res <- blockResult{data: c, raw: len(j.buf)}
			}
		}()
	}

	// Reader: reads blocks in order (hashing the original encrypted bytes)
	// and hands them to the workers; order keeps the results sequential.
	go func() {
		defer close(jobs)
		defer close(order)
		for i := range nBlocks {
			if ctx.Err() != nil {
				return
			}
			n := min(blockSize, total-i*blockSize)
			buf := make([]byte, n)
			res := make(chan blockResult, 1)
			if _, err := r.ReadAt(buf, ncaOff+HeaderSize+i*blockSize); err != nil {
				res <- blockResult{err: err}
				order <- res
				return
			}
			hash.Write(buf)
			order <- res
			jobs <- job{i, buf, res}
		}
	}()

	sizes := make([]byte, 4*nBlocks)
	var done int64 = HeaderSize
	var firstErr error
	i := 0
	for res := range order {
		b := <-res
		if firstErr != nil {
			continue // drain so all goroutines finish
		}
		if b.err == nil {
			_, b.err = w.WriteAt(b.data, pos)
		}
		if b.err != nil {
			firstErr = b.err
			cancel()
			continue
		}
		binary.LittleEndian.PutUint32(sizes[4*i:], uint32(len(b.data)))
		pos += int64(len(b.data))
		done += int64(b.raw)
		i++
		if progress != nil {
			progress(done)
		}
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	if firstErr != nil {
		return 0, sum, firstErr
	}
	if _, err := w.WriteAt(sizes, listPos); err != nil {
		return 0, sum, err
	}
	copy(sum[:], hash.Sum(nil))
	return pos - start, sum, nil
}
