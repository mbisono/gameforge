package merge

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/nx"
)

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	const app = 0x0100ABCD12340000
	text := []byte(strings.Repeat("compressible game data 0123456789 ", 8000))
	rights, _ := hex.DecodeString("0100ABCD123408000000000000000000")
	titleKey := rnd(16)
	prog := buildContentNCA(k, nx.ContentTypeProgram, text, rights, titleKey)
	ctl := buildContentNCA(k, nx.ContentTypeControl, text[:0x5000], nil, nil)
	delta := buildContentNCA(k, nx.ContentTypeData, text[:0x5000], nil, nil)
	meta := buildMetaNCA(t, dir, k, cnmtWith(app|0x800, 65536, nx.MetaPatch, app,
		record(prog, nx.ContentRecordProgram), record(ctl, nx.ContentRecordControl),
		record(delta, nx.ContentRecordDeltaFragment)), nil, nil)
	files := func(skip ...string) map[string][]byte {
		m := map[string][]byte{
			ncaName(prog):  prog,
			ncaName(ctl):   ctl,
			ncaName(delta): delta,
			strings.TrimSuffix(ncaName(meta), ".nca") + ".cnmt.nca": meta,
			"0100abcd123408000000000000000000.tik":                  ticket(rights, ecbEncrypt(k.titlekek, titleKey)),
		}
		for _, s := range skip {
			delete(m, s)
		}
		return m
	}
	check := func(p string, keys *nx.Keys) (CheckResult, string) {
		var logs []string
		res, err := Check(context.Background(), p, keys, nil, func(s fmt.Stringer) { logs = append(logs, s.String()) })
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(p), err)
		}
		return res, strings.Join(logs, "\n")
	}

	good := writeNSP(t, dir, "good.nsp", files())
	if res, log := check(good, keys); res.Problems != 0 || res.Warnings != 0 || res.NCAs != 4 {
		t.Fatalf("intact NSP: %+v\n%s", res, log)
	}
	// Without keys only the names are checked, which still match.
	if res, log := check(good, nil); res.Problems != 0 || res.Warnings != 1 {
		t.Fatalf("without keys: %+v\n%s", res, log)
	}

	// An NSZ is checked through its decompressed NCAs; the dropped delta
	// fragment is fine.
	n, _ := Analyze(good, keys)
	nszPath := filepath.Join(dir, "good.nsz")
	if _, err := Write(context.Background(), []*NSP{n}, nszPath, Options{Keys: keys, Compress: true}, nil, nil); err != nil {
		t.Fatal(err)
	}
	deltaID := strings.TrimSuffix(ncaName(delta), ".nca")
	if res, log := check(nszPath, keys); res.Problems != 0 || res.NCAs != 3 || !strings.Contains(log, deltaID) {
		t.Fatalf("NSZ: %+v\n%s", res, log)
	}

	// One flipped byte in the program NCA.
	b, _ := os.ReadFile(good)
	for _, e := range n.Entries {
		if e.Name == ncaName(prog) {
			b[e.Offset+0x5000] ^= 1
		}
	}
	damaged := filepath.Join(dir, "damaged.nsp")
	os.WriteFile(damaged, b, 0o644)
	if res, log := check(damaged, keys); res.Problems != 1 || !strings.Contains(log, ncaName(prog)) {
		t.Fatalf("damaged: %+v\n%s", res, log)
	}

	if res, log := check(writeNSP(t, dir, "noticket.nsp", files("0100abcd123408000000000000000000.tik")), keys); res.Problems != 1 {
		t.Fatalf("without ticket: %+v\n%s", res, log)
	}
	if res, log := check(writeNSP(t, dir, "missing.nsp", files(ncaName(ctl))), keys); res.Problems != 1 {
		t.Fatalf("missing NCA: %+v\n%s", res, log)
	}
}
