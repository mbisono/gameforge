package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"gameforge/internal/i18n"
	"gameforge/internal/romcheck"
)

// maxCheckedROM is the largest ROM read whole for its checksum: cartridges
// whose checksum covers the whole ROM (SNES, Mega Drive) are much smaller,
// and a larger .bin is a disc image.
const maxCheckedROM = 16 << 20

// romForCheck reads what the header check of a ROM needs: the ROM's
// extension and its first bytes (or all of it). The ROM may be alone in a
// ZIP or 7z. need is 0 for a ROM without a known checksum.
func romForCheck(ctx context.Context, r romFile) (string, []byte, error) {
	ext := r.ext()
	need := romcheck.Need(ext)
	if need == 0 || r.files != 1 {
		return ext, nil, nil
	}
	if need < 0 {
		if r.size > maxCheckedROM {
			return ext, nil, nil
		}
		need = r.size
	}
	limit := int(min(need, maxCheckedROM))
	open := r.open()
	if open == nil {
		return ext, nil, nil
	}
	rc, err := open()
	if err != nil {
		return ext, nil, err
	}
	defer rc.Close()
	b := make([]byte, limit)
	n, err := io.ReadFull(readerCtx{ctx, rc}, b)
	if err != nil && err != io.ErrUnexpectedEOF {
		return ext, nil, err
	}
	return ext, b[:n], nil
}

// checkHeaders checks the header checksums of the checked ROMs of the ROMs
// tab (Nintendo 64, SNES, Mega Drive, Nintendo DS), loose or alone in a
// ZIP or 7z, and shows the results in the status column and the log.
func (t *convTab) checkHeaders(u *ui) {
	if u.busy {
		return
	}
	items, ok := t.romItems()
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	u.separate()
	u.logf("crc.start", len(items))
	go func() {
		defer cancel()
		good, bad, none := 0, 0, 0
		for i, it := range items {
			if ctx.Err() != nil {
				break
			}
			base := filepath.Base(it.path)
			u.setStatus(T("drename.status", base))
			u.setProgress(float64(i) / float64(len(items)))
			r := scanRom(it)
			ext, data, err := romForCheck(ctx, r)
			if r.err != nil {
				err = r.err
			}
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				u.println(i18n.M("drename.skip_error", base, err))
				u.setItemStatus(it, msg("nds.st_error"))
				continue
			}
			res, known := romcheck.Check(ext, data)
			hex := func(v uint32) string { return fmt.Sprintf("%0*X", res.Width, v) }
			switch {
			case !known:
				none++
				u.setItemStatus(it, msg("crc.st_none"))
			case res.OK:
				good++
				u.setItemStatus(it, msg("crc.st_ok", res.System))
				u.logf("crc.ok", base, res.System, hex(res.Stored))
			default:
				bad++
				u.setItemStatus(it, msg("crc.st_bad", res.System))
				u.logf("crc.bad", base, res.System, hex(res.Stored), hex(res.Computed))
			}
		}
		u.setProgress(0)
		u.setStatus("")
		cancelled := ctx.Err() != nil
		if !cancelled {
			u.logf("crc.summary", good, bad, none)
		}
		onGUI(func() {
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if cancelled {
				u.logf("log.cancelled")
			}
			u.flush()
		})
	}()
}
