// Package update asks GitHub for the newest release of the app. Only the
// app's name and version are sent, as the User-Agent.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// URL is the GitHub API page of the newest release; tests replace it.
var URL = "https://api.github.com/repos/tschertel/gameforge/releases/latest"

// Release is a published version.
type Release struct {
	Version string // e.g. "2.12.0", without the "v" of the tag
	Page    string // the release's web page
}

// Latest returns the newest release. agent is sent as the User-Agent.
func Latest(ctx context.Context, agent string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub: %s", resp.Status)
	}
	var r struct {
		Tag  string `json:"tag_name"`
		Page string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, err
	}
	v := strings.TrimPrefix(r.Tag, "v")
	if _, ok := parse(v); !ok {
		return Release{}, errors.New("GitHub: no version in the newest release")
	}
	if !strings.HasPrefix(r.Page, "https://github.com/") {
		r.Page = ""
	}
	return Release{Version: v, Page: r.Page}, nil
}

// Newer reports whether version a (such as "2.12.0") is newer than b.
// Versions that cannot be read are never newer.
func Newer(a, b string) bool {
	x, ok1 := parse(strings.TrimPrefix(a, "v"))
	y, ok2 := parse(strings.TrimPrefix(b, "v"))
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// parse reads "major.minor.patch".
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
