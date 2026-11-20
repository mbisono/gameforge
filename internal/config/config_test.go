package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	c, err := loadOrCreate(path)
	if err != nil || c.Language != "auto" {
		t.Fatalf("create: %+v %v", c, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file was not created")
	}

	c.Language, c.OutputDir = "de", `C:\out`
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := loadOrCreate(path)
	if err != nil || c2.Language != "de" || c2.OutputDir != `C:\out` {
		t.Fatalf("reload: %+v %v", c2, err)
	}

	os.WriteFile(path, []byte("{broken"), 0o644)
	c3, err := loadOrCreate(path)
	if err == nil || c3 == nil || c3.Language != "auto" || c3.Path() != path {
		t.Fatalf("invalid file must give defaults at the same path: %+v %v", c3, err)
	}
}

func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	old, path := filepath.Join(dir, oldFileName), filepath.Join(dir, FileName)
	os.WriteFile(old, []byte(`{"language":"fr"}`), 0o644)
	migrate(old, path)
	c, err := loadOrCreate(path)
	if err != nil || c.Language != "fr" {
		t.Fatalf("old configuration not taken over: %+v %v", c, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("old file still there")
	}

	// An existing new file wins over an old one.
	os.WriteFile(old, []byte(`{"language":"es"}`), 0o644)
	migrate(old, path)
	if c, _ := loadOrCreate(path); c.Language != "fr" {
		t.Errorf("new file replaced: %+v", c)
	}
}

// TestCisoTab carries the settings of the ISO → CSO/ZSO, Xbox → XISO and
// GameCube/Wii → RVZ tabs over to the Discs tab, which took their place.
func TestCisoTab(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		json, tab, format, out string
	}{
		{`{"tab":"ciso","ciso_format":"zso","output_dirs":{"ciso":"D:/cso"}}`, "chd", "zso", `D:/cso`},
		{`{"tab":"ciso","output_dirs":{"ciso":"D:/cso","chd":"D:/chd"}}`, "chd", "cso", `D:/chd`},
		{`{"tab":"7z","ciso_format":"zso","output_dirs":{"ciso":"D:/cso"}}`, "7z", "", ""},
		{`{"tab":"rvz","output_dirs":{"rvz":"D:/wii","xiso":"D:/xbox"}}`, "chd", "", `D:/wii`},
		{`{"tab":"chd","iso_format":"bad"}`, "chd", "", ""},
		{`{"tab":"xiso","output_dirs":{"xiso":"D:/xbox"}}`, "chd", "", `D:/xbox`},
	} {
		path := filepath.Join(dir, FileName)
		os.WriteFile(path, []byte(c.json), 0o644)
		got, err := loadOrCreate(path)
		if err != nil || got.Tab != c.tab || got.IsoFormat != c.format || got.OutputDirs["chd"] != c.out ||
			got.CisoFormat != "" || got.OutputDirs["ciso"] != "" || got.OutputDirs["xiso"] != "" || got.OutputDirs["rvz"] != "" {
			t.Errorf("%s: %+v %v", c.json, got, err)
		}
	}
}
