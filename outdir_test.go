package main

import (
	"os"
	"path/filepath"
	"testing"

	"gameforge/internal/config"
)

func TestOutputDirPerTab(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "PS1")
	os.Mkdir(sub, 0o755)
	file := filepath.Join(sub, "game.cue")
	os.WriteFile(file, nil, 0o644)

	u := &ui{cfg: &config.Config{OutputDir: `C:\old`}}
	if got := u.outputDir("chd"); got != `C:\old` {
		t.Errorf("before: %q", got)
	}
	u.followInput("chd", []string{sub})
	u.followInput("7z", []string{file})
	u.setOutputDir("switch", "", false)
	for tab, want := range map[string]string{"chd": sub, "7z": sub, "switch": "", "rvz": `C:\old`} {
		if got := u.outputDir(tab); got != want {
			t.Errorf("%s: %q, want %q", tab, got, want)
		}
	}
}
