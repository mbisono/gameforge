package merge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/nsz"
	"gameforge/internal/nx"
)

func pfs0Section(t *testing.T, files map[string][]byte) testSection {
	fs := make([]byte, 0x200)
	fs[2], fs[3], fs[4] = 1, 2, 3
	return testSection{plain: pfs0Bytes(t, t.TempDir(), files), fs: fs}
}

// bktrUpdate builds the update's RomFS section: newSec (full IVFC section)
// expressed as relocations over baseSec plus patch data encrypted with two
// counter generations, and the matching FsHeader.
func bktrUpdate(baseSec, newSec, key []byte, fsIVFC []byte) testSection {
	const chunk = 0x4000
	type ent struct {
		virt, phys int64
		patch      uint32
	}
	var ents []ent
	var data []byte
	for v := 0; v < len(newSec); v += chunk {
		c := newSec[v:min(v+chunk, len(newSec))]
		isBase := v+len(c) <= len(baseSec) && bytes.Equal(c, baseSec[v:v+len(c)])
		patch := uint32(1)
		phys := int64(len(data))
		if isBase {
			patch, phys = 0, int64(v)
		} else {
			data = append(data, c...)
			data = append(data, make([]byte, (chunk-len(c)%chunk)%chunk)...)
		}
		// Merge with the previous entry when contiguous in the same storage.
		if n := len(ents); n > 0 && ents[n-1].patch == patch && ents[n-1].phys+int64(v)-ents[n-1].virt == phys {
			continue
		}
		ents = append(ents, ent{int64(v), phys, patch})
	}
	R := int64(len(data))
	reloc := make([]byte, 0x8000)
	binary.LittleEndian.PutUint32(reloc[4:], 1)
	binary.LittleEndian.PutUint64(reloc[8:], uint64(len(newSec)))
	set := reloc[0x4000:]
	binary.LittleEndian.PutUint32(set[4:], uint32(len(ents)))
	binary.LittleEndian.PutUint64(set[8:], uint64(len(newSec)))
	for i, e := range ents {
		b := set[0x10+i*0x14:]
		binary.LittleEndian.PutUint64(b, uint64(e.virt))
		binary.LittleEndian.PutUint64(b[8:], uint64(e.phys))
		binary.LittleEndian.PutUint32(b[16:], e.patch)
	}
	half := (R / 2) &^ 0x3FFF
	subs := []struct {
		off int64
		gen uint32
	}{{0, 1}, {half, 2}}
	ctrex := make([]byte, 0x8000)
	binary.LittleEndian.PutUint32(ctrex[4:], 1)
	cs := ctrex[0x4000:]
	binary.LittleEndian.PutUint32(cs[4:], uint32(len(subs)))
	binary.LittleEndian.PutUint64(cs[8:], uint64(R))
	for i, s := range subs {
		binary.LittleEndian.PutUint64(cs[0x10+i*0x10:], uint64(s.off))
		binary.LittleEndian.PutUint32(cs[0x10+i*0x10+12:], s.gen)
	}
	plain := append(append(data, reloc...), ctrex...)

	fs := bytes.Clone(fsIVFC)
	fs[4] = 4
	binary.LittleEndian.PutUint64(fs[0x100:], uint64(R))
	binary.LittleEndian.PutUint64(fs[0x108:], 0x8000)
	copy(fs[0x110:], "BKTR")
	binary.LittleEndian.PutUint32(fs[0x114:], 1)
	binary.LittleEndian.PutUint32(fs[0x118:], uint32(len(ents)))
	binary.LittleEndian.PutUint64(fs[0x120:], uint64(R+0x8000))
	binary.LittleEndian.PutUint64(fs[0x128:], 0x8000)
	copy(fs[0x130:], "BKTR")
	binary.LittleEndian.PutUint32(fs[0x138:], uint32(len(subs)))
	var iv [8]byte
	copy(iv[:], rnd(8))
	copy(fs[0x140:], iv[:])

	enc := func(start int64, b []byte) {
		for i, s := range subs {
			end := R
			if i+1 < len(subs) {
				end = subs[i+1].off
			}
			sec, _ := nsz.NewSection(start+s.off, end-s.off, key, iv, true)
			binary.BigEndian.PutUint32(sec.Counter[4:8], s.gen)
			nsz.Crypt([]nsz.Section{sec}, start+s.off, b[s.off:end])
		}
		sec, _ := nsz.NewSection(start+R, int64(len(b))-R, key, iv, true)
		nsz.Crypt([]nsz.Section{sec}, start+R, b[R:])
	}
	return testSection{plain: plain, fs: fs, enc: enc}
}

