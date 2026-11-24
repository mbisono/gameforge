package merge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strings"

	"gameforge/internal/i18n"
	"gameforge/internal/nsz"
	"gameforge/internal/nx"
)

// Applying an update ("patching") builds a single base title that already
// contains the update: the update's RomFS is a BKTR section whose
// relocation table maps every byte of the new section either to the base
// game's RomFS section or to the update's own data. Reading the virtual
// section through that table gives the complete updated section, IVFC hash
// levels included, so its FsHeader can be reused as is.
//
// The new NCAs keep their content keys but are rewritten without rights ID
// (no ticket needed) and their RSA signatures no longer match. Emulators do
// not check them; a console only accepts the result with signature patches.

// plainNCA reads the decrypted bytes of an NCA.
type plainNCA struct {
	r    io.ReaderAt
	off  int64
	secs []nsz.Section
}

func (p plainNCA) ReadAt(b []byte, o int64) (int, error) {
	n, err := p.r.ReadAt(b, p.off+o)
	nsz.Crypt(p.secs, o, b[:n])
	return n, err
}

type relocEntry struct {
	virt, phys int64
	patch      bool // data comes from the update, else from the base RomFS
}

// hashWriter writes sequentially at pos and hashes what it writes.
type hashWriter struct {
	w   io.WriterAt
	pos int64
	n   int64
	h   hash.Hash
}

func newHashWriter(w io.WriterAt, pos int64) *hashWriter {
	return &hashWriter{w: w, pos: pos, h: sha256.New()}
}

func (hw *hashWriter) Write(b []byte) (int, error) {
	n, err := hw.w.WriteAt(b, hw.pos+hw.n)
	hw.h.Write(b[:n])
	hw.n += int64(n)
	return n, err
}

func (hw *hashWriter) sum() (s [32]byte) {
	copy(s[:], hw.h.Sum(nil))
	return s
}

// patchContent is one NCA of the patched title.
type patchContent struct {
	rec   nx.ContentRecord // type and ID offset from the source CNMT
	src   *NSP
	entry nx.PFS0Entry
	hdr   *nx.NCAHeader
	key   []byte
	base  *patchContent // base Program NCA when this one has a BKTR section

	outSize int64
	outHash [32]byte
}

// WritePatched applies the newest selected update to the selected base game
// and writes one NSP with the updated game (plus the selected DLCs,
// unchanged). With opt.Compress the result is then compressed to NSZ.
func WritePatched(ctx context.Context, sel []*NSP, outPath string, opt Options,
	progress func(phase Phase, done, total int64), logf func(fmt.Stringer)) (Result, error) {
	var res Result
	if progress == nil {
		progress = func(Phase, int64, int64) {}
	}
	if logf == nil {
		logf = func(fmt.Stringer) {}
	}
	if opt.Keys == nil {
		return res, i18n.Errorf("err.patch_needs_keys")
	}
	if err := checkSelection(sel); err != nil {
		return res, err
	}
	for _, n := range sel {
		res.InputSize += n.Size
		// The patcher reads NCAs directly; DLCs are only copied.
		for _, e := range n.Entries {
			if n.Kind != KindDLC && strings.HasSuffix(strings.ToLower(e.Name), ".ncz") {
				return res, i18n.Errorf("err.patch_nsz", n.Name())
			}
		}
	}
	sel, skip := resolveBundled(sel, logf)
	var base, upd *NSP
	var dlcs []*NSP
	for _, n := range sel {
		switch n.Kind {
		case KindBase:
			if base == nil || n.Version > base.Version {
				base = n
			}
		case KindUpdate:
			if upd != nil {
				logf(i18n.M("log.patch_old_update", min(upd.Version, n.Version)))
			}
			if upd == nil || n.Version > upd.Version {
				upd = n
			}
		case KindDLC:
			dlcs = append(dlcs, n)
		}
	}
	if base == nil {
		return res, i18n.Errorf("err.patch_no_base")
	}
	// A merged base game NSP brings its own update and DLCs, unless newer
	// selected files replace them.
	kept := func(t Title) bool { return !skip[base][strings.ToLower(t.Entry)] }
	if b := base.BundledUpdate(); b != nil && kept(*b) && (upd == nil || b.Meta.Version > upd.Version) {
		upd = base.asUpdate(b)
	}
	if upd == nil {
		return res, i18n.Errorf("err.patch_no_update")
	}
	bundledDLC := map[string]bool{}
	for _, t := range base.Bundled() {
		if t.Kind() == KindDLC && kept(t) {
			for name := range base.titleEntries(t) {
				bundledDLC[name] = true
			}
		}
	}

	if !opt.Compress {
		r, err := writePatchedNSP(ctx, base, upd, dlcs, bundledDLC, outPath, opt, progress, logf)
		r.InputSize = res.InputSize
		return r, err
	}
	// Patch to a temporary NSP, then compress it like any other NSP.
	tmp := strings.TrimSuffix(outPath, ".nsz") + ".patched.tmp.nsp"
	defer os.Remove(tmp)
	defer os.Remove(tmp + ".part")
	pr, err := writePatchedNSP(ctx, base, upd, dlcs, bundledDLC, tmp, opt, progress, logf)
	if err != nil {
		return res, err
	}
	logf(i18n.M("log.patch_written", HumanSize(pr.OutputSize)))
	n, err := Analyze(tmp, opt.Keys)
	if err != nil {
		return res, err
	}
	r, err := Write(ctx, []*NSP{n}, outPath, opt, progress, logf)
	r.InputSize = res.InputSize
	return r, err
}

