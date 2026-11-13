package merge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"gameforge/internal/i18n"
	"gameforge/internal/nsz"
	"gameforge/internal/nx"
)

// Options controls how the merged file is written.
type Options struct {
	Keys     *nx.Keys // required when Compress is set
	Compress bool     // write an NSZ (compressed NCAs) instead of an NSP
	Level    zstd.EncoderLevel
	Verify   bool // re-read the output and compare every NCA with the source
}

// Ext returns the output file extension.
func (o Options) Ext() string {
	if o.Compress {
		return ".nsz"
	}
	return ".nsp"
}

// Phase tells progress callbacks what Write is doing.
type Phase int

const (
	PhaseWrite Phase = iota
	PhaseVerify
)

// Result summarizes a written file.
type Result struct {
	InputSize, OutputSize             int64
	Compressed, Dropped, Decompressed int
}

type outEntry struct {
	name      string
	path      string
	off, size int64 // in the source file
	sections  []nsz.Section
	ncaSize   int64 // set when an NCZ is restored to its NCA: the NCA's size
	outOff    int64
	outSize   int64
	hash      [32]byte // SHA-256 of the source content
	hashed    bool
}

// Write merges sel into outPath. Duplicate entries (identical NCAs, tickets,
// certificates share the same name) are written once. With Compress, game
// data NCAs are stored as NCZ and update delta fragments, which are not
// needed to install or play, are left out. The file is written as .part and
// renamed on success, so a cancelled or failed merge never leaves a broken file.
func Write(ctx context.Context, sel []*NSP, outPath string, opt Options,
	progress func(phase Phase, done, total int64), logf func(fmt.Stringer)) (Result, error) {
	var res Result
	if progress == nil {
		progress = func(Phase, int64, int64) {}
	}
	if logf == nil {
		logf = func(fmt.Stringer) {}
	}
	if opt.Compress && opt.Keys == nil {
		return res, i18n.Errorf("err.nsz_needs_keys")
	}
	if err := checkSelection(sel); err != nil {
		return res, err
	}
	sel, skip := resolveBundled(sel, logf)

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

	entries, err := plan(sel, skip, opt, open, logf, &res)
	if err != nil {
		return res, err
	}
	if res.Decompressed > 0 {
		logf(i18n.M("log.decompressed", res.Decompressed))
	}
	var total int64
	srcs := make([]nx.PFS0Source, len(entries))
	for i, e := range entries {
		total += e.size
		srcs[i] = nx.PFS0Source{Name: e.name, Size: e.size}
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

	// The header length depends only on the names; it is rewritten with the
	// final sizes at the end.
	hdr := nx.BuildPFS0Header(srcs)
	if _, err := out.WriteAt(hdr, 0); err != nil {
		return fail(err)
	}
	pos := int64(len(hdr))
	var done int64
	for _, e := range entries {
		src, err := open(e.path)
		if err != nil {
			return fail(err)
		}
		e.outOff = pos
		report := func(d int64) { progress(PhaseWrite, done+d, total) }
		switch {
		case e.sections != nil:
			e.outSize, e.hash, err = nsz.Compress(ctx, out, pos, src, e.off, e.size, e.sections, nsz.Options{Level: opt.Level}, report)
			e.hashed = true
		case e.ncaSize > 0:
			// NCZ to NCA: progress counts the NCZ bytes, scaled from the NCA bytes.
			h := sha256.New()
			cw := &countingWriter{w: io.MultiWriter(h, io.NewOffsetWriter(out, pos)),
				report: func(n int64) { report(min(n*e.size/e.ncaSize, e.size)) }}
			err = nsz.Decompress(ctx, src, e.off, e.size, cw)
			copy(e.hash[:], h.Sum(nil))
			e.hashed, e.outSize = true, cw.n
		default:
			e.hashed = isNCA(e.name)
			e.hash, err = copyAt(ctx, out, pos, src, e.off, e.size, e.hashed, report)
			e.outSize = e.size
		}
		if err != nil {
			return fail(err)
		}
		pos += e.outSize
		done += e.size
		progress(PhaseWrite, done, total)
		if e.ncaSize > 0 {
			e.size = e.outSize // verify compares with the restored NCA
		}
	}

	for i, e := range entries {
		srcs[i].Size = e.outSize
		// NCA names are the first half of the SHA-256 of their content.
		stem := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(e.name, filepath.Ext(e.name)), ".cnmt"))
		if e.hashed && len(stem) == 32 && hex.EncodeToString(e.hash[:16]) != stem {
			logf(i18n.M("log.hash_name_mismatch", e.name))
		}
	}
	if final := nx.BuildPFS0Header(srcs); len(final) != len(hdr) {
		return fail(fmt.Errorf("PFS0 header size changed"))
	} else if _, err := out.WriteAt(final, 0); err != nil {
		return fail(err)
	}

	if opt.Verify {
		if err := verify(ctx, out, entries, progress); err != nil {
			return fail(err)
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return res, err
	}
	res.InputSize, res.OutputSize = int64(len(hdr))+total, pos
	return res, os.Rename(tmp, outPath)
}

