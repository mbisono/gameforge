package nx

import (
	"fmt"
	"strconv"
	"strings"

	"gameforge/internal/i18n"
)

// keyFirmware is the first firmware of each master key revision.
var keyFirmware = []string{
	"1.0.0", "3.0.0", "3.0.1", "4.0.0", "5.0.0", "6.0.0", "6.2.0", "7.0.0",
	"8.1.0", "9.0.0", "9.1.0", "12.1.0", "13.0.0", "14.0.0", "15.0.0", "16.0.0",
	"17.0.0", "18.0.0", "19.0.0", "20.0.0", "21.0.0", "22.0.0", "23.0.0",
}

// KeyFirmware returns the first firmware that uses master key revision rev,
// or "newer than x" past the newest one known.
func KeyFirmware(rev int) string {
	switch {
	case rev < 0:
		return "?"
	case rev < len(keyFirmware):
		return keyFirmware[rev]
	}
	return i18n.T("fw.newer_than", keyFirmware[len(keyFirmware)-1])
}

// KnownKey reports whether the firmware of master key revision rev is
// known to the app.
func KnownKey(rev int) bool { return rev >= 0 && rev < len(keyFirmware) }

// LastKnownFirmware is the firmware of the newest master key the app knows.
func LastKnownFirmware() string { return keyFirmware[len(keyFirmware)-1] }

// Newest returns the newest master key revision the keys can decrypt, or
// -1 if there is none.
func (k *Keys) Newest() int {
	newest := -1
	for name := range k.m {
		for _, prefix := range []string{"master_key_", "titlekek_", "key_area_key_application_"} {
			if hexRev, ok := strings.CutPrefix(name, prefix); ok && len(hexRev) == 2 {
				if rev, err := strconv.ParseUint(hexRev, 16, 8); err == nil {
					newest = max(newest, int(rev))
				}
			}
		}
	}
	return newest
}

// MissingKeyError reports that prod.keys lacks the keys of the firmware a
// file was made for.
type MissingKeyError struct {
	Rev    int // the master key revision needed
	Newest int // the newest revision of the keys, or -1
}

func (e *MissingKeyError) Error() string {
	if e.Newest < 0 {
		return i18n.T("err.keys.missing_rev", KeyFirmware(e.Rev), fmt.Sprintf("%02x", e.Rev))
	}
	return i18n.T("err.keys.too_old", KeyFirmware(e.Rev), fmt.Sprintf("%02x", e.Rev),
		KeyFirmware(e.Newest), fmt.Sprintf("%02x", e.Newest))
}
