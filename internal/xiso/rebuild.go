package xiso

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Result describes a rebuilt image.
type Result struct {
	Kind       Kind
	Xbox360    bool
	Files      int
	InputSize  int64
	OutputSize int64
}

func sectors(n int64) uint32 { return uint32((n + SectorSize - 1) / SectorSize) }

// layout assigns the new sectors: each directory table, then the files of
// that directory, then its subdirectories, as extract-xiso does. Empty
// directories get one sector of padding.
type layout struct {
	tableSector map[*dirTable]uint32
	sector      map[*Entry]uint32
	next        uint32
}

func (l *layout) dir(t *dirTable) {
	l.tableSector[t] = l.next
	l.next += max(sectors(int64(len(t.raw))), 1)
	for _, e := range t.entries {
		if !e.IsDir() {
			l.sector[e] = l.next
			l.next += sectors(int64(e.Size))
		}
	}
	for _, e := range t.entries {
		if !e.IsDir() {
			continue
		}
		if e.dir == nil || len(e.dir.entries) == 0 {
			l.sector[e] = l.next
			l.next++
			continue
		}
		l.dir(e.dir)
		l.sector[e] = l.tableSector[e.dir]
	}
}

// Rebuild writes the game partition of the image at in as a compact XISO
// at out (via out.part), and returns the SHA-1 of every file by path.
// progress receives the file bytes copied and their total.
func Rebuild(ctx context.Context, in, out string, progress func(done, total int64)) (*Result, map[string][sha1.Size]byte, error) {
	im, err := Open(in)
	if err != nil {
		return nil, nil, err
	}
	defer im.Close()
	res := &Result{Kind: im.Kind, Xbox360: im.Xbox360, Files: im.Files, InputSize: im.size}

	l := &layout{tableSector: map[*dirTable]uint32{}, sector: map[*Entry]uint32{}, next: rootSector}
	l.dir(im.root)

	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	w := &sectorWriter{w: bufio.NewWriterSize(f, 4<<20)}

	// Header: empty sectors, the volume descriptor at 0x10000, empty
	// sectors up to the root directory.
	w.zeros(headerOffset)
	vd := make([]byte, SectorSize)
	copy(vd, magic)
	binary.LittleEndian.PutUint32(vd[20:], l.tableSector[im.root])
	binary.LittleEndian.PutUint32(vd[24:], uint32(len(im.root.raw)))
	copy(vd[28:], im.filetime[:])
	copy(vd[0x7ec:], magic)
	w.write(vd)
	w.zeros(int64(rootSector)*SectorSize - w.pos)

	hashes := map[string][sha1.Size]byte{}
	var done int64
	buf := make([]byte, 1<<20)
	var writeDir func(t *dirTable, prefix string) error
	writeDir = func(t *dirTable, prefix string) error {
		if w.pos != int64(l.tableSector[t])*SectorSize {
			return errors.New("internal layout error")
		}
		raw := append([]byte(nil), t.raw...)
		for _, e := range t.entries {
			binary.LittleEndian.PutUint32(raw[e.off+4:], l.sector[e])
			if e.IsDir() && (e.dir == nil || len(e.dir.entries) == 0) {
				binary.LittleEndian.PutUint32(raw[e.off+8:], SectorSize)
			}
		}
		if len(raw) == 0 {
			raw = []byte{padByte}
		}
		w.write(raw)
		w.pad(padByte)
		for _, e := range t.entries {
			if e.IsDir() {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			h := sha1.New()
			r := io.TeeReader(im.file(e), h)
			for left := int64(e.Size); left > 0; {
				n, err := io.ReadFull(r, buf[:min(int64(len(buf)), left)])
				if err != nil {
					return fmt.Errorf("%s%s: %w", prefix, e.Name, err)
				}
				w.write(buf[:n])
				left -= int64(n)
				done += int64(n)
				if progress != nil {
					progress(done, im.Bytes)
				}
			}
			w.pad(padByte)
			var sum [sha1.Size]byte
			copy(sum[:], h.Sum(nil))
			hashes[prefix+e.Name] = sum
		}
		for _, e := range t.entries {
			if !e.IsDir() {
				continue
			}
			if e.dir == nil || len(e.dir.entries) == 0 {
				w.write([]byte{padByte})
				w.pad(padByte)
				continue
			}
			if err := writeDir(e.dir, prefix+e.Name+"/"); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeDir(im.root, ""); err != nil {
		return nil, nil, err
	}
	if rem := w.pos % fileModulus; rem != 0 {
		w.zeros(fileModulus - rem)
	}
	if w.err == nil {
		w.err = w.w.Flush()
	}
	if w.err != nil {
		return nil, nil, w.err
	}
	if err := f.Close(); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(tmp, out); err != nil {
		return nil, nil, err
	}
	ok = true
	res.OutputSize = w.pos
	return res, hashes, nil
}

// Verify reads every file of a rebuilt image and compares it with the
// SHA-1 recorded while rebuilding.
func Verify(ctx context.Context, path string, want map[string][sha1.Size]byte, progress func(done, total int64)) error {
	im, err := Open(path)
	if err != nil {
		return err
	}
	defer im.Close()
	if im.Files != len(want) {
		return fmt.Errorf("%d files, expected %d", im.Files, len(want))
	}
	var done int64
	return im.walk(func(p string, e *Entry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		exp, ok := want[p]
		if !ok {
			return fmt.Errorf("%s: unexpected file", p)
		}
		h := sha1.New()
		if _, err := io.Copy(h, im.file(e)); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if [sha1.Size]byte(h.Sum(nil)) != exp {
			return fmt.Errorf("%s: content differs", p)
		}
		done += int64(e.Size)
		if progress != nil {
			progress(done, im.Bytes)
		}
		return nil
	})
}

// sectorWriter tracks the output position and the first error.
type sectorWriter struct {
	w   *bufio.Writer
	pos int64
	err error
}

func (s *sectorWriter) write(b []byte) {
	if s.err != nil {
		return
	}
	_, s.err = s.w.Write(b)
	s.pos += int64(len(b))
}

func (s *sectorWriter) fill(b byte, n int64) {
	chunk := make([]byte, min(n, 1<<16))
	for i := range chunk {
		chunk[i] = b
	}
	for n > 0 {
		k := min(n, int64(len(chunk)))
		s.write(chunk[:k])
		n -= k
	}
}

func (s *sectorWriter) zeros(n int64) { s.fill(0, n) }

// pad fills up to the next sector boundary.
func (s *sectorWriter) pad(b byte) {
	if rem := s.pos % SectorSize; rem != 0 {
		s.fill(b, SectorSize-rem)
	}
}
