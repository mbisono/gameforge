// Package i18n holds the interface translations and the current language.
package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jeandeaual/go-locale"
)

// Auto selects the system language.
const Auto = "auto"

// Language is a supported interface language.
type Language struct {
	Code string
	Name string // in the language itself
}

// Languages lists the supported languages, in the order of msg fields.
var Languages = []Language{
	{"en", "English"},
	{"pt", "Português"},
	{"de", "Deutsch"},
	{"fr", "Français"},
	{"es", "Español"},
}

// msg holds one message in every language, in the order of Languages.
type msg [5]string

var current atomic.Int32 // index into Languages

// Detect returns the code of the system language if supported, else "en".
func Detect() string {
	if l, err := locale.GetLanguage(); err == nil {
		l = strings.ToLower(l)
		for _, lang := range Languages {
			if lang.Code == l {
				return l
			}
		}
	}
	return "en"
}

// Set selects the interface language; Auto or an unknown code selects the
// system language. It returns the code actually used.
func Set(code string) string {
	if code == Auto || index(code) < 0 {
		code = Detect()
	}
	current.Store(int32(index(code)))
	return code
}

// Current returns the code of the current language.
func Current() string { return Languages[current.Load()].Code }

// Name returns the native name of a language code.
func Name(code string) string {
	if i := index(code); i >= 0 {
		return Languages[i].Name
	}
	return code
}

func index(code string) int {
	for i, l := range Languages {
		if l.Code == code {
			return i
		}
	}
	return -1
}

func format(key string) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	if s := m[current.Load()]; s != "" {
		return s
	}
	return m[0]
}

// T returns the translated message for key, formatted with args.
func T(key string, args ...any) string {
	if len(args) == 0 {
		return format(key)
	}
	return fmt.Sprintf(format(key), args...)
}

// Message is a message that is translated each time it is shown, so that
// it follows later language changes (for example in the log). Arguments
// that are themselves Messages, or other fmt.Stringers, are shown in the
// current language too.
type Message struct {
	Key  string
	Args []any
}

// M returns the Message for key with args.
func M(key string, args ...any) Message { return Message{key, args} }

func (m Message) String() string { return T(m.Key, m.Args...) }

// Errorf returns an error with the translated message for key; the message
// may wrap an error with %w. Like a Message, it is translated when shown.
func Errorf(key string, args ...any) error { return &msgError{Message{key, args}} }

type msgError struct{ m Message }

func (e *msgError) Error() string { return fmt.Errorf(format(e.m.Key), e.m.Args...).Error() }

// Unwrap returns the error arguments, so errors.Is and errors.As see the
// errors wrapped with %w.
func (e *msgError) Unwrap() []error {
	var out []error
	for _, a := range e.m.Args {
		if err, ok := a.(error); ok {
			out = append(out, err)
		}
	}
	return out
}
