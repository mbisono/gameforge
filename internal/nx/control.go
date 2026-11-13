package nx

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// Control is what the Control NCA of a game says about it.
type Control struct {
	Name    string // English title (other languages as fallback)
	Version string // display version, such as "1.0.8"
	Icon    []byte // JPEG, 256×256; nil if missing
}

// ParseControl reads name, display version and icon from the decrypted
// RomFS section (section 0) of a Control NCA; fs is its FsHeader.
func ParseControl(section, fs []byte) (*Control, error) {
	files, err := romFSFiles(section, fs)
	if err != nil {
		return nil, err
	}
	nacp := files["control.nacp"]
	if len(nacp) < 0x3070 {
		return nil, fmt.Errorf("control.nacp missing or truncated")
	}
	c := &Control{Version: cstring(nacp[0x3060:0x3070])}
	// 16 title entries of 0x300 bytes (name 0x200, publisher 0x100), in the
	// order AmericanEnglish, BritishEnglish, Japanese, French, German, ...
	for i := range 16 {
		if c.Name = cstring(nacp[i*0x300 : i*0x300+0x200]); c.Name != "" {
			break
		}
	}
	for _, lang := range []string{"AmericanEnglish", "BritishEnglish"} {
		if c.Icon = files["icon_"+lang+".dat"]; c.Icon != nil {
			break
		}
	}
	if c.Icon == nil {
		for name, b := range files {
			if strings.HasPrefix(name, "icon_") {
				c.Icon = b
				break
			}
		}
	}
	return c, nil
}

func cstring(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

// romFSFiles returns the files of a small RomFS by name (directories are
// ignored). The RomFS is the data level of the section's IVFC hash tree.
func romFSFiles(section, fs []byte) (map[string][]byte, error) {
	bad := fmt.Errorf("RomFS: bad header")
	ivfc := fs[0x8:]
	if string(ivfc[:4]) != "IVFC" {
		return nil, fmt.Errorf("RomFS: no IVFC header")
	}
	levels := int(binary.LittleEndian.Uint32(ivfc[0xC:])) - 1 // without the master hash
	if levels < 1 || levels > 6 {
		return nil, bad
	}
	lh := ivfc[0x10+(levels-1)*0x18:]
	off, size := binary.LittleEndian.Uint64(lh), binary.LittleEndian.Uint64(lh[8:])
	if off+size > uint64(len(section)) || size < 0x50 {
		return nil, bad
	}
	rom := section[off : off+size]
	u64 := func(o uint64) uint64 { return binary.LittleEndian.Uint64(rom[o:]) }
	metaOff, metaSize, dataOff := u64(0x38), u64(0x40), u64(0x48)
	if metaOff+metaSize > size || dataOff > size {
		return nil, bad
	}
	meta := rom[metaOff : metaOff+metaSize]
	files := map[string][]byte{}
	for p := uint64(0); p+0x20 <= uint64(len(meta)); {
		e := meta[p:]
		fOff, fSize := binary.LittleEndian.Uint64(e[8:]), binary.LittleEndian.Uint64(e[0x10:])
		nameLen := uint64(binary.LittleEndian.Uint32(e[0x1C:]))
		if p+0x20+nameLen > uint64(len(meta)) || dataOff+fOff+fSize > size {
			return nil, bad
		}
		files[string(e[0x20:0x20+nameLen])] = rom[dataOff+fOff : dataOff+fOff+fSize]
		p += (0x20 + nameLen + 3) &^ 3
	}
	return files, nil
}

// FirmwareString formats a required system version (CNMT) as "13.1.0", or ""
// for 0.
func FirmwareString(v uint32) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", v>>26, v>>20&0x3F, v>>16&0xF)
}
