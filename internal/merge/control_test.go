package merge

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"gameforge/internal/nsz"
	"gameforge/internal/nx"
)

const ivfcOrder = 14

// buildIVFC lays out data as a RomFS section with a 6-level IVFC hash
// tree (blocks zero-padded) and returns the section and its FsHeader.
func buildIVFC(data []byte) ([]byte, []byte) {
	bs := 1 << ivfcOrder
	pad := func(b []byte) []byte { return append(bytes.Clone(b), make([]byte, (bs-len(b)%bs)%bs)...) }
	levels := [][]byte{data}
	for len(levels) < 6 {
		var h []byte
		p := pad(levels[0])
		for i := 0; i < len(p); i += bs {
			s := sha256.Sum256(p[i : i+bs])
			h = append(h, s[:]...)
		}
		levels = append([][]byte{h}, levels...)
	}
	master := sha256.Sum256(pad(levels[0]))

	fs := make([]byte, 0x200)
	fs[2], fs[3], fs[4] = 0, 3, 3
	ivfc := fs[0x8:]
	copy(ivfc, "IVFC")
	binary.LittleEndian.PutUint32(ivfc[4:], 0x20000)
	binary.LittleEndian.PutUint32(ivfc[8:], 0x20)
	binary.LittleEndian.PutUint32(ivfc[0xC:], 7)
	copy(ivfc[0xC0:], master[:])
	var section []byte
	for i, l := range levels {
		lh := ivfc[0x10+i*0x18:]
		binary.LittleEndian.PutUint64(lh, uint64(len(section)))
		binary.LittleEndian.PutUint64(lh[8:], uint64(len(l)))
		binary.LittleEndian.PutUint32(lh[16:], ivfcOrder)
		section = append(section, pad(l)...)
	}
	return section, fs
}

type testSection struct {
	plain []byte // plaintext; for BKTR already encrypted (see enc)
	fs    []byte
	enc   func(abs int64, b []byte) // custom encryption (BKTR)
}

// buildNCAWith builds an NCA with sections laid out from 0x4000.
func buildNCAWith(k *testKeys, contentType byte, programID uint64, key, rightsID []byte, secs []testSection) []byte {
	hdr := make([]byte, 0xC00)
	copy(hdr[0x200:], "NCA3")
	hdr[0x205] = contentType
	binary.LittleEndian.PutUint64(hdr[0x210:], programID)
	if rightsID != nil {
		copy(hdr[0x230:], rightsID)
	} else {
		copy(hdr[0x320:], ecbEncrypt(k.kak, key))
	}
	body := make([]byte, 0x4000-0xC00)
	off := int64(0x4000)
	for i, s := range secs {
		data := append(bytes.Clone(s.plain), make([]byte, (0x200-len(s.plain)%0x200)%0x200)...)
		fs := hdr[0x400+i*0x200:]
		copy(fs, s.fs)
		if fs[4] == 3 && s.enc == nil {
			var iv [8]byte
			copy(iv[:], rnd(8))
			copy(fs[0x140:], iv[:])
			sec, _ := nsz.NewSection(off, int64(len(data)), key, iv, true)
			nsz.Crypt([]nsz.Section{sec}, off, data)
		} else if s.enc != nil {
			s.enc(off, data)
		}
		binary.LittleEndian.PutUint32(hdr[0x240+i*0x10:], uint32(off/0x200))
		binary.LittleEndian.PutUint32(hdr[0x244+i*0x10:], uint32((off+int64(len(data)))/0x200))
		h := sha256.Sum256(fs[:0x200])
		copy(hdr[0x280+i*0x20:], h[:])
		body = append(body, data...)
		off += int64(len(data))
	}
	binary.LittleEndian.PutUint64(hdr[0x208:], uint64(off))
	return append(xtsEncrypt(k.header, hdr), body...)
}

func record(nca []byte, typ uint8) nx.ContentRecord {
	r := nx.ContentRecord{Hash: sha256.Sum256(nca), Size: int64(len(nca)), Type: typ}
	copy(r.ID[:], r.Hash[:16])
	return r
}

