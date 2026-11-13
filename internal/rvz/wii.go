package rvz

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"io"
	"sort"
)

// Wii discs keep each partition's data in 32 KiB blocks: a 1 KiB hash
// block (encrypted with the partition's title key, IV 0) and 31 KiB of
// data (encrypted, with bytes 0x3d0-0x3e0 of the encrypted hash block as
// IV). The hash block holds the SHA-1 of each 1 KiB of the block's data
// (H0), of the H0 tables of the 8 blocks of its subgroup (H1) and of the
// H1 tables of the 8 subgroups of its 2 MiB group (H2).

const (
	wiiBlock      = 0x8000
	wiiHashSize   = 0x400
	wiiData       = wiiBlock - wiiHashSize // 0x7c00
	blocksInGroup = 64
	wiiGroup      = blocksInGroup * wiiBlock // 2 MiB
)

// ErrNoCommonKey is returned when a Wii disc needs the Wii common key.
var ErrNoCommonKey = errors.New("the Wii common key is needed to convert Wii discs")

// ErrWrongCommonKey is returned when the common key does not decrypt the
// disc.
var ErrWrongCommonKey = errors.New("the Wii common key does not decrypt this disc (wrong key or damaged disc)")

func cbcDecrypt(key, iv, src, dst []byte) {
	b, _ := aes.NewCipher(key)
	cipher.NewCBCDecrypter(b, iv).CryptBlocks(dst, src)
}

func cbcEncrypt(key, iv, src, dst []byte) {
	b, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(b, iv).CryptBlocks(dst, src)
}

var zeroIV = make([]byte, 16)

// decryptBlock decrypts a 32 KiB block into its hash block and data.
func decryptBlock(key, block, hashes, data []byte) {
	cbcDecrypt(key, zeroIV, block[:wiiHashSize], hashes)
	cbcDecrypt(key, block[0x3d0:0x3e0], block[wiiHashSize:wiiBlock], data)
}

// encryptBlock encrypts a hash block and data into a 32 KiB block.
func encryptBlock(key, hashes, data, block []byte) {
	cbcEncrypt(key, zeroIV, hashes, block[:wiiHashSize])
	cbcEncrypt(key, block[0x3d0:0x3e0], data, block[wiiHashSize:wiiBlock])
}

// hashGroup computes the hash blocks of a 2 MiB group from its 64 blocks of
// data (blocks past the end of the partition are zeros), with zero padding.
func hashGroup(data []byte) [][wiiHashSize]byte {
	out := make([][wiiHashSize]byte, blocksInGroup)
	for i := range blocksInGroup {
		d := data[i*wiiData:]
		for j := range 31 {
			s := sha1.Sum(d[j*0x400 : (j+1)*0x400])
			copy(out[i][j*20:], s[:])
		}
	}
	var h2 [8 * 20]byte
	for sg := range 8 {
		var h1 [8 * 20]byte
		for k := range 8 {
			s := sha1.Sum(out[sg*8+k][:31*20])
			copy(h1[k*20:], s[:])
		}
		for k := range 8 {
			copy(out[sg*8+k][0x280:], h1[:])
		}
		s := sha1.Sum(h1[:])
		copy(h2[sg*20:], s[:])
	}
	for i := range blocksInGroup {
		copy(out[i][0x340:], h2[:])
	}
	return out
}

// hashFields are the 20-byte comparison windows of a hash block, as
// Dolphin writes hash exceptions: each hash, and the padding areas (the
// 32-byte ones as two overlapping windows).
var hashFields = func() []int {
	var f []int
	add := func(off, size int) {
		for l := 0; l < size; l += 20 {
			f = append(f, off+min(l, size-20))
		}
	}
	add(0, 31*20)
	add(0x26c, 20)
	add(0x280, 8*20)
	add(0x320, 32)
	add(0x340, 8*20)
	add(0x3e0, 32)
	return f
}()

// partition is a Wii partition as stored in an RVZ.
type partition struct {
	key       [16]byte
	dataStart int64 // disc offset of the partition data
	files     []file
}

