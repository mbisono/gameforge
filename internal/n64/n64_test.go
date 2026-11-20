package n64

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// rom makes a .z64 ROM of n bytes with a header.
func rom(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*13 + i>>8)
	}
	copy(b, []byte{0x80, 0x37, 0x12, 0x40})
	copy(b[0x20:0x34], "SUPER MARIO 64      ")
	copy(b[0x3B:], "NSME\x01")
	return b
}

// reorder puts a .z64 ROM in another byte order (each order is its own
// inverse).
func reorder(b []byte, o Order) []byte {
	b = bytes.Clone(b)
	ToZ64(b, o)
	return b
}

func TestParse(t *testing.T) {
	z := rom(0x1000)
	for _, o := range []Order{Z64, V64, N64} {
		info, err := Parse(reorder(z, o))
		want := Info{Order: o, Title: "SUPER MARIO 64", Code: "NSME", Version: 1}
		if err != nil || info != want {
			t.Errorf("%v: got %+v, %v", o, info, err)
		}
	}
	if _, err := Parse(make([]byte, 0x40)); err != ErrNotN64 {
		t.Errorf("zeros: %v", err)
	}
}

func TestConvert(t *testing.T) {
	z := rom(3<<20 + 0x400) // more than one buffer, and a partial one
	dir := t.TempDir()
	for _, o := range []Order{V64, N64} {
		src := filepath.Join(dir, "game"+o.String())
		dst := filepath.Join(dir, "game"+o.String()+".z64")
		os.WriteFile(src, reorder(z, o), 0o644)
		n, err := Convert(context.Background(), src, dst, nil)
		if err != nil || n != int64(len(z)) {
			t.Fatalf("%v: %d, %v", o, n, err)
		}
		got, _ := os.ReadFile(dst)
		if !bytes.Equal(got, z) {
			t.Fatalf("%v: converted ROM differs", o)
		}
		// The target is never written over.
		if _, err := Convert(context.Background(), src, dst, nil); err == nil {
			t.Fatalf("%v: wrote over the target", o)
		}
		if got, _ := os.ReadFile(dst); !bytes.Equal(got, z) {
			t.Fatalf("%v: target changed", o)
		}
	}
}
