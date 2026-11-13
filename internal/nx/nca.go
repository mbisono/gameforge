package nx

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
	"strings"

	"gameforge/internal/i18n"
)

// NCA content types.
const (
	ContentTypeProgram    = 0
	ContentTypeMeta       = 1
	ContentTypeControl    = 2
	ContentTypeManual     = 3
	ContentTypeData       = 4
	ContentTypePublicData = 5
)

const (
	ncaHeaderSize = 0xC00
	sectionCTR    = 3
)

// NCASection is one filesystem section of an NCA.
type NCASection struct {
	Start, End int64 // absolute offsets inside the NCA
	FsType     uint8 // 0 RomFS, 1 PartitionFS
	EncType    uint8 // 1 none, 2 XTS, 3 CTR, 4 BKTR
	UpperIV    [8]byte
	PFS0Offset int64 // relative to section start
	PFS0Size   int64
	// BKTR (AesCtrEx) subsection table location, relative to the section
	// start; only set for update NCAs (EncType 4).
	CtrExOffset, CtrExSize int64
}

// NCAHeader is the decrypted, parsed header of an NCA.
type NCAHeader struct {
	ContentType uint8
	KeyGen      uint8
	KeyAreaIdx  uint8
	ProgramID   uint64
	RightsID    [16]byte
	KeyArea     [4][16]byte
	Sections    [4]NCASection
	// Raw is the whole decrypted header, for tools that rewrite it.
	Raw []byte
}

// FsHeader returns the raw 0x200-byte filesystem header of section i.
func (h *NCAHeader) FsHeader(i int) []byte { return h.Raw[0x400+i*0x200 : 0x600+i*0x200] }

// EncryptNCAHeader encrypts a raw 0xC00-byte NCA3 header with the header key.
func EncryptNCAHeader(raw, headerKey []byte) ([]byte, error) {
	x, err := newNintendoXTS(headerKey, 0x200)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(raw))
	x.crypt(out, raw, 0, false)
	return out, nil
}

// EncryptKeyArea wraps a content key for the NCA key area with the key-area
// key (index, master key revision), the inverse of what SectionKey does.
func EncryptKeyArea(keys *Keys, index uint8, rev int, key []byte) ([]byte, error) {
	kak, err := keys.KeyAreaKey(index, rev)
	if err != nil {
		return nil, err
	}
	return ecbEncrypt(kak, key), nil
}

// MasterKeyRev returns the master key revision used by the NCA.
func (h *NCAHeader) MasterKeyRev() int {
	if h.KeyGen == 0 {
		return 0
	}
	return int(h.KeyGen) - 1
}

// HasRightsID reports whether the NCA uses title-key crypto.
func (h *NCAHeader) HasRightsID() bool { return h.RightsID != [16]byte{} }

// ParseNCAHeader decrypts and parses the NCA header at off inside r.
func ParseNCAHeader(r io.ReaderAt, off int64, headerKey []byte) (*NCAHeader, error) {
	enc := make([]byte, ncaHeaderSize)
	if _, err := r.ReadAt(enc, off); err != nil {
		return nil, i18n.Errorf("err.nca.read_header", err)
	}
	x, err := newNintendoXTS(headerKey, 0x200)
	if err != nil {
		return nil, err
	}
	b := make([]byte, ncaHeaderSize)
	x.crypt(b, enc, 0, true)

	if string(b[0x200:0x204]) != "NCA3" {
		if magic := string(b[0x200:0x204]); strings.HasPrefix(magic, "NCA") {
			return nil, i18n.Errorf("err.nca.format", magic)
		}
		return nil, i18n.Errorf("err.nca.decrypt")
	}

	h := &NCAHeader{
		Raw:         b,
		ContentType: b[0x205],
		KeyAreaIdx:  b[0x207],
		ProgramID:   binary.LittleEndian.Uint64(b[0x210:]),
		KeyGen:      max(b[0x206], b[0x220]),
	}
	copy(h.RightsID[:], b[0x230:0x240])
	for i := range h.KeyArea {
		copy(h.KeyArea[i][:], b[0x300+i*16:])
	}
	for i := range h.Sections {
		e := b[0x240+i*0x10:]
		fs := b[0x400+i*0x200 : 0x600+i*0x200]
		s := &h.Sections[i]
		s.Start = int64(binary.LittleEndian.Uint32(e[0:])) * 0x200
		s.End = int64(binary.LittleEndian.Uint32(e[4:])) * 0x200
		s.FsType = fs[2]
		s.EncType = fs[4]
		copy(s.UpperIV[:], fs[0x140:0x148])
		// Hierarchical SHA-256 hash data: layer region 1 is the PFS0 itself.
		s.PFS0Offset = int64(binary.LittleEndian.Uint64(fs[0x40:]))
		s.PFS0Size = int64(binary.LittleEndian.Uint64(fs[0x48:]))
		s.CtrExOffset = int64(binary.LittleEndian.Uint64(fs[0x120:]))
		s.CtrExSize = int64(binary.LittleEndian.Uint64(fs[0x128:]))
	}
	return h, nil
}

// SectionKey returns the AES-CTR key for the NCA's sections. titleKey is the
// already decrypted title key, required only when the NCA has a rights ID.
func (h *NCAHeader) SectionKey(keys *Keys, titleKey []byte) ([]byte, error) {
	if h.HasRightsID() {
		if titleKey == nil {
			return nil, i18n.Errorf("err.nca.no_ticket")
		}
		return titleKey, nil
	}
	kak, err := keys.KeyAreaKey(h.KeyAreaIdx, h.MasterKeyRev())
	if err != nil {
		return nil, err
	}
	return ecbDecrypt(kak, h.KeyArea[2][:]), nil
}

// ReadSection reads and decrypts a whole NCA section into memory. Only meant
// for small sections such as the one inside a Meta NCA.
func ReadSection(r io.ReaderAt, ncaOff int64, h *NCAHeader, idx int, key []byte) ([]byte, error) {
	s := h.Sections[idx]
	size := s.End - s.Start
	if size <= 0 {
		return nil, i18n.Errorf("err.nca.section_empty", idx)
	}
	if size > 64<<20 {
		return nil, i18n.Errorf("err.nca.section_large", idx)
	}
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, ncaOff+s.Start); err != nil {
		return nil, i18n.Errorf("err.nca.read_section", err)
	}
	switch s.EncType {
	case 1:
		return buf, nil
	case sectionCTR:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		cipher.NewCTR(block, sectionIV(s.UpperIV, s.Start)).XORKeyStream(buf, buf)
		return buf, nil
	default:
		return nil, i18n.Errorf("err.nca.crypto", s.EncType)
	}
}

// sectionIV builds the AES-CTR IV for the given absolute NCA offset.
func sectionIV(upper [8]byte, offset int64) []byte {
	iv := make([]byte, 16)
	for i := range 8 {
		iv[i] = upper[7-i]
	}
	binary.BigEndian.PutUint64(iv[8:], uint64(offset)>>4)
	return iv
}
