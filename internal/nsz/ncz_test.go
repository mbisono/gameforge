package nsz

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"gameforge/internal/nx"
)

func ctrEncrypt(key []byte, upper [8]byte, gen *uint32, off int64, b []byte) {
	blk, _ := aes.NewCipher(key)
	var c [16]byte
	for i := range 8 {
		c[i] = upper[7-i]
	}
	if gen != nil {
		binary.BigEndian.PutUint32(c[4:8], *gen)
	}
	xorCTR(blk, c, off, b)
}

// buildNCA returns an NCA (header area left random) with a CTR section, a
// plain section and a BKTR section with three counter generations, all
// holding highly compressible plaintext.
func buildNCA(t *testing.T) ([]byte, *nx.NCAHeader, []byte) {
	key := make([]byte, 16)
	rand.Read(key)
	plain := []byte(strings.Repeat("Nintendo Switch backup test data. ", 20000))

	const (
		s0, s1, s2 = 0xC00, 0x8000, 0x20000
		bktrData   = 0xC000
		tableSize  = 0x8000
	)
	size := int64(s2 + bktrData + tableSize)
	nca := make([]byte, size)
	rand.Read(nca[:s0])
	copy(nca[s0:], plain)

	h := &nx.NCAHeader{}
	var up0, up2 [8]byte
	rand.Read(up0[:])
	rand.Read(up2[:])
	h.Sections[0] = nx.NCASection{Start: s0, End: s1, EncType: 3, UpperIV: up0}
	h.Sections[1] = nx.NCASection{Start: s1, End: s2, EncType: 1}
	// Listed out of order on purpose: sections are sorted by offset.
	h.Sections[3] = nx.NCASection{Start: s2, End: size, EncType: 4, UpperIV: up2, CtrExOffset: bktrData, CtrExSize: tableSize}

	ctrEncrypt(key, up0, nil, s0, nca[s0:s1])

	type ent struct {
		off int64
		gen uint32
	}
	ents := []ent{{0, 1}, {0x4000, 2}, {0x9000, 5}}
	for i, e := range ents {
		end := int64(bktrData)
		if i+1 < len(ents) {
			end = ents[i+1].off
		}
		g := e.gen
		ctrEncrypt(key, up2, &g, s2+e.off, nca[s2+e.off:s2+end])
	}
	table := make([]byte, tableSize)
	binary.LittleEndian.PutUint32(table[4:], 1) // one entry set
	set := table[0x4000:]
	binary.LittleEndian.PutUint32(set[4:], uint32(len(ents)))
	binary.LittleEndian.PutUint64(set[8:], bktrData)
	for i, e := range ents {
		binary.LittleEndian.PutUint64(set[0x10+i*0x10:], uint64(e.off))
		binary.LittleEndian.PutUint32(set[0x10+i*0x10+12:], e.gen)
	}
	ctrEncrypt(key, up2, nil, s2+bktrData, table)
	copy(nca[s2+bktrData:], table)
	return nca, h, key
}

func TestCompressRoundTrip(t *testing.T) {
	nca, h, key := buildNCA(t)
	r := bytes.NewReader(nca)
	size := int64(len(nca))

	secs, ok, err := Sections(r, 0, size, h, key)
	if err != nil || !ok {
		t.Fatalf("Sections: ok=%v err=%v", ok, err)
	}
	if len(secs) != 1+1+3+1 { // CTR, plain, 3 BKTR subsections + table tail
		t.Fatalf("expected 6 sections, got %d", len(secs))
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "x.ncz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const pos = 100 // NCZ inside a container, not at offset 0
	n, sum, err := Compress(context.Background(), f, pos, r, 0, size, secs,
		Options{Level: zstd.SpeedDefault, BlockSizeExp: 14, Workers: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum != sha256.Sum256(nca) {
		t.Fatal("source hash mismatch")
	}
	// Everything but the 0x4000 header and the BKTR table is compressible;
	// this only holds if every subsection was decrypted with its own counter.
	if n > HeaderSize+0x8000+0x2000 {
		t.Fatalf("compressed to %d bytes (from %d): BKTR subsections not decrypted?", n, size)
	}

	var out bytes.Buffer
	if err := Decompress(context.Background(), f, pos, n, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), nca) {
		t.Fatal("decompressed NCA differs from the original")
	}
}

func TestSolidDecompress(t *testing.T) {
	nca, h, key := buildNCA(t)
	secs, _, _ := Sections(bytes.NewReader(nca), 0, int64(len(nca)), h, key)

	plain := append([]byte(nil), nca[HeaderSize:]...)
	applyCTR(secs, HeaderSize, plain)
	enc, _ := zstd.NewWriter(nil)
	ncz := append(append(append([]byte(nil), nca[:HeaderSize]...), encodeSections(secs)...), enc.EncodeAll(plain, nil)...)

	var out bytes.Buffer
	if err := Decompress(context.Background(), bytes.NewReader(ncz), 0, int64(len(ncz)), &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), nca) {
		t.Fatal("solid NCZ not restored")
	}
}

func TestSectionsRejectsUnsupported(t *testing.T) {
	nca, h, key := buildNCA(t)
	r := bytes.NewReader(nca)
	size := int64(len(nca))

	gap := *h
	gap.Sections[1].Start += 0x200
	if _, ok, _ := Sections(r, 0, size, &gap, key); ok {
		t.Error("NCA with a gap between sections must not be compressed")
	}
	xts := *h
	xts.Sections[1].EncType = 2
	if _, ok, _ := Sections(r, 0, size, &xts, key); ok {
		t.Error("XTS sections must not be compressed")
	}
	if _, ok, _ := Sections(r, 0, size+1, h, key); ok {
		t.Error("sections must end at the NCA size")
	}
}