// writePatchedNSP writes the patched game plus the DLC files and the entries
// of base named in bundledDLC (DLCs inside a merged base game NSP).
func writePatchedNSP(ctx context.Context, base, upd *NSP, dlcs []*NSP, bundledDLC map[string]bool, outPath string, opt Options,
	progress func(Phase, int64, int64), logf func(fmt.Stringer)) (Result, error) {
	var res Result
	keys := opt.Keys
	hk, err := keys.HeaderKey()
	if err != nil {
		return res, err
	}
	files := map[string]*os.File{}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	open := func(path string) (*os.File, error) {
		if f, ok := files[path]; ok {
			return f, nil
		}
		f, err := os.Open(path)
		if err == nil {
			files[path] = f
		}
		return f, err
	}

	logf(i18n.M("log.patch_start", upd.Version))
	contents, updMeta, err := planPatch(base, upd, keys, hk, open)
	if err != nil {
		return res, err
	}

	// Names are placeholders of the final length; the real names (content
	// hashes) are known only after writing and go into the final header.
	var srcs []nx.PFS0Source
	for range contents {
		srcs = append(srcs, nx.PFS0Source{Name: strings.Repeat("0", 32) + ".nca"})
	}
	srcs = append(srcs, nx.PFS0Source{Name: strings.Repeat("0", 32) + ".cnmt.nca"})
	seen := map[string]bool{}
	type extra struct {
		n *NSP
		e nx.PFS0Entry
	}
	var extras []extra
	for _, d := range append([]*NSP{base}, dlcs...) {
		for _, e := range d.Entries {
			if seen[strings.ToLower(e.Name)] || d == base && !bundledDLC[strings.ToLower(e.Name)] {
				continue
			}
			seen[strings.ToLower(e.Name)] = true
			extras = append(extras, extra{d, e})
			srcs = append(srcs, nx.PFS0Source{Name: e.Name, Size: e.Size})
		}
	}

	var total int64
	for _, c := range contents {
		total += c.entry.Size
		if c.base != nil {
			total += c.base.entry.Size / 2 // rough: part of the base RomFS is reused
		}
	}
	for _, x := range extras {
		total += x.e.Size
	}

	tmp := outPath + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return res, err
	}
	fail := func(err error) (Result, error) {
		out.Close()
		os.Remove(tmp)
		return res, err
	}
	hdr := nx.BuildPFS0Header(srcs)
	pos := int64(len(hdr))
	var done int64
	report := func(n int64) { done += n; progress(PhaseWrite, min(done, total), total) }

	for i, c := range contents {
		hw := newHashWriter(out, pos)
		if c.base != nil {
			err = writePatchedProgram(ctx, hw, c, keys, hk, report, logf, opt.Verify, out, pos)
		} else {
			err = writeTicketless(ctx, hw, c, keys, hk, open, report)
		}
		if err != nil {
			return fail(err)
		}
		c.outSize, c.outHash = hw.n, hw.sum()
		srcs[i] = nx.PFS0Source{Name: hex.EncodeToString(c.outHash[:16]) + ".nca", Size: c.outSize}
		pos += c.outSize
	}

	meta, err := buildPatchedMeta(base.AppID, updMeta, contents, keys, hk)
	if err != nil {
		return fail(err)
	}
	if _, err := out.WriteAt(meta, pos); err != nil {
		return fail(err)
	}
	ms := sha256.Sum256(meta)
	srcs[len(contents)] = nx.PFS0Source{Name: hex.EncodeToString(ms[:16]) + ".cnmt.nca", Size: int64(len(meta))}
	pos += int64(len(meta))

	for _, x := range extras {
		f, err := open(x.n.Path)
		if err != nil {
			return fail(err)
		}
		var last int64
		if _, err := copyAt(ctx, out, pos, f, x.e.Offset, x.e.Size, false, func(d int64) { report(d - last); last = d }); err != nil {
			return fail(err)
		}
		pos += x.e.Size
	}

	if final := nx.BuildPFS0Header(srcs); len(final) != len(hdr) {
		return fail(fmt.Errorf("PFS0 header size changed"))
	} else if _, err := out.WriteAt(final, 0); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return res, err
	}
	res.OutputSize = pos
	return res, os.Rename(tmp, outPath)
}

