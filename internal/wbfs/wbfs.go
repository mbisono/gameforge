// Package wbfs reads Wii disc images in the WBFS format of USB loaders,
// as plain disc images.
//
// A .wbfs file starts with a header (big endian): "WBFS", the number of
// host sectors, the host sector size and the WBFS cluster size as powers
// of two, and a table of disc slots. One host sector later comes the first
// disc: a copy of its first 256 bytes, then a 16-bit cluster number for
// each cluster of the disc (the largest Wii disc, two layers of 143432
// sectors of 32 KiB), where 0 means a cluster that was not stored (unused
// by the game, read as zeros). On FAT32 drives the file is split into
// game.wbfs, game.wbf1, game.wbf2…, which are one file in order.
package wbfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	wiiSector    = 0x8000
	layerSectors = 143432
	// SingleLayer and DualLayer are the sizes of Wii discs.
	SingleLayer = layerSectors * wiiSector
	DualLayer   = 8511160320
)

// Reader reads the disc of a WBFS image.
type Reader struct {
	files   []*os.File
	starts  []int64 // where each part starts in the joined file
	total   int64
	cluster int64
	table   []uint16
	size    int64
	head    [256]byte
}

// Parts lists the files of a WBFS image: path, then path with .wbf1,
// .wbf2… as long as they exist.
func Parts(path string) []string {
	parts := []string{path}
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	for i := 1; i <= 9; i++ {
		p := fmt.Sprintf("%s.wbf%d", stem, i)
		if _, err := os.Stat(p); err != nil {
			break
		}
		parts = append(parts, p)
	}
	return parts
}

// Open opens a WBFS image and the parts it is split into.
func Open(path string) (*Reader, error) {
	r := &Reader{}
	for _, p := range Parts(path) {
		f, err := os.Open(p)
		if err != nil {
			r.Close()
			return nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			r.Close()
			return nil, err
		}
		r.files = append(r.files, f)
		r.starts = append(r.starts, r.total)
		r.total += st.Size()
	}
	name := filepath.Base(path)
	h := make([]byte, 12)
	if _, err := r.read(h, 0); err != nil || string(h[:4]) != "WBFS" {
		r.Close()
		return nil, fmt.Errorf("%s: not a WBFS image", name)
	}
	hdShift, clShift := h[8], h[9]
	if hdShift < 9 || hdShift > 16 || clShift < 15 || clShift > 30 {
		r.Close()
		return nil, fmt.Errorf("%s: invalid WBFS sector sizes", name)
	}
	hdSector := int64(1) << hdShift
	r.cluster = int64(1) << clShift
	slot := make([]byte, 1)
	if _, err := r.read(slot, 12); err != nil || slot[0] == 0 {
		r.Close()
		return nil, fmt.Errorf("%s: the WBFS image holds no disc", name)
	}
	clusters := int(int64(2*layerSectors*wiiSector) / r.cluster)
	info := make([]byte, 256+2*clusters)
	if _, err := r.read(info, hdSector); err != nil {
		r.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	copy(r.head[:], info)
	r.table = make([]uint16, clusters)
	used := int64(0)
	for i := range r.table {
		r.table[i] = binary.BigEndian.Uint16(info[256+2*i:])
		if r.table[i] != 0 {
			if (int64(r.table[i])+1)*r.cluster > r.total {
				r.Close()
				return nil, fmt.Errorf("%s: cluster %d is past the end of the image (a part missing?)", name, i)
			}
			used = int64(i+1) * r.cluster
		}
	}
	r.size = SingleLayer
	if used > SingleLayer {
		r.size = DualLayer
	}
	return r, nil
}

// Size is the size of the disc: a single-layer disc unless a stored
// cluster lies beyond it.
func (r *Reader) Size() int64 { return r.size }

// Header is the first 256 bytes of the disc (game ID, title).
func (r *Reader) Header() []byte { return r.head[:] }

// Stored is the number of bytes of the disc the image stores.
func (r *Reader) Stored() int64 {
	var n int64
	for _, c := range r.table {
		if c != 0 {
			n += r.cluster
		}
	}
	return n
}

// FileSize is the size of the image's files together.
func (r *Reader) FileSize() int64 { return r.total }

// Close closes the files.
func (r *Reader) Close() error {
	var err error
	for _, f := range r.files {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// read reads from the joined parts.
func (r *Reader) read(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		i := len(r.starts) - 1
		for i > 0 && r.starts[i] > pos {
			i--
		}
		if pos >= r.total {
			return n, io.ErrUnexpectedEOF
		}
		k, err := r.files[i].ReadAt(p[n:], pos-r.starts[i])
		n += k
		if err != nil && err != io.EOF {
			return n, err
		}
		if k == 0 {
			return n, io.ErrUnexpectedEOF
		}
	}
	return n, nil
}

// ReadAt reads the disc; clusters the image does not store read as zeros.
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("wbfs: negative offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	want := len(p)
	if rest := r.size - off; int64(want) > rest {
		want = int(rest)
	}
	n := 0
	for n < want {
		pos := off + int64(n)
		c := pos / r.cluster
		in := pos % r.cluster
		k := int(min(int64(want-n), r.cluster-in))
		if r.table[c] == 0 {
			clear(p[n : n+k])
		} else if _, err := r.read(p[n:n+k], int64(r.table[c])*r.cluster+in); err != nil {
			return n, err
		}
		n += k
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
