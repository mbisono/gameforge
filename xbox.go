package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"gameforge/internal/discinfo"
	"gameforge/internal/gameart"
	"gameforge/internal/xiso"
)

// Xbox and Xbox 360 disc images in the Discs tab: a disc dump is rebuilt
// as a compact XISO (emulators do not read Xbox CHDs); an image that is
// already an XISO is listed for what it is, for renaming.

func isXboxPlatform(p string) bool { return p == discinfo.Xbox || p == discinfo.Xbox360 }

// isXISOName reports whether a file is named as an XISO: "Game.xiso.iso".
func isXISOName(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".xiso.iso")
}

// isXboxDump reports whether an Xbox image is a disc dump, not an XISO.
func isXboxDump(path string) bool {
	im, err := xiso.Open(path)
	if err != nil {
		return false
	}
	defer im.Close()
	return im.Kind != xiso.KindXISO
}

// xisoName is the name of the XISO rebuilt from an image.
func xisoName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.EqualFold(filepath.Ext(base), ".xiso") {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return base + ".xiso.iso"
}

// describeXbox tells what an Xbox or Xbox 360 image is.
func describeXbox(it *convItem) {
	im, err := xiso.Open(it.path)
	if err != nil {
		it.detail = lazyf("⚠ %v", err)
		return
	}
	k, files, size := im.Kind, im.Files, im.Bytes
	it.platform = xboxPlatform(im.Xbox360)
	if r, n, ok := im.RootFile("default.xbe"); ok {
		it.icon, _ = gameart.XBE(r, n)
	}
	im.Close()
	info, _ := discinfo.InfoFromXbox(it.path)
	row := lazyLine(func() string { return T("xiso.row", xisoLayout(k), files, humanSize(size)) })
	if k == xiso.KindXISO {
		it.infoOnly = true
	} else {
		it.ok, it.kind = true, "xiso"
		row = lazyLine(func() string { return T("xiso.row", xisoLayout(k), files, humanSize(size)) + " → XISO" })
	}
	it.detail = withGame(info, row)
}

// rebuildXISO rebuilds an Xbox disc image as an XISO.
func rebuildXISO(ctx context.Context, u *ui, it *convItem, out string, verify bool,
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
}

func xboxPlatform(x360 bool) string {
	if x360 {
		return discinfo.Xbox360
	}
	return discinfo.Xbox
}

// xisoLayout names the layout of an image: XISO or a disc dump (XGD1-3).
func xisoLayout(k xiso.Kind) string {
	if k == xiso.KindXISO {
		return "XISO"
	}
	return T("xiso.dump", k.String())
}
