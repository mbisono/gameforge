package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/gameart"
	"gameforge/internal/xiso"
)

// xisoConverter rebuilds Xbox and Xbox 360 disc images as compact XISOs.
var xisoConverter = &converter{
	id:     "xiso",
	title:  "tab.xiso",
	info:   "tab.xiso.info",
	button: "btn.xiso",
	tip:    "btn.xiso.tip",
	exts:   []string{".iso"},
	accept: xiso.IsXbox,
	types:  []FileType{{TypeName: "Xbox / Xbox 360 ISO", Extensions: []string{".iso"}}},
	describe: func(it *convItem) {
		im, err := xiso.Open(it.path)
		if err != nil {
			it.ok, it.detail = false, lazyf("⚠ %v", err)
			return
		}
		defer im.Close()
		k, files, size := im.Kind, im.Files, im.Bytes
		it.ok, it.platform = true, xboxPlatform(im.Xbox360)
		if r, n, ok := im.RootFile("default.xbe"); ok {
			it.icon, _ = gameart.XBE(r, n)
		}
		it.detail = lazyLine(func() string { return T("xiso.row", xisoLayout(k), files, humanSize(size)) })
	},
	outName: func(it *convItem) string {
		base := strings.TrimSuffix(filepath.Base(it.path), filepath.Ext(it.path))
		if strings.EqualFold(filepath.Ext(base), ".xiso") {
			base = strings.TrimSuffix(base, filepath.Ext(base))
		}
		return base + ".xiso.iso"
	},
	run: func(ctx context.Context, u *ui, it *convItem, out string, verify bool,
		progress func(bool, int64, int64)) (int64, int64, error) {
		res, hashes, err := xiso.Rebuild(ctx, it.path, out, func(done, total int64) { progress(false, done, total) })
		if err != nil {
			return 0, 0, err
		}
		u.println(lazyLine(func() string {
			return "    " + xboxPlatform(res.Xbox360) + ", " + T("xiso.row", xisoLayout(res.Kind), res.Files, humanSize(res.OutputSize))
		}))
		if verify {
			if err := xiso.Verify(ctx, out, hashes, func(done, total int64) { progress(true, done, total) }); err != nil {
				if ctx.Err() == nil {
					err = fmt.Errorf("%s: %w", T("log.verify_failed"), err)
				}
				return 0, 0, err
			}
		}
		return res.InputSize, res.OutputSize, nil
	},
}

func xboxPlatform(x360 bool) string {
	if x360 {
		return "Xbox 360"
	}
	return "Xbox"
}

// xisoLayout names the layout of an image: XISO or a disc dump (XGD1-3).
func xisoLayout(k xiso.Kind) string {
	if k == xiso.KindXISO {
		return "XISO"
	}
	return T("xiso.dump", k.String())
}
