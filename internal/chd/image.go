package chd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Image is a file of a disc image: read as it is, or decoded from ECM
// (Neill Corlett's Error Code Modeler, which drops the sync, EDC and ECC
// fields of CD sectors where they can be computed again).
type Image struct {
	f    *os.File
	size int64
	ecm  *ecmIndex
}

// OpenImage opens a file of a disc image. A name ending in .ecm is
// decoded; so is name.ecm when the file itself is missing, since a CUE
// sheet names game.bin when only game.bin.ecm is there.
func OpenImage(path string) (*Image, error) {
	if !isECMName(path) {
		if _, err := os.Stat(path); err != nil {
			if _, eerr := os.Stat(path + ".ecm"); eerr == nil {
				path += ".ecm"
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	img := &Image{f: f, size: st.Size()}
	if isECMName(path) {
		if img.ecm, err = indexECM(f, st.Size()); err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		img.size = img.ecm.size
	}
	return img, nil
}

func isECMName(path string) bool { return strings.EqualFold(filepath.Ext(path), ".ecm") }

// ImageSize is the size of a file of a disc image, decoded if it is ECM.
func ImageSize(path string) (int64, error) {
	img, err := OpenImage(path)
	if err != nil {
		return 0, err
	}
	defer img.Close()
	return img.Size(), nil
}

// ImageFileSize is the size on disk of a file of a disc image (of
// name.ecm when that is the one there).
func ImageFileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil && !isECMName(path) {
		st, err = os.Stat(path + ".ecm")
	}
	if err != nil {
		return 0
	}
	return st.Size()
}

// Size is the size of the image's data.
func (img *Image) Size() int64 { return img.size }

// Close closes the file.
func (img *Image) Close() error { return img.f.Close() }

// ReadAt reads the image's data.
func (img *Image) ReadAt(p []byte, off int64) (int, error) {
	if img.ecm == nil {
		return img.f.ReadAt(p, off)
	}
	return img.ecm.readAt(img.f, p, off)
}

// CheckECM tells, for an ECM file read whole from the start, whether the
// data matches the checksum ECM stored for it. It returns nil for other
// files and for ECM files not read that way.
func (img *Image) CheckECM() error {
	e := img.ecm
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.seqPos != e.size {
		return nil
	}
	if e.seqEDC != e.want {
		return errors.New("ECM: the decoded data does not match the file's checksum")
	}
	return nil
}

// ECM records: literal bytes, or CD sectors of which only some bytes are
// stored.
const (
	ecmBytes = iota
	ecmMode1
	ecmMode2Form1
	ecmMode2Form2
)

// ecmStored and ecmOut are the bytes per unit of each record type, stored
// in the file and produced.
var (
	ecmStored = [4]int64{1, 0x803, 0x804, 0x918}
	ecmOut    = [4]int64{1, 0x930, 0x920, 0x920}
)

type ecmRecord struct {
	out, in int64 // where the record's data starts, decoded and in the file
	count   int64 // units
	typ     int
}

// ecmIndex is the list of an ECM file's records, so that any part of the
// data can be decoded without decoding all that comes before.
type ecmIndex struct {
	records []ecmRecord
	size    int64  // of the decoded data
	want    uint32 // EDC of the decoded data, stored at the end

	mu     sync.Mutex
	seqPos int64 // decoded data read in order from the start
	seqEDC uint32
	sector [SectorSize]byte // the last sector decoded
	secAt  int64            // its position in the decoded data, -1 for none
}

// indexECM reads the records of an ECM file.
func indexECM(f *os.File, fileSize int64) (*ecmIndex, error) {
	r := &cursor{f: f, end: fileSize}
	magic := make([]byte, 4)
	if err := r.read(magic); err != nil || string(magic) != "ECM\x00" {
		return nil, errors.New("not an ECM file")
	}
	e := &ecmIndex{secAt: -1}
	var out int64
	for {
		c, err := r.byte()
		if err != nil {
			return nil, errors.New("ECM: the file ends before its end mark")
		}
		typ := int(c & 3)
		n := uint64(c>>2) & 0x1f
		for bits := 5; c&0x80 != 0; bits += 7 {
			if bits > 31 {
				return nil, errors.New("ECM: invalid record length")
			}
			if c, err = r.byte(); err != nil {
				return nil, errors.New("ECM: the file ends inside a record")
			}
			n |= uint64(c&0x7f) << bits
		}
		if n == 0xffffffff {
			break
		}
		count := int64(n) + 1
		rec := ecmRecord{out: out, in: r.pos, count: count, typ: typ}
		if r.pos+count*ecmStored[typ] > fileSize {
			return nil, errors.New("ECM: the file ends inside a record")
		}
		e.records = append(e.records, rec)
		r.skip(count * ecmStored[typ])
		out += count * ecmOut[typ]
	}
	sum := make([]byte, 4)
	if err := r.read(sum); err != nil {
		return nil, errors.New("ECM: the file ends before its checksum")
	}
	e.want = binary.LittleEndian.Uint32(sum)
	e.size = out
	return e, nil
}

// readAt decodes data from off.
func (e *ecmIndex) readAt(f *os.File, p []byte, off int64) (int, error) {
	if off >= e.size {
		return 0, io.EOF
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for n < len(p) && off+int64(n) < e.size {
		pos := off + int64(n)
		i := sort.Search(len(e.records), func(i int) bool {
			r := e.records[i]
			return r.out+r.count*ecmOut[r.typ] > pos
		})
		rec := e.records[i]
		rel := pos - rec.out
		if rec.typ == ecmBytes {
			k := min(int64(len(p)-n), rec.count-rel)
			if _, err := f.ReadAt(p[n:n+int(k)], rec.in+rel); err != nil {
				return n, err
			}
			n += int(k)
			continue
		}
		unit := rel / ecmOut[rec.typ]
		start := rec.out + unit*ecmOut[rec.typ]
		if e.secAt != start {
			stored := make([]byte, ecmStored[rec.typ])
			if _, err := f.ReadAt(stored, rec.in+unit*ecmStored[rec.typ]); err != nil {
				return n, err
			}
			decodeECMSector(e.sector[:], rec.typ, stored)
			e.secAt = start
		}
		data := e.sector[:]
		if rec.typ != ecmMode1 {
			data = data[0x10:] // Mode 2 records leave out the sync and header
		}
		n += copy(p[n:], data[rel-unit*ecmOut[rec.typ]:])
	}
	if off == e.seqPos {
		e.seqEDC = edcUpdate(e.seqEDC, p[:n])
		e.seqPos += int64(n)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// decodeECMSector rebuilds a raw sector from the bytes an ECM record
// stores for it.
func decodeECMSector(s []byte, typ int, stored []byte) {
	clear(s)
	switch typ {
	case ecmMode1:
		copy(s, syncHeader[:])
		copy(s[0x0c:0x0f], stored[:3])
		s[0x0f] = 1
		copy(s[0x10:0x810], stored[3:])
		binary.LittleEndian.PutUint32(s[0x810:], edcUpdate(0, s[:0x810]))
		eccGenerate(s)
	case ecmMode2Form1:
		s[0x0f] = 2 // the ECC counts the header as zero; only bytes from 0x10 are used
		copy(s[0x14:0x818], stored)
		copy(s[0x10:0x14], s[0x14:0x18])
		binary.LittleEndian.PutUint32(s[0x818:], edcUpdate(0, s[0x10:0x818]))
		eccGenerate(s)
	case ecmMode2Form2:
		copy(s[0x14:0x92c], stored)
		copy(s[0x10:0x14], s[0x14:0x18])
		binary.LittleEndian.PutUint32(s[0x92c:], edcUpdate(0, s[0x10:0x92c]))
	}
}

// edcTable is the CD-ROM EDC, a CRC-32 with the polynomial
// x^32+x^31+x^16+x^15+x^4+x^3+x+1 (reversed: 0xd8018001), without the
// inversions of the usual CRC-32.
var edcTable = crc32.MakeTable(0xd8018001)

func edcUpdate(edc uint32, p []byte) uint32 { return ^crc32.Update(^edc, edcTable, p) }

// cursor reads a file in order through a buffer, and skips parts of it
// without reading them.
type cursor struct {
	f        *os.File
	pos, end int64
	buf      []byte
	bufAt    int64
}

func (c *cursor) fill(n int) error {
	if c.pos >= c.bufAt && c.pos+int64(n) <= c.bufAt+int64(len(c.buf)) {
		return nil
	}
	size := max(64<<10, n)
	if c.buf == nil || cap(c.buf) < size {
		c.buf = make([]byte, size)
	}
	c.buf = c.buf[:min(int64(size), c.end-c.pos)]
	if len(c.buf) < n {
		return io.ErrUnexpectedEOF
	}
	if _, err := c.f.ReadAt(c.buf, c.pos); err != nil && err != io.EOF {
		return err
	}
	c.bufAt = c.pos
	return nil
}

func (c *cursor) byte() (byte, error) {
	if err := c.fill(1); err != nil {
		return 0, err
	}
	b := c.buf[c.pos-c.bufAt]
	c.pos++
	return b, nil
}

func (c *cursor) read(p []byte) error {
	if err := c.fill(len(p)); err != nil {
		return err
	}
	copy(p, c.buf[c.pos-c.bufAt:])
	c.pos += int64(len(p))
	return nil
}

func (c *cursor) skip(n int64) { c.pos += n }

// ImageExts are the extensions of the disc images whose tracks ParseImage
// reads (an .ecm file is a .bin, .img, .nrg or .cdi compressed with ECM).
var ImageExts = []string{".cue", ".gdi", ".nrg", ".cdi", ".ecm"}

// ParseImage reads the tracks of a CUE sheet, a GDI file, an NRG or CDI
// image, or a raw CD image compressed with ECM (game.bin.ecm without a
// CUE sheet, read as one data track).
func ParseImage(path string) ([]*Track, error) {
	inner := strings.ToLower(path)
	if isECMName(inner) {
		inner = strings.TrimSuffix(inner, filepath.Ext(inner))
	}
	switch filepath.Ext(inner) {
	case ".cue":
		return ParseCue(path)
	case ".gdi":
		return ParseGDI(path)
	case ".nrg":
		return ParseNRG(path)
	case ".cdi":
		return ParseCDI(path)
	}
	if isECMName(path) {
		return parseRawImage(path)
	}
	return nil, fmt.Errorf("%s: not a CD image", filepath.Base(path))
}

// parseRawImage reads a raw CD image without a CUE sheet as one data
// track: of 2352-byte sectors if it starts with a sector's sync pattern,
// else of 2048-byte sectors.
func parseRawImage(path string) ([]*Track, error) {
	img, err := OpenImage(path)
	if err != nil {
		return nil, err
	}
	defer img.Close()
	head := make([]byte, 16)
	if _, err := img.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	t := &Track{Number: 1, File: path}
	switch {
	case [12]byte(head[:12]) == syncHeader && img.Size()%SectorSize == 0:
		t.Type, t.DataSize = "MODE1_RAW", SectorSize
		if head[15] == 2 {
			t.Type = "MODE2_RAW"
		}
	case img.Size()%DVDSectorSize == 0:
		t.Type, t.DataSize = "MODE1", DVDSectorSize
	default:
		return nil, fmt.Errorf("%s: not a CD image (no sector sync, size not a multiple of 2048)", filepath.Base(path))
	}
	t.Frames = int(img.Size() / int64(t.DataSize))
	return []*Track{t}, nil
}

// ConvertImage writes the disc of a CUE sheet, GDI file, NRG or CDI image
// or ECM file as a CD CHD.
func ConvertImage(ctx context.Context, path, outPath string, opt Options) (*Result, error) {
	tracks, err := ParseImage(path)
	if err != nil {
		return nil, err
	}
	return convertTracks(ctx, tracks, outPath, opt)
}
