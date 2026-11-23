package romcheck

import (
	"encoding/binary"
	"testing"
)

func TestSNES(t *testing.T) {
	for _, size := range []int{512 << 10, 3 << 20} { // a power of two; 2 MiB + 1 MiB mirrored
		rom := make([]byte, size)
		for i := range rom {
			rom[i] = byte(i*7 + i>>11)
		}
		at := 0x7fc0 // LoROM
		// The checksum and its complement always add 0x1FE to the sum.
		binary.LittleEndian.PutUint16(rom[at+0x1c:], 0xffff)
		binary.LittleEndian.PutUint16(rom[at+0x1e:], 0)
		sum, _ := mirroredSum(rom)
		binary.LittleEndian.PutUint16(rom[at+0x1e:], uint16(sum))
		binary.LittleEndian.PutUint16(rom[at+0x1c:], ^uint16(sum))
		if r, ok := SNES(rom); !ok || !r.OK {
			t.Fatalf("%d: %+v %v", size, r, ok)
		}
		// With a copier header, and changed.
		headered := append(make([]byte, 512), rom...)
		if r, ok := SNES(headered); !ok || !r.OK {
			t.Fatalf("%d with a copier header: %+v %v", size, r, ok)
		}
		rom[0x1234] ^= 0x40
		if r, ok := SNES(rom); !ok || r.OK {
			t.Fatalf("%d changed: %+v %v", size, r, ok)
		}
	}
}

func TestMegaDriveAndSMD(t *testing.T) {
	rom := make([]byte, 128<<10)
	for i := range rom {
		rom[i] = byte(i*13 + i>>9)
	}
	copy(rom[0x100:], "SEGA MEGA DRIVE ")
	var s uint16
	for i := 0x200; i < len(rom); i += 2 {
		s += binary.BigEndian.Uint16(rom[i:])
	}
	binary.BigEndian.PutUint16(rom[0x18e:], s)
	if r, ok := MegaDrive(rom); !ok || !r.OK {
		t.Fatalf("plain: %+v %v", r, ok)
	}
	// The same ROM as an .smd file.
	smd := make([]byte, 512, 512+len(rom))
	smd[8], smd[9] = 0xaa, 0xbb
	for at := 0; at < len(rom); at += SMDBlock {
		blk := rom[at : at+SMDBlock]
		for i := 1; i < len(blk); i += 2 {
			smd = append(smd, blk[i])
		}
		for i := 0; i < len(blk); i += 2 {
			smd = append(smd, blk[i])
		}
	}
	if !IsSMD(smd, int64(len(smd))) || string(DecodeSMD(smd[512:])) != string(rom) {
		t.Fatal("SMD not decoded")
	}
	if r, ok := MegaDrive(smd); !ok || !r.OK {
		t.Fatalf("smd: %+v %v", r, ok)
	}
	rom[0x5000] ^= 1
	if r, ok := MegaDrive(rom); !ok || r.OK {
		t.Fatalf("changed: %+v %v", r, ok)
	}
	if _, ok := MegaDrive(make([]byte, 0x1000)); ok {
		t.Fatal("a file without the SEGA header was checked")
	}
}

func TestDS(t *testing.T) {
	h := make([]byte, 0x200)
	copy(h, "GAME")
	binary.LittleEndian.PutUint16(h[0x15c:], crc16(h[0xc0:0x15c]))
	binary.LittleEndian.PutUint16(h[0x15e:], crc16(h[:0x15e]))
	if r, ok := DS(h); !ok || !r.OK {
		t.Fatalf("%+v %v", r, ok)
	}
	h[0x20] ^= 1
	if r, ok := DS(h); !ok || r.OK {
		t.Fatalf("changed: %+v %v", r, ok)
	}
}
