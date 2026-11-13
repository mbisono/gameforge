package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/chd"
	"gameforge/internal/discinfo"
	"gameforge/internal/playlist"
	"gameforge/internal/xiso"
)

// chdConverter compresses CD images (CUE + BIN) and ISO images to CHD. An
// ISO becomes a CD CHD for CD games and a DVD CHD otherwise; the choice can
// be changed per file. Xbox discs, also .iso, belong to the XISO tab.
var chdConverter = &converter{
	id:     "chd",
	title:  "tab.chd",
	info:   "tab.chd.info",
	button: "btn.chd",
	tip:    "btn.chd.tip",
	exts:   []string{".cue", ".gdi", ".iso"},
	accept: func(path string) bool { return !isISO(path) || !xiso.IsXbox(path) && !isNintendoDisc(path) },
	types: []FileType{
		{TypeName: "CUE/GDI/ISO", Extensions: []string{".cue", ".gdi", ".iso"}},
		{TypeName: "CUE (CD)", Extensions: []string{".cue"}},
		{TypeName: "GDI (Dreamcast)", Extensions: []string{".gdi"}},
		{TypeName: "ISO", Extensions: []string{".iso"}},
	},
	describe: describeDisc,
	outName: func(it *convItem) string {
		return strings.TrimSuffix(filepath.Base(it.path), filepath.Ext(it.path)) + ".chd"
	},
	run: func(ctx context.Context, u *ui, it *convItem, out string, verify bool,
		progress func(bool, int64, int64)) (int64, int64, error) {
		opt := chd.Options{Progress: func(done, total int64) { progress(false, done, total) }}
		var res *chd.Result
		var err error
		switch {
		case isGDI(it.path):
			if res, err = chd.ConvertGDI(ctx, it.path, out, opt); err == nil {
				u.logf("chd.gd", res.Tracks)
			}
		case !isISO(it.path):
			if res, err = chd.ConvertCue(ctx, it.path, out, opt); err == nil {
				if it.kind == "gd" {
					u.logf("chd.gd", res.Tracks)
				} else {
					u.logf("chd.cd", res.Tracks)
				}
			}
		case it.kind == "cd":
			if res, err = chd.ConvertISOAsCD(ctx, it.path, out, opt); err == nil {
				u.logf("chd.iso_cd")
			}
		default:
			if res, err = chd.ConvertISO(ctx, it.path, out, opt); err == nil {
				u.logf("chd.dvd")
			}
		}
		if err != nil {
			return 0, 0, err
		}
		if verify {
			if _, err := chd.Verify(ctx, out, func(done, total int64) { progress(true, done, total) }); err != nil {
				if ctx.Err() == nil {
					err = fmt.Errorf("%s: %w", T("log.verify_failed"), err)
				}
				return 0, 0, err
			}
		}
		return res.InputSize, res.OutputSize, nil
	},
	extra: func(u *ui, parent *TFrameWidget) []*TButtonWidget {
		check := parent.TButton(Txt(T("btn.check_chd")), Command(func() {
			if p := u.openFile([]FileType{{TypeName: "CHD", Extensions: []string{".chd"}}}, "", true); p != "" {
				u.startCheck(p)
			}
		}))
		return []*TButtonWidget{check}
	},
	options: func(u *ui, parent *TFrameWidget) []Widget {
		v := Variable(tclBool(!u.cfg.ChdNoM3U))
		m3u := parent.TCheckbutton(Txt(T("opt.m3u")), v, Command(func() {
			u.cfg.ChdNoM3U = v.Get() != "1"
			u.saveConfig()
		}))
		setTooltip(m3u, T("opt.m3u.tip"))
		return []Widget{m3u}
	},
	menu: func(u *ui, t *convTab, it *convItem) []menuEntry {
		if !isISO(it.path) || !it.ok {
			return nil
		}
		_, autoCD, _ := discinfo.FromISO(it.path)
		auto := "DVD"
		if autoCD {
			auto = "CD"
		}
		set := func(mode string) func() {
			return func() {
				it.mode = mode
				describeDisc(it)
				t.refresh(u)
			}
		}
		return []menuEntry{
			{T("chd.menu_auto", auto), it.mode == "", set("")},
			{T("chd.menu_cd"), it.mode == "cd", set("cd")},
			{T("chd.menu_dvd"), it.mode == "dvd", set("dvd")},
		}
	},
	after: func(u *ui, done []converted) {
		if u.cfg.ChdNoM3U {
			return
		}
		seen := map[string]bool{}
		for _, d := range done {
			game, _, ok := playlist.Disc(filepath.Base(d.out))
			key := strings.ToLower(filepath.Join(filepath.Dir(d.out), game))
			if !ok || seen[key] {
				continue
			}
			seen[key] = true
			path, discs, err := playlist.Update(d.out)
			switch {
			case err != nil:
				u.logf("log.merge_error", err)
			case path != "":
				u.logf("log.m3u", filepath.Base(path), len(discs))
			}
		}
	},
}

