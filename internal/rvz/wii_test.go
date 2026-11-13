package rvz

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// wiiDisc builds a Wii disc with one encrypted partition of n blocks,
// encrypted with a title key that commonKey decrypts from the ticket. The
// partition holds a file system, files and generator padding; a few stored
// hashes are altered so that the RVZ needs hash exceptions. The disc after
// the partition is padding too.
func wiiDisc(rng *rand.Rand, commonKey []byte, n int) []byte {
	const partOff, dataOff = 0x50000, 0x20000
	dataStart := int64(partOff + dataOff)
	size := dataStart + int64(n)*wiiBlock + 0x100000
	img := make([]byte, size)
	copy(img, "RTST01")
	binary.BigEndian.PutUint32(img[0x18:], wiiMagic)
	copy(img[0x20:], "Wii Test Game")
	// Partition table: one group of one partition.
	binary.BigEndian.PutUint32(img[0x40000:], 1)
	binary.BigEndian.PutUint32(img[0x40004:], 0x40020>>2)
	binary.BigEndian.PutUint32(img[0x40020:], partOff>>2)
	// Ticket.
	ph := img[partOff:]
	binary.BigEndian.PutUint32(ph, 0x10001)
	titleKey, titleID := make([]byte, 16), []byte("\x00\x01\x00\x00RTST")
	for i := range titleKey {
		titleKey[i] = byte(rng.Uint32())
	}
	iv := make([]byte, 16)
	copy(iv, titleID)
	cbcEncrypt(commonKey, iv, titleKey, ph[0x1bf:0x1cf])
	copy(ph[0x1dc:], titleID)
	binary.BigEndian.PutUint32(ph[0x2b8:], dataOff>>2)
	binary.BigEndian.PutUint32(ph[0x2bc:], uint32(int64(n)*wiiBlock>>2))
	for i := 0x2c0; i < dataOff; i++ {
		ph[i] = byte(rng.IntN(4)) // TMD, certificates, H3 (content does not matter)
	}

	// Decrypted partition data: header, FST, files, padding.
	dec := make([]byte, n*wiiData)
	binary.BigEndian.PutUint32(dec[0x18:], wiiMagic)
	seeds := map[int64][]byte{}
	pad := func(buf []byte, base, from, to int64) { // generator padding over [from, to)
		for b := from &^ (blockBytes - 1); b < to; b += blockBytes {
			s, e := max(from, b), min(to, b+blockBytes)
			seed, ok := seeds[b+base*7]
			if !ok {
				seed = make([]byte, SeedSize)
				for i := range seed {
					seed[i] = byte(rng.Uint32())
				}
				seeds[b+base*7] = seed
			}
			tmp := make([]byte, blockBytes)
			junk(seed, b, tmp)
			copy(buf[s-base:e-base], tmp[s-b:e-b])
		}
	}
	type f struct{ off, size int64 }
	var files []f
	pos := int64(0x20000)
	for i := 0; pos < int64(len(dec))-300000; i++ {
		sz := int64(500 + rng.IntN(200000))
		files = append(files, f{pos, sz})
		for k := pos; k < pos+sz; k++ {
			if i%3 == 0 {
				dec[k] = byte(rng.Uint32())
			} else {
				dec[k] = byte(rng.IntN(6))
			}
		}
		next := (pos + sz + 32 + 0x7fff) &^ 0x7fff
		next += int64(rng.IntN(3)) * blockBytes
		next = min(next, int64(len(dec)))
		pad(dec, 0, pos+sz+32, next)
		pos = next
	}
	pad(dec, 0, pos, int64(len(dec)))
	fstOff := int64(0x2440)
	fst := make([]byte, 12*(len(files)+1))
	fst[0] = 1
	binary.BigEndian.PutUint32(fst[8:], uint32(len(files)+1))
	for i, x := range files {
		e := fst[12*(i+1):]
		binary.BigEndian.PutUint32(e[4:], uint32(x.off>>2))
		binary.BigEndian.PutUint32(e[8:], uint32(x.size))
	}
	copy(dec[fstOff:], fst)
	binary.BigEndian.PutUint32(dec[0x424:], uint32(fstOff>>2))
	binary.BigEndian.PutUint32(dec[0x428:], uint32(len(fst)>>2+1))

	// Hash and encrypt, 2 MiB at a time, with a few altered hashes.
	for g := 0; g*blocksInGroup < n; g++ {
		group := make([]byte, blocksInGroup*wiiData)
		copy(group, dec[g*blocksInGroup*wiiData:])
		hashes := hashGroup(group)
		if g == 0 {
			hashes[5][3*20] ^= 0xff  // an H0 hash
			hashes[7][0x325] = 0x42  // H1 padding
			hashes[9][0x26c+3] = 0x7 // H0 padding
		}
		for b := 0; b < blocksInGroup && g*blocksInGroup+b < n; b++ {
			at := dataStart + int64(g*blocksInGroup+b)*wiiBlock
			encryptBlock(titleKey, hashes[b][:], group[b*wiiData:(b+1)*wiiData], img[at:at+wiiBlock])
		}
	}
	// Padding after the partition, and some in the disc header area.
	end := dataStart + int64(n)*wiiBlock
	tail := make([]byte, size-end)
	pad(tail, end, end, size)
	copy(img[end:], tail)
	return img
}

func TestWiiRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	ck := make([]byte, 16)
	for i := range ck {
		ck[i] = byte(rng.Uint32())
	}
	dir := t.TempDir()
	img := wiiDisc(rng, ck, 100) // two 2 MiB groups, the second partial
	in := filepath.Join(dir, "wii.iso")
	os.WriteFile(in, img, 0o644)
	want := sha1.Sum(img)

	out := filepath.Join(dir, "wii.rvz")
	res, err := Compress(context.Background(), in, out, Options{CommonKeys: [][]byte{ck}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Wii || res.Partitions != 1 || res.SHA1 != want {
		t.Fatalf("result %+v", res)
	}
	t.Logf("%d -> %d bytes", len(img), res.OutputSize)
	if res.OutputSize > int64(len(img))/3 {
		t.Errorf("not compressed enough: %d -> %d", len(img), res.OutputSize)
	}
	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Wii() {
		t.Error("not marked as Wii")
	}
	r.Close()
	back := filepath.Join(dir, "back.iso")
	if _, err := Decompress(context.Background(), out, back, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(back)
	if !bytes.Equal(got, img) {
		for i := range got {
			if got[i] != img[i] {
				t.Fatalf("decompressed image differs from byte %#x", i)
			}
		}
		t.Fatal("decompressed image differs in size")
	}

	// Without the key, and with a wrong one.
	if _, err := Compress(context.Background(), in, filepath.Join(dir, "a.rvz"), Options{}, nil); !errors.Is(err, ErrNoCommonKey) {
		t.Errorf("without key: %v", err)
	}
	bad := bytes.Clone(ck)
	bad[0] ^= 1
	if _, err := Compress(context.Background(), in, filepath.Join(dir, "b.rvz"), Options{CommonKeys: [][]byte{bad}}, nil); !errors.Is(err, ErrWrongCommonKey) {
		t.Errorf("wrong key: %v", err)
	}
}

func TestHashFields(t *testing.T) {
	// 31 H0 + pad0 + 8 H1 + 2 for pad1 + 8 H2 + 2 for pad2.
	if len(hashFields) != 31+1+8+2+8+2 || hashFields[len(hashFields)-1] != 0x400-20 {
		t.Fatalf("%v", hashFields)
	}
}