func isNCA(name string) bool { return strings.HasSuffix(strings.ToLower(name), ".nca") }

// resolveBundled keeps one version of each title when a merged NSP already
// contains an update or DLC that is also selected as a file of its own: the
// newest wins. It returns the selection without files a merged NSP
// supersedes and, per merged NSP, the entries of its titles left out.
func resolveBundled(sel []*NSP, logf func(fmt.Stringer)) ([]*NSP, map[*NSP]map[string]bool) {
	skip := map[*NSP]map[string]bool{}
	dropped := map[*NSP]bool{}
	for _, n := range sel {
		replaced := map[uint64]bool{}
		for _, t := range n.Bundled() {
			for _, s := range sel {
				if s == n || dropped[s] || s.TitleID != t.Meta.TitleID {
					continue
				}
				if s.Version >= t.Meta.Version {
					replaced[t.Meta.TitleID] = true
					logf(i18n.M("log.bundled_replaced", t.Kind(), t.Meta.Version, n.Name(), s.Name(), s.Version))
					break
				}
				dropped[s] = true
				logf(i18n.M("log.file_superseded", s.Name(), n.Name(), t.Meta.Version))
			}
		}
		if len(replaced) == 0 {
			continue
		}
		// Leave out the replaced titles' entries, except those another title
		// of the file still lists.
		skip[n] = map[string]bool{}
		for _, t := range n.Titles {
			if replaced[t.Meta.TitleID] {
				for name := range n.titleEntries(t) {
					skip[n][name] = true
				}
			}
		}
		for _, t := range n.Titles {
			if !replaced[t.Meta.TitleID] {
				for name := range n.titleEntries(t) {
					delete(skip[n], name)
				}
			}
		}
	}
	var out []*NSP
	for _, n := range sel {
		if !dropped[n] {
			out = append(out, n)
		}
	}
	return out, skip
}