// wiiPartitions lists the encrypted partitions of a Wii disc, sorted by
// offset, with their title keys and file system tables.
func wiiPartitions(r io.ReaderAt, size int64, commonKeys [][]byte) ([]partitionInfo, error) {
	hdr := make([]byte, 0x80)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	if hdr[0x60] != 0 || hdr[0x61] != 0 {
		return nil, nil // hashes or encryption turned off: stored as raw data
	}
	pt := make([]byte, 32)
	if _, err := r.ReadAt(pt, 0x40000); err != nil {
		return nil, err
	}
	var offs []int64
	for g := range 4 {
		n := binary.BigEndian.Uint32(pt[g*8:])
		at := int64(binary.BigEndian.Uint32(pt[g*8+4:])) << 2
		if n > 64 || at+int64(n)*8 > size {
			continue
		}
		t := make([]byte, n*8)
		if _, err := r.ReadAt(t, at); err != nil {
			return nil, err
		}
		for i := range int(n) {
			offs = append(offs, int64(binary.BigEndian.Uint32(t[i*8:]))<<2)
		}
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	var parts []partitionInfo
	var lastEnd int64
	for _, off := range offs {
		if off < lastEnd || off+0x2c0 > size {
			continue
		}
		ph := make([]byte, 0x2c0)
		if _, err := r.ReadAt(ph, off); err != nil {
			return nil, err
		}
		if binary.BigEndian.Uint32(ph) != 0x10001 {
			continue // not a partition (e.g. scrubbed)
		}
		dataOff := int64(binary.BigEndian.Uint32(ph[0x2b8:])) << 2
		dataSize := int64(binary.BigEndian.Uint32(ph[0x2bc:])) << 2
		dataStart := off + dataOff
		if dataStart%wiiBlock != 0 || dataSize < wiiBlock {
			continue
		}
		dataEnd := min(dataStart+dataSize, size)
		idx := int(ph[0x1f1])
		if idx >= len(commonKeys) || len(commonKeys[idx]) != 16 {
			return nil, ErrNoCommonKey
		}
		iv := make([]byte, 16)
		copy(iv, ph[0x1dc:0x1e4])
		var p partitionInfo
		cbcDecrypt(commonKeys[idx], iv, ph[0x1bf:0x1cf], p.key[:])
		p.offset, p.dataOff, p.dataStart, p.dataEnd = off, dataOff, dataStart, dataEnd
		if !p.checkKey(r) {
			return nil, ErrWrongCommonKey
		}
		p.files, p.fstEnd = p.fileSystem(r)
		parts = append(parts, p)
		lastEnd = dataEnd
	}
	return parts, nil
}

type partitionInfo struct {
	partition
	offset, dataOff, dataEnd int64
	fstEnd                   int64 // decrypted offset of the end of the FST
}

// readDecrypted reads n bytes at a decrypted offset of the partition data.
func (p *partitionInfo) readDecrypted(r io.ReaderAt, off int64, n int) ([]byte, error) {
	out := make([]byte, 0, n)
	blk := make([]byte, wiiBlock)
	hashes := make([]byte, wiiHashSize)
	data := make([]byte, wiiData)
	for len(out) < n {
		b := off / wiiData
		if p.dataStart+(b+1)*wiiBlock > p.dataEnd {
			return nil, io.ErrUnexpectedEOF
		}
		if _, err := r.ReadAt(blk, p.dataStart+b*wiiBlock); err != nil {
			return nil, err
		}
		decryptBlock(p.key[:], blk, hashes, data)
		in := off % wiiData
		k := min(int64(n-len(out)), wiiData-in)
		out = append(out, data[in:in+k]...)
		off += k
	}
	return out, nil
}

// checkKey decrypts the first block and checks its first H0 hash.
func (p *partitionInfo) checkKey(r io.ReaderAt) bool {
	blk := make([]byte, wiiBlock)
	if _, err := r.ReadAt(blk, p.dataStart); err != nil {
		return false
	}
	hashes, data := make([]byte, wiiHashSize), make([]byte, wiiData)
	decryptBlock(p.key[:], blk, hashes, data)
	s := sha1.Sum(data[:0x400])
	return bytes.Equal(s[:], hashes[:20])
}

// fileSystem reads the partition's FST: the files (decrypted offsets) and
// where the FST ends. Failures give no files.
func (p *partitionInfo) fileSystem(r io.ReaderAt) ([]file, int64) {
	h, err := p.readDecrypted(r, 0x420, 12)
	if err != nil {
		return nil, 0
	}
	fstOff := int64(binary.BigEndian.Uint32(h[4:])) << 2
	fstSize := int64(binary.BigEndian.Uint32(h[8:])) << 2
	if fstSize < 12 || fstSize > 64<<20 {
		return nil, 0
	}
	fst, err := p.readDecrypted(r, fstOff, int(fstSize))
	if err != nil {
		return nil, 0
	}
	n := int64(binary.BigEndian.Uint32(fst[8:]))
	if n*12 > fstSize {
		return nil, fstOff + fstSize
	}
	var files []file
	for i := int64(1); i < n; i++ {
		e := fst[i*12:]
		if e[0] != 0 {
			continue
		}
		f := file{int64(binary.BigEndian.Uint32(e[4:])) << 2, int64(binary.BigEndian.Uint32(e[8:]))}
		if f.size > 0 {
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].off < files[j].off })
	return files, fstOff + fstSize
}

// rawOffset converts a decrypted partition offset to a disc offset.
func (p *partitionInfo) rawOffset(off int64) int64 {
	return p.dataStart + off/wiiData*wiiBlock + wiiHashSize + off%wiiData
}
