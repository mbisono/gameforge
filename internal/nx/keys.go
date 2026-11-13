package nx

import (
	"bufio"
	"crypto/aes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"gameforge/internal/i18n"
)

// Keys holds the contents of a prod.keys file.
type Keys struct {
	m map[string][]byte
}

// LoadKeys parses a prod.keys file ("name = hexvalue" per line).
func LoadKeys(path string) (*Keys, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	k := &Keys{m: map[string][]byte{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		b, err := hex.DecodeString(strings.TrimSpace(val))
		if err != nil {
			continue
		}
		k.m[strings.ToLower(strings.TrimSpace(name))] = b
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(k.m) == 0 {
		return nil, i18n.Errorf("err.keys.none", path)
	}
	return k, nil
}

// Count returns how many keys were loaded.
func (k *Keys) Count() int { return len(k.m) }

func (k *Keys) get(name string, size int) ([]byte, error) {
	v, ok := k.m[name]
	if !ok {
		return nil, i18n.Errorf("err.keys.missing", name)
	}
	if len(v) != size {
		return nil, i18n.Errorf("err.keys.size", name)
	}
	return v, nil
}

// HeaderKey returns the 0x20-byte NCA header key.
func (k *Keys) HeaderKey() ([]byte, error) { return k.get("header_key", 0x20) }

var keyAreaNames = [3]string{"application", "ocean", "system"}

// KeyAreaKey returns the key-area key for the given index (0 application,
// 1 ocean, 2 system) and master key revision, deriving it when the dumped
// key is missing.
func (k *Keys) KeyAreaKey(index uint8, rev int) ([]byte, error) {
	if int(index) >= len(keyAreaNames) {
		return nil, i18n.Errorf("err.keys.kak_index", index)
	}
	name := keyAreaNames[index]
	if v, err := k.get(fmt.Sprintf("key_area_key_%s_%02x", name, rev), 0x10); err == nil {
		return v, nil
	}
	mk, err := k.get(fmt.Sprintf("master_key_%02x", rev), 0x10)
	if err != nil {
		return nil, &MissingKeyError{Rev: rev, Newest: k.Newest()}
	}
	kekSrc, err := k.get("aes_kek_generation_source", 0x10)
	if err != nil {
		return nil, err
	}
	keySrc, err := k.get("aes_key_generation_source", 0x10)
	if err != nil {
		return nil, err
	}
	kakSrc, err := k.get(fmt.Sprintf("key_area_key_%s_source", name), 0x10)
	if err != nil {
		return nil, err
	}
	kek := ecbDecrypt(mk, kekSrc)
	srcKek := ecbDecrypt(kek, kakSrc)
	return ecbDecrypt(srcKek, keySrc), nil
}

// Titlekek returns the title-key encryption key for a master key revision.
func (k *Keys) Titlekek(rev int) ([]byte, error) {
	if v, err := k.get(fmt.Sprintf("titlekek_%02x", rev), 0x10); err == nil {
		return v, nil
	}
	mk, err := k.get(fmt.Sprintf("master_key_%02x", rev), 0x10)
	if err != nil {
		return nil, &MissingKeyError{Rev: rev, Newest: k.Newest()}
	}
	src, err := k.get("titlekek_source", 0x10)
	if err != nil {
		return nil, err
	}
	return ecbDecrypt(mk, src), nil
}

// ecbDecrypt decrypts data (a multiple of 16 bytes) with AES-ECB.
func ecbDecrypt(key, data []byte) []byte {
	c, err := aes.NewCipher(key)
	if err != nil {
		panic(err) // keys are length-checked by callers
	}
	out := make([]byte, len(data))
	for i := 0; i+16 <= len(data); i += 16 {
		c.Decrypt(out[i:i+16], data[i:i+16])
	}
	return out
}

// ecbEncrypt encrypts data (a multiple of 16 bytes) with AES-ECB.
func ecbEncrypt(key, data []byte) []byte {
	c, err := aes.NewCipher(key)
	if err != nil {
		panic(err) // keys are length-checked by callers
	}
	out := make([]byte, len(data))
	for i := 0; i+16 <= len(data); i += 16 {
		c.Encrypt(out[i:i+16], data[i:i+16])
	}
	return out
}
