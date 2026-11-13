package merge

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/nx"
)

type hfs0File struct {
	name string
	data []byte
}

// hfs0Bytes builds an HFS0 partition with the files in order.
func hfs0Bytes(files []hfs0File) []byte {
	var strs, data []byte
	entries := make([]byte, 0x40*len(files))
	for i, f := range files {
		e := entries[i*0x40:]
		binary.LittleEndian.PutUint64(e[0:], uint64(len(data)))
		binary.LittleEndian.PutUint64(e[8:], uint64(len(f.data)))
		binary.LittleEndian.PutUint32(e[16:], uint32(len(strs)))
		strs = append(append(strs, f.name...), 0)
		data = append(data, f.data...)
	}
	hdr := make([]byte, 0x10)
	copy(hdr, "HFS0")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(files)))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(len(strs)))
	return append(append(append(hdr, entries...), strs...), data...)
}

// xciBytes builds a game card image whose secure partition holds files,
// optionally preceded by the 0x1000-byte key area some dumps include.
func xciBytes(files []hfs0File, keyArea bool) []byte {
	const rootOff = 0xF000
	root := hfs0Bytes([]hfs0File{{"update", hfs0Bytes(nil)}, {"secure", hfs0Bytes(files)}})
	img := make([]byte, rootOff)
	copy(img[0x100:], "HEAD")
	binary.LittleEndian.PutUint64(img[0x130:], rootOff)
	img = append(img, root...)
	if keyArea {
		img = append(make([]byte, 0x1000), img...)
	}
	return img
}

func TestXCI(t *testing.T) {
	dir := t.TempDir()
	k := newTestKeys(t, dir)
	keys, err := nx.LoadKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	const app = 0x0100ABCD12340000
	prog := buildContentNCA(k, nx.ContentTypeProgram, []byte(strings.Repeat("card data ", 3000)), nil, nil)
	meta := buildMetaNCA(t, dir, k, cnmtWith(app, 0, nx.MetaApplication, app|0x800, record(prog, nx.ContentRecordProgram)), nil, nil)
	metaName := strings.TrimSuffix(ncaName(meta), ".nca") + ".cnmt.nca"
	files := []hfs0File{{ncaName(prog), prog}, {metaName, meta}}

	for _, keyArea := range []bool{false, true} {
		p := filepath.Join(dir, map[bool]string{false: "game.xci", true: "game-full.xci"}[keyArea])
		os.WriteFile(p, xciBytes(files, keyArea), 0o644)
		n, err := Analyze(p, keys)
		if err != nil || n.Kind != KindBase || n.TitleID != app || !n.FromCNMT || len(n.Entries) != 2 {
			t.Fatalf("key area %v: %+v %v", keyArea, n, err)
		}

		// Converted to an NSP with the same NCAs.
		out := filepath.Join(dir, "from-xci.nsp")
		os.Remove(out)
		if _, err := Write(context.Background(), []*NSP{n}, out, Options{Verify: true}, nil, nil); err != nil {
			t.Fatal(err)
		}
		f, _ := os.Open(out)
		entries, err := nx.ReadPFS0(f, 0)
		if err != nil || len(entries) != 2 {
			t.Fatalf("NSP from XCI: %v %v", entries, err)
		}
		for _, e := range entries {
			b := make([]byte, e.Size)
			f.ReadAt(b, e.Offset)
			if e.Name == ncaName(prog) && !bytes.Equal(b, prog) {
				t.Error("program NCA changed")
			}
		}
		f.Close()

		if res, err := Check(context.Background(), p, keys, nil, nil); err != nil || res.Problems != 0 || res.NCAs != 2 {
			t.Fatalf("check XCI: %+v %v", res, err)
		}
	}

	bad := filepath.Join(dir, "bad.xci")
	os.WriteFile(bad, make([]byte, 0x2000), 0o644)
	if _, err := Analyze(bad, keys); err == nil {
		t.Error("an empty file was taken for an XCI")
	}
}