// readContentMeta returns the CNMT of an NSP with the given meta type.
func readContentMeta(n *NSP, f *os.File, keys *nx.Keys, typ uint8) (*nx.ContentMeta, error) {
	metas, err := readMetas(f, n.Entries, keys)
	for _, m := range metas {
		if m.Meta.Type == typ {
			return m.Meta, nil
		}
	}
	if err == nil {
		err = i18n.Errorf("err.no_cnmt_nca")
	}
	return nil, fmt.Errorf("%s: %w", n.Name(), err)
}

// planPatch lists the NCAs of the patched title: every content of the
// update except delta fragments, plus base contents the update lacks.
func planPatch(base, upd *NSP, keys *nx.Keys, hk []byte, open func(string) (*os.File, error)) ([]*patchContent, *nx.ContentMeta, error) {
	bf, err := open(base.Path)
	if err != nil {
		return nil, nil, err
	}
	uf, err := open(upd.Path)
	if err != nil {
		return nil, nil, err
	}
	baseMeta, err := readContentMeta(base, bf, keys, nx.MetaApplication)
	if err != nil {
		return nil, nil, err
	}
	updMeta, err := readContentMeta(upd, uf, keys, nx.MetaPatch)
	if err != nil {
		return nil, nil, err
	}
	tickets := map[[16]byte][]byte{}
	for _, n := range []*NSP{base, upd} {
		f, _ := open(n.Path)
		if err := readTickets(f, n.Entries, tickets); err != nil {
			return nil, nil, err
		}
	}

	load := func(n *NSP, f *os.File, rec nx.ContentRecord) (*patchContent, error) {
		name := hex.EncodeToString(rec.ID[:]) + ".nca"
		for _, e := range n.Entries {
			if strings.EqualFold(e.Name, name) {
				h, err := nx.ParseNCAHeader(f, e.Offset, hk)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", name, err)
				}
				key, err := contentKey(h, keys, tickets)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", name, err)
				}
				return &patchContent{rec: rec, src: n, entry: e, hdr: h, key: key}, nil
			}
		}
		return nil, i18n.Errorf("err.patch_missing_nca", name, n.Name())
	}

	type slot struct{ typ, idOffset uint8 }
	var contents []*patchContent
	have := map[slot]bool{}
	for _, rec := range updMeta.Contents {
		if rec.Type == nx.ContentRecordDeltaFragment || rec.Type == nx.ContentRecordMeta {
			continue
		}
		c, err := load(upd, uf, rec)
		if err != nil {
			return nil, nil, err
		}
		contents = append(contents, c)
		have[slot{rec.Type, rec.IDOffset}] = true
	}
	baseBySlot := map[slot]*patchContent{}
	for _, rec := range baseMeta.Contents {
		if rec.Type == nx.ContentRecordMeta {
			continue
		}
		c, err := load(base, bf, rec)
		if err != nil {
			return nil, nil, err
		}
		baseBySlot[slot{rec.Type, rec.IDOffset}] = c
		if !have[slot{rec.Type, rec.IDOffset}] {
			contents = append(contents, c)
		}
	}

	for _, c := range contents {
		if c.src != upd || c.rec.Type != nx.ContentRecordProgram {
			continue
		}
		for _, s := range c.hdr.Sections {
			if s.End > s.Start && s.EncType == 4 {
				c.base = baseBySlot[slot{nx.ContentRecordProgram, c.rec.IDOffset}]
				if c.base == nil {
					return nil, nil, i18n.Errorf("err.patch_no_base")
				}
				break
			}
		}
	}
	hasProgram := false
	for _, c := range contents {
		hasProgram = hasProgram || c.rec.Type == nx.ContentRecordProgram
	}
	if !hasProgram {
		return nil, nil, i18n.Errorf("err.patch_unsupported", "CNMT without Program NCA")
	}
	sort.SliceStable(contents, func(i, j int) bool { return contents[i].rec.Type < contents[j].rec.Type })
	return contents, updMeta, nil
}

