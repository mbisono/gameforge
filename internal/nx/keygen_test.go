package nx

import (
	"errors"
	"testing"

	"gameforge/internal/i18n"
)

func TestMissingKey(t *testing.T) {
	i18n.Set("en")
	k := &Keys{m: map[string][]byte{
		"master_key_00": make([]byte, 16), "master_key_13": make([]byte, 16), "titlekek_12": make([]byte, 16),
		"master_key_source": make([]byte, 16), "header_key": make([]byte, 32),
	}}
	if got := k.Newest(); got != 0x13 {
		t.Fatalf("Newest = %#x", got)
	}
	_, err := k.KeyAreaKey(0, 0x14)
	var e *MissingKeyError
	if !errors.As(err, &e) || e.Rev != 0x14 || e.Newest != 0x13 {
		t.Fatalf("err = %v", err)
	}
	want := "prod.keys too old: this file needs the keys of firmware 21.0.0 (master_key_14), but your prod.keys only goes up to firmware 20.0.0 (master_key_13). Dump the keys again from a console with a newer firmware"
	if err.Error() != want {
		t.Errorf("got %q", err)
	}
	if got := KeyFirmware(0x16); got != "23.0.0" {
		t.Errorf("KeyFirmware(0x16) = %q", got)
	}
	if got := KeyFirmware(0x17); got != "newer than 23.0.0" {
		t.Errorf("KeyFirmware(0x17) = %q", got)
	}
}
