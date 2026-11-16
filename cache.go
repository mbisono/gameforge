package main

import (
	"os"
	"path/filepath"

	"gameforge/internal/nointro"
	"gameforge/internal/redump"
	"gameforge/internal/titledb"
)

// cacheDirName is the folder, next to the configuration file, that holds
// the downloaded databases.
const cacheDirName = "cache"

// cacheFiles are the caches of older versions, which kept them next to
// the configuration file.
var cacheFiles = []string{titledb.FileName, redump.FileName, nointro.FileName}

// cachePath is the path of a cache file, in the cache folder next to the
// configuration file (the program folder, or the user configuration folder
// if that is not writable).
func cachePath(configPath, name string) string {
	dir := filepath.Join(filepath.Dir(configPath), cacheDirName)
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, name)
}

// moveCaches moves the caches of older versions into the cache folder;
// one that is already there stays, and the old copy is removed.
func moveCaches(configPath string) {
	for _, name := range cacheFiles {
		old := filepath.Join(filepath.Dir(configPath), name)
		if _, err := os.Stat(old); err != nil {
			continue
		}
		to := cachePath(configPath, name)
		if _, err := os.Stat(to); err == nil {
			os.Remove(old)
			continue
		}
		os.Rename(old, to)
	}
}