func plainReader(f io.ReaderAt, c *patchContent) (plainNCA, error) {
	secs, err := nsz.CryptoSections(f, c.entry.Offset, c.hdr, c.key)
	return plainNCA{f, c.entry.Offset, secs}, err
}

// hasStorageExtras reports whether a FsHeader uses sparse or compressed
// storage (FsHeader 0x148..0x1A0), which this patcher does not implement.
func hasStorageExtras(fs []byte) bool {
	return !bytes.Equal(fs[0x148:0x1A0], make([]byte, 0x1A0-0x148))
}

type outSection struct {
	idx      int
	fs       []byte // new FsHeader
	entry    []byte // section table entry bytes 8..16 from the source
	start    int64  // in the new NCA
	size     int64
	srcStart int64 // section start in the source NCA
	src      plainNCA
	reloc    []relocEntry // set for a patched (BKTR) section
	virtSize int64
	baseSrc  plainNCA
	baseSec  nx.NCASection
}

func writePatchedProgram(ctx context.Context, hw *hashWriter, c *patchContent, keys *nx.Keys, hk []byte,
	report func(int64), logf func(fmt.Stringer), verify bool, out *os.File, ncaPos int64) error {
	open := func(n *NSP) (*os.File, error) { return os.Open(n.Path) }
	uf, err := open(c.src)
	if err != nil {
		return err
	}
	defer uf.Close()
	bf, err := open(c.base.src)
	if err != nil {
		return err
	}
	defer bf.Close()
	upd, err := plainReader(uf, c)
	if err != nil {
		return err
	}
	base, err := plainReader(bf, c.base)
	if err != nil {
		return err
	}

	// Section plan: update sections (BKTR ones patched), plus base-only
	// sections such as the logo.
	var secs []*outSection
	for i := range 4 {
		us, bs := c.hdr.Sections[i], c.base.hdr.Sections[i]
		switch {
		case us.End > us.Start:
			o := &outSection{idx: i, fs: bytes.Clone(c.hdr.FsHeader(i)), entry: c.hdr.Raw[0x248+i*0x10 : 0x250+i*0x10],
				srcStart: us.Start, size: us.End - us.Start, src: upd}
			if hasStorageExtras(o.fs) {
				return i18n.Errorf("err.patch_unsupported", "sparse/compressed storage")
			}
			if us.EncType == 4 {
				if bs.End <= bs.Start || hasStorageExtras(c.base.hdr.FsHeader(i)) {
					return i18n.Errorf("err.patch_unsupported", "base RomFS section")
				}
				if o.reloc, o.virtSize, err = readRelocation(upd, us, o.fs); err != nil {
					return err
				}
				o.baseSrc, o.baseSec = base, bs
				o.size = (o.virtSize + 0x1FF) &^ 0x1FF
				o.fs[4] = 3 // plain AES-CTR now
				clear(o.fs[0x100:0x140])
			}
			secs = append(secs, o)
		case bs.End > bs.Start:
			secs = append(secs, &outSection{idx: i, fs: bytes.Clone(c.base.hdr.FsHeader(i)), entry: c.base.hdr.Raw[0x248+i*0x10 : 0x250+i*0x10],
				srcStart: bs.Start, size: bs.End - bs.Start, src: base})
		}
	}
	sort.Slice(secs, func(i, j int) bool { return secs[i].srcStart < secs[j].srcStart })
	cursor := int64(0x4000)
	for _, s := range secs {
		s.start = cursor
		cursor += s.size
	}

	raw := bytes.Clone(c.hdr.Raw)
	raw[0x204] = 0 // distribution: download
	binary.LittleEndian.PutUint64(raw[0x208:], uint64(cursor))
	clear(raw[0x230:0x240]) // no rights ID: the key goes into the key area
	clear(raw[0x240:0x340])
	ka, err := nx.EncryptKeyArea(keys, raw[0x207], c.hdr.MasterKeyRev(), c.key)
	if err != nil {
		return err
	}
	copy(raw[0x300+0x20:], ka)
	clear(raw[0x400:])
	var outSecs []nsz.Section
	for _, s := range secs {
		e := raw[0x240+s.idx*0x10:]
		binary.LittleEndian.PutUint32(e, uint32(s.start/0x200))
		binary.LittleEndian.PutUint32(e[4:], uint32((s.start+s.size)/0x200))
		copy(e[8:16], s.entry)
		copy(raw[0x400+s.idx*0x200:], s.fs)
		fh := sha256.Sum256(s.fs)
		copy(raw[0x280+s.idx*0x20:], fh[:])
		var iv [8]byte
		copy(iv[:], s.fs[0x140:0x148])
		sec, err := nsz.NewSection(s.start, s.size, c.key, iv, s.fs[4] != 1)
		if err != nil {
			return err
		}
		outSecs = append(outSecs, sec)
	}
	encHdr, err := nx.EncryptNCAHeader(raw, hk)
	if err != nil {
		return err
	}
	if _, err := hw.Write(encHdr); err != nil {
		return err
	}
	if _, err := hw.Write(make([]byte, 0x4000-len(encHdr))); err != nil {
		return err
	}

	buf := make([]byte, 4<<20)
	emit := func(off int64, b []byte) error {
		nsz.Crypt(outSecs, off, b)
		_, err := hw.Write(b)
		report(int64(len(b)))
		return err
	}
	copyRange := func(src io.ReaderAt, srcOff, dstOff, n int64) error {
		for k := int64(0); k < n; {
			if err := ctx.Err(); err != nil {
				return err
			}
			m := min(int64(len(buf)), n-k)
			if _, err := src.ReadAt(buf[:m], srcOff+k); err != nil {
				return err
			}
			if err := emit(dstOff+k, buf[:m]); err != nil {
				return err
			}
			k += m
		}
		return nil
	}
	for _, s := range secs {
		if s.reloc == nil {
			if err := copyRange(s.src, s.srcStart, s.start, s.size); err != nil {
				return err
			}
			continue
		}
		baseSize := s.baseSec.End - s.baseSec.Start
		for i, e := range s.reloc {
			end := s.virtSize
			if i+1 < len(s.reloc) {
				end = s.reloc[i+1].virt
			}
			src, srcOff := s.src, s.srcStart+e.phys
			if !e.patch {
				if e.phys+end-e.virt > baseSize {
					return i18n.Errorf("err.patch_unsupported", "relocation outside base RomFS")
				}
				src, srcOff = s.baseSrc, s.baseSec.Start+e.phys
			}
			if err := copyRange(src, srcOff, s.start+e.virt, end-e.virt); err != nil {
				return err
			}
		}
		// The padding after the section, in pieces (emit encrypts in place,
		// so each piece is cleared again).
		for k, pad := int64(0), s.size-s.virtSize; k < pad; {
			m := min(int64(len(buf)), pad-k)
			clear(buf[:m])
			if err := emit(s.start+s.virtSize+k, buf[:m]); err != nil {
				return err
			}
			k += m
		}
	}

	if !verify {
		return nil
	}
	// Re-read the patched RomFS from the output and check its IVFC hash
	// tree: any wrong relocation shows up as a hash mismatch.
	plain := plainNCA{out, ncaPos, outSecs}
	for _, s := range secs {
		if s.reloc == nil || s.fs[3] != 3 {
			continue
		}
		// A failure fails the merge, so the broken file is removed.
		if err := verifyIVFC(ctx, io.NewSectionReader(plain, s.start, s.size), s.fs); err != nil {
			return i18n.Errorf("err.ivfc", err)
		}
		logf(i18n.M("log.ivfc_ok"))
	}
	return nil
}

