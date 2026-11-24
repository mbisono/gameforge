package cachefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.cache")
	type data struct{ Names map[string]int }
	if err := Load(path, &data{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if err := Save(path, data{map[string]int{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	var got data
	if err := Load(path, &got); err != nil || got.Names["a"] != 1 {
		t.Fatalf("%v %v", got, err)
	}
	os.WriteFile(path, []byte("not gzip"), 0o644)
	if err := Load(path, &got); err == nil {
		t.Fatal("a broken cache must fail")
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}
