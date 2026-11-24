// Package cachefile reads and writes the app's database caches: a value
// encoded with gob and compressed with gzip.
package cachefile

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
)

// Load decodes the cache file at path into v; the error wraps
// os.ErrNotExist if there is none.
func Load(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := gob.NewDecoder(zr).Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Save writes v to the cache file at path, replacing an old one only once
// the new one is complete.
func Save(path string, v any) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	zw := gzip.NewWriter(tmp)
	err = gob.NewEncoder(zw).Encode(v)
	if err == nil {
		err = zw.Close()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