// readRelocation parses the IndirectStorage table of a BKTR section.
func readRelocation(upd plainNCA, s nx.NCASection, fs []byte) ([]relocEntry, int64, error) {
	unsupported := i18n.Errorf("err.patch_unsupported", "relocation table")
	off := int64(binary.LittleEndian.Uint64(fs[0x100:]))
	size := int64(binary.LittleEndian.Uint64(fs[0x108:]))
	count := int(binary.LittleEndian.Uint32(fs[0x118:]))
	if string(fs[0x110:0x114]) != "BKTR" || size < 0x8000 || size > 256<<20 || off+size > s.End-s.Start {
		return nil, 0, unsupported
	}
	table := make([]byte, size)
	if _, err := upd.ReadAt(table, s.Start+off); err != nil {
		return nil, 0, err
	}
	sets := int(binary.LittleEndian.Uint32(table[4:]))
	virtSize := int64(binary.LittleEndian.Uint64(table[8:]))
	if sets < 1 || sets > 0x7FE || 0x4000*(1+sets) > len(table) {
		return nil, 0, unsupported // no L2 node support
	}
	var entries []relocEntry
	for i := range sets {
		set := table[0x4000*(1+i) : 0x4000*(2+i)]
		n := int(binary.LittleEndian.Uint32(set[4:]))
		if n < 1 || 0x10+n*0x14 > len(set) {
			return nil, 0, unsupported
		}
		for j := range n {
			e := set[0x10+j*0x14:]
			entries = append(entries, relocEntry{
				virt:  int64(binary.LittleEndian.Uint64(e)),
				phys:  int64(binary.LittleEndian.Uint64(e[8:])),
				patch: binary.LittleEndian.Uint32(e[16:]) != 0,
			})
		}
	}
	if len(entries) != count || entries[0].virt != 0 || virtSize <= entries[len(entries)-1].virt {
		return nil, 0, unsupported
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].virt <= entries[i-1].virt || entries[i].phys < 0 {
			return nil, 0, unsupported
		}
	}
	return entries, virtSize, nil
}

