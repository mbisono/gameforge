package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/n64"
	"gameforge/internal/romcheck"
)

// romJob is a tool of the ROMs tab that rewrites loose ROMs: in place, or
// as a new file whose original is removed once the new one is read back.
type romJob struct {
	// pick tells whether a checked file is one the tool works on.
	pick func(path string) bool
	// target is the new file's path: the same path to rewrite it in place.
	target func(path string) string
	// write writes src's new form to dst, which must not exist, and reads
	// it back.
	write func(ctx context.Context, src, dst string, progress func(done, total int64)) error
	// Message keys: no files, the question (with the number of files) and
	// its detail, a file done (old and new name), the summary (done, all).
	empty, confirm, detail, done, summary string
}

// runROMJob runs a tool on the checked files of the ROMs tab, in the
// background, after asking.
func (t *convTab) runROMJob(u *ui, job romJob) {
	if u.busy {
		return
	}
	var items []*convItem
	for _, it := range t.items {
		if markable(it) && !isArchive(it.path) && job.pick(it.path) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T(job.empty)))
		return
	}
	todo, ok := t.keepChecked(items)
	if !ok {
		return
	}
	where := T("n64.confirm.permanent")
	if usesTrash {
		where = T("n64.confirm.trash")
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T(job.confirm, len(todo))), Detail(T(job.detail)+"\n\n"+where))
	if answer != "yes" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	u.separate()
	type move struct{ from, to string }
	go func() {
		defer cancel()
		var moves []move
		done := map[string]bool{}
		for _, it := range todo {
			if ctx.Err() != nil {
				break
			}
			base := filepath.Base(it.path)
			to := job.target(it.path)
			inPlace := to == it.path
			dst := to
			if inPlace {
				dst = it.path + ".gameforge.tmp"
			}
			u.setStatus(T("nds.status", base))
			if _, err := os.Stat(dst); err == nil {
				u.logf("n64.exists", base, filepath.Base(dst))
				u.setItemStatus(it, msg("nds.st_error"))
				continue
			}
			st, _ := os.Stat(it.path)
			err := job.write(ctx, it.path, dst, func(d, total int64) { u.setProgress(float64(d) / float64(max(total, 1))) })
			u.setProgress(0)
			if ctx.Err() != nil {
				os.Remove(dst)
				break
			}
			if err == nil && st != nil {
				os.Chtimes(dst, st.ModTime(), st.ModTime())
			}
			if err == nil && inPlace {
				if err = os.Rename(dst, it.path); err != nil {
					os.Remove(dst)
				}
			}
			if err != nil {
				u.logf("drename.skip_error", base, err)
				u.setItemStatus(it, msg("nds.st_error"))
				continue
			}
			u.logf(job.done, base, filepath.Base(to))
			if inPlace {
				done[it.path] = true
				u.setItemStatus(it, msg("nds.st_done"))
				continue
			}
			removeMu.Lock()
			err = removeFile(it.path)
			removeMu.Unlock()
			if err != nil {
				u.logf("sz.delete_error", base, err)
			}
			moves = append(moves, move{it.path, to})
		}
		u.setStatus("")
		cancelled := ctx.Err() != nil
		if !cancelled {
			u.logf(job.summary, len(moves)+len(done), len(todo))
		}
		onGUI(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			renamed := map[string]string{}
			for _, m := range moves {
				if _, err := os.Stat(m.from); err == nil {
					continue // the original could not be removed: list both
				}
				renamed[m.from] = m.to
				t.moved(m.from, m.to)
				done[m.to] = true
			}
			for _, p := range updatePlaylists(renamed) {
				u.logf("drename.m3u", filepath.Base(p))
			}
			u.flush()
			for _, it := range todo {
				t.conv.describe(it)
				if done[it.path] {
					it.status = msg("nds.st_done")
				}
			}
			t.refresh(u)
		})
	}()
}

// writeVerified writes data to dst, which must not exist, and reads it
// back; on an error dst is removed.
func writeVerified(dst string, data []byte) error {
	f, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(dst)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	back := make([]byte, len(data)+1)
	n, err := f.ReadAt(back, 0)
	if err != nil && err != io.EOF {
		return err
	}
	if !bytes.Equal(back[:n], data) {
		return errors.New("the new file differs from what was written")
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// withExt is path with another extension.
func withExt(path, ext string) string { return strings.TrimSuffix(path, filepath.Ext(path)) + ext }

// fileHead reads the first n bytes of a file and its size.
func fileHead(path string, n int) ([]byte, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0
	}
	b := make([]byte, n)
	k, _ := io.ReadFull(f, b)
	return b[:k], st.Size()
}

var (
	snesExts = []string{".sfc", ".smc", ".swc", ".fig"}
	mdExts   = []string{".smd", ".md", ".gen", ".bin"}
)

// hasCopierHeader tells whether a SNES or PC Engine ROM starts with the
// 512-byte header of a copier, by its size.
func hasCopierHeader(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= 512 {
		return false
	}
	switch {
	case hasExt(path, snesExts):
		return st.Size()%1024 == 512
	case hasExt(path, []string{".pce"}):
		return st.Size()%8192 == 512
	}
	return false
}

// n64Job writes Nintendo 64 ROMs in another byte order as .z64.
var n64Job = romJob{
	pick: func(path string) bool {
		info, err := n64.Read(path)
		return hasExt(path, []string{".z64", ".v64", ".n64"}) && err == nil && info.Order != n64.Z64
	},
	target: func(path string) string { return withExt(path, ".z64") },
	write: func(ctx context.Context, src, dst string, progress func(done, total int64)) error {
		_, err := n64.Convert(ctx, src, dst, progress)
		return err
	},
	empty: "n64.empty", confirm: "n64.confirm", detail: "n64.confirm.detail", done: "n64.converted", summary: "n64.summary",
}

// smdJob writes Mega Drive ROMs in the interleaved .smd format as .md.
var smdJob = romJob{
	pick: func(path string) bool {
		head, size := fileHead(path, 16)
		return hasExt(path, mdExts) && romcheck.IsSMD(head, size)
	},
	target: func(path string) string { return withExt(path, ".md") },
	write: func(ctx context.Context, src, dst string, progress func(done, total int64)) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if !romcheck.IsSMD(data, int64(len(data))) {
			return errors.New("not an .smd file")
		}
		return writeVerified(dst, romcheck.DecodeSMD(data[512:]))
	},
	empty: "smd.empty", confirm: "smd.confirm", detail: "smd.confirm.detail", done: "n64.converted", summary: "n64.summary",
}

// headerJob removes the copier header of SNES and PC Engine ROMs; a SNES
// .smc (or .swc, .fig) becomes .sfc, the extension of headerless ROMs.
var headerJob = romJob{
	pick: hasCopierHeader,
	target: func(path string) string {
		if hasExt(path, []string{".smc", ".swc", ".fig"}) {
			return withExt(path, ".sfc")
		}
		return path
	},
	write: func(ctx context.Context, src, dst string, progress func(done, total int64)) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if !hasCopierHeader(src) {
			return errors.New("no copier header")
		}
		return writeVerified(dst, data[512:])
	},
	empty: "hdr.empty", confirm: "hdr.confirm", detail: "hdr.confirm.detail", done: "hdr.removed", summary: "hdr.summary",
}
