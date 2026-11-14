package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/ciso"
	"gameforge/internal/discinfo"
	"gameforge/internal/xiso"
)

// cisoTarget is the format ISO images are compressed to, from the tab's
// option.
var cisoTarget = ciso.CSO

// cisoConverter compresses ISO images to CSO or ZSO.
var cisoConverter = &converter{
	id:     "ciso",
	title:  "tab.ciso",
	info:   "tab.ciso.info",
	button: "btn.ciso",
	tip:    "btn.ciso.tip",
	exts:   []string{".iso"},
	accept: func(path string) bool { return !xiso.IsXbox(path) && !isNintendoDisc(path) },
	// Dropped ISO images go to the Discs tab, unless this tab is shown.
	drop:  func(string) bool { return false },
	types: []FileType{{TypeName: "ISO", Extensions: []string{".iso"}}},
	describe: func(it *convItem) {
		it.ok = false
		st, err := os.Stat(it.path)
		if err != nil {
			it.detail = lazyf("⚠ %v", err)
			return
		}
		it.platform, _, _ = discinfo.FromISO(it.path)
		it.ok = true
		size := humanSize(st.Size())
		it.detail = lazyLine(func() string { return T("ciso.row_iso", size, cisoTarget) })
	},
	outName: func(it *convItem) string {
		return strings.TrimSuffix(filepath.Base(it.path), filepath.Ext(it.path)) + cisoTarget.Ext()
	},
	run: func(ctx context.Context, u *ui, it *convItem, out string, verify bool,
		progress func(bool, int64, int64)) (int64, int64, error) {
		res, err := ciso.Compress(ctx, it.path, out, cisoTarget, func(done, total int64) { progress(false, done, total) })
		if err != nil {
			return 0, 0, err
		}
		if verify {
			sum, err := ciso.Hash(ctx, out, func(done, total int64) { progress(true, done, total) })
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
	},
	options: func(u *ui, parent *TFrameWidget) []Widget {
		if u.cfg.CisoFormat == "zso" {
			cisoTarget = ciso.ZSO
		} else {
			cisoTarget = ciso.CSO
		}
		v := Variable(map[ciso.Format]string{ciso.CSO: "cso", ciso.ZSO: "zso"}[cisoTarget])
		set := func() {
			u.cfg.CisoFormat = v.Get()
			cisoTarget = map[string]ciso.Format{"cso": ciso.CSO, "zso": ciso.ZSO}[v.Get()]
			u.saveConfig()
			for _, t := range u.tabs {
				if t.conv.id == "ciso" {
					t.refresh(u)
				}
			}
		}
		label := parent.TLabel(Txt(T("opt.ciso_format")))
		cso := parent.TRadiobutton(Txt(T("opt.ciso_cso")), v, Value("cso"), Command(set))
		zso := parent.TRadiobutton(Txt(T("opt.ciso_zso")), v, Value("zso"), Command(set))
		setTooltip(cso, T("opt.ciso_cso.tip"))
		setTooltip(zso, T("opt.ciso_zso.tip"))
		return []Widget{label, cso, zso}
	},
}
