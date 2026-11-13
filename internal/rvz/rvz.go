// Package rvz writes and reads RVZ, Dolphin's compressed GameCube and Wii
// disc format (a variant of WIA).
//
// An RVZ divides the disc into chunks (groups), each compressed with
// Zstandard on its own. Before compression, the pseudorandom padding of the
// disc is replaced by the seed of the generator that made it ("RVZ
// packing"), which turns gigabytes of incompressible filler into a few
// bytes. Wii partition data is stored decrypted and without its hash
// blocks, which are recomputed when reading; where the original hashes
// differ from the recomputed ones, "hash exceptions" keep the original. The
// layout follows Dolphin's docs/WiaAndRvz.md.
package rvz

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"

	"github.com/klauspost/compress/zstd"
)

const (
	head1Size    = 0x48
	head2Size    = 0xdc
	partSize     = 0x30 // wia_part_t
	rawEntrySize = 0x18
	groupSize    = 12 // rvz_group_t

	// DefaultChunkSize is the RVZ block size Dolphin suggests; chunk
	// sizes are powers of two from 32 KiB to 2 MiB.
	DefaultChunkSize = 128 << 10

	version           = 0x01000000
	versionCompatible = 0x00030000

	discGameCube = 1
	discWii      = 2

	comprNone = 0
	comprZstd = 5
)

var errBadPack = errors.New("invalid RVZ packed data")

// Result describes a conversion.
type Result struct {
	InputSize  int64
	OutputSize int64
	SHA1       [sha1.Size]byte // of the disc image
	Wii        bool
	Partitions int
}

// Options controls writing an RVZ.
type Options struct {
	Level zstd.EncoderLevel
	// CommonKeys are the Wii common keys by index (0 normal, 1 Korean, 2
	// vWii), needed to decrypt Wii partitions.
	CommonKeys [][]byte
	// ChunkSize is the block size (0: DefaultChunkSize).
	ChunkSize int
}

// IsGameCube reports whether a disc image starts like a GameCube disc.
func IsGameCube(r io.ReaderAt) bool {
	b := make([]byte, 0x20)
	_, err := r.ReadAt(b, 0)
	return err == nil && binary.BigEndian.Uint32(b[0x1c:]) == gcMagic
}

// IsWii reports whether a disc image starts like a Wii disc.
func IsWii(r io.ReaderAt) bool {
	b := make([]byte, 0x20)
	_, err := r.ReadAt(b, 0)
	return err == nil && binary.BigEndian.Uint32(b[0x18:]) == wiiMagic
}

// region is a stretch of the disc stored as raw data or partition data.
type region struct {
	start, size int64 // disc range (raw: start rounded down to 32 KiB)
	part        *partition
	decBase     int64 // partition: decrypted offset of the region start
	firstGroup  int
	groups      int
	// Raw data entry fields as stored (offset not rounded).
	entryOff, entrySize int64
}

// layout lists the regions of a disc as Dolphin lays them out: raw data
// (skipping the first 0x80 bytes, kept in the header), and for each Wii
// partition its header area as raw data and its data split in two at the
// end of the FST rounded up to 2 MiB.
type layout struct {
	regions []*region
	parts   []partitionInfo
	pd      [][2]*region // per partition
	groups  int
	chunk   int64
}

func (l *layout) addRaw(off, size int64) {
	if off < 0x80 {
		skip := min(0x80-off, size)
		off, size = off+skip, size-skip
	}
	if size <= 0 {
		return
	}
	start := off &^ (blockBytes - 1)
	r := &region{start: start, size: size + off - start, entryOff: off, entrySize: size,
		firstGroup: l.groups, groups: int((size + l.chunk - 1) / l.chunk)}
	l.groups += r.groups
	l.regions = append(l.regions, r)
}

func (l *layout) addPartData(p *partition, off, size int64) *region {
	size = size / wiiBlock * wiiBlock
	r := &region{start: off, size: size, part: p, decBase: (off - p.dataStart) / wiiBlock * wiiData,
		firstGroup: l.groups, groups: int((size + l.chunk - 1) / l.chunk)}
	l.groups += r.groups
	l.regions = append(l.regions, r)
	return r
}

