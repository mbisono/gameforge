package main

import (
	"archive/zip"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/i18n"
	"gameforge/internal/sevenzip"
)

// sideExts are files that live next to games but are not games: RetroArch
// saves, states, thumbnails and settings, playlists, patches and notes.
// Their boxes start unchecked in the 7z tab; any other extension starts
// checked.
var sideExts = map[string]bool{
	".auto": true, ".srm": true, ".sav": true, ".rtc": true, ".mcr": true,
	".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".gif": true,
	".cfg": true, ".opt": true, ".rmp": true, ".ini": true, ".cht": true,
	".lpl": true, ".m3u": true, ".cue": true, ".txt": true, ".nfo": true,
	".xml": true, ".dat": true, ".ips": true, ".bps": true, ".ups": true,
	".ldci": true, ".bak": true,
}

// romPlatforms names the platform of common ROM extensions.
var romPlatforms = map[string]string{
	".gb": "Game Boy", ".gbc": "Game Boy Color", ".gba": "Game Boy Advance",
	".nes": "NES", ".fds": "Famicom Disk", ".sfc": "SNES", ".smc": "SNES",
	".n64": "Nintendo 64", ".z64": "Nintendo 64", ".v64": "Nintendo 64",
	".nds": "Nintendo DS", ".vb": "Virtual Boy",
	".md": "Mega Drive", ".gen": "Mega Drive", ".smd": "Mega Drive", ".32x": "32X",
	".sms": "Master System", ".gg": "Game Gear", ".sg": "SG-1000",
	".pce": "PC Engine", ".ws": "WonderSwan", ".wsc": "WonderSwan Color",
	".ngp": "Neo Geo Pocket", ".ngc": "Neo Geo Pocket Color",
	".lnx": "Lynx", ".a26": "Atari 2600", ".a78": "Atari 7800", ".col": "ColecoVision",
}

// extOf is a file's extension in lower case; files named like
// "game.state1" count as ".state", and every ".state*" as a side file.
func extOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if strings.HasPrefix(ext, ".state") {
		return ".state"
	}
	return ext
}

// removeFile removes an original after its archive is verified; tests replace it.
var removeFile = removeOriginal

// sevenZip holds the 7z tab's choices; ui is set when the tab is built.
var sevenZip struct {
	ui      *ui
	exts    *TFrameWidget // the extension boxes, rebuilt when the files change
	shown   string        // the extensions and counts the boxes show
	deleteV *VariableOpt
}

// szEnabled says whether files with this extension are compressed.
func szEnabled(ext string) bool {
	if ext == ".7z" || ext == ".state" {
		return false
	}
	if u := sevenZip.ui; u != nil {
		if v, ok := u.cfg.SevenZipExts[ext]; ok {
			return v
		}
	}
	return !sideExts[ext]
}

// sevenZipConverter compresses game files, or recompresses ZIP files, to
// 7z, one archive per file.
var sevenZipConverter = &converter{
	id:     "7z",
	title:  "tab.sz",
	info:   "tab.sz.info",
	button: "btn.sz",
	tip:    "btn.sz.tip",
	anyExt: true,
	accept: func(path string) bool { return szEnabled(extOf(path)) },
	drop:   func(string) bool { return false }, // only when the tab is shown
	types:  []FileType{{TypeName: "*", Extensions: []string{"*"}}},
	describe: func(it *convItem) {
		it.ok = false
		st, err := os.Stat(it.path)
		if err != nil {
			it.detail = lazyf("⚠ %v", err)
			return
		}
		size := humanSize(st.Size())
		ext := extOf(it.path)
		if ext != ".zip" {
			it.platform = romPlatforms[ext]
			it.ok = true
			it.detail = msg("sz.row", size)
			return
		}
		it.platform = "ZIP"
		zr, files, err := openZip(it.path)
		if err != nil {
			it.detail = lazyf("%v", err)
			return
		}
		zr.Close()
		it.ok = true
		it.detail = msg("sz.row_zip", len(files), size)
	},
	outName: func(it *convItem) string {
		return strings.TrimSuffix(filepath.Base(it.path), filepath.Ext(it.path)) + ".7z"
	},
	run: func(ctx context.Context, u *ui, it *convItem, out string, verify bool,
		progress func(bool, int64, int64)) (int64, int64, error) {
		st, err := os.Stat(it.path)
		if err != nil {
			return 0, 0, err
		}
		res, err := writeSevenZip(ctx, u, it.path, st, out, progress)
		if err != nil {
			return 0, 0, err
		}
		del := u.cfg.SevenZipDelete
		if verify || del { // never remove an original without checking its archive
			if err := checkSevenZip(ctx, out, res.Entries, progress); err != nil {
				if ctx.Err() == nil {
					err = fmt.Errorf("%s: %w", T("log.verify_failed"), err)
				}
				return 0, 0, err
			}
		}
		if del {
			// A failure here keeps the archive: it is complete and verified.
			if err := removeFile(it.path); err != nil {
				u.logf("sz.delete_error", filepath.Base(it.path), err)
			} else {
				u.logf("sz.deleted", filepath.Base(it.path))
			}
		}
		return st.Size(), res.OutputSize, nil
	},
	options: func(u *ui, parent *TFrameWidget) []Widget {
		sevenZip.ui = u
		box := parent.TFrame()
		label := box.TLabel(Txt(T("sz.exts")))
		setTooltip(label, T("sz.exts.tip"))
		Grid(label, Row(0), Column(0), Sticky("nw"), pad)
		sevenZip.exts = box.TFrame()
		sevenZip.shown = "\x00" // rebuilt by the first refresh
		Grid(sevenZip.exts, Row(0), Column(1), Sticky("w"))
		sevenZip.deleteV = Variable(tclBool(u.cfg.SevenZipDelete))
		del := box.TCheckbutton(Txt(T("sz.delete")), sevenZip.deleteV, Command(func() {
			u.cfg.SevenZipDelete = sevenZip.deleteV.Get() == "1"
			u.saveConfig()
		}))
		tip := T("sz.delete.tip")
		if usesTrash {
			tip += "\n\n" + T("sz.delete.trash")
		}
		setTooltip(del, tip)
		Grid(del, Row(1), Column(0), Columnspan(2), Sticky("w"), pad)
		return []Widget{box}
	},
	refreshed: func(u *ui, t *convTab) { fillExtBoxes(u, t) },
}

// fillExtBoxes shows one box per extension among the tab's files, with
// how many files have it, when the files have changed.
func fillExtBoxes(u *ui, t *convTab) {
	if sevenZip.exts == nil {
		return
	}
	count := map[string]int{}
	for _, p := range t.pool {
		if e := extOf(p); e != ".7z" && e != "" {
			count[e]++
		}
	}
	exts := make([]string, 0, len(count))
	for e := range count {
		exts = append(exts, e)
	}
	slices.SortFunc(exts, func(a, b string) int {
		return cmp.Or(cmp.Compare(count[b], count[a]), cmp.Compare(a, b))
	})
	var key strings.Builder
	for _, e := range exts {
		fmt.Fprintf(&key, "%s=%d;", e, count[e])
	}
	if key.String() == sevenZip.shown {
		return
	}
	sevenZip.shown = key.String()
	for _, w := range WinfoChildren(sevenZip.exts.Window) {
		Destroy(w)
	}
	if len(exts) == 0 {
		Grid(sevenZip.exts.TLabel(Txt(T("sz.exts_none"))), Row(0), Column(0), Sticky("w"), pad)
		return
	}
	for i, e := range exts {
		e := e
		var v *VariableOpt
		v = Variable(tclBool(szEnabled(e)))
		cb := sevenZip.exts.TCheckbutton(Txt(fmt.Sprintf("%s (%d)", e, count[e])), v, Command(func() {
			if u.busy {
				return
			}
			if u.cfg.SevenZipExts == nil {
				u.cfg.SevenZipExts = map[string]bool{}
			}
			u.cfg.SevenZipExts[e] = v.Get() == "1"
			u.saveConfig()
			t.refilter(u)
		}))
		if e == ".state" {
			enable(cb, false) // save states are never compressed
		}
		Grid(cb, Row(i/8), Column(i%8), Sticky("w"), pad)
	}
	if u.busy {
		enableTree(sevenZip.exts.Window, false)
	}
}

// writeSevenZip writes the archive for one file: the file itself, or the
// files inside a ZIP. The input is closed when it returns, so that it can
// be removed afterwards.
func writeSevenZip(ctx context.Context, u *ui, path string, st os.FileInfo, out string,
	progress func(bool, int64, int64)) (sevenzip.Result, error) {
	srcs := []sevenzip.Source{{
		Name: filepath.Base(path), Size: st.Size(), Modified: st.ModTime(),
		Open: func() (io.ReadCloser, error) { return os.Open(path) },
	}}
	var zipCRCs []uint32
	if extOf(path) == ".zip" {
		zr, files, err := openZip(path)
		if err != nil {
			return sevenzip.Result{}, err
		}
		defer zr.Close()
		srcs = srcs[:0]
		for _, f := range files {
			srcs = append(srcs, sevenzip.Source{Name: f.Name, Size: int64(f.UncompressedSize64), Modified: f.Modified, Open: f.Open})
			zipCRCs = append(zipCRCs, f.CRC32)
		}
		u.logf("sz.zip_files", len(files))
	}
	res, err := sevenzip.Write(ctx, out, srcs, func(done, total int64) { progress(false, done, total) })
	if err != nil {
		return res, err
	}
	// The ZIP reader already checks each file's CRC; this also covers the
	// copy into the archive.
	for i, crc := range zipCRCs {
		if res.Entries[i].Size > 0 && res.Entries[i].CRC != crc {
			return res, fmt.Errorf("%s: CRC differs from the ZIP's", res.Entries[i].Name)
		}
	}
	return res, nil
}

// openZip opens a ZIP archive and lists its files, refusing archives the
// app cannot read. The caller closes it.
func openZip(path string) (*zip.ReadCloser, []*zip.File, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, i18n.Errorf("sz.bad_zip", err)
	}
	var files []*zip.File
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if zf.Flags&1 != 0 {
			zr.Close()
			return nil, nil, i18n.Errorf("sz.zip_encrypted")
		}
		if zf.Method != zip.Store && zf.Method != zip.Deflate {
			zr.Close()
			return nil, nil, i18n.Errorf("sz.zip_method", zf.Method)
		}
		files = append(files, zf)
	}
	return zr, files, nil
}

// checkSevenZip reads the written archive back and compares its files with
// what was written.
func checkSevenZip(ctx context.Context, path string, want []sevenzip.Entry, progress func(bool, int64, int64)) error {
	got, err := sevenzip.Check(ctx, path, func(done, total int64) { progress(true, done, total) })
	if err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("%d files instead of %d", len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Name != w.Name || g.Size != w.Size || g.CRC != w.CRC {
			return fmt.Errorf("%s differs", w.Name)
		}
	}
	return nil
}
