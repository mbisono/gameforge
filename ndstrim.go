package main

import (
	"context"
	"errors"
	"path/filepath"

	. "modernc.org/tk9.0"

	"gameforge/internal/nds"
)

// describeNDS shows a Nintendo DS ROM in the ROMs tab: its icon, game
// code, title and size, and whether it is trimmed.
func describeNDS(it *convItem, size string) {
	it.icon = ndsIcon(it.path)
	info, err := nds.Read(it.path)
	switch {
	case err != nil:
		it.detail = msg("sz.row", size)
	case info.Trimmed():
		it.detail = msg("nds.row_trimmed", info.Code, info.Name(), size, humanSize(info.Capacity))
	default:
		it.detail = msg("nds.row", info.Code, info.Name(), size)
	}
}

// trimNDS trims the checked Nintendo DS ROMs of the ROMs tab (cuts the
// empty space after the game), or with untrim restores their full size.
// The files are replaced, after the new ones are read back.
func (t *convTab) trimNDS(u *ui, untrim bool) {
	if u.busy {
		return
	}
	var items []*convItem
	for _, it := range t.items {
		if markable(it) && hasExt(it.path, []string{".nds"}) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("nds.empty")))
		return
	}
	items, ok := t.keepChecked(items)
	if !ok {
		return
	}
	ask := "nds.trim_confirm"
	if untrim {
		ask = "nds.untrim_confirm"
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T(ask, len(items))), Detail(T("nds.confirm.detail")))
	if answer != "yes" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	u.separate()
	go func() {
		defer cancel()
		var before, after int64
		changed := 0
		for _, it := range items {
			if ctx.Err() != nil {
				break
			}
			base := filepath.Base(it.path)
			u.setStatus(T("nds.status", base))
			progress := func(done, total int64) { u.setProgress(float64(done) / float64(max(total, 1))) }
			var res nds.Result
			var err error
			if untrim {
				res, err = nds.Untrim(ctx, it.path, progress)
			} else {
				res, err = nds.Trim(ctx, it.path, progress)
			}
			u.setProgress(0)
			switch {
			case ctx.Err() != nil:
			case errors.Is(err, nds.ErrNotEmpty):
				u.logf("nds.not_empty", base)
				u.setItemStatus(it, msg("nds.st_kept"))
			case err != nil:
				u.logf("drename.skip_error", base, err)
				u.setItemStatus(it, msg("nds.st_error"))
			case res.Before == res.After:
				key := "nds.already_trimmed"
				if untrim {
					key = "nds.already_full"
				}
				u.logf(key, base)
			default:
				changed++
				before += res.Before
				after += res.After
				key := "nds.trimmed"
				if untrim {
					key = "nds.untrimmed"
				}
				u.logf(key, base, humanSize(res.Before), humanSize(res.After))
				u.setItemStatus(it, msg("nds.st_done"))
			}
		}
		u.setStatus("")
		cancelled := ctx.Err() != nil
		if !cancelled {
			u.logf("nds.summary", changed, len(items), humanSize(before), humanSize(after))
		}
		onGUI(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			u.flush()
			for _, it := range items {
				t.conv.describe(it)
			}
			t.refresh(u)
		})
	}()
}
