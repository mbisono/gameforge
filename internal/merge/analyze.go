// Package merge groups NSP files by game and merges base game, update and
// DLCs into a single multi-title NSP.
package merge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gameforge/internal/i18n"
	"gameforge/internal/nx"
)

// Kind classifies the content of an NSP.
type Kind int

const (
	KindUnknown Kind = iota
	KindBase
	KindUpdate
	KindDLC
)

func (k Kind) String() string {
	switch k {
	case KindBase:
		return i18n.T("kind.base")
	case KindUpdate:
		return i18n.T("kind.update")
	case KindDLC:
		return i18n.T("kind.dlc")
	}
	return i18n.T("kind.unknown")
}

// NSP is an analysed input file.
type NSP struct {
	Path    string
	Size    int64
	Entries []nx.PFS0Entry

	Kind    Kind
	TitleID uint64
	AppID   uint64
	Version uint32
	// FromCNMT is true when the metadata came from the decrypted CNMT,
	// false when it was guessed from the file name.
	FromCNMT bool
	// Note explains why CNMT metadata could not be used, or is nil; it is
	// translated when shown.
	Note error
	// Titles lists every CNMT in the file; a merged NSP has several.
	Titles []Title
}

// Title is one content meta (CNMT) inside an NSP.
type Title struct {
	Meta  *nx.ContentMeta
	Entry string // the .cnmt.nca it was read from
	// Control holds name, display version and icon of a base game or
	// update, nil if unavailable (DLCs have none).
	Control *nx.Control
}

// Firmware returns the minimum system version the title needs, such as
// "13.1.0", or "" if unknown. DLCs state an application version instead.
func (t Title) Firmware() string {
	if t.Kind() == KindDLC {
		return ""
	}
	return nx.FirmwareString(t.Meta.RequiredSystemVersion)
}

// Own returns the title of n itself (not one bundled into it), or nil when
// it was not read from a CNMT.
func (n *NSP) Own() *Title {
	for i, t := range n.Titles {
		if t.Meta.TitleID == n.TitleID {
			return &n.Titles[i]
		}
	}
	return nil
}

// Control returns the Control data of n's own title, or nil.
func (n *NSP) Control() *nx.Control {
	if t := n.Own(); t != nil {
		return t.Control
	}
	return nil
}

// Kind returns the kind of content the title describes.
func (t Title) Kind() Kind {
	return map[uint8]Kind{nx.MetaApplication: KindBase, nx.MetaPatch: KindUpdate, nx.MetaAddOnContent: KindDLC}[t.Meta.Type]
}

// Bundled returns the titles merged into n besides its own: the update and
// DLCs inside a merged base game NSP.
func (n *NSP) Bundled() []Title {
	var out []Title
	for _, t := range n.Titles {
		if t.Meta.TitleID != n.TitleID {
			out = append(out, t)
		}
	}
	return out
}

// BundledUpdate returns the newest update merged into n, or nil.
func (n *NSP) BundledUpdate() *Title {
	var u *Title
	for _, t := range n.Bundled() {
		if t.Kind() == KindUpdate && t.Meta.ApplicationID == n.AppID && (u == nil || t.Meta.Version > u.Meta.Version) {
			u = &t
		}
	}
	return u
}

// titleEntries returns the lower-case names of the entries that belong to t:
// its Meta NCA, the NCAs (or NCZs) it lists and the tickets and certificates
// of its title ID.
func (n *NSP) titleEntries(t Title) map[string]bool {
	out := map[string]bool{strings.ToLower(t.Entry): true}
	for _, r := range t.Meta.Contents {
		id := fmt.Sprintf("%x", r.ID)
		out[id+".nca"], out[id+".ncz"] = true, true
	}
	prefix := fmt.Sprintf("%016x", t.Meta.TitleID)
	for _, e := range n.Entries {
		name := strings.ToLower(e.Name)
		if strings.HasPrefix(name, prefix) && (strings.HasSuffix(name, ".tik") || strings.HasSuffix(name, ".cert")) {
			out[name] = true
		}
	}
	return out
}