// writeTicketless copies an NCA, moving its title key into the key area
// when it uses a rights ID so that no ticket is needed.
func writeTicketless(ctx context.Context, hw *hashWriter, c *patchContent, keys *nx.Keys, hk []byte,
	open func(string) (*os.File, error), report func(int64)) error {
	f, err := open(c.src.Path)
	if err != nil {
		return err
	}
	from := int64(0)
	if c.hdr.HasRightsID() {
		raw := bytes.Clone(c.hdr.Raw)
		clear(raw[0x230:0x240])
		clear(raw[0x300:0x340])
		ka, err := nx.EncryptKeyArea(keys, raw[0x207], c.hdr.MasterKeyRev(), c.key)
		if err != nil {
			return err
		}
		copy(raw[0x320:], ka)
		enc, err := nx.EncryptNCAHeader(raw, hk)
		if err != nil {
			return err
		}
		if _, err := hw.Write(enc); err != nil {
			return err
		}
		from = int64(len(enc))
	}
	var last int64
	_, err = copyAt(ctx, hw, 0, f, c.entry.Offset+from, c.entry.Size-from, false, func(d int64) { report(d - last); last = d })
	return err
}

// WriteAt lets hashWriter serve as copyAt's destination; it only supports
// sequential writes.
func (hw *hashWriter) WriteAt(b []byte, _ int64) (int, error) { return hw.Write(b) }

