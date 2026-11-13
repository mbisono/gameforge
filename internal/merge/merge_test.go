package merge

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/nx"
)

type testKeys struct {
	header, kak, titlekek []byte
	path                  string
}

func newTestKeys(t *testing.T, dir string) *testKeys {
	k := &testKeys{header: rnd(32), kak: rnd(16), titlekek: rnd(16), path: filepath.Join(dir, "prod.keys")}
	content := fmt.Sprintf("header_key = %x\nkey_area_key_application_00 = %x\ntitlekek_00 = %x\n", k.header, k.kak, k.titlekek)
	if err := os.WriteFile(k.path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return k
}

func rnd(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func ecbEncrypt(key, data []byte) []byte {
	c, _ := aes.NewCipher(key)
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += 16 {
		c.Encrypt(out[i:], data[i:i+16])
	}
	return out
}

// xtsEncrypt mirrors nx's Nintendo XTS for building test fixtures.
func xtsEncrypt(key, data []byte) []byte {
	d, _ := aes.NewCipher(key[:16])
	tw, _ := aes.NewCipher(key[16:])
	out := make([]byte, len(data))
	for sec := 0; sec*0x200 < len(data); sec++ {
		var t [16]byte
		binary.BigEndian.PutUint64(t[8:], uint64(sec))
		tw.Encrypt(t[:], t[:])
		for i := sec * 0x200; i < (sec+1)*0x200; i += 16 {
			var b [16]byte
			for j := range b {
				b[j] = data[i+j] ^ t[j]
			}
			d.Encrypt(b[:], b[:])
			for j := range b {
				out[i+j] = b[j] ^ t[j]
			}
			carry := t[15] >> 7
			for j := 15; j > 0; j-- {
				t[j] = t[j]<<1 | t[j-1]>>7
			}
			t[0] <<= 1
			if carry != 0 {
				t[0] ^= 0x87
			}
		}
	}
	return out
}

func pfs0Bytes(t *testing.T, dir string, files map[string][]byte) []byte {
	var srcs []nx.PFS0Source
	for name, data := range files {
		p := filepath.Join(dir, "src-"+name)
		os.WriteFile(p, data, 0o644)
		srcs = append(srcs, nx.PFS0Source{Name: name, Path: p, Size: int64(len(data))})
	}
	out := filepath.Join(dir, "tmp.pfs0")
	f, _ := os.Create(out)
	if err := nx.WritePFS0(context.Background(), f, srcs, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(out)
	return b
}

// buildMetaNCA creates an encrypted Meta NCA holding a CNMT. If rightsID is
// non-nil the section key is the title key, otherwise it lives in the key area.
func buildMetaNCA(t *testing.T, dir string, k *testKeys, cnmt []byte, rightsID []byte, titleKey []byte) []byte {
	sectionKey := rnd(16)
	if rightsID != nil {
		sectionKey = titleKey
	}

	inner := pfs0Bytes(t, dir, map[string][]byte{"Meta.cnmt": cnmt})
	section := make([]byte, 0x200+len(inner))
	copy(section[0x200:], inner)
	section = append(section, make([]byte, (0x200-len(section)%0x200)%0x200)...)

	hdr := make([]byte, 0xC00)
	copy(hdr[0x200:], "NCA3")
	binary.LittleEndian.PutUint64(hdr[0x208:], uint64(0x4000+len(section)))
	hdr[0x205] = nx.ContentTypeMeta
	if rightsID != nil {
		copy(hdr[0x230:], rightsID)
	}
	copy(hdr[0x300+32:], ecbEncrypt(k.kak, sectionKey))
	binary.LittleEndian.PutUint32(hdr[0x240:], 0x4000/0x200)
	binary.LittleEndian.PutUint32(hdr[0x244:], uint32((0x4000+len(section))/0x200))
	fs := hdr[0x400:]
	fs[2], fs[3], fs[4] = 1, 2, 3 // PartitionFS, HierarchicalSha256, AES-CTR
	upper := rnd(8)
	copy(fs[0x140:], upper)
	binary.LittleEndian.PutUint64(fs[0x40:], 0x200)
	binary.LittleEndian.PutUint64(fs[0x48:], uint64(len(inner)))

	var iv [16]byte
	for i := range 8 {
		iv[i] = upper[7-i]
	}
	binary.BigEndian.PutUint64(iv[8:], 0x4000>>4)
	blk, _ := aes.NewCipher(sectionKey)
	cipher.NewCTR(blk, iv[:]).XORKeyStream(section, section)

	// Like real NCAs, the first section starts at 0x4000.
	return append(append(xtsEncrypt(k.header, hdr), make([]byte, 0x4000-0xC00)...), section...)
}

// buildContentNCA creates an NCA of the given content type with one AES-CTR
// section holding plain. With rightsID the key is the title key.
func buildContentNCA(k *testKeys, contentType byte, plain []byte, rightsID, titleKey []byte) []byte {
	sectionKey := rnd(16)
	if rightsID != nil {
		sectionKey = titleKey
	}
	section := append([]byte(nil), plain...)
	section = append(section, make([]byte, (0x200-len(section)%0x200)%0x200)...)

	hdr := make([]byte, 0xC00)
	copy(hdr[0x200:], "NCA3")
	binary.LittleEndian.PutUint64(hdr[0x208:], uint64(0x4000+len(section)))
	hdr[0x205] = contentType
	if rightsID != nil {
		copy(hdr[0x230:], rightsID)
	}
	copy(hdr[0x300+32:], ecbEncrypt(k.kak, sectionKey))
	binary.LittleEndian.PutUint32(hdr[0x240:], 0x4000/0x200)
	binary.LittleEndian.PutUint32(hdr[0x244:], uint32((0x4000+len(section))/0x200))
	fs := hdr[0x400:]
	fs[2], fs[3], fs[4] = 0, 3, 3 // RomFS, HierarchicalIntegrity, AES-CTR
	upper := rnd(8)
	copy(fs[0x140:], upper)

	var iv [16]byte
	for i := range 8 {
		iv[i] = upper[7-i]
	}
	binary.BigEndian.PutUint64(iv[8:], 0x4000>>4)
	blk, _ := aes.NewCipher(sectionKey)
	cipher.NewCTR(blk, iv[:]).XORKeyStream(section, section)
	// Like real NCAs, the first section starts at 0x4000.
	return append(append(xtsEncrypt(k.header, hdr), make([]byte, 0x4000-0xC00)...), section...)
}

// ncaName names an NCA like the console does: first half of its SHA-256.
func ncaName(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16]) + ".nca"
}

func cnmt(titleID uint64, version uint32, typ uint8, appID uint64) []byte {
	b := make([]byte, 0x30)
	binary.LittleEndian.PutUint64(b[0:], titleID)
	binary.LittleEndian.PutUint32(b[8:], version)
	b[0xC] = typ
	binary.LittleEndian.PutUint64(b[0x20:], appID)
	return b
}

func ticket(rightsID, encTitleKey []byte) []byte {
	b := make([]byte, 0x2C0)
	binary.LittleEndian.PutUint32(b, 0x10004)
	body := b[0x140:]
	copy(body[0x40:], encTitleKey)
	copy(body[0x160:], rightsID)
	return b
}

func writeNSP(t *testing.T, dir, name string, files map[string][]byte) string {
	p := filepath.Join(dir, name)
	os.WriteFile(p, pfs0Bytes(t, t.TempDir(), files), 0o644)
	return p
}

func TestAnalyzeAndMerge(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}

	const app = 0x0100ABCD12340000
	base := writeNSP(t, dir, "Jogo Teste [0100ABCD12340000][v0].nsp", map[string][]byte{
		"aaaa.nca":      rnd(1000),
		"base.cnmt.nca": buildMetaNCA(t, dir, k, cnmt(app, 0, nx.MetaApplication, 0), nil, nil),
	})

	// Update uses title-key crypto with a ticket, and is misnamed on purpose
	// to prove the CNMT wins over the file name.
	rights, _ := hex.DecodeString("0100ABCD123408000000000000000000")
	titleKey := rnd(16)
	upd := writeNSP(t, dir, "whatever.nsp", map[string][]byte{
		"bbbb.nca":     rnd(500),
		"upd.cnmt.nca": buildMetaNCA(t, dir, k, cnmt(app|0x800, 196608, nx.MetaPatch, app), rights, titleKey),
		"rights.tik":   ticket(rights, ecbEncrypt(k.titlekek, titleKey)),
		"rights.cert":  rnd(0x700),
	})
	dlc := writeNSP(t, dir, "Jogo DLC [0100ABCD12341001][v0].nsp", map[string][]byte{
		"cccc.nca":     rnd(300),
		"dlc.cnmt.nca": buildMetaNCA(t, dir, k, cnmt(app+0x1001, 0, nx.MetaAddOnContent, app), nil, nil),
		"rights.cert":  rnd(0x700), // duplicate name, must be skipped
	})

	var nsps []*NSP
	for _, p := range []string{base, upd, dlc} {
		n, err := Analyze(p, keys)
		if err != nil {
			t.Fatal(err)
		}
		if !n.FromCNMT {
			t.Fatalf("%s: CNMT não lido: %s", n.Name(), n.Note)
		}
		nsps = append(nsps, n)
	}
	if nsps[1].Kind != KindUpdate || nsps[1].Version != 196608 || nsps[1].AppID != app {
		t.Fatalf("update mal analisado: %+v", nsps[1])
	}

	games, unknown := Group(nsps)
	if len(games) != 1 || len(unknown) != 0 {
		t.Fatalf("esperado 1 jogo, obtido %d (+%d desconhecidos)", len(games), len(unknown))
	}
	g := games[0]
	sel := g.DefaultSelection()
	if got := g.OutputName(sel, ".nsp"); got != "Jogo Teste [0100ABCD12340000][v196608][1 DLC].nsp" {
		t.Fatalf("nome de saída: %q", got)
	}

	out := filepath.Join(dir, g.OutputName(sel, ".nsp"))
	if _, err := Write(context.Background(), sel, out, Options{Verify: true}, nil, nil); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(out)
	defer f.Close()
	entries, err := nx.ReadPFS0(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 8 {
		t.Fatalf("esperado 8 arquivos no NSP mesclado, obtido %d", len(entries))
	}
	// Contents must be byte-identical to the sources.
	for _, e := range entries {
		got := make([]byte, e.Size)
		f.ReadAt(got, e.Offset)
		for _, n := range nsps {
			for _, s := range n.Entries {
				if s.Name == e.Name {
					want := make([]byte, s.Size)
					sf, _ := os.Open(n.Path)
					sf.ReadAt(want, s.Offset)
					sf.Close()
					if string(got) != string(want) {
						t.Fatalf("conteúdo de %s difere", e.Name)
					}
					goto next
				}
			}
		}
	next:
	}

	// The merged NSP is itself analysable as the base game.
	m, err := Analyze(out, keys)
	if err != nil || m.Kind != KindBase || m.AppID != app {
		t.Fatalf("NSP mesclado: %+v %v", m, err)
	}
}

func TestClassifyTitleID(t *testing.T) {
	for id, want := range map[uint64]Kind{0x0100ABCD12340000: KindBase, 0x0100ABCD12340800: KindUpdate, 0x0100ABCD12341001: KindDLC, 0x0100ABCD12341FFF: KindDLC} {
		k, app := ClassifyTitleID(id)
		if k != want || app != 0x0100ABCD12340000 {
			t.Errorf("%016X: %v %016X", id, k, app)
		}
	}
}

func TestAnalyzeWithoutKeys(t *testing.T) {
	dir := t.TempDir()
	p := writeNSP(t, dir, "Jogo [0100ABCD12340800][v65536].nsp", map[string][]byte{"x.nca": rnd(10)})
	n, err := Analyze(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != KindUpdate || n.Version != 65536 || n.AppID != 0x0100ABCD12340000 || n.FromCNMT {
		t.Fatalf("%+v", n)
	}
}

func TestGroupKeepsAllFiles(t *testing.T) {
	const app = 0x0100ABCD12340000
	mk := func(name string, kind Kind, id uint64, v uint32) *NSP {
		return &NSP{Path: name, Kind: kind, TitleID: id, AppID: app, Version: v}
	}
	nsps := []*NSP{
		mk("upd2", KindUpdate, app|0x800, 131072),
		mk("base", KindBase, app, 0),
		mk("upd1", KindUpdate, app|0x800, 65536),
		mk("dlc1-new", KindDLC, app+0x1001, 65536),
		mk("dlc1-old", KindDLC, app+0x1001, 0),
		mk("dlc2", KindDLC, app+0x1002, 0),
	}
	games, _ := Group(nsps)
	if len(games) != 1 || len(games[0].Files()) != 6 {
		t.Fatalf("todos os arquivos devem ser mantidos: %+v", games)
	}
	var got []string
	for _, n := range games[0].DefaultSelection() {
		got = append(got, n.Path)
	}
	if fmt.Sprint(got) != "[base upd2 dlc1-new dlc2]" {
		t.Fatalf("seleção padrão: %v", got)
	}
}

// A merged NSP merged again with a newer update keeps only the newer one.
func TestMergedNSP(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	const app = 0x0100ABCD12340000
	rights, _ := hex.DecodeString("0100ABCD123408000000000000000000")
	titleKey := rnd(16)
	tik := ticket(rights, ecbEncrypt(k.titlekek, titleKey))
	ncas := map[string][]byte{}
	nsp := func(name string, titleID uint64, v uint32, typ uint8, extra map[string][]byte) *NSP {
		prog := buildContentNCA(k, nx.ContentTypeProgram, rnd(700), nil, nil)
		ncas[name] = prog
		files := map[string][]byte{
			ncaName(prog): prog,
			name + ".cnmt.nca": buildMetaNCA(t, dir, k, cnmtWith(titleID, v, typ, app,
				record(prog, nx.ContentRecordProgram)), nil, nil),
		}
		for n, b := range extra {
			files[n] = b
		}
		n, err := Analyze(writeNSP(t, dir, name+".nsp", files), keys)
		if err != nil || !n.FromCNMT {
			t.Fatalf("%s: %v", name, err)
		}
		return n
	}
	base := nsp("base", app, 0, nx.MetaApplication, nil)
	upd1 := nsp("upd1", app|0x800, 65536, nx.MetaPatch, map[string][]byte{"0100abcd123408000000000000000000.tik": tik})
	upd2 := nsp("upd2", app|0x800, 131072, nx.MetaPatch, map[string][]byte{"0100abcd123408000000000000000000.tik": rnd(len(tik))})
	dlc := nsp("dlc", app+0x1001, 0, nx.MetaAddOnContent, nil)

	merge := func(name string, sel ...*NSP) (*NSP, []string) {
		var logs []string
		p := filepath.Join(dir, name)
		if _, err := Write(context.Background(), sel, p, Options{Verify: true}, nil, func(s fmt.Stringer) { logs = append(logs, s.String()) }); err != nil {
			t.Fatal(err)
		}
		n, err := Analyze(p, keys)
		if err != nil {
			t.Fatal(err)
		}
		return n, logs
	}
	has := func(n *NSP, nca []byte) bool {
		for _, e := range n.Entries {
			if e.Name == ncaName(nca) {
				return true
			}
		}
		return false
	}
	tikOf := func(n *NSP) []byte {
		f, _ := os.Open(n.Path)
		defer f.Close()
		for _, e := range n.Entries {
			if strings.HasSuffix(e.Name, ".tik") {
				b := make([]byte, e.Size)
				f.ReadAt(b, e.Offset)
				return b
			}
		}
		return nil
	}

	m1, _ := merge("m1.nsp", base, upd1, dlc)
	if m1.Kind != KindBase || len(m1.Bundled()) != 2 || m1.BundledUpdate().Meta.Version != 65536 {
		t.Fatalf("merged NSP: %+v", m1)
	}
	games, _ := Group([]*NSP{m1, upd1})
	if got := names(games[0].DefaultSelection()); got != "[m1.nsp]" {
		t.Fatalf("same update already inside: %v", got)
	}
	if v, _, _ := games[0].UpdateVersion(); v != 65536 || games[0].DLCCount() != 1 {
		t.Fatalf("game summary: v%d, %d DLC", v, games[0].DLCCount())
	}
	games, _ = Group([]*NSP{m1, upd2})
	sel := games[0].DefaultSelection()
	if got := names(sel); got != "[m1.nsp upd2.nsp]" {
		t.Fatalf("newer update: %v", got)
	}
	if got := games[0].OutputName(sel, ".nsp"); !strings.Contains(got, "[v131072][1 DLC]") {
		t.Fatalf("output name: %s", got)
	}

	// The older update inside m1 is replaced, ticket included; base and DLC stay.
	m2, logs := merge("m2.nsp", m1, upd2)
	if has(m2, ncas["upd1"]) || !has(m2, ncas["upd2"]) || !has(m2, ncas["base"]) || !has(m2, ncas["dlc"]) {
		t.Fatalf("contents: %v", m2.Entries)
	}
	if len(m2.Titles) != 3 || m2.BundledUpdate().Meta.Version != 131072 || !bytes.Equal(tikOf(m2), tikOf(upd2)) {
		t.Fatalf("titles/ticket of m2 wrong: %d titles", len(m2.Titles))
	}
	if !strings.Contains(strings.Join(logs, "\n"), "m1.nsp") {
		t.Errorf("replacement not logged: %q", logs)
	}

	// An older update file is left out when the merged NSP has a newer one.
	m3, logs := merge("m3.nsp", m2, upd1)
	if has(m3, ncas["upd1"]) || !has(m3, ncas["upd2"]) || !strings.Contains(strings.Join(logs, "\n"), "upd1.nsp") {
		t.Fatalf("older file not left out: %v %q", m3.Entries, logs)
	}
}

func names(l []*NSP) string {
	var s []string
	for _, n := range l {
		s = append(s, n.Name())
	}
	return fmt.Sprint(s)
}

func TestDuplicates(t *testing.T) {
	const app = 0x0100ABCD12340000
	mk := func(name string, kind Kind, id uint64, v uint32, files ...string) *NSP {
		n := &NSP{Path: name, Kind: kind, TitleID: id, AppID: app, Version: v}
		for _, f := range files {
			n.Entries = append(n.Entries, nx.PFS0Entry{Name: f})
		}
		return n
	}
	updA := mk("updA", KindUpdate, app|0x800, 65536, "aa.nca", "bb.cnmt.nca", "x.tik")
	updB := mk("updB", KindUpdate, app|0x800, 65536, "BB.cnmt.nca", "aa.nca") // same NCAs, other order and case
	nsz := mk("updC", KindUpdate, app|0x800, 65536, "aa.ncz", "bb.cnmt.nca")  // compressed copy
	other := mk("old", KindUpdate, app|0x800, 0, "cc.nca")
	games, _ := Group([]*NSP{updA, updB, nsz, other})
	g := games[0]
	orig := g.LatestUpdate() // picked by the default selection, so the original
	for _, n := range []*NSP{updA, updB, nsz} {
		if want := map[bool]*NSP{true: nil, false: orig}[n == orig]; g.DuplicateOf(n) != want {
			t.Errorf("%s: duplicate of %v, want %v", n.Path, g.DuplicateOf(n), want)
		}
	}
	if g.DuplicateOf(other) != nil {
		t.Error("different update counted as a copy")
	}
}

func TestPatchedBase(t *testing.T) {
	const app = 0x0100ABCD12340000
	mk := func(name string, kind Kind, id uint64, v uint32) *NSP {
		return &NSP{Path: name, Kind: kind, TitleID: id, AppID: app, Version: v}
	}
	base, patched := mk("base", KindBase, app, 0), mk("patched", KindBase, app, 131072)
	upd, dlc := mk("upd", KindUpdate, app|0x800, 196608), mk("dlc", KindDLC, app+0x1001, 0)

	// The original base wins over a newer one with an applied update.
	games, _ := Group([]*NSP{patched, upd, base, dlc})
	if got := names(games[0].DefaultSelection()); got != "[base upd dlc]" {
		t.Fatalf("com a base original: %v", got)
	}
	// Without it, the patched base is used, but no update goes with it.
	games, _ = Group([]*NSP{patched, upd, dlc})
	if got := names(games[0].DefaultSelection()); got != "[patched dlc]" {
		t.Fatalf("só a base atualizada: %v", got)
	}
	if checkSelection([]*NSP{patched, dlc}) != nil || checkSelection([]*NSP{base, upd}) != nil {
		t.Fatal("seleção válida recusada")
	}
	if checkSelection([]*NSP{patched, upd}) == nil {
		t.Fatal("base atualizada + atualização deveria ser recusada")
	}
}

func TestMergeNSZ(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	const app = 0x0100ABCD12340000
	text := []byte(strings.Repeat("compressible game data 0123456789 ", 8000))

	baseProg := buildContentNCA(k, nx.ContentTypeProgram, text, nil, nil)
	rights, _ := hex.DecodeString("0100ABCD123408000000000000000000")
	titleKey := rnd(16)
	updProg := buildContentNCA(k, nx.ContentTypeProgram, text[:len(text)/2], rights, titleKey)
	delta := buildContentNCA(k, nx.ContentTypeData, text[:0x5000], rights, titleKey)
	control := buildContentNCA(k, nx.ContentTypeControl, text[:0x5000], nil, nil)

	base := writeNSP(t, dir, "Jogo [0100ABCD12340000][v0].nsp", map[string][]byte{
		ncaName(baseProg): baseProg,
		ncaName(control):  control,
		"base.cnmt.nca":   buildMetaNCA(t, dir, k, cnmt(app, 0, nx.MetaApplication, 0), nil, nil),
	})
	upd := writeNSP(t, dir, "Jogo [0100ABCD12340800][v65536].nsp", map[string][]byte{
		ncaName(updProg): updProg,
		ncaName(delta):   delta,
		"upd.cnmt.nca":   buildMetaNCA(t, dir, k, cnmt(app|0x800, 65536, nx.MetaPatch, app), rights, titleKey),
		"rights.tik":     ticket(rights, ecbEncrypt(k.titlekek, titleKey)),
	})
	var sel []*NSP
	for _, p := range []string{base, upd} {
		n, err := Analyze(p, keys)
		if err != nil {
			t.Fatal(err)
		}
		sel = append(sel, n)
	}

	out := filepath.Join(dir, "out.nsz")
	var logs []string
	res, err := Write(context.Background(), sel, out, Options{Keys: keys, Compress: true, Verify: true}, nil,
		func(s fmt.Stringer) { logs = append(logs, s.String()) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Compressed != 2 || res.Dropped != 1 {
		t.Fatalf("compressed %d, dropped %d; want 2, 1 (log: %q)", res.Compressed, res.Dropped, logs)
	}
	if res.OutputSize*2 > res.InputSize {
		t.Fatalf("NSZ not smaller: %d -> %d", res.InputSize, res.OutputSize)
	}
	for _, l := range logs {
		if strings.Contains(l, "⚠") {
			t.Errorf("unexpected warning: %s", l)
		}
	}

	f, _ := os.Open(out)
	defer f.Close()
	entries, err := nx.ReadPFS0(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, want := range []string{
		strings.TrimSuffix(ncaName(baseProg), "a") + "z",
		strings.TrimSuffix(ncaName(updProg), "a") + "z",
		ncaName(control), "base.cnmt.nca", "upd.cnmt.nca", "rights.tik",
	} {
		if !names[want] {
			t.Errorf("missing %s in %v", want, names)
		}
	}
	if names[ncaName(delta)] || len(entries) != 6 {
		t.Errorf("delta fragment must be dropped: %v", names)
	}

	// Without compression the NSZ goes back to a plain NSP whose NCAs are
	// byte-identical to the originals.
	n, err := Analyze(out, keys)
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "back.nsp")
	res2, err := Write(context.Background(), []*NSP{n}, plain, Options{Verify: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Decompressed != 2 {
		t.Fatalf("decompressed %d, want 2", res2.Decompressed)
	}
	pf, _ := os.Open(plain)
	defer pf.Close()
	back, err := nx.ReadPFS0(pf, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{}
	for _, e := range back {
		b := make([]byte, e.Size)
		pf.ReadAt(b, e.Offset)
		got[e.Name] = b
	}
	for _, orig := range [][]byte{baseProg, updProg, control} {
		if !bytes.Equal(got[ncaName(orig)], orig) {
			t.Errorf("%s not restored byte for byte", ncaName(orig))
		}
	}
	if len(back) != 6 {
		t.Errorf("restored NSP has %d entries, want 6", len(back))
	}
}
