package sevenzip

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"unicode/utf16"

	"github.com/ulikunitz/xz/lzma"
)

var errFormat = errors.New("7z: invalid archive")

// folder is a group of files compressed together in one pack stream.
type folder struct {
	codec    []byte
	prop     []byte
	unpack   int64
	crc      uint32
	hasCRC   bool
	sizes    []int64 // substreams
	crcs     []uint32
	crcKnown []bool
}

type archive struct {
	packPos   int64
	packSizes []int64
	folders   []folder
	entries   []Entry
	empty     []bool // per entry: no data stream
}

// Check reads the archive at path, decompresses every file and checks its
// CRC. It returns the files in archive order. progress reports the bytes
// decompressed out of the total.
func Check(ctx context.Context, path string, progress func(done, total int64)) ([]Entry, error) {
	return Extract(ctx, path, nil, progress)
}

// Extract is Check that also writes the files to the writers want returns
// for them (want gets each file's name and the size the header records;
// a nil writer skips the file).
func Extract(ctx context.Context, path string, want func(name string, size int64) io.Writer, progress func(done, total int64)) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := readHeader(f)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, fo := range a.folders {
		total += fo.unpack
	}

	// Data streams in folder order, matched to the non-empty entries.
	var streams []*Entry
	for i := range a.entries {
		if !a.empty[i] {
			streams = append(streams, &a.entries[i])
		}
	}
	si := 0
	var done int64
	offset := int64(sigHeaderSize) + a.packPos
	buf := make([]byte, 1<<20)
	for fi, fo := range a.folders {
		if fi >= len(a.packSizes) {
			return nil, errFormat
		}
		src := io.NewSectionReader(f, offset, a.packSizes[fi])
		offset += a.packSizes[fi]
		r, err := decoder(src, fo)
		if err != nil {
			return nil, err
		}
		folderCRC := crc32.NewIEEE()
		for k, size := range fo.sizes {
			if si >= len(streams) {
				return nil, errFormat
			}
			e := streams[si]
			si++
			h := crc32.NewIEEE()
			var out io.Writer
			if want != nil {
				out = want(e.Name, size)
			}
			for left := size; left > 0; {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				n, err := io.ReadFull(r, buf[:min(left, int64(len(buf)))])
				h.Write(buf[:n])
				folderCRC.Write(buf[:n])
				if out != nil {
					if _, err := out.Write(buf[:n]); err != nil {
						return nil, err
					}
				}
				left -= int64(n)
				done += int64(n)
				if progress != nil {
					progress(done, total)
				}
				if err != nil {
					return nil, fmt.Errorf("%s: %w", e.Name, err)
				}
			}
			e.Size = size
			e.CRC = h.Sum32()
			if fo.crcKnown[k] && fo.crcs[k] != e.CRC {
				return nil, fmt.Errorf("%s: CRC mismatch", e.Name)
			}
		}
		if fo.hasCRC && folderCRC.Sum32() != fo.crc {
			return nil, errors.New("7z: folder CRC mismatch")
		}
		// The stream must end where the folder ends.
		if n, _ := r.Read(buf[:1]); n != 0 {
			return nil, errors.New("7z: data after the last file")
		}
	}
	if si != len(streams) {
		return nil, errFormat
	}
	return a.entries, nil
}

// decoder decompresses the pack stream of a folder.
func decoder(src io.Reader, fo folder) (io.Reader, error) {
	switch {
	case bytes.Equal(fo.codec, codecLZMA2):
		if len(fo.prop) != 1 {
			return nil, errFormat
		}
		d := min(max(dictSize(fo.prop[0]), lzma.MinDictCap), lzma.MaxDictCap)
		return lzma.Reader2Config{DictCap: int(d)}.NewReader2(bufio.NewReaderSize(src, 1<<16))
	case bytes.Equal(fo.codec, codecLZMA):
		// The decoder wants the .lzma header: properties, then size.
		if len(fo.prop) != 5 {
			return nil, errFormat
		}
		hdr := binary.LittleEndian.AppendUint64(append([]byte(nil), fo.prop...), uint64(fo.unpack))
		return lzma.NewReader(io.MultiReader(bytes.NewReader(hdr), bufio.NewReaderSize(src, 1<<16)))
	case bytes.Equal(fo.codec, codecCopy):
		return src, nil
	}
	return nil, fmt.Errorf("%w: codec %X", ErrUnsupported, fo.codec)
}

