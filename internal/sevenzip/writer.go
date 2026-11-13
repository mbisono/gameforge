package sevenzip

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"time"
	"unicode/utf16"

	"github.com/itchio/lzma"
)

// Source is a file to put in an archive.
type Source struct {
	Name     string // path inside the archive, with "/" between folders
	Size     int64
	Modified time.Time
	Open     func() (io.ReadCloser, error)
}

// Result describes a written archive.
type Result struct {
	InputSize  int64
	OutputSize int64
	Entries    []Entry // the files as stored, with the CRC of what was read
}

// Level is the LZMA level used (the LZMA SDK's levels; 5 is 7-Zip's
// "Normal"). Higher levels only enlarge the dictionary beyond the size of
// most game files and gain almost nothing.
const Level = lzma.DefaultCompression

// Write creates the archive out with the sources, in order, in one solid
// LZMA stream. progress reports the bytes read so far out of the total.
func Write(ctx context.Context, out string, sources []Source, progress func(done, total int64)) (Result, error) {
	var res Result
	var total int64
	for _, s := range sources {
		total += s.Size
	}
	f, err := os.Create(out)
	if err != nil {
		return res, err
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<20)
	if _, err := bw.Write(make([]byte, sigHeaderSize)); err != nil {
		return res, err
	}

	pack := &countWriter{w: bw}
	strip := &headerStrip{w: pack}
	var lz io.WriteCloser
	if total > 0 {
		// The encoder writes the .lzma header (properties and size) first;
		// in 7z the properties go to the coder and the size is the folder's.
		lz = lzma.NewWriterSizeLevel(strip, total, Level)
		defer func() {
			if lz != nil {
				lz.Close() // stops the encoder's goroutine after an error
			}
		}()
	}

	var done int64
	buf := make([]byte, 1<<20)
	for _, s := range sources {
		e := Entry{Name: s.Name, Modified: s.Modified}
		if s.Size > 0 {
			rc, err := s.Open()
			if err != nil {
				return res, err
			}
			h := crc32.NewIEEE()
			for {
				if err := ctx.Err(); err != nil {
					rc.Close()
					return res, err
				}
				n, rerr := rc.Read(buf)
				if n > 0 {
					h.Write(buf[:n])
					if _, err := lz.Write(buf[:n]); err != nil {
						rc.Close()
						return res, err
					}
					e.Size += int64(n)
					done += int64(n)
					if progress != nil {
						progress(done, total)
					}
				}
				if rerr == io.EOF {
					break
				}
				if rerr != nil {
					rc.Close()
					return res, rerr
				}
			}
			rc.Close()
			if e.Size != s.Size {
				return res, io.ErrUnexpectedEOF
			}
			e.CRC = h.Sum32()
		}
		res.Entries = append(res.Entries, e)
	}
	if lz != nil {
		err := lz.Close()
		lz = nil
		if err != nil {
			return res, err
		}
		if strip.n < lzmaHeaderSize {
			return res, io.ErrShortWrite
		}
	}

	hdr := header(res.Entries, pack.n, strip.props[:])
	if _, err := bw.Write(hdr); err != nil {
		return res, err
	}
	if err := bw.Flush(); err != nil {
		return res, err
	}
	var sig [sigHeaderSize]byte
	copy(sig[:], signature)
	sig[6], sig[7] = 0, 4
	binary.LittleEndian.PutUint64(sig[12:], uint64(pack.n))
	binary.LittleEndian.PutUint64(sig[20:], uint64(len(hdr)))
	binary.LittleEndian.PutUint32(sig[28:], crc32.ChecksumIEEE(hdr))
	binary.LittleEndian.PutUint32(sig[8:], crc32.ChecksumIEEE(sig[12:]))
	if _, err := f.WriteAt(sig[:], 0); err != nil {
		return res, err
	}
	if err := f.Close(); err != nil {
		return res, err
	}
	res.InputSize = total
	res.OutputSize = sigHeaderSize + pack.n + int64(len(hdr))
	return res, nil
}

