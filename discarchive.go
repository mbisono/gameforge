package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gameforge/internal/chd"
	"gameforge/internal/i18n"
	"gameforge/internal/sevenzip"
	"gameforge/internal/xiso"
)

// The Discs tab also reads disc images that need no unpacking by hand:
// raw images compressed with ECM (game.bin.ecm, with or without a CUE
// sheet), Nero's NRG and DiscJuggler's CDI images, and ZIP and 7z files
// holding a disc image, which are unpacked into a temporary folder for the
// conversion and removed afterwards.

// discImageExts are the extensions of the disc images looked for inside a
// ZIP or 7z, in order of preference: a CUE or GDI sheet first, since it
// names the other files.
var discImageExts = []string{".cue", ".gdi", ".cdi", ".nrg", ".iso", ".ecm"}

// isCDImage reports whether a file is read through its tracks: a CUE
// sheet, a GDI file, an NRG or CDI image, or an ECM file.
func isCDImage(p string) bool { return hasExt(p, chd.ImageExts) }

// discStem is a disc image's name without its extension (and without the
// .ecm of game.bin.ecm).
func discStem(p string) string {
	name := filepath.Base(p)
	if hasExt(name, []string{".ecm"}) {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// ecmListed tells whether an ECM file is listed by itself: not when a CUE
// sheet in its folder names it (game.cue naming game.bin for game.bin.ecm),
// since the sheet is listed instead.
func ecmListed(p string) bool {
	inner := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	sheets, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.cue"))
	for _, s := range sheets {
		if b, err := os.ReadFile(s); err == nil && len(b) < 1<<20 && strings.Contains(strings.ToLower(string(b)), strings.ToLower(inner)) {
			return false
		}
	}
	return true
}

// archiveEntry is a file inside a ZIP or 7z.
type archiveEntry struct {
	name string
	size int64
}

// archiveEntries lists the files of a ZIP or 7z.
func archiveEntries(p string) ([]archiveEntry, error) {
	var out []archiveEntry
	if hasExt(p, []string{".zip"}) {
		zr, files, err := openZip(p)
		if err != nil {
			return nil, err
		}
		zr.Close()
		for _, f := range files {
			out = append(out, archiveEntry{f.Name, int64(f.UncompressedSize64)})
		}
		return out, nil
	}
	entries, err := sevenzip.List(p)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.Dir {
			out = append(out, archiveEntry{e.Name, e.Size})
		}
	}
	return out, nil
}

// archiveDisc finds the disc image inside a ZIP or 7z: its name, the
// number of files and their total size. It returns "" if there is none.
func archiveDisc(p string) (string, int, int64) {
	entries, err := archiveEntries(p)
	if err != nil {
		return "", 0, 0
	}
	var size int64
	for _, e := range entries {
		size += e.size
	}
	for _, ext := range discImageExts {
		for _, e := range entries {
			if hasExt(e.name, []string{ext}) {
				return e.name, len(entries), size
			}
		}
	}
	return "", len(entries), size
}

// describeDiscArchive tells what disc image a ZIP or 7z holds.
func describeDiscArchive(it *convItem) {
	inner, n, size := archiveDisc(it.path)
	if inner == "" {
		it.detail = msg("chd.row_archive_none")
		return
	}
	it.ok, it.kind, it.size = true, "archive", size
	it.platform = map[bool]string{true: "ZIP", false: "7z"}[hasExt(it.path, []string{".zip"})]
	target := "CHD"
	if hasExt(inner, []string{".iso"}) && isoTarget != "" {
		target = strings.ToUpper(isoTarget)
	}
	it.detail = msg("chd.row_archive", path.Base(inner), n-1, humanSize(size), target)
}

// archiveOutName is the name of what a ZIP or 7z of a disc becomes.
func archiveOutName(it *convItem) string {
	inner, _, _ := archiveDisc(it.path)
	ext := ".chd"
	if hasExt(inner, []string{".iso"}) && isoTarget != "" {
		ext = "." + isoTarget
	}
	return discStem(it.path) + ext
}

// convertDiscArchive unpacks a ZIP or 7z into a temporary folder next to
// the output, converts the disc image inside, and removes the folder.
func convertDiscArchive(ctx context.Context, u *ui, it *convItem, out string, verify bool,
	progress func(bool, int64, int64)) (int64, int64, error) {
	inner, _, _ := archiveDisc(it.path)
	if inner == "" {
		return 0, 0, errors.New(T("chd.row_archive_none"))
	}
	tmp, err := os.MkdirTemp(filepath.Dir(out), ".gameforge-")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(tmp)
	u.logf("chd.archive_unpack", filepath.Base(it.path))
	if err := unpackArchive(ctx, it.path, tmp, func(done, total int64) { progress(false, done, total) }); err != nil {
		return 0, 0, err
	}
	img := filepath.Join(tmp, filepath.FromSlash(inner))
	sub := &convItem{path: img}
	describeDisc(sub)
	switch {
	case sub.kind == "xiso" || sub.kind == "rvz" || xiso.IsXbox(img):
		return 0, 0, i18n.Errorf("chd.archive_other", path.Base(inner))
	case !sub.ok:
		return 0, 0, fmt.Errorf("%s: %s", path.Base(inner), stringOf(sub.detail))
	}
	st, _ := os.Stat(it.path)
	_, outSize, err := convertDisc(ctx, u, sub, out, verify, progress)
	if err != nil {
		return 0, 0, err
	}
	var in int64
	if st != nil {
		in = st.Size()
	}
	return in, outSize, nil
}

// unpackArchive writes the files of a ZIP or 7z into dir. Names that
// would leave dir are refused.
func unpackArchive(ctx context.Context, p, dir string, progress func(done, total int64)) error {
	target := func(name string) (string, error) {
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return "", fmt.Errorf("%s: unsafe name in the archive", name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		return dst, os.MkdirAll(filepath.Dir(dst), 0o755)
	}
	if hasExt(p, []string{".zip"}) {
		zr, files, err := openZip(p)
		if err != nil {
			return err
		}
		defer zr.Close()
		var total, done int64
		for _, f := range files {
			total += int64(f.UncompressedSize64)
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := unpackZipFile(ctx, f, target, func(n int64) {
				done += n
				progress(done, total)
			}); err != nil {
				return err
			}
		}
		return nil
	}
	var open []*os.File
	defer func() {
		for _, f := range open {
			f.Close()
		}
	}()
	var werr error
	_, err := sevenzip.Extract(ctx, p, func(name string, size int64) io.Writer {
		dst, err := target(name)
		if err == nil {
			var f *os.File
			if f, err = os.Create(dst); err == nil {
				open = append(open, f)
				return f
			}
		}
		if werr == nil {
			werr = err
		}
		return nil
	}, progress)
	if err == nil {
		err = werr
	}
	for _, f := range open {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	open = nil
	return err
}

func unpackZipFile(ctx context.Context, f *zip.File, target func(string) (string, error), progress func(int64)) error {
	dst, err := target(f.Name)
	if err != nil {
		return err
	}
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			w.Close()
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				w.Close()
				return err
			}
			progress(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			w.Close()
			return rerr
		}
	}
	return w.Close()
}
