package merge

import "testing"

func TestStandardName(t *testing.T) {
	const app = 0x0100000000010000
	cases := []struct {
		title, dlc string
		n          *NSP
		want       string
	}{
		{"The Legend of Zelda: Breath of the Wild™", "",
			&NSP{Path: `C:\x\zelda bad name.NSP`, Kind: KindBase, TitleID: app, AppID: app},
			"The Legend of Zelda - Breath of the Wild [0100000000010000][v0].nsp"},
		{"Super Game", "",
			&NSP{Path: "/x/upd.nsz", Kind: KindUpdate, TitleID: app + 0x800, AppID: app, Version: 196608},
			"Super Game [0100000000010800][v196608].nsz"},
		{"Super Game", "Super Game - Expansion Pass",
			&NSP{Path: "/x/a.nsp", Kind: KindDLC, TitleID: app + 0x1001, AppID: app},
			"Super Game - Expansion Pass [0100000000011001][v0].nsp"},
		{"Super Game", "Bonus: Outfits",
			&NSP{Path: "/x/a.nsp", Kind: KindDLC, TitleID: app + 0x1002, AppID: app, Version: 65536},
			"Super Game - Bonus - Outfits [0100000000011002][v65536].nsp"},
		{"Super Game", "",
			&NSP{Path: "/x/a.nsp", Kind: KindDLC, TitleID: app + 0x1003, AppID: app},
			"Super Game - DLC [0100000000011003][v0].nsp"},
		{"Bad/Name?", "", &NSP{Path: "/x/a.xci", Kind: KindBase, TitleID: app, AppID: app}, "Bad_Name_ [0100000000010000][v0].xci"},
		{"", "", &NSP{Path: "/x/a.nsp", Kind: KindBase, TitleID: app, AppID: app}, ""},
	}
	for _, c := range cases {
		if got := StandardName(c.title, c.n, c.dlc); got != c.want {
			t.Errorf("%q: got %q, want %q", c.title, got, c.want)
		}
	}
}