// asUpdate returns a view of the update t bundled in the merged NSP n.
func (n *NSP) asUpdate(t *Title) *NSP {
	return &NSP{Path: n.Path, Size: n.Size, Entries: n.Entries, Kind: KindUpdate, TitleID: t.Meta.TitleID,
		AppID: t.Meta.ApplicationID, Version: t.Meta.Version, FromCNMT: true, Titles: []Title{*t}}
}

// Name returns the file name without directory.
func (n *NSP) Name() string { return filepath.Base(n.Path) }

// Patched reports whether n is a base game with an update already applied
// (original base games are v0). Updates only apply to the original base, so
// such a file cannot take another update.
func (n *NSP) Patched() bool { return n.Kind == KindBase && n.Version > 0 }

// checkSelection rejects a base game with an applied update selected
// together with an update: the update would be applied on top of it again.
func checkSelection(sel []*NSP) error {
	for _, b := range sel {
		if !b.Patched() {
			continue
		}
		for _, u := range sel {
			if u.Kind == KindUpdate {
				return i18n.Errorf("err.patched_base_update", b.Name(), b.Version)
			}
		}
	}
	return nil
}

var (
	reTitleID = regexp.MustCompile(`\[([0-9A-Fa-f]{16})\]`)
	reVersion = regexp.MustCompile(`\[v(\d+)\]`)
)

