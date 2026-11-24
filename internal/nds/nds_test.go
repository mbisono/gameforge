package nds

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// rom makes a ROM of a 1 MiB cartridge (8 << 17) whose game uses used
// bytes, with a banner, filled with 0xFF to size.
func rom(used, size int, dsi bool) []byte {
	b := bytes.Repeat([]byte{0xFF}, size)
	for i := 0; i < used; i++ {
		b[i] = byte(i * 7)
	}
	clear(b[:headerLen])
	copy(b, "TESTGAME\x00\x00\x00\x00ATSE01")
	b[0x14] = 3
	if dsi {
		b[0x12] = 2
		binary.LittleEndian.PutUint32(b[0x80:], uint32(used-0x1000))
		binary.LittleEndian.PutUint32(b[0x210:], uint32(used))
	} else {
		binary.LittleEndian.PutUint32(b[0x80:], uint32(used))
	}
	banner := 0x1000
	binary.LittleEndian.PutUint32(b[0x68:], uint32(banner))
	title := utf16.Encode([]rune("Test Game\nSubtitle\nPublisher"))
	en := b[banner+titleAt+0x100 : banner+titleAt+0x200]
	clear(en)
	for i, c := range title {
		binary.LittleEndian.PutUint16(en[2*i:], c)
	}
	binary.LittleEndian.PutUint16(b[0x15E:], CRC16(b[:0x15E]))
	return b
}

func TestRead(t *testing.T) {
	b := rom(300000, 1<<20, false)
	info, err := ReadAt(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	want := Info{Code: "ATSE", Maker: "01", Header: "TESTGAME", Title: "Test Game - Subtitle",
		Size: 1 << 20, Used: 300000, Capacity: 1 << 20}
	if info != want {
		t.Fatalf("got %+v\nwant %+v", info, want)
	}
	if info.Trimmed() {
		t.Error("a full ROM counts as trimmed")
	}
	b[0x20] ^= 1 // the header's CRC no longer matches
	if _, err := ReadAt(bytes.NewReader(b), int64(len(b))); !errors.Is(err, ErrNotDS) {
		t.Errorf("a broken header: %v", err)
	}
}

func TestTrim(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dsi, sig bool
		keep     int
	}{
		{"plain", false, false, 300000},
		{"signature", false, true, 300000 + sigLen},
		{"dsi", true, false, 300000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := rom(300000, 1<<20, tc.dsi)
			if tc.sig {
				copy(full[300000:], bytes.Repeat([]byte{0x5A}, sigLen))
			}
			path := filepath.Join(t.TempDir(), "game.nds")
			os.WriteFile(path, full, 0o644)
			res, err := Trim(context.Background(), path, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if res.Before != 1<<20 || res.After != int64(tc.keep) || !bytes.Equal(got, full[:tc.keep]) {
				t.Fatalf("trimmed %+v to %d bytes", res, len(got))
			}
			info, err := Read(path)
			if err != nil || !info.Trimmed() {
				t.Fatalf("after trimming: %+v, %v", info, err)
			}
			// The CRC of the full ROM follows from the trimmed one's.
			if PaddedCRC(crc32.ChecksumIEEE(got), int64(len(got)), 1<<20) != crc32.ChecksumIEEE(full) {
				t.Error("PaddedCRC differs")
			}
			if res, _ := Trim(context.Background(), path, nil); res.Before != res.After {
				t.Errorf("trimmed twice: %+v", res)
			}
			if _, err := Untrim(context.Background(), path, nil); err != nil {
				t.Fatal(err)
			}
			got, _ = os.ReadFile(path)
			if !bytes.Equal(got, full) {
				t.Fatal("untrimmed ROM differs from the original")
			}
		})
	}
}

func TestTrimKeepsData(t *testing.T) {
	full := rom(300000, 1<<20, false)
	full[900000] = 0
	path := filepath.Join(t.TempDir(), "game.nds")
	os.WriteFile(path, full, 0o644)
	if _, err := Trim(context.Background(), path, nil); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("got %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, full) {
		t.Fatal("the ROM changed")
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp")); len(m) > 0 {
		t.Fatalf("left %v", m)
	}
}
