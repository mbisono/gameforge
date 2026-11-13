package titledb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testVersions = `{
  "0100eba004726000": {"262144": "2020-11-23", "524288": "2021-11-29", "196608": "2020-08-28"},
  "0100eba004727001": {"65536": "2021-01-05"}
}`

const testCatalog = `{
  "70010000001487": {"id": "0100EBA004726000", "name": "Huntdown", "isDemo": false, "screenshots": ["x"]},
  "70010000001488": {"id": "0100EBA004727001", "name": "Bonus Pack", "isDemo": false},
  "70010000001489": {"id": "0100EBA004727002", "name": "Second Pack ", "isDemo": false},
  "70010000001490": {"id": null, "name": "No ID"},
  "70010000001491": {"id": "0100AAAA00000000", "name": "Demo", "isDemo": true}
}`

func TestDownloadAndCache(t *testing.T) {
	var gets, sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets = append(gets, r.URL.Path)
		body, tag := testVersions, `"v1"`
		if strings.HasSuffix(r.URL.Path, "US.en.json") {
			body, tag = testCatalog, `"c1"`
		}
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		sent = append(sent, r.URL.Path)
		w.Header().Set("ETag", tag)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	saved := sources
	defer func() { sources = saved }()
	sources = [2]string{srv.URL + "/versions.json", srv.URL + "/US.en.json"}

	var reported bool
	db, err := Download(context.Background(), nil, func(file string, done, total int64) { reported = true })
	if err != nil {
		t.Fatal(err)
	}
	if !reported {
		t.Error("no progress reported")
	}
	check := func(db *DB) {
		t.Helper()
		const app, dlc1, dlc2 = 0x0100EBA004726000, 0x0100EBA004727001, 0x0100EBA004727002
		if r, ok := db.Latest(app); !ok || r.Version != 524288 || r.Date != "2021-11-29" {
			t.Errorf("Latest = %+v, %v", r, ok)
		}
		if r, ok := db.Latest(dlc1); !ok || r.Version != 65536 {
			t.Errorf("Latest DLC = %+v, %v", r, ok)
		}
		if got := db.DLCs(app); len(got) != 2 || got[0] != dlc1 || got[1] != dlc2 {
			t.Errorf("DLCs = %X", got)
		}
		if db.Name(dlc2) != "Second Pack" || db.Name(app) != "Huntdown" {
			t.Errorf("names = %q, %q", db.Name(dlc2), db.Name(app))
		}
		if db.Name(0x0100AAAA00000000) != "" || len(db.Names) != 3 {
			t.Errorf("demo or entry without ID kept: %v", db.Names)
		}
		if !db.Known(app) || db.Known(0x0100000000010000) {
			t.Error("Known is wrong")
		}
		if db.DataDate() != "2021-11-29" {
			t.Errorf("DataDate = %q", db.DataDate())
		}
	}
	check(db)

	path := filepath.Join(t.TempDir(), FileName)
	if _, err := Load(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if err := db.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	check(loaded)

	// Unchanged files are not downloaded again.
	sent = nil
	again, err := Download(context.Background(), loaded, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 {
		t.Errorf("downloaded again: %v", sent)
	}
	check(again)
	if !again.Downloaded.After(loaded.Downloaded) && !again.Downloaded.Equal(loaded.Downloaded) {
		t.Error("download time not updated")
	}
}

func TestDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	saved := sources
	defer func() { sources = saved }()
	sources = [2]string{srv.URL + "/versions.json", srv.URL + "/US.en.json"}
	if _, err := Download(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}
