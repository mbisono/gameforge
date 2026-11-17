package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.12.0", "2.11.0", true}, {"v2.11.1", "2.11.0", true}, {"3.0.0", "2.99.99", true},
		{"2.11.0", "2.11.0", false}, {"2.10.9", "2.11.0", false}, {"2.9.0", "2.10.0", false},
		{"2.12", "2.11.0", false}, {"", "2.11.0", false}, {"2.12.0-beta", "2.11.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestLatest(t *testing.T) {
	agent := ""
	body := `{"tag_name":"v2.12.0","html_url":"https://github.com/tschertel/gameforge/releases/tag/v2.12.0","assets":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	old := URL
	URL = srv.URL
	defer func() { URL = old }()

	r, err := Latest(context.Background(), "GameForge/2.11.0")
	if err != nil || r.Version != "2.12.0" || r.Page != "https://github.com/tschertel/gameforge/releases/tag/v2.12.0" {
		t.Errorf("got %+v, %v", r, err)
	}
	if agent != "GameForge/2.11.0" {
		t.Errorf("User-Agent %q", agent)
	}
	// A page outside GitHub is not offered as a link.
	body = `{"tag_name":"v2.12.0","html_url":"https://example.com/x"}`
	if r, _ := Latest(context.Background(), "x"); r.Page != "" {
		t.Errorf("page %q", r.Page)
	}
	body = `{"tag_name":"nightly"}`
	if _, err := Latest(context.Background(), "x"); err == nil {
		t.Error("a tag without a version was taken")
	}
	srv.Close()
	if _, err := Latest(context.Background(), "x"); err == nil {
		t.Error("no error without a server")
	}
}