// List reads the header of the archive at path and returns its files with
// the sizes and CRCs it records, without decompressing them (a file whose
// CRC the header does not give has CRC 0).
func List(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := readHeader(f)
	if err != nil {
		return nil, err
	}
	entries := a.entries
	// Data streams in folder order, matched to the non-empty entries.
	si := 0
	for _, fo := range a.folders {
		for j, size := range fo.sizes {
			for si < len(entries) && a.empty[si] {
				si++
			}
			if si >= len(entries) {
				return nil, errFormat
			}
			entries[si].Size = size
			if fo.crcKnown[j] {
				entries[si].CRC = fo.crcs[j]
			}
			si++
		}
	}
	return entries, nil
}

func readHeader(f *os.File) (*archive, error) {
	var sig [sigHeaderSize]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil {
		return nil, errFormat
	}
	if !bytes.Equal(sig[:6], signature) || sig[6] != 0 {
		return nil, errFormat
	}
	if crc32.ChecksumIEEE(sig[12:]) != binary.LittleEndian.Uint32(sig[8:]) {
		return nil, errors.New("7z: start header CRC mismatch")
	}
	off := binary.LittleEndian.Uint64(sig[12:])
	size := binary.LittleEndian.Uint64(sig[20:])
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if size == 0 || size > 64<<20 || off > uint64(st.Size()) || sigHeaderSize+off+size > uint64(st.Size()) {
		return nil, errFormat
	}
	hdr := make([]byte, size)
	if _, err := f.ReadAt(hdr, int64(sigHeaderSize+off)); err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(hdr) != binary.LittleEndian.Uint32(sig[28:]) {
		return nil, errors.New("7z: header CRC mismatch")
	}
	// 7-Zip compresses the header itself: the encoded header tells where
	// the compressed one is and how to unpack it.
	for range 4 {
		if len(hdr) == 0 || hdr[0] != idEncodedHeader {
			break
		}
		if hdr, err = unpackHeader(f, hdr[1:]); err != nil {
			return nil, err
		}
	}
	p := &parser{b: hdr}
	a, err := p.header()
	if err == nil && p.err != nil {
		err = p.err
	}
	return a, err
}

// unpackHeader decompresses a header from the streams info of an encoded
// header.
func unpackHeader(f *os.File, info []byte) ([]byte, error) {
	p := &parser{b: info}
	a := &archive{}
	if err := p.streams(a); err != nil {
		return nil, err
	}
	if p.err != nil {
		return nil, p.err
	}
	if len(a.folders) != 1 || len(a.packSizes) != 1 || a.folders[0].unpack > 64<<20 {
		return nil, fmt.Errorf("%w: header streams", ErrUnsupported)
	}
	fo := a.folders[0]
	src := io.NewSectionReader(f, int64(sigHeaderSize)+a.packPos, a.packSizes[0])
	r, err := decoder(src, fo)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, fo.unpack)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, fmt.Errorf("7z header: %w", err)
	}
	if fo.hasCRC && crc32.ChecksumIEEE(hdr) != fo.crc {
		return nil, errors.New("7z: header CRC mismatch")
	}
	return hdr, nil
}

// parser reads header fields; the first error sticks and later reads
// return zeros.
type parser struct {
	b   []byte
	err error
}

func (p *parser) byte() byte {
	if p.err != nil || len(p.b) == 0 {
		p.err = errFormat
		return 0
	}
	v := p.b[0]
	p.b = p.b[1:]
	return v
}

func (p *parser) take(n uint64) []byte {
	if p.err != nil || n > uint64(len(p.b)) {
		p.err = errFormat
		return nil
	}
	v := p.b[:n]
	p.b = p.b[n:]
	return v
}

