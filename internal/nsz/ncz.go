// Package nsz implements the NCZ format used by NSZ files: an NCA whose
// sections are stored decrypted and zstd-compressed. Installers (DBI,
// Tinfoil, nsz) re-encrypt the data with the keys stored in the NCZ header,
// so the original NCA is restored byte for byte.
//
// The layout follows nsz (github.com/nicoboss/nsz, docs/formats.md):
//
//	[0, 0x4000)   first 0x4000 bytes of the NCA, unchanged (still encrypted)
//	"NCZSECTN"    u64 count, count × {offset, size, cryptoType, pad u64; key[16]; counter[16]}
//	"NCZBLOCK"    version 2, type 1, unused, blockSizeExp u8, blocks u32,
//	              decompressedSize u64, blocks × compressed size u32
//	blocks        one zstd frame per block; stored raw if not smaller
package nsz

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
	"sort"

	"gameforge/internal/i18n"
	"gameforge/internal/nx"
)

// HeaderSize is the part of the NCA that is stored unchanged.
const HeaderSize = 0x4000

const (
	cryptoNone = 1
	cryptoCTR  = 3
	cryptoBKTR = 4
)

// Section is one entry of the NCZ section table. Offsets are absolute
// offsets inside the NCA.
type Section struct {
	Offset, Size int64
	CryptoType   uint64
	Key          [16]byte
	Counter      [16]byte // bytes 8..16 are replaced by the block offset

	block cipher.Block
}

func (s *Section) encrypted() bool {
	return s.CryptoType == cryptoCTR || s.CryptoType == cryptoBKTR
}

func (s *Section) init() error {
	if !s.encrypted() {
		return nil
	}
	b, err := aes.NewCipher(s.Key[:])
	s.block = b
	return err
}

// Sections returns the NCZ section table for an NCA, or ok=false when the
// NCA must be stored uncompressed (sections not contiguous, or an
// encryption type installers cannot restore). key is the decrypted content
// key of the NCA.
func Sections(r io.ReaderAt, ncaOff, ncaSize int64, h *nx.NCAHeader, key []byte) (sections []Section, ok bool, err error) {
	var fs []nx.NCASection
	for _, s := range h.Sections {
		if s.End > s.Start {
			fs = append(fs, s)
		}
	}
	if len(fs) == 0 || len(key) != 16 {
		return nil, false, nil
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Start < fs[j].Start })
	for i := 1; i < len(fs); i++ {
		if fs[i].Start != fs[i-1].End {
			return nil, false, nil
		}
	}
	if fs[len(fs)-1].End != ncaSize {
		return nil, false, nil
	}

	sections, err = cryptoSections(r, ncaOff, fs, key)
	if err != nil || sections == nil {
		return nil, false, err
	}
	return sections, true, nil
}

// CryptoSections returns the decryption layout of every section of an NCA
// (update sections split into their counter subsections), without the
// checks Sections makes for NCZ. Use Crypt to decrypt data with it.
func CryptoSections(r io.ReaderAt, ncaOff int64, h *nx.NCAHeader, key []byte) ([]Section, error) {
	var fs []nx.NCASection
	for _, s := range h.Sections {
		if s.End > s.Start {
			fs = append(fs, s)
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Start < fs[j].Start })
	sections, err := cryptoSections(r, ncaOff, fs, key)
	if err == nil && sections == nil {
		err = i18n.Errorf("err.nca.crypto", 0)
	}
	return sections, err
}

// NewSection returns a plain or AES-CTR section with the given key and
// FsHeader upper IV (bytes 0x140..0x148).
func NewSection(offset, size int64, key []byte, upperIV [8]byte, encrypted bool) (Section, error) {
	s := Section{Offset: offset, Size: size, CryptoType: cryptoNone}
	if encrypted {
		s.CryptoType = cryptoCTR
		copy(s.Key[:], key)
		for i := range 8 {
			s.Counter[i] = upperIV[7-i]
		}
	}
	return s, s.init()
}

// Crypt encrypts or decrypts buf, the NCA bytes at absolute offset abs.
func Crypt(sections []Section, abs int64, buf []byte) { applyCTR(sections, abs, buf) }

// cryptoSections builds the sections for fs (sorted by offset); it returns
// nil, nil for an encryption type that cannot be handled.
func cryptoSections(r io.ReaderAt, ncaOff int64, fs []nx.NCASection, key []byte) ([]Section, error) {
	var sections []Section
	if len(key) != 16 {
		return nil, nil
	}
	for _, s := range fs {
		sec := Section{Offset: s.Start, Size: s.End - s.Start}
		copy(sec.Key[:], key)
		for i := range 8 {
			sec.Counter[i] = s.UpperIV[7-i]
		}
		switch s.EncType {
		case cryptoNone:
			sec.CryptoType = cryptoNone
			sections = append(sections, sec)
		case cryptoCTR:
			sec.CryptoType = cryptoCTR
			sections = append(sections, sec)
		case cryptoBKTR:
			sec.CryptoType = cryptoCTR
			sub, err := bktrSections(r, ncaOff, s, sec)
			if err != nil {
				return nil, err
			}
			sections = append(sections, sub...)
		default:
			return nil, nil
		}
	}
	for i := range sections {
		if err := sections[i].init(); err != nil {
			return nil, err
		}
	}
	return sections, nil
}