// buildPatchedMeta creates the Meta NCA of the patched title: an Application
// CNMT listing the new contents, versioned like the applied update.
func buildPatchedMeta(appID uint64, upd *nx.ContentMeta, contents []*patchContent, keys *nx.Keys, hk []byte) ([]byte, error) {
	cnmt := make([]byte, 0x30)
	binary.LittleEndian.PutUint64(cnmt[0:], appID)
	binary.LittleEndian.PutUint32(cnmt[8:], upd.Version)
	cnmt[0xC] = nx.MetaApplication
	binary.LittleEndian.PutUint16(cnmt[0xE:], 0x10)
	binary.LittleEndian.PutUint16(cnmt[0x10:], uint16(len(contents)))
	binary.LittleEndian.PutUint64(cnmt[0x20:], appID|0x800)
	binary.LittleEndian.PutUint32(cnmt[0x28:], upd.RequiredSystemVersion)
	for _, c := range contents {
		r := nx.ContentRecord{Hash: c.outHash, Size: c.outSize, Type: c.rec.Type, IDOffset: c.rec.IDOffset}
		copy(r.ID[:], c.outHash[:16])
		cnmt = append(cnmt, r.Encode()...)
	}
	cnmt = append(cnmt, make([]byte, 0x20)...) // digest

	name := fmt.Sprintf("Application_%016x.cnmt", appID)
	pfs0 := append(nx.BuildPFS0Header([]nx.PFS0Source{{Name: name, Size: int64(len(cnmt))}}), cnmt...)

	// HierarchicalSha256: a hash table of the PFS0's 0x1000-byte blocks.
	const block = 0x1000
	var table []byte
	for i := 0; i < len(pfs0); i += block {
		h := sha256.Sum256(pfs0[i:min(i+block, len(pfs0))])
		table = append(table, h[:]...)
	}
	pfs0Off := (len(table) + 0x1FF) &^ 0x1FF
	section := make([]byte, (pfs0Off+len(pfs0)+0x1FF)&^0x1FF)
	copy(section, table)
	copy(section[pfs0Off:], pfs0)

	fs := make([]byte, 0x200)
	binary.LittleEndian.PutUint16(fs, 2)
	fs[2], fs[3], fs[4] = 1, 2, 3
	master := sha256.Sum256(table)
	copy(fs[0x8:], master[:])
	binary.LittleEndian.PutUint32(fs[0x28:], block)
	binary.LittleEndian.PutUint32(fs[0x2C:], 2)
	binary.LittleEndian.PutUint64(fs[0x38:], uint64(len(table)))
	binary.LittleEndian.PutUint64(fs[0x40:], uint64(pfs0Off))
	binary.LittleEndian.PutUint64(fs[0x48:], uint64(len(pfs0)))

	// Keys and key generation follow the update, whose firmware level the
	// patched program needs anyway.
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	var prog *patchContent
	for _, c := range contents {
		if c.rec.Type == nx.ContentRecordProgram {
			prog = c
			break
		}
	}
	if prog == nil {
		prog = contents[0]
	}
	const start = 0xC00
	raw := make([]byte, 0xC00)
	copy(raw[0x200:], "NCA3")
	raw[0x205] = nx.ContentTypeMeta
	raw[0x206] = prog.hdr.Raw[0x206]
	binary.LittleEndian.PutUint64(raw[0x208:], uint64(start+len(section)))
	binary.LittleEndian.PutUint64(raw[0x210:], appID)
	copy(raw[0x21C:0x220], prog.hdr.Raw[0x21C:0x220])
	raw[0x220] = prog.hdr.Raw[0x220]
	binary.LittleEndian.PutUint32(raw[0x240:], start/0x200)
	binary.LittleEndian.PutUint32(raw[0x244:], uint32((start+len(section))/0x200))
	fh := sha256.Sum256(fs)
	copy(raw[0x280:], fh[:])
	ka, err := nx.EncryptKeyArea(keys, 0, prog.hdr.MasterKeyRev(), key)
	if err != nil {
		return nil, err
	}
	copy(raw[0x320:], ka)
	copy(raw[0x400:], fs)

	sec, err := nsz.NewSection(start, int64(len(section)), key, [8]byte{}, true)
	if err != nil {
		return nil, err
	}
	nsz.Crypt([]nsz.Section{sec}, start, section)
	enc, err := nx.EncryptNCAHeader(raw, hk)
	if err != nil {
		return nil, err
	}
	return append(enc, section...), nil
}

