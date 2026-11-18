package arcade

import (
	"slices"
	"strings"
)

// File is a file inside a set's archive, as its header records it.
type File struct {
	Name string
	Size int64
	CRC  uint32
}

type fileKey struct {
	size int64
	crc  uint32
}

// Result is what a check found about a set's archive.
type Result struct {
	Set     *Set     // the set of this name; nil if the list has none
	Missing int      // files neither in the archive nor in the sets it uses
	Wrong   int      // files present under their name, with another CRC
	Needs   []string // parent sets the archive needs, not in the folder
	Bios    []string // BIOS sets it uses, not in the folder: not checked
	Uses    []string // sets in the folder whose files it uses
	Like    *Set     // for an unknown name: the set the files are
}

// OK reports whether the set is complete.
func (r Result) OK() bool {
	return r.Set != nil && r.Missing == 0 && r.Wrong == 0 && len(r.Needs) == 0
}

// Audit checks the archive of a set: name is its file name without the
// extension, files what it holds, and other returns the files of another
// set's archive in the same folder (false if there is none). A set's files
// may be in its own archive (non-merged sets), or, in split and merged
// sets, partly in those of its parent and BIOS.
func (l *List) Audit(name string, files []File, other func(set string) ([]File, bool)) Result {
	s := l.Sets[strings.ToLower(name)]
	if s == nil {
		return Result{Like: l.Identify(files)}
	}
	res := Result{Set: s}
	own := map[fileKey]bool{}
	byName := map[string]bool{}
	for _, f := range files {
		own[fileKey{f.Size, f.CRC}] = true
		byName[strings.ToLower(baseName(f.Name))] = true
	}
	// The sets it takes files from: its parent, then the parent's BIOS.
	var chain []*Set
	have := map[string]map[fileKey]bool{}
	for p, seen := s, map[string]bool{s.Name: true}; ; {
		next := cmpOr(p.RomOf, p.CloneOf)
		a := l.Sets[strings.ToLower(next)]
		if next == "" || a == nil || seen[a.Name] {
			break
		}
		seen[a.Name] = true
		chain = append(chain, a)
		if fs, ok := other(a.Name); ok {
			keys := map[fileKey]bool{}
			for _, f := range fs {
				keys[fileKey{f.Size, f.CRC}] = true
			}
			have[a.Name] = keys
		}
		p = a
	}
	needs, uses := map[string]bool{}, map[string]bool{}
	for _, r := range s.Roms {
		if r.NoDump {
			continue
		}
		k := fileKey{r.Size, r.CRC}
		if own[k] {
			continue
		}
		found := false
		for _, a := range chain {
			if have[a.Name][k] {
				uses[a.Name], found = true, true
				break
			}
		}
		if found {
			continue
		}
		if r.Merge != "" {
			if owner := ownerOf(chain, k); owner != "" && have[owner] == nil {
				needs[owner] = true // a BIOS is only noted, below
				continue
			}
		}
		if byName[strings.ToLower(r.Name)] {
			res.Wrong++
		} else {
			res.Missing++
		}
	}
	// BIOS sets the emulator loads too (Flycast's), whatever they hold.
	// A BIOS is not part of the game: one missing from the folder is only
	// noted, and its files are not checked.
	for _, n := range s.Needs {
		if _, ok := other(n); ok {
			res.Uses = append(res.Uses, n)
		} else {
			res.Bios = append(res.Bios, n)
		}
	}
	for _, a := range chain {
		if needs[a.Name] {
			if a.Bios {
				res.Bios = append(res.Bios, a.Name)
			} else {
				res.Needs = append(res.Needs, a.Name)
			}
		}
		if uses[a.Name] {
			res.Uses = append(res.Uses, a.Name)
		}
	}
	return res
}

// ownerOf returns the set of the chain whose own file this is (the one
// that holds it in a split set).
func ownerOf(chain []*Set, k fileKey) string {
	last := ""
	for _, a := range chain {
		for _, r := range a.Roms {
			if (fileKey{r.Size, r.CRC}) == k {
				if r.Merge == "" {
					return a.Name
				}
				last = a.Name
			}
		}
	}
	return last
}

// Identify returns the set whose own files (not those of its parent or
// BIOS) are all among files, or nil: the set of an archive with another
// name. Of several, the one with the most files wins, then a parent.
func (l *List) Identify(files []File) *Set {
	if len(files) == 0 {
		return nil
	}
	if l.index == nil {
		l.index = map[fileKey][]string{}
		for name, s := range l.Sets {
			for _, r := range s.Roms {
				if r.Merge == "" && !r.NoDump {
					k := fileKey{r.Size, r.CRC}
					if !slices.Contains(l.index[k], name) {
						l.index[k] = append(l.index[k], name)
					}
				}
			}
		}
	}
	count := map[string]int{}
	seen := map[fileKey]bool{}
	for _, f := range files {
		k := fileKey{f.Size, f.CRC}
		if seen[k] {
			continue
		}
		seen[k] = true
		for _, name := range l.index[k] {
			count[name]++
		}
	}
	var best *Set
	bestN := 0
	for name, n := range count {
		s := l.Sets[name]
		if n != ownCount(s) {
			continue
		}
		if best == nil || n > bestN || n == bestN && better(s, best) {
			best, bestN = s, n
		}
	}
	return best
}

// better tells which of two sets with as many files to choose: a parent,
// then the first name.
func better(a, b *Set) bool {
	if (a.CloneOf == "") != (b.CloneOf == "") {
		return a.CloneOf == ""
	}
	return a.Name < b.Name
}

// ownCount is the number of different files a set holds itself.
func ownCount(s *Set) int {
	seen := map[fileKey]bool{}
	for _, r := range s.Roms {
		if r.Merge == "" && !r.NoDump {
			seen[fileKey{r.Size, r.CRC}] = true
		}
	}
	return len(seen)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
