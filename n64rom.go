package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/n64"
)

// describeN64 shows a Nintendo 64 ROM in the ROMs tab: its game code,
// title and revision, and its byte order when it is not the cartridge's.
func describeN64(it *convItem, size string) {
	info, err := n64.Read(it.path)
	if err != nil {
		return
	}
	title := info.Title
	if info.Version > 0 {
		title += " (Rev " + strconv.Itoa(info.Version) + ")"
	}
	if info.Order != n64.Z64 {
		it.detail = msg("n64.row_swapped", info.Code, title, size, info.Order.String())
		return
	}
	it.detail = msg("n64.row", info.Code, title, size)
}

// convertN64 writes the checked Nintendo 64 ROMs that are in another byte
// order (.v64, .n64) as .z64, next to them, and removes the originals once
// the new files are read back.
func (t *convTab) convertN64(u *ui) {
	if u.busy {
		return
	}
	var items []*convItem
	for _, it := range t.items {
		if markable(it) && hasExt(it.path, []string{".z64", ".v64", ".n64"}) {
			items = append(items, it)
		}
	}
	items, ok := t.keepChecked(items)
	if !ok {
		return
	}
	var todo []*convItem
	for _, it := range items {
		if info, err := n64.Read(it.path); err == nil && info.Order != n64.Z64 {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("n64.empty")))
		return
	}
	where := T("n64.confirm.permanent")
	if usesTrash {
		where = T("n64.confirm.trash")
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("n64.confirm", len(todo))), Detail(T("n64.confirm.detail")+"\n\n"+where))
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
		for _, it := range todo {
			if ctx.Err() != nil {
				break
			}
			base := filepath.Base(it.path)
			to := strings.TrimSuffix(it.path, filepath.Ext(it.path)) + ".z64"
			u.setStatus(T("nds.status", base))
			if _, err := os.Stat(to); err == nil {
				u.logf("n64.exists", base, filepath.Base(to))
				u.setItemStatus(it, msg("nds.st_error"))
				continue
			}
			_, err := n64.Convert(ctx, it.path, to, func(done, total int64) {
				u.setProgress(float64(done) / float64(max(total, 1)))
			})
			u.setProgress(0)
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				u.logf("drename.skip_error", base, err)
				u.setItemStatus(it, msg("nds.st_error"))
				continue
			}
			u.logf("n64.converted", base, filepath.Base(to))
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
			u.logf("n64.summary", len(moves), len(todo))
		}
		PostEvent(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			renamed := map[string]string{}
			for _, m := range moves {
				if _, err := os.Stat(m.from); err == nil {
					// The original could not be removed: list both.
					continue
				}
				renamed[m.from] = m.to
				t.moved(m.from, m.to)
			}
			for _, p := range updatePlaylists(renamed) {
				u.logf("drename.m3u", filepath.Base(p))
			}
			u.flush()
			converted := map[string]bool{}
			for _, to := range renamed {
				converted[to] = true
			}
			for _, it := range todo {
				t.conv.describe(it)
				if converted[it.path] {
					it.status = msg("nds.st_done")
				}
			}
			t.refresh(u)
		}, false)
	}()
}
