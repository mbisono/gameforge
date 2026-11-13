package nx

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
)

// nintendoXTS implements AES-128-XTS with Nintendo's tweak convention:
// the sector number is encoded big-endian (standard XTS uses little-endian).
type nintendoXTS struct {
	data, tweak cipher.Block
	sectorSize  int
}

func newNintendoXTS(key []byte, sectorSize int) (*nintendoXTS, error) {
	d, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, err
	}
	t, err := aes.NewCipher(key[16:32])
	if err != nil {
		return nil, err
	}
	return &nintendoXTS{data: d, tweak: t, sectorSize: sectorSize}, nil
}

func (x *nintendoXTS) crypt(dst, src []byte, sector uint64, decrypt bool) {
	for off := 0; off < len(src); off += x.sectorSize {
		var t [16]byte
		binary.BigEndian.PutUint64(t[8:], sector)
		x.tweak.Encrypt(t[:], t[:])

		end := min(off+x.sectorSize, len(src))
		for i := off; i+16 <= end; i += 16 {
			var b [16]byte
			for j := range b {
				b[j] = src[i+j] ^ t[j]
			}
			if decrypt {
				x.data.Decrypt(b[:], b[:])
			} else {
				x.data.Encrypt(b[:], b[:])
			}
			for j := range b {
				dst[i+j] = b[j] ^ t[j]
			}
			gfMul2(&t)
		}
		sector++
	}
}

// gfMul2 multiplies the tweak by x in GF(2^128) (little-endian byte order).
func gfMul2(t *[16]byte) {
	carry := t[15] >> 7
	for i := 15; i > 0; i-- {
		t[i] = t[i]<<1 | t[i-1]>>7
	}
	t[0] <<= 1
	if carry != 0 {
		t[0] ^= 0x87
	}
}