func cnmtWith(titleID uint64, version uint32, typ uint8, ext uint64, recs ...nx.ContentRecord) []byte {
	b := make([]byte, 0x30)
	binary.LittleEndian.PutUint64(b, titleID)
	binary.LittleEndian.PutUint32(b[8:], version)
	b[0xC] = typ
	binary.LittleEndian.PutUint16(b[0xE:], 0x10)
	binary.LittleEndian.PutUint16(b[0x10:], uint16(len(recs)))
	binary.LittleEndian.PutUint64(b[0x20:], ext)
	binary.LittleEndian.PutUint32(b[0x28:], 0x1234)
	for _, r := range recs {
		b = append(b, r.Encode()...)
	}
	return append(b, make([]byte, 0x20)...)
}

// romFS builds a RomFS with the given files in its root directory (the
// directory tables are left empty: only the file table is read).
func romFS(files map[string][]byte) []byte {
	var meta, data []byte
	for name, b := range files {
		e := make([]byte, 0x20)
		binary.LittleEndian.PutUint64(e[8:], uint64(len(data)))
		binary.LittleEndian.PutUint64(e[0x10:], uint64(len(b)))
		binary.LittleEndian.PutUint32(e[0x1C:], uint32(len(name)))
		e = append(e, name...)
		meta = append(meta, append(e, make([]byte, (4-len(e)%4)%4)...)...)
		data = append(data, append(bytes.Clone(b), make([]byte, (16-len(b)%16)%16)...)...)
	}
	hdr := make([]byte, 0x50)
	binary.LittleEndian.PutUint64(hdr, 0x50)
	binary.LittleEndian.PutUint64(hdr[0x38:], 0x50)
	binary.LittleEndian.PutUint64(hdr[0x40:], uint64(len(meta)))
	dataOff := (0x50 + len(meta) + 15) &^ 15
	binary.LittleEndian.PutUint64(hdr[0x48:], uint64(dataOff))
	rom := append(hdr, meta...)
	return append(append(rom, make([]byte, dataOff-len(rom))...), data...)
}

// controlNCA builds a Control NCA with a NACP (English name, display
// version) and an icon.
func controlNCA(k *testKeys, app uint64, name, version string, icon []byte) []byte {
	nacp := make([]byte, 0x4000)
	copy(nacp[0x300:], "Wrong (British)") // English name must win over others
	copy(nacp, name)
	copy(nacp[0x3060:], version)
	sec, fs := buildIVFC(romFS(map[string][]byte{"control.nacp": nacp, "icon_AmericanEnglish.dat": icon}))
	return buildNCAWith(k, nx.ContentTypeControl, app, rnd(16), nil, []testSection{{plain: sec, fs: fs}})
}

func TestControlInfo(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	const app = 0x0100ABCD12340000
	icon := []byte("\xff\xd8 not really a JPEG")
	ctl := controlNCA(k, app, "Test Game", "1.2.3", icon)
	cnmt := cnmtWith(app, 0, nx.MetaApplication, app|0x800, record(ctl, nx.ContentRecordControl))
	binary.LittleEndian.PutUint32(cnmt[0x28:], 13<<26|1<<20) // firmware 13.1.0
	p := writeNSP(t, dir, "whatever [0100ABCD12340000][v0].nsp", map[string][]byte{
		ncaName(ctl):    ctl,
		"base.cnmt.nca": buildMetaNCA(t, dir, k, cnmt, nil, nil),
	})
	n, err := Analyze(p, keys)
	if err != nil {
		t.Fatal(err)
	}
	c := n.Control()
	if c == nil || c.Name != "Test Game" || c.Version != "1.2.3" || !bytes.Equal(c.Icon, icon) {
		t.Fatalf("control: %+v", c)
	}
	if fw := n.Own().Firmware(); fw != "13.1.0" {
		t.Fatalf("firmware %q", fw)
	}
	games, _ := Group([]*NSP{n})
	if got := games[0].OutputName([]*NSP{n}, ".nsp"); got != "Test Game [0100ABCD12340000][v0].nsp" {
		t.Fatalf("output name %q", got)
	}
}