// Analyze reads an NSP and determines what it contains. keys may be nil,
// in which case the file name is used to guess title ID and version.
func Analyze(path string, keys *nx.Keys) (*NSP, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var entries []nx.PFS0Entry
	if strings.EqualFold(filepath.Ext(path), ".xci") {
		entries, err = nx.ReadXCI(f) // the card's secure partition
	} else {
		entries, err = nx.ReadPFS0(f, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	n := &NSP{Path: path, Size: st.Size(), Entries: entries}

	if keys != nil {
		n.Titles, err = readMetas(f, entries, keys)
		if err != nil {
			n.Note = err
		}
		readControls(f, n, keys)
	} else {
		n.Note = i18n.Errorf("err.no_keys")
	}

	if len(n.Titles) > 0 {
		// An NSP may already contain several titles; prefer the base game.
		t := n.Titles[0]
		for _, c := range n.Titles {
			if c.Meta.Type == nx.MetaApplication {
				t = c
				break
			}
		}
		n.FromCNMT = true
		n.TitleID, n.AppID, n.Version = t.Meta.TitleID, t.Meta.ApplicationID, t.Meta.Version
		n.Kind = t.Kind()
		return n, nil
	}

	guessFromName(n)
	return n, nil
}

func readMetas(f *os.File, entries []nx.PFS0Entry, keys *nx.Keys) ([]Title, error) {
	hk, err := keys.HeaderKey()
	if err != nil {
		return nil, err
	}

	encTitleKeys := map[[16]byte][]byte{}
	if err := readTickets(f, entries, encTitleKeys); err != nil {
		return nil, err
	}

	var metas []Title
	var lastErr error
	for _, e := range entries {
		if !strings.HasSuffix(strings.ToLower(e.Name), ".cnmt.nca") {
			continue
		}
		m, err := readMeta(f, e, hk, keys, encTitleKeys)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", e.Name, err)
			continue
		}
		metas = append(metas, Title{Meta: m, Entry: e.Name})
	}
	if len(metas) == 0 && lastErr == nil {
		lastErr = i18n.Errorf("err.no_cnmt_nca")
	}
	return metas, lastErr
}

func readMeta(f *os.File, e nx.PFS0Entry, hk []byte, keys *nx.Keys, encTitleKeys map[[16]byte][]byte) (*nx.ContentMeta, error) {
	h, err := nx.ParseNCAHeader(f, e.Offset, hk)
	if err != nil {
		return nil, err
	}
	key, err := contentKey(h, keys, encTitleKeys)
	if err != nil {
		return nil, err
	}
	sec, err := nx.ReadSection(f, e.Offset, h, 0, key)
	if err != nil {
		return nil, err
	}
	return nx.ExtractCNMT(sec, h.Sections[0])
}

// readControls reads the Control NCA of each title of n that lists one. A
// missing or unreadable one is not an error: it only adds information.
func readControls(f *os.File, n *NSP, keys *nx.Keys) {
	hk, err := keys.HeaderKey()
	if err != nil {
		return
	}
	tickets := map[[16]byte][]byte{}
	readTickets(f, n.Entries, tickets)
	byName := map[string]nx.PFS0Entry{}
	for _, e := range n.Entries {
		byName[strings.ToLower(e.Name)] = e
	}
	for i, t := range n.Titles {
		for _, r := range t.Meta.Contents {
			e, ok := byName[fmt.Sprintf("%x.nca", r.ID)]
			if r.Type != nx.ContentRecordControl || !ok {
				continue
			}
			h, err := nx.ParseNCAHeader(f, e.Offset, hk)
			if err != nil {
				continue
			}
			key, err := contentKey(h, keys, tickets)
			if err != nil {
				continue
			}
			sec, err := nx.ReadSection(f, e.Offset, h, 0, key)
			if err != nil {
				continue
			}
			n.Titles[i].Control, _ = nx.ParseControl(sec, h.FsHeader(0))
		}
	}
}

// readTickets adds the encrypted title keys of all tickets in entries to
// encTitleKeys, by rights ID.
func readTickets(f io.ReaderAt, entries []nx.PFS0Entry, encTitleKeys map[[16]byte][]byte) error {
	for _, e := range entries {
		if !strings.HasSuffix(strings.ToLower(e.Name), ".tik") || e.Size > 0x1000 {
			continue
		}
		b := make([]byte, e.Size)
		if _, err := f.ReadAt(b, e.Offset); err != nil {
			return err
		}
		if rid, tk, err := nx.ParseTicket(b); err == nil {
			encTitleKeys[rid] = tk
		}
	}
	return nil
}

// contentKey returns the decrypted AES-CTR key of an NCA, using the title
// key from a ticket when the NCA has a rights ID.
func contentKey(h *nx.NCAHeader, keys *nx.Keys, encTitleKeys map[[16]byte][]byte) ([]byte, error) {
	var titleKey []byte
	if h.HasRightsID() {
		enc, ok := encTitleKeys[h.RightsID]
		if !ok {
			return nil, i18n.Errorf("err.tik.missing", h.RightsID)
		}
		var err error
		if titleKey, err = nx.DecryptTitleKey(keys, enc, h.MasterKeyRev()); err != nil {
			return nil, err
		}
	}
	return h.SectionKey(keys, titleKey)
}

// guessFromName fills title ID, version and kind from the usual
// "Name [0100XXXXXXXXXXXX][v123].nsp" naming convention.
func guessFromName(n *NSP) {
	name := n.Name()
	if m := reTitleID.FindStringSubmatch(name); m != nil {
		n.TitleID, _ = strconv.ParseUint(m[1], 16, 64)
	}
	if m := reVersion.FindStringSubmatch(name); m != nil {
		v, _ := strconv.ParseUint(m[1], 10, 32)
		n.Version = uint32(v)
	}
	if n.TitleID == 0 {
		return
	}
	n.Kind, n.AppID = ClassifyTitleID(n.TitleID)
}

// ClassifyTitleID derives content kind and base application ID from a title ID.
func ClassifyTitleID(id uint64) (Kind, uint64) {
	switch low := id & 0xFFF; {
	case low == 0:
		return KindBase, id
	case low == 0x800:
		return KindUpdate, id &^ 0xFFF
	default:
		return KindDLC, (id - 0x1000) &^ 0xFFF
	}
}
