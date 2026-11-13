package nx

import (
	"bytes"
	"encoding/binary"
	"strings"

	"gameforge/internal/i18n"
)

// Content meta types found in a CNMT.
const (
	MetaApplication  = 0x80
	MetaPatch        = 0x81
	MetaAddOnContent = 0x82
)

// ContentMeta is the relevant part of a CNMT.
type ContentMeta struct {
	TitleID       uint64
	Version       uint32
	Type          uint8
	ApplicationID uint64 // base game this content belongs to
	// RequiredSystemVersion is the u32 at extended header offset 8 (for
	// add-on content it is the required application version).
	RequiredSystemVersion uint32
	Contents              []ContentRecord
}

// Content record types.
const (
	ContentRecordMeta             = 0
	ContentRecordProgram          = 1
	ContentRecordData             = 2
	ContentRecordControl          = 3
	ContentRecordHtmlDocument     = 4
	ContentRecordLegalInformation = 5
	ContentRecordDeltaFragment    = 6
)

// ContentRecord is one NCA listed in a CNMT.
type ContentRecord struct {
	Hash     [32]byte
	ID       [16]byte
	Size     int64
	Type     uint8
	IDOffset uint8
}

// ContentRecordSize is the size of an encoded ContentRecord.
const ContentRecordSize = 0x38

// Encode returns the binary form of the record.
func (r ContentRecord) Encode() []byte {
	b := make([]byte, ContentRecordSize)
	copy(b, r.Hash[:])
	copy(b[0x20:], r.ID[:])
	var sz [8]byte
	binary.LittleEndian.PutUint64(sz[:], uint64(r.Size))
	copy(b[0x30:0x36], sz[:6])
	b[0x36], b[0x37] = r.Type, r.IDOffset
	return b
}

// ParseCNMT parses a raw .cnmt file.
func ParseCNMT(b []byte) (*ContentMeta, error) {
	if len(b) < 0x28 {
		return nil, i18n.Errorf("err.cnmt.truncated")
	}
	m := &ContentMeta{
		TitleID: binary.LittleEndian.Uint64(b[0:]),
		Version: binary.LittleEndian.Uint32(b[8:]),
		Type:    b[0xC],
	}
	switch m.Type {
	case MetaApplication:
		m.ApplicationID = m.TitleID
	case MetaPatch, MetaAddOnContent:
		m.ApplicationID = binary.LittleEndian.Uint64(b[0x20:])
	default:
		return nil, i18n.Errorf("err.cnmt.type", m.Type)
	}
	// Extended header: u64 at 0x20 (patch/application ID), u32 at 0x28.
	m.RequiredSystemVersion = binary.LittleEndian.Uint32(b[0x28:])
	extSize := int(binary.LittleEndian.Uint16(b[0xE:]))
	count := int(binary.LittleEndian.Uint16(b[0x10:]))
	recs := b[min(0x20+extSize, len(b)):]
	if len(recs) < count*ContentRecordSize {
		return nil, i18n.Errorf("err.cnmt.truncated")
	}
	for i := range count {
		e := recs[i*ContentRecordSize:]
		var r ContentRecord
		copy(r.Hash[:], e[:0x20])
		copy(r.ID[:], e[0x20:0x30])
		var sz [8]byte
		copy(sz[:6], e[0x30:0x36])
		r.Size = int64(binary.LittleEndian.Uint64(sz[:]))
		r.Type, r.IDOffset = e[0x36], e[0x37]
		m.Contents = append(m.Contents, r)
	}
	return m, nil
}

// ParseTicket extracts the rights ID and the encrypted title key from a
// common ticket.
func ParseTicket(b []byte) (rightsID [16]byte, encTitleKey []byte, err error) {
	if len(b) < 4 {
		return rightsID, nil, i18n.Errorf("err.tik.truncated")
	}
	var bodyOff int
	switch binary.LittleEndian.Uint32(b) {
	case 0x10000, 0x10003: // RSA-4096
		bodyOff = 4 + 0x200 + 0x3C
	case 0x10001, 0x10004: // RSA-2048
		bodyOff = 4 + 0x100 + 0x3C
	case 0x10002, 0x10005: // ECDSA
		bodyOff = 4 + 0x3C + 0x40
	default:
		return rightsID, nil, i18n.Errorf("err.tik.sig")
	}
	if len(b) < bodyOff+0x180 {
		return rightsID, nil, i18n.Errorf("err.tik.truncated")
	}
	body := b[bodyOff:]
	copy(rightsID[:], body[0x160:0x170])
	if body[0x141] != 0 {
		return rightsID, nil, i18n.Errorf("err.tik.personal")
	}
	return rightsID, append([]byte(nil), body[0x40:0x50]...), nil
}

// DecryptTitleKey decrypts a ticket's title key for an NCA's master key revision.
func DecryptTitleKey(keys *Keys, encTitleKey []byte, rev int) ([]byte, error) {
	kek, err := keys.Titlekek(rev)
	if err != nil {
		return nil, err
	}
	return ecbDecrypt(kek, encTitleKey), nil
}

// ExtractCNMT finds the .cnmt file inside a decrypted Meta NCA section.
func ExtractCNMT(section []byte, s NCASection) (*ContentMeta, error) {
	base := s.PFS0Offset
	if base < 0 || base >= int64(len(section)) {
		return nil, i18n.Errorf("err.cnmt.pfs0_offset")
	}
	r := bytes.NewReader(section)
	entries, err := ReadPFS0(r, base)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, ".cnmt") {
			continue
		}
		if e.Offset+e.Size > int64(len(section)) {
			return nil, i18n.Errorf("err.cnmt.bounds")
		}
		return ParseCNMT(section[e.Offset : e.Offset+e.Size])
	}
	return nil, i18n.Errorf("err.cnmt.none")
}
