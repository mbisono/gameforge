// Package config stores the user's options in a JSON file next to the
// program, creating it with defaults when it does not exist.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the name of the configuration file.
const FileName = "gameforge.json"

// Names used before the app was renamed GameForge; their configuration is
// taken over on the first run.
const (
	oldFileName = "nspmerge.json"
	oldDirName  = "nspmerge"
	dirName     = "gameforge"
)

// Config holds the persisted options.
type Config struct {
	// Language is a language code ("en", "pt", ...) or "auto" to follow the system.
	Language string `json:"language"`
	// Theme is "light", "dark" or "auto" to follow the system.
	Theme     string `json:"theme"`
	KeysPath  string `json:"keys_path"`
	OutputDir string `json:"output_dir"`
	LastDir   string `json:"last_dir"`
	// Format is "nsz" (compressed, default) or "nsp".
	Format string `json:"format"`
	// Level is the NSZ compression level: "fast", "normal" (default) or "max".
	Level      string `json:"level"`
	SkipVerify bool   `json:"skip_verify"`
	// ApplyUpdate builds one patched base game (for emulators) instead of
	// keeping base, update and DLC as separate titles.
	ApplyUpdate bool `json:"apply_update"`
	// ChdNoM3U turns off the .m3u playlists written for games on several
	// discs converted to CHD.
	ChdNoM3U bool `json:"chd_no_m3u,omitempty"`
	// CisoFormat is the compressed ISO format written: "cso" (default) or
	// "zso".
	CisoFormat string `json:"ciso_format,omitempty"`
	// WiiKeyPath is a Wii common key file chosen by the user (the keys
	// folder is also searched).
	WiiKeyPath string `json:"wii_key_path,omitempty"`
	// RvzChunk is the RVZ block size in KiB (0: 128).
	RvzChunk int `json:"rvz_chunk_kib,omitempty"`
	// SevenZipExts records the extensions the user checked (true) or
	// unchecked (false) in the 7z tab; others follow the defaults.
	SevenZipExts map[string]bool `json:"sevenzip_exts,omitempty"`
	// SevenZipDelete removes the original files after their 7z archive is
	// written and verified.
	SevenZipDelete bool `json:"sevenzip_delete,omitempty"`

	// Window layout of the last run: geometry "WxH+X+Y" of the normal
	// (not maximized) window, whether it was maximized, the widths of the
	// list's columns and the position of the divider above the log.
	Window    string `json:"window,omitempty"`
	Maximized bool   `json:"maximized,omitempty"`
	Columns   []int  `json:"columns,omitempty"`
	Divider   int    `json:"divider,omitempty"`
	// Tab is the tab shown last: "switch" or a converter id ("chd", "xiso",
	// "rvz", "ciso", "7z").
	Tab string `json:"tab,omitempty"`

	path string
}

// AppDir returns the program's folder. Under "go run" the executable lives
// in a temporary build folder, so the working directory is used instead.
func AppDir() string {
	exe, err := os.Executable()
	if err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			if !strings.Contains(dir, "go-build") {
				return dir
			}
		}
	}
	wd, _ := os.Getwd()
	return wd
}

// Load reads the configuration from the program folder, creating the file
// with defaults when missing. If the program folder is not writable (e.g.
// installed under /usr/bin), the user's config folder is used instead.
func Load() (*Config, error) {
	primary := filepath.Join(AppDir(), FileName)
	migrate(filepath.Join(AppDir(), oldFileName), primary)
	c, err := loadOrCreate(primary)
	if err == nil || c != nil {
		return c, err
	}
	if dir, uerr := os.UserConfigDir(); uerr == nil {
		user := filepath.Join(dir, dirName, FileName)
		migrate(filepath.Join(dir, oldDirName, oldFileName), user)
		if c, ferr := loadOrCreate(user); c != nil {
			return c, ferr
		}
	}
	// Keep running with defaults; saving will report the error again.
	d := &Config{path: primary}
	d.fillDefaults()
	return d, err
}

// migrate renames the configuration file of an older version to its new
// name, if there is no file with the new name yet. Failures are ignored:
// the defaults are used then.
func migrate(old, path string) {
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if _, err := os.Stat(old); err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.Rename(old, path)
}

// fillDefaults sets options missing from the file (e.g. written by an
// older version) to their defaults.
func (c *Config) fillDefaults() {
	if c.Language == "" {
		c.Language = "auto"
	}
	switch c.Theme {
	case "light", "dark":
	default:
		c.Theme = "auto"
	}
	if c.Format != "nsp" {
		c.Format = "nsz"
	}
	switch c.Level {
	case "fast", "normal", "max":
	default:
		c.Level = "normal"
	}
}

func loadOrCreate(path string) (*Config, error) {
	c := &Config{path: path}
	c.fillDefaults()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := c.Save(); err != nil {
			return nil, err // not writable: let Load try the fallback
		}
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		// Readable but invalid: use defaults here; the next save repairs it.
		d := &Config{path: path}
		d.fillDefaults()
		return d, err
	}
	c.fillDefaults()
	return c, nil
}

// Path returns the location of the configuration file.
func (c *Config) Path() string { return c.path }

// Save writes the configuration file.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, append(b, '\n'), 0o644)
}
