package rvz

import (
	"encoding/binary"
	"errors"
	"io"
	"sort"
)

const (
	gcMagic  = 0xc2339f3d // at 0x1c of GameCube discs
	wiiMagic = 0x5d1c9ea3 // at 0x18 of Wii discs
)

// file is a file's extent on the disc.
type file struct{ off, size int64 }

// gcFiles lists the files of a GameCube disc from its file system table
// (FST), sorted by offset.
func gcFiles(r io.ReaderAt, discSize int64) ([]file, error) {
	hdr := make([]byte, 0x440)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	fstOff := int64(binary.BigEndian.Uint32(hdr[0x424:]))
	fstSize := int64(binary.BigEndian.Uint32(hdr[0x428:]))
	if fstOff <= 0 || fstSize < 12 || fstOff+fstSize > discSize || fstSize > 64<<20 {
		return nil, errors.New("no file system table")
	}
	fst := make([]byte, fstSize)
	if _, err := r.ReadAt(fst, fstOff); err != nil {
		return nil, err
	}
	n := int64(binary.BigEndian.Uint32(fst[8:]))
	if n*12 > fstSize {
		return nil, errors.New("invalid file system table")
	}
	var files []file
	for i := int64(1); i < n; i++ {
		e := fst[i*12:]
		if e[0] != 0 {
			continue // directory
		}
		f := file{int64(binary.BigEndian.Uint32(e[4:])), int64(binary.BigEndian.Uint32(e[8:]))}
		if f.size > 0 && f.off+f.size <= discSize {
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].off < files[j].off })
	return files, nil
}

// fileEnd returns the end of the file containing off, or -1.
func fileEnd(files []file, off int64) int64 {
	i := sort.Search(len(files), func(i int) bool { return files[i].off > off }) - 1
	if i >= 0 && off < files[i].off+files[i].size {
		return files[i].off + files[i].size
	}
	return -1
}