func TestWritePatched(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	const app = 0x0100ABCD12340000
	baseData := []byte(strings.Repeat("base game asset data ", 0x30000/21+1))[:0x30000]
	newData := bytes.Clone(baseData)
	copy(newData[0x10000:], strings.Repeat("PATCHED ", 0x400))
	newData = append(newData, []byte(strings.Repeat("new update content ", 0x300))...)
	baseSec, baseFS := buildIVFC(baseData)
	newSec, newFS := buildIVFC(newData)

	baseKey, updKey := rnd(16), rnd(16)
	logo := []byte(strings.Repeat("LOGO", 0x100))
	baseProg := buildNCAWith(k, nx.ContentTypeProgram, app, baseKey, nil, []testSection{
		pfs0Section(t, map[string][]byte{"main": []byte("old code")}),
		{plain: baseSec, fs: baseFS},
		pfs0Section(t, map[string][]byte{"logo.png": logo}),
	})
	baseCtl := buildNCAWith(k, nx.ContentTypeControl, app, rnd(16), nil, []testSection{
		pfs0Section(t, map[string][]byte{"control.nacp": []byte("v1.0.0")}),
	})
	updExe := pfs0Section(t, map[string][]byte{"main": []byte("new code")})
	updProg := buildNCAWith(k, nx.ContentTypeProgram, app, updKey, nil, []testSection{
		updExe, bktrUpdate(baseSec, newSec, updKey, newFS),
	})
	rights, _ := hex.DecodeString("0100ABCD123408000000000000000000")
	titleKey := rnd(16)
	updCtlPlain := map[string][]byte{"control.nacp": []byte("v1.2.0")}
	updCtl := buildNCAWith(k, nx.ContentTypeControl, app, titleKey, rights, []testSection{pfs0Section(t, updCtlPlain)})
	delta := buildNCAWith(k, nx.ContentTypeData, app|0x800, rnd(16), nil, []testSection{pfs0Section(t, map[string][]byte{"d": rnd(0x3000)})})

	base := writeNSP(t, dir, "Jogo [0100ABCD12340000][v0].nsp", map[string][]byte{
		ncaName(baseProg): baseProg,
		ncaName(baseCtl):  baseCtl,
		"base.cnmt.nca": buildMetaNCA(t, dir, k, cnmtWith(app, 0, nx.MetaApplication, app|0x800,
			record(baseProg, nx.ContentRecordProgram), record(baseCtl, nx.ContentRecordControl)), nil, nil),
	})
	upd := writeNSP(t, dir, "Jogo [0100ABCD12340800][v131072].nsp", map[string][]byte{
		ncaName(updProg): updProg,
		ncaName(updCtl):  updCtl,
		ncaName(delta):   delta,
		"upd.cnmt.nca": buildMetaNCA(t, dir, k, cnmtWith(app|0x800, 131072, nx.MetaPatch, app,
			record(updProg, nx.ContentRecordProgram), record(updCtl, nx.ContentRecordControl),
			record(delta, nx.ContentRecordDeltaFragment)), nil, nil),
		"0100abcd123408000000000000000000.tik": ticket(rights, ecbEncrypt(k.titlekek, titleKey)),
	})
	var sel []*NSP
	for _, p := range []string{base, upd} {
		n, err := Analyze(p, keys)
		if err != nil || !n.FromCNMT {
			t.Fatalf("%s: %v %s", p, err, n.Note)
		}
		sel = append(sel, n)
	}

	out := filepath.Join(dir, "patched.nsp")
	var logs []string
	res, err := WritePatched(context.Background(), sel, out, Options{Keys: keys, Verify: true}, nil,
		func(s fmt.Stringer) { logs = append(logs, s.String()) })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d -> %d bytes; log: %q", res.InputSize, res.OutputSize, logs)
	if !strings.Contains(strings.Join(logs, "\n"), "IVFC") || strings.Contains(strings.Join(logs, "\n"), "⚠") {
		t.Fatalf("IVFC check did not pass: %q", logs)
	}

	// The output is a base game at the update's version.
	n, err := Analyze(out, keys)
	if err != nil || n.Kind != KindBase || n.Version != 131072 || !n.FromCNMT {
		t.Fatalf("patched NSP: %+v %v", n, err)
	}
	f, _ := os.Open(out)
	defer f.Close()
	hk, _ := keys.HeaderKey()
	meta, err := readContentMeta(n, f, keys, nx.MetaApplication)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Contents) != 2 {
		t.Fatalf("expected program + control in CNMT, got %d", len(meta.Contents))
	}
	for _, rec := range meta.Contents {
		var e nx.PFS0Entry
		for _, x := range n.Entries {
			if x.Name == hex.EncodeToString(rec.ID[:])+".nca" {
				e = x
			}
		}
		if e.Size != rec.Size {
			t.Fatalf("CNMT record %x not found with the right size", rec.ID)
		}
		buf := make([]byte, e.Size)
		f.ReadAt(buf, e.Offset)
		if sha256.Sum256(buf) != rec.Hash {
			t.Fatalf("CNMT hash mismatch for %s", e.Name)
		}
		h, err := nx.ParseNCAHeader(f, e.Offset, hk)
		if err != nil {
			t.Fatal(err)
		}
		if h.HasRightsID() {
			t.Fatalf("%s still needs a ticket", e.Name)
		}
		key, err := h.SectionKey(keys, nil)
		if err != nil {
			t.Fatal(err)
		}
		secs, err := nsz.CryptoSections(f, e.Offset, h, key)
		if err != nil {
			t.Fatal(err)
		}
		plain := bytes.Clone(buf)
		nsz.Crypt(secs, 0, plain)
		sec := func(i int) []byte { return plain[h.Sections[i].Start:h.Sections[i].End] }
		switch rec.Type {
		case nx.ContentRecordProgram:
			if !bytes.Contains(sec(0), []byte("new code")) {
				t.Error("ExeFS not taken from the update")
			}
			if !bytes.Equal(sec(1)[:len(newSec)], newSec) {
				t.Error("patched RomFS differs from the expected updated section")
			}
			if !bytes.Contains(sec(2), logo) {
				t.Error("logo section of the base game missing")
			}
		case nx.ContentRecordControl:
			if !bytes.Contains(sec(0), []byte("v1.2.0")) {
				t.Error("control data not taken from the update")
			}
		}
	}
	for _, e := range n.Entries {
		if strings.HasSuffix(e.Name, ".tik") || e.Name == ncaName(delta) {
			t.Errorf("unexpected entry %s", e.Name)
		}
	}

	// A normally merged NSP (base + update) can be patched on its own, with
	// the same program NCA as a result.
	merged := filepath.Join(dir, "merged.nsp")
	if _, err := Write(context.Background(), sel, merged, Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	mn, err := Analyze(merged, keys)
	if err != nil || mn.Kind != KindBase || mn.BundledUpdate() == nil || mn.BundledUpdate().Meta.Version != 131072 {
		t.Fatalf("merged NSP: %+v %v", mn, err)
	}
	outMerged := filepath.Join(dir, "patched-from-merged.nsp")
	if _, err := WritePatched(context.Background(), []*NSP{mn}, outMerged, Options{Keys: keys, Verify: true}, nil, nil); err != nil {
		t.Fatal(err)
	}
	pm, err := Analyze(outMerged, keys)
	if err != nil || pm.Kind != KindBase || pm.Version != 131072 || len(pm.Titles) != 1 {
		t.Fatalf("patched from merged: %+v %v", pm, err)
	}
	progName := func(n *NSP) string {
		for _, e := range n.Entries {
			if e.Size > 0x10000 {
				return e.Name
			}
		}
		return ""
	}
	if a, b := progName(n), progName(pm); a == "" || a != b {
		t.Errorf("program NCA differs: %s vs %s", a, b)
	}

	// A wrong relocation must be caught by the IVFC check.
	bad := bytes.Clone(newSec)
	dataOff := binary.LittleEndian.Uint64(newFS[0x8+0x10+5*0x18:]) // level 5: RomFS data
	bad[dataOff+0x12345] ^= 1
	if err := verifyIVFC(context.Background(), bytes.NewReader(bad), newFS); err == nil {
		t.Error("IVFC check accepted corrupted data")
	}

	// Patched and compressed in one go.
	outNSZ := filepath.Join(dir, "patched.nsz")
	r2, err := WritePatched(context.Background(), sel, outNSZ, Options{Keys: keys, Compress: true, Verify: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r2.OutputSize >= res.OutputSize {
		t.Errorf("NSZ (%d) not smaller than patched NSP (%d)", r2.OutputSize, res.OutputSize)
	}
	if _, err := os.Stat(strings.TrimSuffix(outNSZ, ".nsz") + ".patched.tmp.nsp"); err == nil {
		t.Error("temporary NSP left behind")
	}
}
