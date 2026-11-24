// Package nds reads the header and banner of Nintendo DS ROMs, and trims
// them (cuts the empty space after the game) or restores their full size.
//
// A DS cartridge's size is a power of two (128 KiB << the header's byte at
// 0x14), and the ROM is usually smaller: the header tells at 0x80 how much
// of it is used (at 0x210 for games with DSi features, whose DSi part comes
// after), and the rest is filled with 0xFF. Download Play games keep an RSA
// signature of 0x88 bytes right after the used part. Trimming keeps the
// used part and the signature and cuts only 0xFF bytes, so the original is
// restored exactly by filling the cartridge's size with 0xFF again.
package nds

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const (
	headerLen = 0x220
	sigLen    = 0x88
	pad       = 0xFF
	// titleAt is where the banner's titles start, 0x100 bytes each, in
	// Japanese, English, French, German, Italian and Spanish.
	titleAt = 0x240
)

// ErrNotDS is returned for a file that is not a Nintendo DS ROM.
var ErrNotDS = errors.New("not a Nintendo DS ROM")

// ErrNotEmpty is returned when the space after the game holds data, which
// trimming would lose.
var ErrNotEmpty = errors.New("the space after the game is not empty (0xFF), so trimming would lose data")

// Info is what the header and banner tell about a ROM.
type Info struct {
	Code     string // game code, e.g. "ASME"
	Maker    string // maker code, e.g. "01"
	Header   string // the header's short title, e.g. "SUPERMARIO64"
	Title    string // the banner's English title, lines joined by " - ", without the publisher
	DSi      bool   // has DSi features (DSi-enhanced or DSi-only)
	Size     int64  // size of the file
	Used     int64  // size of the game: used part plus the Download Play signature
	Capacity int64  // size of the cartridge
}

// Trimmed tells whether the file is smaller than its cartridge.
func (i Info) Trimmed() bool { return i.Size < i.Capacity }

// Name is the banner's title, or the header's when there is none.
func (i Info) Name() string {
	if i.Title != "" {
		return i.Title
	}
	return i.Header
}

// Read reads the header and banner of the ROM at path.
func Read(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	return ReadAt(f, st.Size())
}

// ReadAt reads the header and banner of a ROM of the given size.
func ReadAt(r io.ReaderAt, size int64) (Info, error) {
	h := make([]byte, headerLen)
	if size < headerLen {
		return Info{}, ErrNotDS
	}
	if _, err := r.ReadAt(h, 0); err != nil {
		return Info{}, err
	}
	// The header's CRC-16 (at 0x15E, of the first 0x15E bytes) tells a DS
	// ROM from any other file.
	if CRC16(h[:0x15E]) != binary.LittleEndian.Uint16(h[0x15E:]) || h[0x14] > 15 {
		return Info{}, ErrNotDS
	}
	info := Info{
		Code:     ascii(h[0x0C:0x10]),
		Maker:    ascii(h[0x10:0x12]),
		Header:   ascii(h[0x00:0x0C]),
		DSi:      h[0x12]&2 != 0,
		Size:     size,
		Capacity: 128 << 10 << h[0x14],
	}
	info.Used = int64(binary.LittleEndian.Uint32(h[0x80:]))
	if info.DSi {
		info.Used = max(info.Used, int64(binary.LittleEndian.Uint32(h[0x210:])))
	}
	if info.Used < headerLen || info.Used > info.Capacity {
		return Info{}, fmt.Errorf("%w: used size %d, cartridge %d", ErrNotDS, info.Used, info.Capacity)
	}
	// The Download Play signature, when there is one.
	if info.Used < size {
		sig := make([]byte, min(sigLen, size-info.Used))
		if _, err := r.ReadAt(sig, info.Used); err != nil && err != io.EOF {
			return Info{}, err
		}
		if len(bytes.Trim(sig, "\xff")) > 0 && info.Used+sigLen <= info.Capacity {
			info.Used += sigLen
		}
	}
	info.Title = bannerTitle(r, size, int64(binary.LittleEndian.Uint32(h[0x68:])))
	return info, nil
}

