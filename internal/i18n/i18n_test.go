package i18n

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var verbs = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// Every message must exist in every language with the same format verbs.
func TestMessagesComplete(t *testing.T) {
	for key, m := range messages {
		want := verbs.FindAllString(m[0], -1)
		for i, s := range m {
			if s == "" {
				t.Errorf("%s: missing %s translation", key, Languages[i].Code)
				continue
			}
			if got := verbs.FindAllString(s, -1); !slices.Equal(got, want) {
				t.Errorf("%s [%s]: verbs %v, want %v", key, Languages[i].Code, got, want)
			}
		}
	}
}

func TestSet(t *testing.T) {
	defer Set("en")
	if Set("de") != "de" || T("btn.cancel") != "Abbrechen" {
		t.Fatal("German not selected")
	}
	if T("no.such.key") != "no.such.key" {
		t.Fatal("unknown keys must fall back to the key")
	}
	if code := Set(Auto); code != Detect() {
		t.Fatalf("auto selected %q, want %q", code, Detect())
	}
}

// Messages and errors are translated when shown, not when created.
func TestLazyTranslation(t *testing.T) {
	defer Set("en")
	Set("en")
	inner := errors.New("disk full")
	m := M("log.merge_error", Errorf("err.verify", "x.nca"))
	wrapped := Errorf("db.download_error", inner)
	en, enErr := m.String(), wrapped.Error()
	Set("de")
	if de := m.String(); de == en || !strings.Contains(de, T("err.verify", "x.nca")) {
		t.Errorf("message not translated again: %q (English %q)", de, en)
	}
	if wrapped.Error() == enErr {
		t.Errorf("error not translated again: %q", wrapped.Error())
	}
	if !errors.Is(wrapped, inner) {
		t.Error("wrapped error lost")
	}
}
