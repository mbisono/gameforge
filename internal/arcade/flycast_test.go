package arcade

import (
	"strings"
	"testing"
)

// A piece of Flycast's table, in its C form: BIOS sets, a cartridge game
// with a clone and a Copy entry, and a GD-ROM game.
const flycastTable = `
#include "naomi_roms.h"
/* comment { "not", "a", "game" } */
const BIOS_t BIOS[] =
{
	{
		"awbios",
		{
			{ 0, "bios0.ic23", 0x000000, 0x020000, 0x719b2b0b },
		}
	},
	{
		"naomi",
		{
			{ 0, "epr-21576h.ic27", 0x000000, 0x200000, 0xd4895685 }, // Japan
			{ 1, "epr-21578h.ic27", 0x000000, 0x200000, 0x7b452946 },
			{ 0, nullptr, 0, 0, 0 },
		}
	},
	{ nullptr }
};

const Game Games[] =
{
    // The King of Fighters Neowave
    {
        "kofnw",
		nullptr,
        "The King of Fighters Neowave",
        0x8000000,
        0x99,
        "awbios",
        AW,
        ROT0,
        {
            { "ax2201en_p01.ic18", 0x0000000, 0x0800000, 0x27aab918 },
            { "ax2201m01.ic11",    0x1000000, 0x1000000, 0x22ea665b },
            { "copy",              0x1800000, 0x0800000, 0x0000000, Copy, 0x1000000 },
        },
		nullptr,
        &kofnw_inputs,
    },
    {
        "kofnwj",
        "kofnw",
        "The King of Fighters Neowave (Japan)",
        0x8000000,
        0x99,
        "awbios",
        AW,
        ROT0,
        {
            { "ax2201jp_p01.ic18", 0x0000000, 0x0800000, 0xecc4a5c7 },
            { "ax2201m01.ic11",    0x1000000, 0x1000000, 0x22ea665b },
        },
    },
    {
        "ikaruga",
        nullptr,
        "Ikaruga",
        0x4000,
        0,
        nullptr,
        GD,
        ROT270,
        {
            { "317-0323-jpn.pic", 0, 0x4000, 0x1b6d4a4e, Key },
            { nullptr, 0, 0 },
        },
        "gdl-0010",
    },
    { nullptr }
};
`

func TestParseFlycast(t *testing.T) {
	l, err := ParseFlycast(strings.NewReader(flycastTable))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Sets) != 5 {
		t.Fatalf("%d sets", len(l.Sets))
	}
	k := l.Sets["kofnw"]
	if k.Title != "The King of Fighters Neowave" || len(k.Roms) != 2 || k.Roms[1].Size != 0x1000000 || k.Roms[1].CRC != 0x22ea665b || !slicesEq(k.Needs, "awbios") {
		t.Errorf("kofnw %+v", k)
	}
	j := l.Sets["kofnwj"]
	if j.CloneOf != "kofnw" || j.Roms[0].Merge != "" || j.Roms[1].Merge == "" {
		t.Errorf("kofnwj %+v", j)
	}
	g := l.Sets["ikaruga"]
	if !slicesEq(g.Needs, "naomi") || !slicesEq(g.Chds, "gdl-0010") || len(g.Roms) != 1 {
		t.Errorf("ikaruga %+v", g)
	}
	if n := l.Sets["naomi"]; n == nil || !n.Bios || len(n.Roms) != 2 {
		t.Errorf("naomi %+v", n)
	}

	// The clone takes the shared file from its parent; a BIOS set not in
	// the folder is only noted.
	parent := []File{{"ax2201en_p01.ic18", 0x800000, 0x27aab918}, {"ax2201m01.ic11", 0x1000000, 0x22ea665b}}
	r := l.Audit("kofnwj", []File{{"ax2201jp_p01.ic18", 0x800000, 0xecc4a5c7}}, func(set string) ([]File, bool) {
		if set == "kofnw" {
			return parent, true
		}
		return nil, false
	})
	if !r.OK() || !slicesEq(r.Uses, "kofnw") || len(r.Needs) != 0 || !slicesEq(r.Bios, "awbios") || r.Missing != 0 {
		t.Errorf("kofnwj without BIOS %+v", r)
	}
	r = l.Audit("kofnw", parent, func(set string) ([]File, bool) { return []File{{"bios0.ic23", 0x20000, 0x719b2b0b}}, set == "awbios" })
	if !r.OK() || len(r.Needs) != 0 || !slicesEq(r.Uses, "awbios") {
		t.Errorf("kofnw audit %+v", r)
	}
}
