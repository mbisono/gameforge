// Package n64 reads the header of Nintendo 64 ROMs and writes ROMs dumped
// in another byte order as .z64, the cartridge's own (big-endian) order,
// which No-Intro lists.
//
// The first four bytes tell the order: 80 37 12 40 in a .z64, 37 80 40 12
// in a .v64 (bytes swapped in pairs, from the Doctor V64) and 40 12 37 80
// in an .n64 (4-byte words reversed).
package n64

import (
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"strings"
)

// Order is the byte order of a ROM.
type Order int

const (
	Unknown Order = iota
	Z64           // big-endian, as the cartridge
	V64           // bytes swapped in pairs
	N64           // 4-byte words reversed
)

func (o Order) String() string {
	return [...]string{"?", ".z64", ".v64", ".n64"}[o]
}

// OrderOf tells the byte order from the first four bytes of a ROM.
func OrderOf(head []byte) Order {
	switch {
	case bytes.HasPrefix(head, []byte{0x80, 0x37, 0x12, 0x40}):
		return Z64
	case bytes.HasPrefix(head, []byte{0x37, 0x80, 0x40, 0x12}):
		return V64
	case bytes.HasPrefix(head, []byte{0x40, 0x12, 0x37, 0x80}):
		return N64
	}
	return Unknown
}

// ToZ64 puts b, which starts at a multiple of 4 bytes into the ROM, in the
// cartridge's order.
func ToZ64(b []byte, o Order) {
	switch o {
	case V64:
		for i := 0; i+1 < len(b); i += 2 {
			b[i], b[i+1] = b[i+1], b[i]
		}
	case N64:
		for i := 0; i+3 < len(b); i += 4 {
			b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
		}
	}
}

// ErrNotN64 is returned for a file that is not a Nintendo 64 ROM.
var ErrNotN64 = errors.New("not a Nintendo 64 ROM")

// Info is what the header tells about a ROM.
type Info struct {
	Order   Order
	Title   string // the header's title, e.g. "SUPER MARIO 64"
	Code    string // game code, e.g. "NSME"
	Version int    // 0 for the first release, 1 for Rev 1…
}

// Read reads the header of the ROM at path.
func Read(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	h := make([]byte, 0x40)
	if _, err := io.ReadFull(f, h); err != nil {
		return Info{}, ErrNotN64
	}
	return Parse(h)
}

// Parse reads the first 0x40 bytes of a ROM, in any byte order.
func Parse(h []byte) (Info, error) {
	o := OrderOf(h)
	if o == Unknown || len(h) < 0x40 {
		return Info{}, ErrNotN64
	}
	h = bytes.Clone(h[:0x40])
	ToZ64(h, o)
	return Info{
		Order:   o,
		Title:   text(h[0x20:0x34]),
		Code:    text(h[0x3B:0x3F]),
		Version: int(h[0x3F]),
	}, nil
}

// text is the printable ASCII of a header field (Japanese titles are in
// Shift JIS, which is left out), with spaces trimmed.
func text(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7F {
			s.WriteByte(c)
		} else {
			s.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(s.String()), " ")
}

// Convert writes the ROM at src to dst (which must not exist) in the
// cartridge's byte order, then reads it back. It returns the size written.
// On an error, dst is removed.
func Convert(ctx context.Context, src, dst string, progress func(done, total int64)) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return 0, err
	}
	size := st.Size()
	if size%4 != 0 {
		return 0, errors.New("the ROM's size is not a multiple of 4 bytes")
	}
	head := make([]byte, 4)
	if _, err := in.ReadAt(head, 0); err != nil {
		return 0, ErrNotN64
	}
	o := OrderOf(head)
	if o == Unknown {
		return 0, ErrNotN64
	}
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(dst)
		}
	}()
	var n int64
	step := func(k int) error {
		n += int64(k)
		if progress != nil {
			progress(n, 2*size)
		}
		return ctx.Err()
	}
	buf := make([]byte, 1<<20)
	h := crc32.NewIEEE()
	for {
		k, err := io.ReadFull(in, buf)
		ToZ64(buf[:k], o)
		if _, werr := out.Write(buf[:k]); werr != nil {
			return 0, werr
		}
		h.Write(buf[:k])
		if serr := step(k); serr != nil {
			return 0, serr
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	want := h.Sum32()
	if err := out.Sync(); err != nil {
		return 0, err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	h.Reset()
	got, err := io.CopyBuffer(writerFunc(func(p []byte) (int, error) {
		h.Write(p)
		return len(p), step(len(p))
	}), out, buf)
	if err != nil {
		return 0, err
	}
	if got != size || h.Sum32() != want {
		return 0, errors.New("the new file differs from what was written")
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	os.Chtimes(dst, st.ModTime(), st.ModTime())
	ok = true
	return size, nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