func isISO(path string) bool { return strings.EqualFold(filepath.Ext(path), ".iso") }
func isGDI(path string) bool { return strings.EqualFold(filepath.Ext(path), ".gdi") }

// describeDisc tells what a disc image is: its platform, and for an ISO
// whether it becomes a CD or a DVD CHD.
func describeDisc(it *convItem) {
	it.ok, it.platform, it.kind = false, "", ""
	if !isISO(it.path) {
		parse := chd.ParseCue
		if isGDI(it.path) {
			parse = chd.ParseGDI
		}
		tracks, err := parse(it.path)
		if err != nil {
			it.detail = lazyf("⚠ %v", err)
			return
		}
		var size int64
		seen := map[string]bool{}
		for _, t := range tracks {
			if !seen[t.File] {
				seen[t.File] = true
				if st, err := os.Stat(t.File); err == nil {
					size += st.Size()
				}
			}
		}
		it.size = size
		if chd.IsGDROM(tracks) {
			it.ok, it.kind, it.platform = true, "gd", discinfo.Dreamcast
			it.detail = msg("chd.row_gd", len(tracks), humanSize(size))
			return
		}
		it.platform, _ = discinfo.FromCue(it.path)
		it.ok, it.kind, it.detail = true, "cd", msg("chd.row_cd", len(tracks), humanSize(size))
		return
	}
	st, err := os.Stat(it.path)
	switch {
	case err != nil:
		it.detail = lazyf("⚠ %v", err)
		return
	case st.Size() == 0 || st.Size()%chd.DVDSectorSize != 0:
		it.detail = msg("chd.row_bad_iso")
		return
	}
	platform, autoCD, _ := discinfo.FromISO(it.path)
	it.ok, it.platform = true, platform
	it.kind = map[bool]string{true: "cd", false: "dvd"}[autoCD]
	if it.mode != "" {
		it.kind = it.mode
	}
	size, kind, chosen := humanSize(st.Size()), strings.ToUpper(it.kind), it.mode != ""
	it.detail = lazyLine(func() string {
		how := T("chd.auto")
		if chosen {
			how = T("chd.chosen")
		}
		return T("chd.row_iso", size, kind, how)
	})
}

// openFiles asks for several files, starting in and remembering the last
// folder used.
func (u *ui) openFiles(types []FileType) []string {
	opts := []string{"-parent", ".", "-title", T("app.title"), "-filetypes", fileTypes(types), "-multiple", "1"}
	if u.cfg.LastDir != "" {
		opts = append(opts, "-initialdir", u.cfg.LastDir)
	}
	cmd := "tk_getOpenFile"
	if !nativeDialogs() {
		cmd = "::tk::dialog::file::"
		opts = append([]string{"open"}, opts...)
		tcl("auto_load", cmd)
		patchDialogs()
	}
	list := tcl(append([]string{cmd}, opts...)...)
	n, _ := strconv.Atoi(tcl("llength", list))
	var paths []string
	for i := range n {
		paths = append(paths, filepath.FromSlash(tcl("lindex", list, strconv.Itoa(i))))
	}
	if len(paths) > 0 {
		u.cfg.LastDir = filepath.Dir(paths[0])
		u.saveConfig()
	}
	return paths
}