// header builds the archive header: one folder (an LZMA coder) holding
// the non-empty files as substreams, then the file names and times.
func header(entries []Entry, packSize int64, props []byte) []byte {
	var b hbuf
	b.byte(idHeader)
	var sizes []int64
	var crcs []uint32
	var unpack int64
	for _, e := range entries {
		if e.Size > 0 {
			sizes = append(sizes, e.Size)
			crcs = append(crcs, e.CRC)
			unpack += e.Size
		}
	}
	if len(sizes) > 0 {
		b.byte(idMainStreamsInfo)

		b.byte(idPackInfo)
		b.num(0) // pack position
		b.num(1) // pack streams
		b.byte(idSize)
		b.num(uint64(packSize))
		b.byte(idEnd)

		b.byte(idUnpackInfo)
		b.byte(idFolder)
		b.num(1)  // folders
		b.byte(0) // not external
		b.num(1)  // coders
		b.byte(byte(len(codecLZMA)) | 0x20)
		b.Write(codecLZMA)
		b.num(uint64(len(props)))
		b.Write(props)
		b.byte(idCodersUnpack)
		b.num(uint64(unpack))
		b.byte(idEnd)

		b.byte(idSubStreamsInfo)
		if len(sizes) != 1 {
			b.byte(idNumUnpackStream)
			b.num(uint64(len(sizes)))
			b.byte(idSize)
			for _, s := range sizes[:len(sizes)-1] {
				b.num(uint64(s))
			}
		}
		b.byte(idCRC)
		b.byte(1) // all defined
		for _, c := range crcs {
			b.u32(c)
		}
		b.byte(idEnd)

		b.byte(idEnd)
	}

	b.byte(idFilesInfo)
	b.num(uint64(len(entries)))
	if len(sizes) < len(entries) {
		empty := make([]bool, len(entries))
		var emptyFile []bool
		for i, e := range entries {
			if e.Size == 0 {
				empty[i] = true
				emptyFile = append(emptyFile, !e.Dir)
			}
		}
		b.prop(idEmptyStream, bits(empty))
		b.prop(idEmptyFile, bits(emptyFile))
	}
	var names hbuf
	names.byte(0) // not external
	for _, e := range entries {
		for _, c := range utf16.Encode([]rune(e.Name)) {
			names.u16(c)
		}
		names.u16(0)
	}
	b.prop(idName, names.Bytes())
	var times hbuf
	times.byte(1) // all defined
	times.byte(0) // not external
	for _, e := range entries {
		times.u64(toFiletime(e.Modified))
	}
	b.prop(idMTime, times.Bytes())
	b.byte(idEnd)

	b.byte(idEnd)
	return b.Bytes()
}

// bits packs a bit vector, most significant bit first.
func bits(v []bool) []byte {
	out := make([]byte, (len(v)+7)/8)
	for i, set := range v {
		if set {
			out[i/8] |= 0x80 >> (i % 8)
		}
	}
	return out
}

// hbuf builds header bytes.
type hbuf struct{ bytes.Buffer }

func (b *hbuf) byte(v byte) { b.WriteByte(v) }

func (b *hbuf) u16(v uint16) { b.Write(binary.LittleEndian.AppendUint16(nil, v)) }

func (b *hbuf) u32(v uint32) { b.Write(binary.LittleEndian.AppendUint32(nil, v)) }

func (b *hbuf) u64(v uint64) { b.Write(binary.LittleEndian.AppendUint64(nil, v)) }

// num writes a 7z number: the high bits of the first byte say how many
// little-endian bytes follow.
func (b *hbuf) num(v uint64) {
	first, mask := byte(0), byte(0x80)
	i := 0
	for ; i < 8; i++ {
		if v < 1<<(7*(i+1)) {
			first |= byte(v >> (8 * i))
			break
		}
		first |= mask
		mask >>= 1
	}
	b.byte(first)
	for ; i > 0; i-- {
		b.byte(byte(v))
		v >>= 8
	}
}

// prop writes a file property: its ID, size and data.
func (b *hbuf) prop(id byte, data []byte) {
	b.byte(id)
	b.num(uint64(len(data)))
	b.Write(data)
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// lzmaHeaderSize is the size of the .lzma header: 5 bytes of properties
// (lc/lp/pb and the dictionary size) and the 8-byte uncompressed size.
const lzmaHeaderSize = 13

// headerStrip keeps the properties of the .lzma header and passes on the
// compressed data after it.
type headerStrip struct {
	w     io.Writer
	props [5]byte
	n     int // header bytes seen
}

func (h *headerStrip) Write(p []byte) (int, error) {
	total := len(p)
	for h.n < lzmaHeaderSize && len(p) > 0 {
		if h.n < len(h.props) {
			h.props[h.n] = p[0]
		}
		h.n++
		p = p[1:]
	}
	if len(p) > 0 {
		if _, err := h.w.Write(p); err != nil {
			return 0, err
		}
	}
	return total, nil
}
