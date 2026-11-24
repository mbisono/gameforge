package redump

import (
	"testing"
	"time"
)

// TestOldest: downloading one list leaves the others as old as they are.
func TestOldest(t *testing.T) {
	jan := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	db := &DB{Downloaded: mar, Lists: map[string]*List{
		"PS1":    {Checked: jan},
		"Saturn": {Checked: mar},
	}}
	if got := db.Oldest(); !got.Equal(jan) {
		t.Errorf("oldest %v, want %v", got, jan)
	}
	// A list saved by an older version has no date of its own.
	db.Lists["PS1"].Checked = time.Time{}
	if got := db.Oldest(); !got.Equal(mar) {
		t.Errorf("oldest %v, want the download date %v", got, mar)
	}
}