// bktrSections splits an update (BKTR) section into its AES-CTR-EX
// subsections, each with its own counter generation, so the data is
// decrypted correctly and compresses well. If the subsection table cannot be
// parsed the whole section is used with the base counter: the result is
// still restored exactly (the same keystream is applied twice), only
// compressed less.
func bktrSections(r io.ReaderAt, ncaOff int64, s nx.NCASection, base Section) ([]Section, error) {
	whole := []Section{base}
	size := s.End - s.Start
	if s.CtrExSize < 2*0x4000 || s.CtrExOffset < 0 || s.CtrExOffset+s.CtrExSize > size || s.CtrExSize > 64<<20 {
		return whole, nil
	}
	table := make([]byte, s.CtrExSize)
	if _, err := r.ReadAt(table, ncaOff+s.Start+s.CtrExOffset); err != nil {
		return nil, i18n.Errorf("err.nca.read_section", err)
	}
	if err := base.init(); err != nil {
		return nil, err
	}
	xorCTR(base.block, base.Counter, s.Start+s.CtrExOffset, table)

	type entry struct {
		off int64
		gen uint32
	}
	var entries []entry
	var end int64
	sets := int(binary.LittleEndian.Uint32(table[4:]))
	if sets < 1 || 0x4000*(1+sets) > len(table) {
		return whole, nil
	}
	for i := range sets {
		set := table[0x4000*(1+i) : 0x4000*(2+i)]
		n := int(binary.LittleEndian.Uint32(set[4:]))
		if n < 1 || 0x10+n*0x10 > len(set) {
			return whole, nil
		}
		end = int64(binary.LittleEndian.Uint64(set[8:]))
		for j := range n {
			e := set[0x10+j*0x10:]
			entries = append(entries, entry{int64(binary.LittleEndian.Uint64(e)), binary.LittleEndian.Uint32(e[12:])})
		}
	}
	if entries[0].off != 0 || end > size || end <= entries[len(entries)-1].off {
		return whole, nil
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].off <= entries[i-1].off {
			return whole, nil
		}
	}

	var out []Section
	for i, e := range entries {
		next := end
		if i+1 < len(entries) {
			next = entries[i+1].off
		}
		sec := base
		sec.Offset, sec.Size = s.Start+e.off, next-e.off
		binary.BigEndian.PutUint32(sec.Counter[4:8], e.gen)
		out = append(out, sec)
	}
	if end < size {
		tail := base
		tail.Offset, tail.Size = s.Start+end, size-end
		out = append(out, tail)
	}
	return out, nil
}

// applyCTR encrypts/decrypts buf, which holds the NCA bytes starting at the
// absolute offset abs, according to the sections. Bytes outside encrypted
// sections are left unchanged.
func applyCTR(sections []Section, abs int64, buf []byte) {
	end := abs + int64(len(buf))
	i := sort.Search(len(sections), func(i int) bool { return sections[i].Offset+sections[i].Size > abs })
	for ; i < len(sections) && sections[i].Offset < end; i++ {
		s := &sections[i]
		if !s.encrypted() {
			continue
		}
		lo, hi := max(abs, s.Offset), min(end, s.Offset+s.Size)
		if lo < hi {
			xorCTR(s.block, s.Counter, lo, buf[lo-abs:hi-abs])
		}
	}
}

// xorCTR applies AES-CTR for data at the absolute NCA offset off.
func xorCTR(block cipher.Block, counter [16]byte, off int64, b []byte) {
	binary.BigEndian.PutUint64(counter[8:], uint64(off)>>4)
	s := cipher.NewCTR(block, counter[:])
	if skip := off & 0xF; skip != 0 {
		var z [16]byte
		s.XORKeyStream(z[:skip], z[:skip])
	}
	s.XORKeyStream(b, b)
}

func encodeSections(sections []Section) []byte {
	b := make([]byte, 16, 16+0x40*len(sections))
	copy(b, "NCZSECTN")
	binary.LittleEndian.PutUint64(b[8:], uint64(len(sections)))
	for _, s := range sections {
		var e [0x40]byte
		binary.LittleEndian.PutUint64(e[0:], uint64(s.Offset))
		binary.LittleEndian.PutUint64(e[8:], uint64(s.Size))
		binary.LittleEndian.PutUint64(e[16:], s.CryptoType)
		copy(e[32:], s.Key[:])
		copy(e[48:], s.Counter[:])
		b = append(b, e[:]...)
	}
	return b
}

func decodeSections(r io.ReaderAt, off int64) ([]Section, int64, error) {
	var hdr [16]byte
	if _, err := r.ReadAt(hdr[:], off); err != nil {
		return nil, 0, err
	}
	if string(hdr[:8]) != "NCZSECTN" {
		return nil, 0, i18n.Errorf("err.ncz.format")
	}
	n := binary.LittleEndian.Uint64(hdr[8:])
	if n == 0 || n > 1<<20 {
		return nil, 0, i18n.Errorf("err.ncz.format")
	}
	raw := make([]byte, 0x40*n)
	if _, err := r.ReadAt(raw, off+16); err != nil {
		return nil, 0, err
	}
	sections := make([]Section, n)
	for i := range sections {
		e := raw[i*0x40:]
		s := &sections[i]
		s.Offset = int64(binary.LittleEndian.Uint64(e[0:]))
		s.Size = int64(binary.LittleEndian.Uint64(e[8:]))
		s.CryptoType = binary.LittleEndian.Uint64(e[16:])
		copy(s.Key[:], e[32:48])
		copy(s.Counter[:], e[48:64])
		if err := s.init(); err != nil {
			return nil, 0, err
		}
	}
	sort.Slice(sections, func(i, j int) bool { return sections[i].Offset < sections[j].Offset })
	return sections, off + 16 + int64(len(raw)), nil
}
