// Package datfile downloads and reads the clrmamepro databases (.dat) that
// libretro publishes (github.com/libretro/libretro-database): the Redump
// databases of discs and the No-Intro databases of cartridges.
package datfile

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BaseURL is libretro-database, whose folders hold the databases
// ("metadat/redump/", "metadat/no-intro/", "dat/").
var BaseURL = "https://raw.githubusercontent.com/libretro/libretro-database/master/"

// Progress reports the download of a database file; total is -1 if the
// server did not state it.
type Progress func(file string, done, total int64)

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 30 * time.Second,
	TLSHandshakeTimeout:   30 * time.Second,
}}

// Fetch downloads u and passes the body to parse. If the file still has
// the ETag etag, it returns "" without downloading; otherwise the file's
// ETag, or "-" if the server sent none.
func Fetch(ctx context.Context, u, name, etag string, progress Progress, parse func(io.Reader) error) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gameforge")
	if etag != "" && etag != "-" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return "", nil
	case http.StatusOK:
	default:
		return "", fmt.Errorf("%s: %s", name, resp.Status)
	}
	var done int64
	r := readerFunc(func(b []byte) (int, error) {
		n, err := resp.Body.Read(b)
		done += int64(n)
		if progress != nil {
			progress(name, done, resp.ContentLength)
		}
		return n, err
	})
	if err := parse(r); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if tag := resp.Header.Get("ETag"); tag != "" {
		return tag, nil
	}
	return "-", nil
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(b []byte) (int, error) { return f(b) }

// Unquote reads a value that may be in double quotes.
func Unquote(s string) string {
	s = strings.TrimSpace(s)
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return strings.Trim(s, `"`)
}

// RomFields reads `rom ( name "x y" size 1 crc AB … )`.
func RomFields(line string) map[string]string {
	out := map[string]string{}
	s := strings.TrimSuffix(strings.TrimPrefix(line, "rom ("), ")")
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		key, rest, ok := strings.Cut(s, " ")
		if !ok {
			break
		}
		rest = strings.TrimSpace(rest)
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			val, s = rest[1:end+1], rest[end+2:]
		} else {
			val, s, _ = strings.Cut(rest, " ")
		}
		out[key] = val
	}
	return out
}
