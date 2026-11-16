package retroach

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// isoDisc is a minimal ISO 9660 disc: sectors by address, with a root
// folder, optional subfolders and files.
type isoDisc struct {
	sectors map[int64][]byte
	next    int64
	start   int64
}

func newISO(start int64) *isoDisc {
	return &isoDisc{sectors: map[int64][]byte{}, next: start + 20, start: start}
}

func (d *isoDisc) disc() Disc {
	return Disc{Start: d.start, Read: func(lba int64) ([]byte, error) {
		if b, ok := d.sectors[lba]; ok {
			return b, nil
		}
		return make([]byte, 2048), nil
	}}
}

func (d *isoDisc) put(data []byte) int64 {
	at := d.next
	for i := 0; i < len(data) || i == 0; i += 2048 {
		s := make([]byte, 2048)
		copy(s, data[i:min(len(data), i+2048)])
		d.sectors[d.next] = s
		d.next++
	}
	return at
}

func record(name string, lba, size int64, dir bool) []byte {
	r := make([]byte, 33+len(name)+(len(name)+1)%2)
	r[0] = byte(len(r))
	binary.LittleEndian.PutUint32(r[2:], uint32(lba))
	binary.LittleEndian.PutUint32(r[10:], uint32(size))
	if dir {
		r[25] = 2
	}
	r[32] = byte(len(name))
	copy(r[33:], name)
	return r
}

// folder writes a folder of entries (name → lba, size; names ending in /
// are folders) and returns its address.
func (d *isoDisc) folder(entries [][3]any) int64 {
	var b bytes.Buffer
	b.Write(record("\x00", 0, 2048, true))
	b.Write(record("\x01", 0, 2048, true))
	for _, e := range entries {
		name := e[0].(string)
		dir := name[len(name)-1] == '/'
		if dir {
			name = name[:len(name)-1]
		}
		b.Write(record(name, e[1].(int64), e[2].(int64), dir))
	}
	return d.put(b.Bytes())
}

func (d *isoDisc) root(lba int64) {
	pvd := make([]byte, 2048)
	copy(pvd, "\x01CD001")
	binary.LittleEndian.PutUint16(pvd[128:], 2048)
	copy(pvd[156:], record("\x00", lba, 2048, true))
	d.sectors[d.start+16] = pvd
}

func sum(parts ...[]byte) string {
	h := md5.New()
	for _, p := range parts {
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestHashPlayStation(t *testing.T) {
	exe := make([]byte, 5000)
	copy(exe, "PS-X EXE")
	binary.LittleEndian.PutUint32(exe[28:], 2048) // code after the header: 2048 bytes
	for i := 32; i < len(exe); i++ {
		exe[i] = byte(i)
	}
	d := newISO(0)
	exeAt := d.put(exe)
	cnf := []byte("BOOT = cdrom:\\SLUS_007.08;1\r\nTCB = 4\r\n")
	cnfAt := d.put(cnf)
	d.root(d.folder([][3]any{{"SLUS_007.08;1", exeAt, int64(len(exe))}, {"SYSTEM.CNF;1", cnfAt, int64(len(cnf))}}))
	got, err := HashDisc(PS1, d.disc())
	if want := sum([]byte("SLUS_007.08"), exe[:4096]); err != nil || got != want {
		t.Errorf("PS1: %s %v, want %s", got, err, want)
	}

	// PS2: BOOT2, the file's size from its directory entry.
	elf := append([]byte("\x7fELF"), bytes.Repeat([]byte{9}, 3000)...)
	d = newISO(0)
	elfAt := d.put(elf)
	cnf = []byte("BOOT2 = cdrom0:\\SLES_504.90;1\nVER = 1.01\n")
	cnfAt = d.put(cnf)
	d.root(d.folder([][3]any{{"SLES_504.90;1", elfAt, int64(len(elf))}, {"SYSTEM.CNF;1", cnfAt, int64(len(cnf))}}))
	got, err = HashDisc(PS2, d.disc())
	if want := sum([]byte("SLES_504.90"), elf); err != nil || got != want {
		t.Errorf("PS2: %s %v, want %s", got, err, want)
	}

	// PS1 without SYSTEM.CNF boots PSX.EXE.
	d = newISO(0)
	exeAt = d.put(exe)
	d.root(d.folder([][3]any{{"PSX.EXE;1", exeAt, int64(len(exe))}}))
	got, err = HashDisc(PS1, d.disc())
	if want := sum([]byte("PSX.EXE"), exe[:4096]); err != nil || got != want {
		t.Errorf("PSX.EXE: %s %v", got, err)
	}
}

func TestHashPSP(t *testing.T) {
	sfo, eboot := []byte("\x00PSF param"), bytes.Repeat([]byte("EBOOT"), 1000)
	d := newISO(0)
	sfoAt, ebootAt := d.put(sfo), d.put(eboot)
	sysdir := d.folder([][3]any{{"EBOOT.BIN;1", ebootAt, int64(len(eboot))}})
	game := d.folder([][3]any{{"PARAM.SFO;1", sfoAt, int64(len(sfo))}, {"SYSDIR/", sysdir, int64(2048)}})
	d.root(d.folder([][3]any{{"PSP_GAME/", game, int64(2048)}}))
	got, err := HashDisc(PSP, d.disc())
	if want := sum(sfo, eboot); err != nil || got != want {
		t.Errorf("PSP: %s %v, want %s", got, err, want)
	}
}

func TestHashDreamcastAndSega(t *testing.T) {
	d := newISO(45000)
	ip := make([]byte, 2048)
	copy(ip, "SEGA SEGAKATANA SEGA ENTERPRISES")
	copy(ip[96:], "1ST_READ.BIN    ")
	d.sectors[45000] = ip
	boot := bytes.Repeat([]byte{1, 2, 3}, 2000)
	d.next = 500000 // in the last track
	bootAt := d.put(boot)
	d.next = 45020
	d.root(d.folder([][3]any{{"1ST_READ.BIN;1", bootAt, int64(len(boot))}}))
	got, err := HashDisc(Dreamcast, d.disc())
	if want := sum(ip[:256], boot); err != nil || got != want {
		t.Errorf("Dreamcast: %s %v, want %s", got, err, want)
	}

	for _, c := range []struct {
		console int
		magic   string
	}{{Saturn, "SEGA SEGASATURN "}, {SegaCD, "SEGADISCSYSTEM  "}} {
		s0 := make([]byte, 2048)
		copy(s0, c.magic)
		s0[600] = 7 // beyond the 512 bytes hashed
		d := &isoDisc{sectors: map[int64][]byte{0: s0}}
		got, err := HashDisc(c.console, d.disc())
		if want := sum(s0[:512]); err != nil || got != want {
			t.Errorf("%s: %s %v", c.magic, got, err)
		}
	}
	if _, err := HashDisc(Saturn, newISO(0).disc()); err == nil {
		t.Error("an empty disc hashed as a Saturn disc")
	}
}
