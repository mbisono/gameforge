package discinfo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"

	"gameforge/internal/xiso"
)

// Xbox platforms.
const (
	Xbox    = "Xbox"
	Xbox360 = "Xbox 360"
)

// InfoFromXbox reads the game information of an Xbox or Xbox 360 disc
// image (a disc dump or an XISO). For an Xbox game it comes from the
// certificate of default.xbe; of an Xbox 360 game only the platform is
// known.
func InfoFromXbox(path string) (Info, error) {
	im, err := xiso.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer im.Close()
	if im.Xbox360 {
		return Info{Platform: Xbox360}, nil
	}
	r, size, ok := im.RootFile("default.xbe")
	if !ok {
		return Info{Platform: Xbox}, nil
	}
	info, err := XBEInfo(r, size)
	info.Platform = Xbox
	return info, err
}

// XBEInfo reads the certificate of an Xbox executable: the title ID as
// the product code (publisher letters and number, e.g. EA-091), the title
// (often shortened, sometimes empty) and the regions it runs in.
func XBEInfo(r io.ReaderAt, size int64) (Info, error) {
	le := binary.LittleEndian
	hdr := make([]byte, 0x120)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return Info{}, err
	}
	if string(hdr[:4]) != "XBEH" {
		return Info{}, errors.New("not an XBE")
	}
	at := int64(le.Uint32(hdr[0x118:])) - int64(le.Uint32(hdr[0x104:]))
	cert := make([]byte, 0xB0)
	if at < 0 || at+int64(len(cert)) > size {
		return Info{}, errors.New("invalid XBE certificate address")
	}
	if _, err := r.ReadAt(cert, at); err != nil {
		return Info{}, err
	}
	var info Info
	id := le.Uint32(cert[8:])
	pub := []byte{byte(id >> 24), byte(id >> 16)}
	if isCodeChar(pub[0]) && isCodeChar(pub[1]) {
		info.Serial = fmt.Sprintf("%s-%03d", pub, id&0xffff)
	}
	name := make([]uint16, 40)
	for i := range name {
		name[i] = le.Uint16(cert[0x0C+2*i:])
	}
	for i, c := range name {
		if c == 0 {
			name = name[:i]
			break
		}
	}
	info.Title = strings.TrimSpace(string(utf16.Decode(name)))
	regions := le.Uint32(cert[0xA0:])
	for _, r := range []struct {
		flag uint32
		name string
	}{{1, "USA"}, {2, "Japan"}, {4, "Europe"}} {
		if regions&r.flag != 0 {
			info.Regions = append(info.Regions, r.name)
		}
	}
	return info, nil
}

func isCodeChar(c byte) bool { return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }
