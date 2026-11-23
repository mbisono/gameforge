package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	. "modernc.org/tk9.0"

	"gameforge/internal/discinfo"
	"gameforge/internal/rvz"
	"gameforge/internal/wbfs"
)

// nintendoDisc reports whether a disc image is a GameCube or Wii disc.
func nintendoDisc(path string) (gc, wii bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	return rvz.IsGameCube(f), rvz.IsWii(f)
}

func isRVZ(path string) bool { return strings.EqualFold(filepath.Ext(path), ".rvz") }

// describeRVZ tells what game an RVZ holds; it is listed, not converted.
func describeRVZ(it *convItem) {
	it.infoOnly = true
	info, err := discinfo.InfoFromRVZ(it.path)
	if err != nil {
		it.detail = lazyf("⚠ %v", err)
		return
	}
	it.platform = info.Platform
	size := int64(0)
	if h, err := rvz.ReadHeader(it.path); err == nil {
		size = h.ISOSize
		if !h.Wii {
			it.icon = gcBanner(it.path)
		}
	}
	it.detail = msg("rvz.row_rvz", info.Serial, info.Title, humanSize(size), humanSize(discSize(it.path)))
}

// gcBanner returns the banner of a GameCube disc image (ISO, GCM or RVZ)
// as PNG, or nil.
func gcBanner(path string) []byte {
	var png []byte
	if isRVZ(path) {
		r, err := rvz.Open(path)
		if err != nil {
			return nil
		}
		defer r.Close()
		png, _ = discinfo.GameCubeBanner(r, r.ISOSize)
		return png
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	png, _ = discinfo.GameCubeBanner(f, discSize(path))
	return png
}

func isNintendoDisc(path string) bool {
	gc, wii := nintendoDisc(path)
	return gc || wii
}

// gcTitle returns the game ID and title in a GameCube disc header.
func gcTitle(path string) (id, title string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	b := make([]byte, 0x60)
	if _, err := f.ReadAt(b, 0); err != nil {
		return "", ""
	}
	return headerTitle(b)
}

// headerTitle returns the game ID and title in the first 0x60 bytes of a
// GameCube or Wii disc.
func headerTitle(b []byte) (id, title string) {
	clean := func(s []byte) string {
		if i := strings.IndexByte(string(s), 0); i >= 0 {
			s = s[:i]
		}
		return strings.TrimSpace(strings.ToValidUTF8(string(s), ""))
	}
	return clean(b[:6]), clean(b[0x20:0x60])
}

// wiiKeys are the Wii common keys by index (0 normal, 1 Korean); nil when
// not loaded. wiiKeyFile is the file the normal key came from.
var (
	wiiKeys    [][]byte
	wiiKeyFile string
	wiiKeyErr  error
	wiiStatus  *TLabelWidget
)

// Files the Wii common keys are looked for in, in the keys folder.
var (
	wiiKeyNames    = []string{"wii_common_key.bin", "wii_common_key.txt", "common-key.bin", "common-key.txt"}
	koreanKeyNames = []string{"wii_korean_key.bin", "wii_korean_key.txt", "korean-key.bin", "korean-key.txt"}
)

// readKey reads a 16-byte AES key: binary, or 32 hex digits in a text file.
func readKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 16 {
		return b, nil
	}
	run := ""
	for _, c := range string(b) + " " {
		if strings.ContainsRune("0123456789abcdefABCDEF", c) {
			run += string(c)
			continue
		}
		if len(run) == 32 {
			return hex.DecodeString(run)
		}
		run = ""
	}
	return nil, fmt.Errorf("%s: %s", filepath.Base(path), T("wii_key.format"))
}

// loadWiiKeys loads the common keys: the chosen file, or the usual names in
// the keys folder.
func (u *ui) loadWiiKeys() {
	wiiKeys, wiiKeyFile, wiiKeyErr = nil, "", nil
	candidates := []string{}
	if u.cfg.WiiKeyPath != "" {
		candidates = append(candidates, u.cfg.WiiKeyPath)
	}
	for _, n := range wiiKeyNames {
		candidates = append(candidates, filepath.Join(keysDir(), n))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		k, err := readKey(c)
		if err != nil {
			wiiKeyErr = err
			continue
		}
		wiiKeys, wiiKeyFile, wiiKeyErr = [][]byte{k}, c, nil
		break
	}
	if wiiKeys != nil {
		for _, n := range koreanKeyNames {
			if k, err := readKey(filepath.Join(keysDir(), n)); err == nil {
				wiiKeys = append(wiiKeys, k)
				break
			}
		}
	}
	u.showWiiKey()
}

func (u *ui) showWiiKey() {
	if wiiStatus == nil {
		return
	}
	switch {
	case wiiKeys != nil:
		wiiStatus.Configure(Txt(T("wii_key.loaded", filepath.Base(wiiKeyFile))))
	case wiiKeyErr != nil:
		wiiStatus.Configure(Txt("⚠ " + wiiKeyErr.Error()))
	default:
		wiiStatus.Configure(Txt(T("wii_key.missing")))
	}
}

// redescribeNintendo describes the Wii discs of the Discs tab again (after
// the key changed) and shows them.
func (u *ui) redescribeNintendo() {
	for _, t := range u.tabs {
		if t.conv.id != "chd" {
			continue
		}
		for _, it := range t.items {
			if it.platform != discinfo.Wii || isRVZ(it.path) {
				continue
			}
			was := it.ok
			t.conv.describe(it)
			if it.ok && !was {
				t.checked[it.path] = true
			}
		}
		t.refresh(u)
	}
}

// rvzChunks are the block sizes offered in the RVZ tab, in KiB; rvzChunk is
// the one chosen.
var (
	rvzChunks = []int{128, 512, 1024, 2048}
	rvzChunk  = 128
)

func chunkLabel(kib int) string {
	if kib >= 1024 {
		return strconv.Itoa(kib/1024) + " MiB"
	}
	return strconv.Itoa(kib) + " KiB"
}

func discSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// describeNintendo tells what a GameCube or Wii disc image is; it becomes
// an RVZ (a Wii disc only with the Wii common key).
func describeNintendo(it *convItem, gc, wii bool) {
	id, title := gcTitle(it.path)
	it.platform = map[bool]string{true: discinfo.Wii, false: discinfo.GameCube}[wii]
	if gc {
		it.icon = gcBanner(it.path)
	}
	if wii && wiiKeys == nil {
		it.detail = msg("rvz.need_key")
		return
	}
	it.ok, it.kind = true, "rvz"
	it.detail = msg("rvz.row_iso", id, title, humanSize(discSize(it.path)))
}

// isWBFS reports whether a file is a WBFS image of a Wii disc.
func isWBFS(path string) bool { return hasExt(path, []string{".wbfs"}) }

// describeWBFS tells what Wii disc a WBFS image holds; it becomes an RVZ
// (with the Wii common key).
func describeWBFS(it *convItem) {
	r, err := wbfs.Open(it.path)
	if err != nil {
		it.detail = lazyf("⚠ %v", err)
		return
	}
	defer r.Close()
	id, title := headerTitle(r.Header())
	it.platform, it.size = discinfo.Wii, r.FileSize()
	if wiiKeys == nil {
		it.detail = msg("rvz.need_key")
		return
	}
	it.ok, it.kind = true, "rvz"
	it.detail = msg("rvz.row_wbfs", id, title, humanSize(r.FileSize()), humanSize(r.Size()))
}

// compressRVZ compresses a GameCube or Wii disc image (or a WBFS image) to
// RVZ, Dolphin's format.
func compressRVZ(ctx context.Context, u *ui, it *convItem, out string, verify bool,
	progress func(bool, int64, int64)) (int64, int64, error) {
	opt := rvz.Options{Level: zstd.SpeedBestCompression, CommonKeys: wiiKeys, ChunkSize: rvzChunk << 10}
	prog := func(done, total int64) { progress(false, done, total) }
	var res *rvz.Result
	var err error
	if isWBFS(it.path) {
		var r *wbfs.Reader
		if r, err = wbfs.Open(it.path); err == nil {
			res, err = rvz.CompressReader(ctx, r, r.Size(), out, opt, prog)
			if err == nil {
				res.InputSize = r.FileSize()
			}
			r.Close()
		}
	} else {
		res, err = rvz.Compress(ctx, it.path, out, opt, prog)
	}
	switch {
	case errors.Is(err, rvz.ErrWrongCommonKey):
		return 0, 0, errors.New(T("rvz.wrong_key"))
	case errors.Is(err, rvz.ErrNoCommonKey):
		return 0, 0, errors.New(T("rvz.need_key"))
	case err != nil:
		return 0, 0, err
	}
	if res.Wii {
		u.logf("rvz.wii_parts", res.Partitions)
	}
	if verify {
		sum, err := rvz.Hash(ctx, out, func(done, total int64) { progress(true, done, total) })
		if err == nil && sum != res.SHA1 {
			err = fmt.Errorf("content differs")
		}
		if err != nil {
			if ctx.Err() == nil {
				err = fmt.Errorf("%s: %w", T("log.verify_failed"), err)
			}
			return 0, 0, err
		}
	}
	return res.InputSize, res.OutputSize, nil
}

// rvzOptions are the Discs tab's options for GameCube and Wii discs: the
// RVZ block size and the Wii common key.
func rvzOptions(u *ui, parent *TFrameWidget) []Widget {
	rvzChunk = 128
	if slices.Contains(rvzChunks, u.cfg.RvzChunk) {
		rvzChunk = u.cfg.RvzChunk
	}
	v := Variable(strconv.Itoa(rvzChunk))
	widgets := []Widget{parent.TLabel(Txt(T("opt.rvz_row"))), parent.TLabel(Txt(T("opt.rvz_chunk")))}
	for _, kib := range rvzChunks {
		text := chunkLabel(kib)
		if kib == 128 {
			text = T("opt.rvz_chunk_default", text)
		}
		rb := parent.TRadiobutton(Txt(text), v, Value(strconv.Itoa(kib)), Command(func() {
			rvzChunk, _ = strconv.Atoi(v.Get())
			u.cfg.RvzChunk = rvzChunk
			u.saveConfig()
		}))
		setTooltip(rb, T("opt.rvz_chunk.tip"))
		widgets = append(widgets, rb)
	}
	widgets = append(widgets, parent.TLabel(Txt("   ")))
	label := parent.TLabel(Txt(T("opt.wii_key")))
	wiiStatus = parent.TLabel()
	browse := parent.TButton(Txt(T("btn.browse")), Command(func() {
		types := []FileType{{TypeName: T("opt.wii_key"), Extensions: []string{".bin", ".txt"}}}
		p := u.openFile(types, keysDir(), false)
		if p == "" {
			return
		}
		if _, err := readKey(p); err != nil {
			MessageBox(Parent(App), Icon("error"), Title(T("app.title")), Msg(err.Error()))
			return
		}
		u.cfg.WiiKeyPath = p
		u.saveConfig()
		u.loadWiiKeys()
		u.redescribeNintendo()
	}))
	setTooltip(label, T("opt.wii_key.tip"))
	setTooltip(wiiStatus, T("opt.wii_key.tip"))
	u.showWiiKey()
	return append(widgets, label, wiiStatus, browse)
}