// bannerTitle reads the English title of the banner, or "".
func bannerTitle(r io.ReaderAt, size, at int64) string {
	if at < headerLen || at+titleAt+0x200 > size {
		return ""
	}
	b := make([]byte, 0x100)
	if _, err := r.ReadAt(b, at+titleAt+0x100); err != nil {
		return ""
	}
	u := make([]uint16, 0, 0x80)
	for i := 0; i < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	var lines []string
	for _, l := range strings.Split(string(utf16.Decode(u)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 1 {
		lines = lines[:len(lines)-1] // the last line is the publisher
	}
	title := ""
	for i, l := range lines {
		switch {
		case i == 0:
		case strings.HasSuffix(title, ":") || strings.HasSuffix(title, "-"):
			title += " "
		default:
			title += " - "
		}
		title += l
	}
	return title
}

func ascii(b []byte) string {
	b = bytes.TrimRight(b, "\x00 ")
	for _, c := range b {
		if c < 0x20 || c > 0x7E {
			return ""
		}
	}
	return string(b)
}

// CRC16 is the CRC-16 (MODBUS) of DS headers (and of their logo).
func CRC16(b []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, c := range b {
		crc ^= uint16(c)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// Result is what Trim and Untrim did.
type Result struct {
	Before, After int64
}

// Trim rewrites the ROM at path without the empty space after the game.
// A ROM that is already trimmed is left as it is.
func Trim(ctx context.Context, path string, progress func(done, total int64)) (Result, error) {
	info, err := Read(path)
	if err != nil {
		return Result{}, err
	}
	if info.Size <= info.Used {
		return Result{info.Size, info.Size}, nil
	}
	return rewrite(ctx, path, info, info.Used, progress)
}

// Untrim rewrites the ROM at path at the full size of its cartridge,
// filling it with 0xFF. A ROM at full size is left as it is.
func Untrim(ctx context.Context, path string, progress func(done, total int64)) (Result, error) {
	info, err := Read(path)
	if err != nil {
		return Result{}, err
	}
	if info.Size >= info.Capacity {
		return Result{info.Size, info.Size}, nil
	}
	return rewrite(ctx, path, info, info.Capacity, progress)
}

// rewrite writes the ROM at the new size to a file next to it, reads it
// back, and replaces the ROM with it.
func rewrite(ctx context.Context, path string, info Info, size int64, progress func(done, total int64)) (Result, error) {
	in, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return Result{}, err
	}
	tmp := out.Name()
	done := false
	defer func() {
		if !done {
			out.Close()
			os.Remove(tmp)
		}
	}()
	total := max(info.Size, size) + size // read and written, then read back
	var n int64
	step := func(k int) error {
		n += int64(k)
		if progress != nil {
			progress(n, total)
		}
		return ctx.Err()
	}
	buf := make([]byte, 1<<20)
	h := crc32.NewIEEE()
	w := io.MultiWriter(out, h)
	// The part that is kept.
	keep := min(info.Size, size)
	for left := keep; left > 0; {
		k, err := io.ReadFull(in, buf[:min(int64(len(buf)), left)])
		if err != nil {
			return Result{}, err
		}
		if _, err := w.Write(buf[:k]); err != nil {
			return Result{}, err
		}
		left -= int64(k)
		if err := step(k); err != nil {
			return Result{}, err
		}
	}
	if size < info.Size {
		// The part that is cut must be empty.
		for left := info.Size - size; left > 0; {
			k, err := io.ReadFull(in, buf[:min(int64(len(buf)), left)])
			if err != nil {
				return Result{}, err
			}
			if len(bytes.Trim(buf[:k], "\xff")) > 0 {
				return Result{}, ErrNotEmpty
			}
			left -= int64(k)
			if err := step(k); err != nil {
				return Result{}, err
			}
		}
	} else {
		ff := bytes.Repeat([]byte{pad}, len(buf))
		for left := size - info.Size; left > 0; {
			k := int(min(int64(len(ff)), left))
			if _, err := w.Write(ff[:k]); err != nil {
				return Result{}, err
			}
			left -= int64(k)
			if err := step(k); err != nil {
				return Result{}, err
			}
		}
	}
	want := h.Sum32()
	if err := out.Sync(); err != nil {
		return Result{}, err
	}
	// Read back what was written.
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return Result{}, err
	}
	h.Reset()
	var got int64
	for {
		k, err := out.Read(buf)
		h.Write(buf[:k])
		got += int64(k)
		if serr := step(k); serr != nil {
			return Result{}, serr
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, err
		}
	}
	if got != size || h.Sum32() != want {
		return Result{}, errors.New("the new file differs from what was written")
	}
	if err := out.Close(); err != nil {
		return Result{}, err
	}
	in.Close()
	st, err := os.Stat(path)
	if err == nil {
		os.Chtimes(tmp, st.ModTime(), st.ModTime())
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		done = true
		return Result{}, err
	}
	done = true
	return Result{info.Size, size}, nil
}

// PaddedCRC returns the CRC-32 of data whose CRC is crc and size is size,
// followed by 0xFF bytes up to the size to.
func PaddedCRC(crc uint32, size, to int64) uint32 {
	ff := bytes.Repeat([]byte{pad}, 1<<20)
	for size < to {
		k := min(int64(len(ff)), to-size)
		crc = crc32.Update(crc, crc32.IEEETable, ff[:k])
		size += k
	}
	return crc
}