// plan decides, per entry, whether it is copied, compressed or dropped.
// Entries in skip are left out.
func plan(sel []*NSP, skip map[*NSP]map[string]bool, opt Options, open func(string) (*os.File, error), logf func(fmt.Stringer), res *Result) ([]*outEntry, error) {
	var hk []byte
	encTitleKeys := map[[16]byte][]byte{}
	if opt.Compress {
		var err error
		if hk, err = opt.Keys.HeaderKey(); err != nil {
			return nil, err
		}
		for _, n := range sel {
			f, err := open(n.Path)
			if err != nil {
				return nil, err
			}
			if err := readTickets(f, n.Entries, encTitleKeys); err != nil {
				return nil, err
			}
		}
	}

	seen := map[string]bool{}
	var entries []*outEntry
	for _, n := range sel {
		f, err := open(n.Path)
		if err != nil {
			return nil, err
		}
		for _, e := range n.Entries {
			lname := strings.ToLower(e.Name)
			// An NCA and the NCZ holding it are the same content.
			key := strings.TrimSuffix(lname, ".ncz")
			if key != lname {
				key += ".nca"
			}
			if seen[key] || skip[n][lname] {
				continue
			}
			seen[key] = true
			oe := &outEntry{name: e.Name, path: n.Path, off: e.Offset, size: e.Size}
			entries = append(entries, oe)
			if !opt.Compress && key != lname {
				// Writing an NSP: the NCZ goes back to its NCA.
				if oe.ncaSize, err = nsz.NCASize(f, e.Offset); err != nil {
					return nil, fmt.Errorf("%s: %w", e.Name, err)
				}
				oe.name = e.Name[:len(e.Name)-1] + "a"
				res.Decompressed++
				continue
			}
			if !opt.Compress || !isNCA(lname) || strings.HasSuffix(lname, ".cnmt.nca") {
				continue
			}

			h, err := nx.ParseNCAHeader(f, e.Offset, hk)
			if err != nil {
				logf(i18n.M("log.ncz_skip", e.Name, err))
				continue
			}
			switch {
			case h.ContentType == nx.ContentTypeData && n.Kind == KindUpdate:
				// Delta fragments only serve incremental console updates; nsz
				// drops them too.
				entries = entries[:len(entries)-1]
				res.Dropped++
				logf(i18n.M("log.delta_dropped", e.Name, HumanSize(e.Size)))
			case (h.ContentType == nx.ContentTypeProgram || h.ContentType == nx.ContentTypePublicData) && e.Size > nsz.HeaderSize:
				key, err := contentKey(h, opt.Keys, encTitleKeys)
				var secs []nsz.Section
				ok := false
				if err == nil {
					secs, ok, err = nsz.Sections(f, e.Offset, e.Size, h, key)
				}
				switch {
				case err != nil:
					logf(i18n.M("log.ncz_skip", e.Name, err))
				case ok:
					oe.sections = secs
					oe.name = e.Name[:len(e.Name)-1] + "z"
					res.Compressed++
				}
			}
		}
	}
	return entries, nil
}

// copyAt copies size bytes from src at off to dst at pos, optionally
// hashing them.
func copyAt(ctx context.Context, dst io.WriterAt, pos int64, src io.ReaderAt, off, size int64, hashIt bool, progress func(int64)) ([32]byte, error) {
	var sum [32]byte
	var h hash.Hash
	if hashIt {
		h = sha256.New()
	}
	buf := make([]byte, 4<<20)
	for done := int64(0); done < size; {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n := min(int64(len(buf)), size-done)
		if _, err := src.ReadAt(buf[:n], off+done); err != nil {
			return sum, err
		}
		if h != nil {
			h.Write(buf[:n])
		}
		if _, err := dst.WriteAt(buf[:n], pos+done); err != nil {
			return sum, err
		}
		done += n
		progress(done)
	}
	if h != nil {
		copy(sum[:], h.Sum(nil))
	}
	return sum, nil
}

// verify re-reads every NCA/NCZ from the output and checks that it
// restores exactly the source content.
func verify(ctx context.Context, out *os.File, entries []*outEntry, progress func(Phase, int64, int64)) error {
	var total, done int64
	for _, e := range entries {
		if e.hashed {
			total += e.size
		}
	}
	for _, e := range entries {
		if !e.hashed {
			continue
		}
		h := sha256.New()
		w := &countingWriter{w: h, report: func(n int64) { progress(PhaseVerify, done+n, total) }}
		var err error
		if e.sections != nil {
			err = nsz.Decompress(ctx, out, e.outOff, e.outSize, w)
		} else {
			_, err = io.Copy(w, io.NewSectionReader(out, e.outOff, e.outSize))
		}
		if err != nil {
			return fmt.Errorf("%s: %w", e.name, err)
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		if sum != e.hash || w.n != e.size {
			return i18n.Errorf("err.verify", e.name)
		}
		done += e.size
	}
	return nil
}

type countingWriter struct {
	w      io.Writer
	n      int64
	report func(int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.report(c.n)
	return n, err
}

// HumanSize formats a byte count, e.g. "1.50 GiB".
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