func (p *parser) num() uint64 {
	first := p.byte()
	mask := byte(0x80)
	var v uint64
	for i := 0; i < 8; i++ {
		if first&mask == 0 {
			return v | uint64(first&(mask-1))<<(8*i)
		}
		v |= uint64(p.byte()) << (8 * i)
		mask >>= 1
	}
	return v
}

// count reads a number of items, refusing absurd values: more than the
// rest of the header could describe (one bit each, as in a bit vector)
// would only size allocations from a damaged header.
func (p *parser) count() int {
	v := p.num()
	if v > 1<<24 || v > uint64(len(p.b))*8+8 {
		p.err = errFormat
		return 0
	}
	return int(v)
}

func (p *parser) u32() uint32 {
	b := p.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (p *parser) bits(n int) []bool {
	b := p.take(uint64((n + 7) / 8))
	v := make([]bool, n)
	for i := range v {
		if b != nil {
			v[i] = b[i/8]&(0x80>>(i%8)) != 0
		}
	}
	return v
}

// defined reads an "all defined" byte, or a bit vector when it is 0.
func (p *parser) defined(n int) []bool {
	if p.byte() == 0 {
		return p.bits(n)
	}
	v := make([]bool, n)
	for i := range v {
		v[i] = true
	}
	return v
}

func (p *parser) header() (*archive, error) {
	switch p.byte() {
	case idHeader:
	default:
		return nil, errFormat
	}
	a := &archive{}
	for p.err == nil {
		switch id := p.byte(); id {
		case idEnd:
			if len(a.entries) == 0 && len(a.folders) > 0 {
				return nil, errFormat
			}
			return a, nil
		case idMainStreamsInfo:
			if err := p.streams(a); err != nil {
				return nil, err
			}
		case idFilesInfo:
			if err := p.files(a); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: header property %#x", ErrUnsupported, id)
		}
	}
	return nil, p.err
}

func (p *parser) streams(a *archive) error {
	for p.err == nil {
		switch id := p.byte(); id {
		case idEnd:
			return nil
		case idPackInfo:
			a.packPos = int64(p.num())
			n := p.count()
			for p.err == nil {
				switch p.byte() {
				case idSize:
					a.packSizes = make([]int64, n)
					for i := range a.packSizes {
						a.packSizes[i] = int64(p.num())
					}
					continue
				case idCRC:
					def := p.defined(n)
					for _, d := range def {
						if d {
							p.u32()
						}
					}
					continue
				case idEnd:
				default:
					p.err = errFormat
				}
				break
			}
		case idUnpackInfo:
			if err := p.unpackInfo(a); err != nil {
				return err
			}
		case idSubStreamsInfo:
			p.subStreams(a)
		default:
			return fmt.Errorf("%w: streams property %#x", ErrUnsupported, id)
		}
	}
	return p.err
}

func (p *parser) unpackInfo(a *archive) error {
	if p.byte() != idFolder {
		return errFormat
	}
	n := p.count()
	if p.byte() != 0 {
		return fmt.Errorf("%w: external folders", ErrUnsupported)
	}
	a.folders = make([]folder, n)
	for i := range a.folders {
		if p.num() != 1 {
			return fmt.Errorf("%w: several coders", ErrUnsupported)
		}
		flags := p.byte()
		a.folders[i].codec = p.take(uint64(flags & 0x0F))
		if flags&0x10 != 0 {
			if p.num() != 1 || p.num() != 1 {
				return fmt.Errorf("%w: complex coder", ErrUnsupported)
			}
		}
		if flags&0x20 != 0 {
			a.folders[i].prop = p.take(p.num())
		}
		if flags&0x80 != 0 {
			return fmt.Errorf("%w: alternative coders", ErrUnsupported)
		}
	}
	if p.byte() != idCodersUnpack {
		return errFormat
	}
	for i := range a.folders {
		a.folders[i].unpack = int64(p.num())
		a.folders[i].sizes = []int64{a.folders[i].unpack}
	}
	for p.err == nil {
		switch p.byte() {
		case idEnd:
			for i := range a.folders {
				fo := &a.folders[i]
				fo.crcs = []uint32{fo.crc}
				fo.crcKnown = []bool{fo.hasCRC}
			}
			return nil
		case idCRC:
			def := p.defined(n)
			for i, d := range def {
				if d {
					a.folders[i].crc, a.folders[i].hasCRC = p.u32(), true
				}
			}
		default:
			return errFormat
		}
	}
	return p.err
}

func (p *parser) subStreams(a *archive) {
	counts := make([]int, len(a.folders))
	for i := range counts {
		counts[i] = 1
	}
	id := p.byte()
	if id == idNumUnpackStream {
		for i := range counts {
			counts[i] = p.count()
		}
		id = p.byte()
	}
	for i := range a.folders {
		fo := &a.folders[i]
		fo.sizes = make([]int64, counts[i])
		fo.crcs = make([]uint32, counts[i])
		fo.crcKnown = make([]bool, counts[i])
		if counts[i] == 1 {
			fo.sizes[0] = fo.unpack
			fo.crcs[0], fo.crcKnown[0] = fo.crc, fo.hasCRC
		}
	}
	if id == idSize {
		for i := range a.folders {
			fo := &a.folders[i]
			if counts[i] == 0 {
				continue
			}
			var sum int64
			for k := 0; k < counts[i]-1; k++ {
				fo.sizes[k] = int64(p.num())
				sum += fo.sizes[k]
			}
			if sum > fo.unpack {
				p.err = errFormat
				return
			}
			fo.sizes[counts[i]-1] = fo.unpack - sum
		}
		id = p.byte()
	}
	for p.err == nil && id != idEnd {
		if id != idCRC {
			p.err = errFormat
			return
		}
		// Digests of the substreams whose CRC the folder does not give.
		var need int
		for i, fo := range a.folders {
			if !(counts[i] == 1 && fo.hasCRC) {
				need += counts[i]
			}
		}
		def := p.defined(need)
		k := 0
		for i := range a.folders {
			fo := &a.folders[i]
			if counts[i] == 1 && fo.hasCRC {
				continue
			}
			for j := 0; j < counts[i]; j++ {
				if def[k] {
					fo.crcs[j], fo.crcKnown[j] = p.u32(), true
				}
				k++
			}
		}
		id = p.byte()
	}
}

func (p *parser) files(a *archive) error {
	n := p.count()
	a.entries = make([]Entry, n)
	a.empty = make([]bool, n)
	var emptyFile []bool
	for p.err == nil {
		id := p.byte()
		if id == idEnd {
			break
		}
		data := p.take(p.num())
		q := &parser{b: data}
		switch id {
		case idEmptyStream:
			a.empty = q.bits(n)
		case idEmptyFile:
			emptyFile = q.bits(countTrue(a.empty))
		case idName:
			if q.byte() != 0 {
				return fmt.Errorf("%w: external names", ErrUnsupported)
			}
			for i := range a.entries {
				var name []uint16
				for {
					b := q.take(2)
					if b == nil {
						return errFormat
					}
					c := binary.LittleEndian.Uint16(b)
					if c == 0 {
						break
					}
					name = append(name, c)
				}
				a.entries[i].Name = string(utf16.Decode(name))
			}
		case idMTime:
			def := q.defined(n)
			if q.byte() != 0 {
				return fmt.Errorf("%w: external times", ErrUnsupported)
			}
			for i, d := range def {
				if d {
					b := q.take(8)
					if b != nil {
						a.entries[i].Modified = fromFiletime(binary.LittleEndian.Uint64(b))
					}
				}
			}
		}
		if q.err != nil {
			return q.err
		}
	}
	k := 0
	for i := range a.entries {
		if a.empty[i] {
			a.entries[i].Dir = k >= len(emptyFile) || !emptyFile[k]
			k++
		}
	}
	return p.err
}

func countTrue(v []bool) int {
	n := 0
	for _, b := range v {
		if b {
			n++
		}
	}
	return n
}
