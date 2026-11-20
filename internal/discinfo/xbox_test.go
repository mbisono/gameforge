package discinfo

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
	"unicode/utf16"
)

// xbe is a minimal default.xbe with a certificate.
func xbe(id uint32, title string, regions uint32) []byte {
	b := make([]byte, 0x400)
	le := binary.LittleEndian
	copy(b, "XBEH")
	const base, cert = 0x10000, 0x200
	le.PutUint32(b[0x104:], base)
	le.PutUint32(b[0x118:], base+cert)
	le.PutUint32(b[cert+8:], id)
	for i, c := range utf16.Encode([]rune(title)) {
		le.PutUint16(b[cert+0x0C+2*i:], c)
	}
	le.PutUint32(b[cert+0xA0:], regions)
	return b
}

func TestXBEInfo(t *testing.T) {
	for _, c := range []struct {
		id           uint32
		title        string
		regions      uint32
		serial, name string
		want         []string
	}{
		{0x4541005B, "Burnout 3", 4, "EA-091", "Burnout 3", []string{"Europe"}},
		{0x56550015, "The Simpsons Hit & Run", 3, "VU-021", "The Simpsons Hit & Run", []string{"USA", "Japan"}},
		{0x4D530004, "", 7, "MS-004", "", []string{"USA", "Japan", "Europe"}},
		{0x0000FFFF, "Homebrew", 0x80000000, "", "Homebrew", nil},
	} {
		b := xbe(c.id, c.title, c.regions)
		info, err := XBEInfo(bytes.NewReader(b), int64(len(b)))
		if err != nil || info.Serial != c.serial || info.Title != c.name || !slices.Equal(info.Regions, c.want) {
			t.Errorf("%08X: %+v %v", c.id, info, err)
		}
	}
	if _, err := XBEInfo(bytes.NewReader(make([]byte, 0x400)), 0x400); err == nil {
		t.Error("not an XBE")
	}
}
