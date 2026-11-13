package merge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"gameforge/internal/i18n"
	"gameforge/internal/nsz"
	"gameforge/internal/nx"
)

// CheckResult summarizes the check of a backup.
type CheckResult struct {
	NCAs     int // NCAs (or NCZs) checked
	Problems int // damaged or missing content
	Warnings int // oddities that do not stop the game from working
}

// Check verifies an existing NSP or NSZ:
//   - every NCA hashes to what its CNMT lists (an NCZ is decompressed
//     first), or, if no CNMT lists it, to its own name, which is its hash;
//   - every content a CNMT lists is in the file (delta fragments aside, which
//     are not needed to play);
//   - every NCA that needs a ticket has one.
//
// The whole-file hash covers every byte, so the hash trees inside the NCAs
// need no separate check. Without keys only the names can be checked.
// Findings go to logf; an error means the file could not be read at all.
func Check(ctx context.Context, path string, keys *nx.Keys, progress func(done, total int64), logf func(fmt.Stringer)) (CheckResult, error) {
	var res CheckResult
	if progress == nil {
		progress = func(int64, int64) {}
	}
	if logf == nil {
		logf = func(fmt.Stringer) {}
	}
	n, err := Analyze(path, keys)
	if err != nil {
		return res, err
	}
	f, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer f.Close()
	if !n.FromCNMT {
		logf(i18n.M("check.no_keys"))
		res.Warnings++
	}
	problem := func(key string, args ...any) { logf(i18n.M(key, args...)); res.Problems++ }

	// Contents listed by the CNMTs, by NCA ID.
	listed := map[string]nx.ContentRecord{}
	for _, t := range n.Titles {
		for _, r := range t.Meta.Contents {
			listed[hex.EncodeToString(r.ID[:])] = r
		}
	}
	present := map[string]bool{}
	var ncas []nx.PFS0Entry
	var total int64
	for _, e := range n.Entries {
		if id, ok := ncaID(e.Name); ok {
			present[id] = true
			ncas = append(ncas, e)
			total += e.Size
		}
	}
	ids := slices.Sorted(maps.Keys(listed))
	for _, id := range ids {
		r := listed[id]
		switch {
		case present[id]:
		case r.Type == nx.ContentRecordDeltaFragment:
			logf(i18n.M("check.delta_missing", id))
		default:
			problem("check.missing", id)
		}
	}

	var hk []byte
	tickets := map[[16]byte][]byte{}
	if keys != nil {
		hk, _ = keys.HeaderKey()
		readTickets(f, n.Entries, tickets)
	}
	var done int64
	for _, e := range ncas {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.NCAs++
		id, _ := ncaID(e.Name)
		if hk != nil {
			if h, err := nx.ParseNCAHeader(f, e.Offset, hk); err != nil {
				problem("check.read_error", e.Name, err)
			} else if _, ok := tickets[h.RightsID]; h.HasRightsID() && !ok {
				problem("check.no_ticket", e.Name)
			}
		}

		h := sha256.New()
		base := done
		report := func(n int64) { progress(base+min(n, e.Size), total) }
		if strings.HasSuffix(strings.ToLower(e.Name), ".ncz") {
			// Progress follows the decompressed bytes, scaled to the NCZ size.
			want := max(listed[id].Size, e.Size)
			cw := &countingWriter{w: h, report: func(n int64) { report(n * e.Size / want) }}
			err = nsz.Decompress(ctx, f, e.Offset, e.Size, cw)
		} else {
			_, err = copyHash(ctx, h, io.NewSectionReader(f, e.Offset, e.Size), report)
		}
		done += e.Size
		progress(done, total)
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			problem("check.read_error", e.Name, err)
			continue
		}
		sum := h.Sum(nil)
		if r, ok := listed[id]; ok {
			if !bytes.Equal(sum, r.Hash[:]) {
				problem("check.hash", e.Name)
			}
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name), ".cnmt.nca") && n.FromCNMT {
			logf(i18n.M("check.unlisted", e.Name))
			res.Warnings++
		}
		if hex.EncodeToString(sum[:16]) != id {
			problem("check.hash", e.Name)
		}
	}
	return res, nil
}

// ncaID returns the content ID (lower-case hex) of an NCA or NCZ entry name.
func ncaID(name string) (string, bool) {
	name = strings.ToLower(name)
	for _, ext := range []string{".cnmt.nca", ".nca", ".ncz"} {
		if id, ok := strings.CutSuffix(name, ext); ok {
			if _, err := hex.DecodeString(id); err == nil && len(id) == 32 {
				return id, true
			}
			return "", false
		}
	}
	return "", false
}

// copyHash copies r into h in chunks, reporting the bytes done.
func copyHash(ctx context.Context, h io.Writer, r io.Reader, report func(int64)) (int64, error) {
	buf := make([]byte, 4<<20)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, err := r.Read(buf)
		h.Write(buf[:m])
		n += int64(m)
		report(n)
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}
