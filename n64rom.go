package main

import (
	"strconv"

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
func (t *convTab) convertN64(u *ui) { t.runROMJob(u, n64Job) }