// verifyIVFC checks the HierarchicalIntegrity hash tree of a RomFS section
// (r reads the decrypted section). The last block of a level is accepted
// hashed either zero-padded or as is.
func verifyIVFC(ctx context.Context, r io.ReaderAt, fs []byte) error {
	ivfc := fs[0x8:]
	if string(ivfc[:4]) != "IVFC" {
		return fmt.Errorf("no IVFC header")
	}
	levels := int(binary.LittleEndian.Uint32(ivfc[0xC:])) - 1
	if levels < 1 || levels > 6 {
		return fmt.Errorf("IVFC: %d levels", levels+1)
	}
	hashes := bytes.Clone(ivfc[0xC0:0xE0])
	for l := range levels {
		lh := ivfc[0x10+l*0x18:]
		off := int64(binary.LittleEndian.Uint64(lh))
		size := int64(binary.LittleEndian.Uint64(lh[8:]))
		order := binary.LittleEndian.Uint32(lh[16:])
		if order < 4 || order > 24 {
			return fmt.Errorf("IVFC level %d: block order %d", l, order)
		}
		bs := int64(1) << order
		padTo := bs
		if l == 0 && size > bs { // the master hash covers all of level 0
			bs = size
			padTo = (size + padTo - 1) / padTo * padTo
		}
		blocks := (size + bs - 1) / bs
		if int64(len(hashes)) < blocks*32 {
			return fmt.Errorf("IVFC level %d: hash table too short", l)
		}
		var next []byte
		if l+1 < levels {
			next = make([]byte, 0, size)
		}
		buf := make([]byte, bs)
		for b := range blocks {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(bs, size-b*bs)
			blk := buf[:n]
			if _, err := r.ReadAt(blk, off+b*bs); err != nil {
				return err
			}
			want := hashes[b*32 : b*32+32]
			got := sha256.Sum256(blk)
			if !bytes.Equal(got[:], want) {
				padded := sha256.Sum256(append(bytes.Clone(blk), make([]byte, padTo-n)...))
				if !bytes.Equal(padded[:], want) {
					return fmt.Errorf("IVFC level %d, block %d: hash mismatch", l, b)
				}
			}
			if next != nil {
				next = append(next, blk...)
			}
		}
		hashes = next
	}
	return nil
}
