package merge

import (
	"fmt"
	"path/filepath"
	"strings"
)

// StandardName returns the usual file name for n: "Title [TitleID][vN]"
// plus the extension of n. A base game with bundled content counts it as
// the merged output does ("[vN]" of the newest update, "[n DLC]"). A DLC
// is named "Title - DLC name"; dlcName may be empty. It returns "" for a
// file whose title is unknown.
func StandardName(title string, n *NSP, dlcName string) string {
	if title == "" || n.Kind == KindUnknown {
		return ""
	}
	title = cleanTitle(title)
	ext := strings.ToLower(filepath.Ext(n.Path))
	var name string
	switch n.Kind {
	case KindBase:
		return mergedName(title, n.AppID, []*NSP{n}, ext)
	case KindUpdate:
		name = fmt.Sprintf("%s [%016X][v%d]", title, n.TitleID, n.Version)
	case KindDLC:
		dlc := cleanTitle(dlcName)
		switch {
		case dlc == "":
			name = title + " - DLC"
		case strings.HasPrefix(strings.ToLower(dlc), strings.ToLower(title)):
			name = dlc
		default:
			name = title + " - " + dlc
		}
		name += fmt.Sprintf(" [%016X][v%d]", n.TitleID, n.Version)
	}
	return sanitize(name) + ext
}

// cleanTitle makes a title fit for a file name: "A: B" becomes "A - B",
// trademark signs go, and runs of spaces become one.
func cleanTitle(s string) string {
	s = strings.ReplaceAll(s, ": ", " - ")
	s = strings.NewReplacer("™", "", "®", "", "©", "", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, ". ")
}
