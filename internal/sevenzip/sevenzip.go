// Package sevenzip writes and reads 7z archives.
//
// The writer puts all files in one solid LZMA stream (the LZMA SDK's
// encoder, ported to Go) with a plain, uncompressed header, as 7-Zip does
// with -mhc=off; 7-Zip and every 7z reader open it. The reader handles
// what the writer produces and, more generally, archives with a plain
// header whose folders each have a single LZMA, LZMA2 or copy coder; it is
// used to verify written archives.
package sevenzip

import (
	"errors"
	"time"
)

// Property IDs of the 7z header.
const (
	idEnd             = 0x00
	idHeader          = 0x01
	idMainStreamsInfo = 0x04
	idFilesInfo       = 0x05
	idPackInfo        = 0x06
	idUnpackInfo      = 0x07
	idSubStreamsInfo  = 0x08
	idSize            = 0x09
	idCRC             = 0x0A
	idFolder          = 0x0B
	idCodersUnpack    = 0x0C
	idNumUnpackStream = 0x0D
	idEmptyStream     = 0x0E
	idEmptyFile       = 0x0F
	idName            = 0x11
	idMTime           = 0x14
	idEncodedHeader   = 0x17
	idDummy           = 0x19
)

// Codec IDs.
var (
	codecCopy  = []byte{0x00}
	codecLZMA  = []byte{0x03, 0x01, 0x01}
	codecLZMA2 = []byte{0x21}
)

var signature = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}

const sigHeaderSize = 32

// ErrUnsupported is returned for archives the reader does not handle.
var ErrUnsupported = errors.New("7z: unsupported archive feature")

// Entry is a file in an archive.
type Entry struct {
	Name     string // path inside the archive, with "/" between folders
	Size     int64
	CRC      uint32
	Modified time.Time // zero when not stored
	Dir      bool
}

// fileTime converts to and from Windows FILETIME (100 ns since 1601).
const filetimeEpoch = 116444736000000000

func toFiletime(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano()/100 + filetimeEpoch)
}

func fromFiletime(v uint64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, (int64(v)-filetimeEpoch)*100)
}

// dictSize is the dictionary size an LZMA2 property byte stands for.
func dictSize(p byte) int64 {
	if p >= 40 {
		return 0xFFFFFFFF
	}
	return int64(2|p&1) << (p/2 + 11)
}