func newLayout(r io.ReaderAt, size int64, wii bool, keys [][]byte, chunk int) (*layout, error) {
	l := &layout{chunk: int64(chunk)}
	if !wii {
		l.addRaw(0, size)
		return l, nil
	}
	parts, err := wiiPartitions(r, size, keys)
	if err != nil {
		return nil, err
	}
	l.parts = parts
	var last int64
	for i := range l.parts {
		p := &l.parts[i]
		l.addRaw(last, p.offset-last)
		l.addRaw(p.offset, p.dataOff)
		split := p.dataEnd
		if p.fstEnd > 0 {
			fstEnd := p.rawOffset(p.fstEnd)
			split = min(p.dataEnd, (fstEnd-p.dataStart+wiiGroup-1)/wiiGroup*wiiGroup+p.dataStart)
		}
		pd0 := l.addPartData(&p.partition, p.dataStart, split-p.dataStart)
		pd1 := l.addPartData(&p.partition, split, p.dataEnd-split)
		l.pd = append(l.pd, [2]*region{pd0, pd1})
		last = pd1.start + pd1.size
	}
	l.addRaw(last, size-last)
	return l, nil
}

type group struct {
	data       []byte
	compressed bool
	packedSize uint32
	zero       bool
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// encodeGroup builds a stored group from its exception lists (nil for raw
// data), its data, and the RVZ packed form of the data (nil if none).
func encodeGroup(enc *zstd.Encoder, lists, data, packed []byte, exceptions bool) group {
	if allZero(data) && !exceptions {
		return group{zero: true}
	}
	g := group{}
	payload := data
	if packed != nil {
		payload, g.packedSize = packed, uint32(len(packed))
	}
	plain := append([]byte(nil), lists...)
	for len(plain)%4 != 0 {
		plain = append(plain, 0) // NONE: data starts at a 4-byte boundary
	}
	plain = append(plain, payload...)
	c := enc.EncodeAll(append(append([]byte(nil), lists...), payload...), nil)
	if len(c) < len(plain) {
		g.data, g.compressed = c, true
	} else {
		g.data = plain
	}
	return g
}

// encodeWii encodes the chunks of one 2 MiB group of partition data.
// blocks holds the encrypted blocks (up to 64); decOff is the decrypted
// offset of the group.
func encodeWii(enc *zstd.Encoder, p *partition, blocks []byte, decOff int64, chunk int) []group {
	nb := len(blocks) / wiiBlock
	dec := make([]byte, blocksInGroup*wiiData)
	actual := make([][wiiHashSize]byte, nb)
	for b := range nb {
		decryptBlock(p.key[:], blocks[b*wiiBlock:(b+1)*wiiBlock], actual[b][:], dec[b*wiiData:(b+1)*wiiData])
	}
	computed := hashGroup(dec)
	bpc := chunk / wiiBlock
	chunks := (nb + bpc - 1) / bpc
	lists := make([][]byte, chunks)
	counts := make([]int, chunks)
	for b := range nb {
		c := b / bpc
		base := (b % bpc) * wiiHashSize
		for _, off := range hashFields {
			if [20]byte(actual[b][off:off+20]) != [20]byte(computed[b][off:off+20]) {
				lists[c] = binary.BigEndian.AppendUint16(lists[c], uint16(base+off))
				lists[c] = append(lists[c], actual[b][off:off+20]...)
				counts[c]++
			}
		}
	}
	data := dec[:nb*wiiData]
	runs := scan(data, decOff, p.files)
	out := make([]group, chunks)
	for c := range chunks {
		s, e := c*bpc*wiiData, min((c+1)*bpc, nb)*wiiData
		l := binary.BigEndian.AppendUint16(nil, uint16(counts[c]))
		l = append(l, lists[c]...)
		out[c] = encodeGroup(enc, l, data[s:e], emit(data, runs, s, e), counts[c] > 0)
	}
	return out
}

// Compress writes a GameCube or Wii disc image as an RVZ (Zstandard,
// 128 KiB chunks unless opt says otherwise) at out, via out.part. progress receives the bytes read
// and the total.
func Compress(ctx context.Context, in, out string, opt Options, progress func(done, total int64)) (*Result, error) {
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
	wii := IsWii(src)
	if !wii && !IsGameCube(src) || size < 0x50000 {
		return nil, errors.New("not a GameCube or Wii disc image")
	}
	chunk := opt.ChunkSize
	if chunk == 0 {
		chunk = DefaultChunkSize
	}
	if chunk < 32<<10 || chunk > wiiGroup || chunk&(chunk-1) != 0 {
		return nil, fmt.Errorf("rvz: invalid chunk size %d", chunk)
	}
	l, err := newLayout(src, size, wii, opt.CommonKeys, chunk)
	if err != nil {
		return nil, err
	}
	var gcFS []file
	if !wii {
		gcFS, _ = gcFiles(src, size) // without the FST, less padding is found
	}
	dhead := make([]byte, 0x80)
	if _, err := src.ReadAt(dhead, 0); err != nil {
		return nil, err
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
	w := newBufWriter(f)
	pos := int64(head1Size + head2Size + len(l.parts)*partSize)
	w.Write(make([]byte, pos))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A unit is a raw chunk or a 2 MiB group of partition data.
	type unit struct {
		r    *region
		off  int64 // disc offset
		data []byte
		res  chan []group
	}
	workers := runtime.NumCPU()
	jobs := make(chan unit, workers)
	order := make(chan unit, workers*4)
	level := opt.Level
	if level == 0 {
		level = zstd.SpeedBetterCompression
	}
	for range workers {
		go func() {
			enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
			defer enc.Close()
			for u := range jobs {
				if u.r.part == nil {
					packed := pack(u.data, u.off, gcFS)
					u.res <- []group{encodeGroup(enc, nil, u.data, packed, false)}
					continue
				}
				dec := u.r.decBase + (u.off-u.r.start)/wiiBlock*wiiData
				u.res <- encodeWii(enc, u.r.part, u.data, dec, chunk)
			}
		}()
	}
	hash := sha1.New()
	var hashed int64 // the disc up to here is in hash
	var readErr error
	hashTo := func(end int64, data []byte, at int64) {
		// Hash the disc up to end; data holds the disc from at.
		for hashed < end && readErr == nil {
			if hashed >= at && hashed < at+int64(len(data)) {
				n := min(end, at+int64(len(data))) - hashed
				hash.Write(data[hashed-at : hashed-at+n])
				hashed += n
				continue
			}
			n := min(end-hashed, 1<<20) // a gap: read it
			if hashed < at {
				n = min(n, at-hashed)
			}
			b := make([]byte, n)
			if _, err := src.ReadAt(b, hashed); err != nil {
				readErr = err
				return
			}
			hash.Write(b)
			hashed += n
		}
	}
	go func() {
		defer close(jobs)
		defer close(order)
		for _, r := range l.regions {
			step := int64(chunk)
			if r.part != nil {
				step = wiiGroup
			}
			for off := r.start; off < r.start+r.size; off += step {
				if ctx.Err() != nil {
					return
				}
				data := make([]byte, min(step, r.start+r.size-off))
				if _, err := src.ReadAt(data, off); err != nil {
					readErr = err
					cancel()
					return
				}
				hashTo(off+int64(len(data)), data, off)
				if readErr != nil {
					cancel()
					return
				}
				u := unit{r, off, data, make(chan []group, 1)}
				order <- u
				jobs <- u
			}
		}
		hashTo(size, nil, size)
	}()

	var groups []byte
	var done int64
	for u := range order {
		gs := <-u.res
		if ctx.Err() != nil {
			continue
		}
		for _, g := range gs {
			var e [groupSize]byte
			if !g.zero {
				binary.BigEndian.PutUint32(e[0:], uint32(pos/4))
				n := uint32(len(g.data))
				if g.compressed {
					n |= 0x80000000
				}
				binary.BigEndian.PutUint32(e[4:], n)
				binary.BigEndian.PutUint32(e[8:], g.packedSize)
				w.Write(g.data)
				pos += int64(len(g.data))
				if pad := (4 - pos%4) % 4; pad > 0 {
					w.Write(make([]byte, pad))
					pos += pad
				}
			}
			groups = append(groups, e[:]...)
		}
		done += int64(len(u.data))
		if progress != nil {
			progress(min(done, size), size)
		}
	}
	if readErr != nil {
		return nil, readErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(groups)/groupSize != l.groups {
		return nil, errors.New("internal error: group count")
	}

	// Tables: partitions (uncompressed), raw data entries and groups
	// (compressed).
	parts := make([]byte, 0, len(l.parts)*partSize)
	for i, p := range l.parts {
		e := make([]byte, partSize)
		copy(e, p.key[:])
		for k, r := range l.pd[i] {
			d := e[16+16*k:]
			binary.BigEndian.PutUint32(d[0:], uint32(r.start/wiiBlock))
			binary.BigEndian.PutUint32(d[4:], uint32(r.size/wiiBlock))
			binary.BigEndian.PutUint32(d[8:], uint32(r.firstGroup))
			binary.BigEndian.PutUint32(d[12:], uint32(r.groups))
		}
		parts = append(parts, e...)
	}
	var raws []byte
	nRaw := 0
	for _, r := range l.regions {
		if r.part != nil {
			continue
		}
		e := make([]byte, rawEntrySize)
		binary.BigEndian.PutUint64(e[0:], uint64(r.entryOff))
		binary.BigEndian.PutUint64(e[8:], uint64(r.entrySize))
		binary.BigEndian.PutUint32(e[16:], uint32(r.firstGroup))
		binary.BigEndian.PutUint32(e[20:], uint32(r.groups))
		raws = append(raws, e...)
		nRaw++
	}
	enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
	rawZ := enc.EncodeAll(raws, nil)
	groupsZ := enc.EncodeAll(groups, nil)
	enc.Close()
	rawOff := pos
	w.Write(rawZ)
	pos += int64(len(rawZ))
	groupOff := pos
	w.Write(groupsZ)
	pos += int64(len(groupsZ))
	if err := w.Flush(); err != nil {
		return nil, err
	}

	discType := uint32(discGameCube)
	if wii {
		discType = discWii
	}
	h2 := make([]byte, head2Size)
	binary.BigEndian.PutUint32(h2[0:], discType)
	binary.BigEndian.PutUint32(h2[4:], comprZstd)
	binary.BigEndian.PutUint32(h2[8:], uint32(zstdLevelNumber(level)))
	binary.BigEndian.PutUint32(h2[12:], uint32(chunk))
	copy(h2[16:], dhead)
	binary.BigEndian.PutUint32(h2[0x90:], uint32(len(l.parts)))
	binary.BigEndian.PutUint32(h2[0x94:], partSize)
	binary.BigEndian.PutUint64(h2[0x98:], head1Size+head2Size)
	ph := sha1.Sum(parts)
	copy(h2[0xa0:], ph[:])
	binary.BigEndian.PutUint32(h2[0xb4:], uint32(nRaw))
	binary.BigEndian.PutUint64(h2[0xb8:], uint64(rawOff))
	binary.BigEndian.PutUint32(h2[0xc0:], uint32(len(rawZ)))
	binary.BigEndian.PutUint32(h2[0xc4:], uint32(l.groups))
	binary.BigEndian.PutUint64(h2[0xc8:], uint64(groupOff))
	binary.BigEndian.PutUint32(h2[0xd0:], uint32(len(groupsZ)))
	h1 := make([]byte, head1Size)
	copy(h1, "RVZ\x01")
	binary.BigEndian.PutUint32(h1[4:], version)
	binary.BigEndian.PutUint32(h1[8:], versionCompatible)
	binary.BigEndian.PutUint32(h1[12:], head2Size)
	discHash := sha1.Sum(h2)
	copy(h1[16:], discHash[:])
	binary.BigEndian.PutUint64(h1[36:], uint64(size))
	binary.BigEndian.PutUint64(h1[44:], uint64(pos))
	headHash := sha1.Sum(h1[:head1Size-sha1.Size])
	copy(h1[52:], headHash[:])
	head := append(append(h1, h2...), parts...)
	if _, err := f.WriteAt(head, 0); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, out); err != nil {
		return nil, err
	}
	ok = true
	res := &Result{InputSize: size, OutputSize: pos, Wii: wii, Partitions: len(l.parts)}
	copy(res.SHA1[:], hash.Sum(nil))
	return res, nil
}

// zstdLevelNumber is the zstd level number of an encoder level, for the
// informational compr_level field.
func zstdLevelNumber(l zstd.EncoderLevel) int {
	switch l {
	case zstd.SpeedFastest:
		return 1
	case zstd.SpeedDefault:
		return 3
	case zstd.SpeedBetterCompression:
		return 7
	}
	return 11 // SpeedBestCompression
}

// Reader reads an RVZ.
type Reader struct {
	f         *os.File
	ISOSize   int64
	DiscType  int
	compr     uint32
	chunk     int64
	regions   []*region
	groups    []rvzGroup
	firstData [0x80]byte
}

type rvzGroup struct {
	off        int64
	size       uint32
	compressed bool
	packedSize uint32
}

// Open reads the headers and tables of an RVZ.
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

// Wii reports whether the RVZ holds a Wii disc.
func (r *Reader) Wii() bool { return r.DiscType == discWii }

func (r *Reader) init() error {
	h := make([]byte, head1Size+head2Size)
	if _, err := r.f.ReadAt(h, 0); err != nil {
		return fmt.Errorf("reading header: %w", err)
	}
	h1, h2 := h[:head1Size], h[head1Size:]
	if string(h1[:4]) != "RVZ\x01" {
		if string(h1[:4]) == "WIA\x01" {
			return errors.New("WIA files are not supported (only RVZ)")
		}
		return errors.New("not an RVZ file")
	}
	if sha1.Sum(h1[:head1Size-sha1.Size]) != [sha1.Size]byte(h1[52:72]) {
		return errors.New("damaged RVZ header")
	}
	if binary.BigEndian.Uint32(h1[8:]) > version {
		return errors.New("RVZ version not supported")
	}
	if n := binary.BigEndian.Uint32(h1[12:]); n < head2Size {
		return errors.New("damaged RVZ header")
	}
	if sha1.Sum(h2) != [sha1.Size]byte(h1[16:36]) {
		return errors.New("damaged RVZ header")
	}
	r.ISOSize = int64(binary.BigEndian.Uint64(h1[36:]))
	r.DiscType = int(binary.BigEndian.Uint32(h2[0:]))
	r.compr = binary.BigEndian.Uint32(h2[4:])
	r.chunk = int64(binary.BigEndian.Uint32(h2[12:]))
	copy(r.firstData[:], h2[16:])
	if r.compr != comprZstd && r.compr != comprNone {
		return fmt.Errorf("RVZ compression %d is not supported (only Zstandard)", r.compr)
	}
	if r.chunk < 32<<10 || r.chunk > wiiGroup || r.chunk&(r.chunk-1) != 0 {
		return errors.New("RVZ chunk size not supported")
	}
	nPart := binary.BigEndian.Uint32(h2[0x90:])
	pSize := binary.BigEndian.Uint32(h2[0x94:])
	partOff := int64(binary.BigEndian.Uint64(h2[0x98:]))
	nRaw := binary.BigEndian.Uint32(h2[0xb4:])
	rawOff := int64(binary.BigEndian.Uint64(h2[0xb8:]))
	rawLen := binary.BigEndian.Uint32(h2[0xc0:])
	nGroups := binary.BigEndian.Uint32(h2[0xc4:])
	groupOff := int64(binary.BigEndian.Uint64(h2[0xc8:]))
	groupLen := binary.BigEndian.Uint32(h2[0xd0:])
	if nPart > 64 || nRaw > 1<<16 || nGroups > 1<<24 {
		return errors.New("invalid RVZ header")
	}

	if nPart > 0 {
		if pSize < 0x10 || pSize > 0x100 {
			return errors.New("invalid RVZ partition table")
		}
		pt := make([]byte, nPart*pSize)
		if _, err := r.f.ReadAt(pt, partOff); err != nil {
			return fmt.Errorf("reading partitions: %w", err)
		}
		if sha1.Sum(pt) != [sha1.Size]byte(h2[0xa0:0xb4]) {
			return errors.New("damaged RVZ partition table")
		}
		for i := range nPart {
			e := make([]byte, partSize)
			copy(e, pt[i*pSize:(i+1)*pSize])
			p := &partition{}
			copy(p.key[:], e[:16])
			var pds [2]*region
			for k := range 2 {
				d := e[16+16*k:]
				pds[k] = &region{
					start:      int64(binary.BigEndian.Uint32(d[0:])) * wiiBlock,
					size:       int64(binary.BigEndian.Uint32(d[4:])) * wiiBlock,
					firstGroup: int(binary.BigEndian.Uint32(d[8:])),
					groups:     int(binary.BigEndian.Uint32(d[12:])),
					part:       p,
				}
			}
			p.dataStart = pds[0].start
			for _, pd := range pds {
				pd.decBase = (pd.start - p.dataStart) / wiiBlock * wiiData
				if pd.size > 0 {
					r.regions = append(r.regions, pd)
				}
			}
		}
	}
	if nRaw > 0 {
		raw, err := r.table(rawOff, rawLen, int(nRaw)*rawEntrySize)
		if err != nil {
			return err
		}
		for i := range int(nRaw) {
			e := raw[i*rawEntrySize:]
			off := int64(binary.BigEndian.Uint64(e[0:]))
			size := int64(binary.BigEndian.Uint64(e[8:]))
			start := off &^ (blockBytes - 1)
			r.regions = append(r.regions, &region{start: start, size: size + off - start,
				firstGroup: int(binary.BigEndian.Uint32(e[16:])), groups: int(binary.BigEndian.Uint32(e[20:]))})
		}
	}
	g, err := r.table(groupOff, groupLen, int(nGroups)*groupSize)
	if err != nil {
		return err
	}
	r.groups = make([]rvzGroup, nGroups)
	for i := range r.groups {
		e := g[i*groupSize:]
		s := binary.BigEndian.Uint32(e[4:])
		r.groups[i] = rvzGroup{
			off:        int64(binary.BigEndian.Uint32(e[0:])) * 4,
			size:       s &^ 0x80000000,
			compressed: s&0x80000000 != 0 && r.compr == comprZstd,
			packedSize: binary.BigEndian.Uint32(e[8:]),
		}
	}
	sort.Slice(r.regions, func(i, j int) bool { return r.regions[i].start < r.regions[j].start })
	for _, reg := range r.regions {
		if reg.firstGroup+reg.groups > len(r.groups) || reg.start < 0 || reg.start+reg.size > r.ISOSize+wiiBlock {
			return errors.New("invalid RVZ data entries")
		}
		need := int((reg.size + r.chunk - 1) / r.chunk)
		if reg.groups < need-1 || reg.groups > need {
			return errors.New("invalid RVZ data entries")
		}
	}
	return nil
}

// table reads a compressed table of n bytes.
func (r *Reader) table(off int64, length uint32, n int) ([]byte, error) {
	if length > 256<<20 {
		return nil, errors.New("invalid RVZ table")
	}
	b := make([]byte, length)
	if _, err := r.f.ReadAt(b, off); err != nil {
		return nil, fmt.Errorf("reading RVZ table: %w", err)
	}
	if r.compr == comprZstd {
		dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		if b, err = dec.DecodeAll(b, nil); err != nil {
			return nil, fmt.Errorf("RVZ table: %w", err)
		}
	}
	if len(b) < n {
		return nil, errors.New("truncated RVZ table")
	}
	return b[:n], nil
}

// readGroup returns group i decompressed (nil for an all-zero group): for
// partition data the exception lists, then the data.
func (r *Reader) readGroup(dec *zstd.Decoder, i int) ([]byte, error) {
	g := r.groups[i]
	if g.size == 0 {
		return nil, nil
	}
	b := make([]byte, g.size)
	if _, err := r.f.ReadAt(b, g.off); err != nil {
		return nil, fmt.Errorf("chunk %d: %w", i, err)
	}
	if g.compressed {
		var err error
		if b, err = dec.DecodeAll(b, nil); err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
	}
	return b, nil
}

// decodeData turns a group's stored data into out, given its disc (or
// decrypted partition) offset.
func (r *Reader) decodeData(i int, b []byte, off int64, out []byte) error {
	g := r.groups[i]
	if g.packedSize != 0 {
		if len(b) < int(g.packedSize) {
			return fmt.Errorf("chunk %d: %w", i, errBadPack)
		}
		if err := unpack(b[:g.packedSize], off, out); err != nil {
			return fmt.Errorf("chunk %d: %w", i, err)
		}
		return nil
	}
	if len(b) < len(out) {
		return fmt.Errorf("chunk %d: truncated", i)
	}
	copy(out, b)
	return nil
}

// rawChunk decodes chunk k of a raw region.
func (r *Reader) rawChunk(dec *zstd.Decoder, reg *region, k int) ([]byte, error) {
	start := reg.start + int64(k)*r.chunk
	out := make([]byte, min(r.chunk, reg.start+reg.size-start))
	if k >= reg.groups {
		return out, nil
	}
	i := reg.firstGroup + k
	b, err := r.readGroup(dec, i)
	if err != nil || b == nil {
		return out, err
	}
	return out, r.decodeData(i, b, start, out)
}

// wiiGroupData rebuilds 2 MiB group w of a partition region, encrypted.
func (r *Reader) wiiGroupData(dec *zstd.Decoder, reg *region, w int) ([]byte, error) {
	totalBlocks := int(reg.size / wiiBlock)
	first := w * blocksInGroup
	nb := min(blocksInGroup, totalBlocks-first)
	bpc := int(r.chunk / wiiBlock)
	data := make([]byte, blocksInGroup*wiiData)
	type exc struct {
		block, off int
		hash       []byte
	}
	var excs []exc
	for c := 0; c*bpc < nb; c++ {
		k := (first + c*bpc) / bpc // chunk index in the region
		cb := min(bpc, nb-c*bpc)
		out := data[c*bpc*wiiData : (c*bpc+cb)*wiiData]
		if k >= reg.groups {
			continue
		}
		i := reg.firstGroup + k
		b, err := r.readGroup(dec, i)
		if err != nil {
			return nil, err
		}
		if b == nil {
			continue // zeros, no exceptions
		}
		if len(b) < 2 {
			return nil, fmt.Errorf("chunk %d: truncated", i)
		}
		n := int(binary.BigEndian.Uint16(b))
		p := 2
		if len(b) < p+n*22 {
			return nil, fmt.Errorf("chunk %d: truncated", i)
		}
		for e := range n {
			o := int(binary.BigEndian.Uint16(b[p+e*22:]))
			blk, in := c*bpc+o/wiiHashSize, o%wiiHashSize
			if in+20 > wiiHashSize || blk >= blocksInGroup {
				return nil, fmt.Errorf("chunk %d: invalid hash exception", i)
			}
			excs = append(excs, exc{blk, in, b[p+e*22+2 : p+e*22+22]})
		}
		p += n * 22
		if !r.groups[i].compressed {
			p = (p + 3) &^ 3
		}
		if p > len(b) {
			return nil, fmt.Errorf("chunk %d: truncated", i)
		}
		decOff := reg.decBase + int64(first+c*bpc)*wiiData
		if err := r.decodeData(i, b[p:], decOff, out); err != nil {
			return nil, err
		}
	}
	hashes := hashGroup(data)
	for _, e := range excs {
		copy(hashes[e.block][e.off:], e.hash)
	}
	out := make([]byte, nb*wiiBlock)
	for b := range nb {
		encryptBlock(reg.part.key[:], hashes[b][:], data[b*wiiData:(b+1)*wiiData], out[b*wiiBlock:(b+1)*wiiBlock])
	}
	return out, nil
}

// decode writes the disc image to w, decoding in parallel, and returns its
// SHA-1.
func decode(ctx context.Context, path string, w io.Writer, progress func(done, total int64)) ([sha1.Size]byte, error) {
	var sum [sha1.Size]byte
	r, err := Open(path)
	if err != nil {
		return sum, err
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	type job struct {
		reg   *region
		index int   // chunk (raw) or 2 MiB group (partition)
		off   int64 // disc offset
		res   chan result
	}
	workers := runtime.NumCPU()
	jobs := make(chan job, workers)
	order := make(chan job, workers*4)
	for range workers {
		go func() {
			dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
			if err == nil {
				defer dec.Close()
			}
			for j := range jobs {
				if err != nil {
					j.res <- result{err: err}
					continue
				}
				var d []byte
				var e error
				if j.reg.part == nil {
					d, e = r.rawChunk(dec, j.reg, j.index)
				} else {
					d, e = r.wiiGroupData(dec, j.reg, j.index)
				}
				j.res <- result{d, e}
			}
		}()
	}
	go func() {
		defer close(jobs)
		defer close(order)
		for _, reg := range r.regions {
			step := r.chunk
			if reg.part != nil {
				step = wiiGroup
			}
			for k, off := 0, reg.start; off < reg.start+reg.size; k, off = k+1, off+step {
				if ctx.Err() != nil {
					return
				}
				j := job{reg, k, off, make(chan result, 1)}
				order <- j
				jobs <- j
			}
		}
	}()
	h := sha1.New()
	var pos int64 // bytes of the disc written
	var firstErr error
	put := func(b []byte) {
		if firstErr != nil {
			return
		}
		h.Write(b)
		if _, err := w.Write(b); err != nil {
			firstErr = err
			cancel()
		}
		pos += int64(len(b))
	}
	for j := range order {
		v := <-j.res
		if firstErr != nil {
			continue
		}
		if v.err != nil {
			firstErr = v.err
			cancel()
			continue
		}
		data, off := v.data, j.off
		if off < 0x80 { // the first 0x80 bytes come from the header
			copy(data, r.firstData[off:])
		}
		for pos < off && firstErr == nil { // a gap: zeros
			put(make([]byte, min(off-pos, 1<<20)))
		}
		if skip := pos - off; skip < int64(len(data)) {
			end := min(int64(len(data)), r.ISOSize-off)
			if skip < end {
				put(data[skip:end])
			}
		}
		if progress != nil {
			progress(pos, r.ISOSize)
		}
	}
	for pos < r.ISOSize && firstErr == nil {
		put(make([]byte, min(r.ISOSize-pos, 1<<20)))
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	if firstErr != nil {
		return sum, firstErr
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// Hash returns the SHA-1 of the disc image stored in an RVZ.
func Hash(ctx context.Context, path string, progress func(done, total int64)) ([sha1.Size]byte, error) {
	return decode(ctx, path, io.Discard, progress)
}

// Decompress writes the disc image of an RVZ to out, via out.part.
func Decompress(ctx context.Context, in, out string, progress func(done, total int64)) (*Result, error) {
	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	bw := newBufWriter(f)
	sum, err := decode(ctx, in, bw, progress)
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
	res := &Result{SHA1: sum}
	if st, err := os.Stat(in); err == nil {
		res.InputSize = st.Size()
	}
	if st, err := os.Stat(out); err == nil {
		res.OutputSize = st.Size()
	}
	return res, nil
}
