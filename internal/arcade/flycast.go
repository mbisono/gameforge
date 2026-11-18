package arcade

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Flycast, which runs Naomi, Naomi 2 and Atomiswave games, publishes no
// list of sets: it keeps its games in a table in its source code
// (core/hw/naomi/naomi_roms.cpp), with each game's short name, parent,
// BIOS and files (name, size and CRC, taken from MAME). ParseFlycast reads
// that table as data; nothing of Flycast is built into this program.

// ParseFlycast reads Flycast's table of Naomi and Atomiswave games.
func ParseFlycast(r io.Reader) (*List, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	toks := cTokens(string(src))
	l := &List{Sets: map[string]*Set{}}

	// BIOS sets: { "naomi", { { region, "file", offset, length, crc, type }, … }, "archive" }
	for _, e := range cArray(toks, "BIOS") {
		name := e.str(0)
		if name == "" {
			continue
		}
		s := &Set{Name: name, Title: name, Bios: true, Works: true}
		if arch := e.str(2); arch != "" {
			s.Name = arch
		}
		for _, b := range e.list(1) {
			if rom, ok := flycastRom(b.str(1), b.num(3), b.num(4), b.ident(5)); ok {
				s.Roms = append(s.Roms, rom)
			}
		}
		if prev := l.Sets[strings.ToLower(s.Name)]; prev != nil {
			prev.Roms = append(prev.Roms, s.Roms...) // several BIOS entries in one archive
			continue
		}
		l.Sets[strings.ToLower(s.Name)] = s
	}
	biosArchive := map[string]string{}
	for _, e := range cArray(toks, "BIOS") {
		if name, arch := e.str(0), e.str(2); name != "" {
			biosArchive[name] = cmpOr(arch, name)
		}
	}

	// Games: { "name", "parent", "description", size, key, "bios", cart,
	// rotation, { { "file", offset, length, crc, type, src }, … }, "gdrom", … }
	for _, e := range cArray(toks, "Games") {
		name := e.str(0)
		if name == "" {
			continue
		}
		s := &Set{Name: name, CloneOf: e.str(1), Title: e.str(2), Works: true}
		s.RomOf = s.CloneOf
		bios := cmpOr(e.str(5), "naomi")
		s.Needs = []string{cmpOr(biosArchive[bios], bios)}
		for _, b := range e.list(8) {
			if rom, ok := flycastRom(b.str(0), b.num(2), b.num(3), b.ident(4)); ok {
				s.Roms = append(s.Roms, rom)
			}
		}
		if gd := e.str(9); gd != "" {
			s.Disks, s.Chds = 1, []string{gd} // a GD-ROM, as a CHD
		}
		l.Sets[strings.ToLower(name)] = s
	}
	// A clone's files that its parent has come from the parent's archive.
	for _, s := range l.Sets {
		p := l.Sets[strings.ToLower(s.CloneOf)]
		if p == nil {
			continue
		}
		have := map[fileKey]bool{}
		for _, r := range p.Roms {
			have[fileKey{r.Size, r.CRC}] = true
		}
		for i, r := range s.Roms {
			if have[fileKey{r.Size, r.CRC}] {
				s.Roms[i].Merge = r.Name
			}
		}
	}
	if len(l.Sets) == 0 {
		return nil, fmt.Errorf("no games in Flycast's table")
	}
	return l, nil
}

// flycastRom is a file of the table; Copy entries name no file.
func flycastRom(name string, length, crc int64, blobType string) (Rom, bool) {
	if name == "" || blobType == "Copy" {
		return Rom{}, false
	}
	return Rom{Name: name, Size: length, CRC: uint32(crc), NoDump: crc == 0}, true
}

// cNode is an element of a C initializer: tokens, or a braced list.
type cNode struct {
	toks   []string
	kids   []cNode
	isList bool
}

func (n cNode) field(i int) (cNode, bool) {
	if !n.isList || i >= len(n.kids) {
		return cNode{}, false
	}
	return n.kids[i], true
}

// str is a string field ("" for nullptr, NULL or no field).
func (n cNode) str(i int) string {
	f, ok := n.field(i)
	if !ok || len(f.toks) != 1 || !strings.HasPrefix(f.toks[0], `"`) {
		return ""
	}
	return strings.Trim(f.toks[0], `"`)
}

// num is a number field (0 if it is not a plain number).
func (n cNode) num(i int) int64 {
	f, ok := n.field(i)
	if !ok || len(f.toks) != 1 {
		return 0
	}
	v, err := strconv.ParseInt(f.toks[0], 0, 64)
	if err != nil {
		return 0
	}
	return v
}

// ident is an identifier field, such as a blob type.
func (n cNode) ident(i int) string {
	f, ok := n.field(i)
	if !ok || len(f.toks) != 1 {
		return ""
	}
	return f.toks[0]
}

// list is a braced list field: its elements.
func (n cNode) list(i int) []cNode {
	f, ok := n.field(i)
	if !ok || !f.isList {
		return nil
	}
	return f.kids
}

// cArray returns the elements of the array initialized as "name[] = { … }".
func cArray(toks []string, name string) []cNode {
	for i := 0; i+4 < len(toks); i++ {
		if toks[i] == name && toks[i+1] == "[" && toks[i+2] == "]" && toks[i+3] == "=" && toks[i+4] == "{" {
			n, _ := cParse(toks, i+4)
			return n.kids
		}
	}
	return nil
}

// cParse reads the braced list starting at toks[i] ("{"), and returns it
// and the position after its "}".
func cParse(toks []string, i int) (cNode, int) {
	n := cNode{isList: true}
	cur := cNode{}
	i++
	for i < len(toks) {
		switch t := toks[i]; t {
		case "{":
			var sub cNode
			sub, i = cParse(toks, i)
			cur = sub
			continue
		case "}":
			if cur.isList || len(cur.toks) > 0 {
				n.kids = append(n.kids, cur)
			}
			return n, i + 1
		case ",":
			n.kids = append(n.kids, cur)
			cur = cNode{}
		default:
			if !cur.isList {
				cur.toks = append(cur.toks, t)
			}
		}
		i++
	}
	return n, i
}

// cTokens splits C source into strings, words and punctuation, without
// comments and preprocessor lines.
func cTokens(src string) []string {
	var out []string
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 4
		case c == '#' && (i == 0 || src[i-1] == '\n'):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			out = append(out, src[i:min(j+1, len(src))])
			i = j + 1
		case c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= '0' && src[j] <= '9' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z') {
				j++
			}
			out = append(out, src[i:j])
			i = j
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		default:
			out = append(out, string(c))
			i++
		}
	}
	return out
}
