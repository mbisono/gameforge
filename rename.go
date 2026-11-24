package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	. "modernc.org/tk9.0"

	"gameforge/internal/merge"
)

// renaming is a file of the Switch list and its new path.
type renaming struct {
	nsp *merge.NSP
	to  string
}

// officialTitle returns the game's name from its Control data (read with
// prod.keys) or else from the game database, or "" if neither has it.
func (u *ui) officialTitle(g *merge.Game) string {
	if c := g.Control(); c != nil && c.Name != "" {
		return c.Name
	}
	if u.db != nil {
		return u.db.Name(g.AppID)
	}
	return ""
}

// renamePlan lists the checked files of the Switch list whose name differs
// from the standard one.
func (u *ui) renamePlan() []renaming {
	var plan []renaming
	for _, g := range u.games {
		title := u.officialTitle(g)
		if title == "" {
			continue
		}
		for _, n := range g.Files() {
			if !u.checked[n.Path] {
				continue
			}
			dlc := ""
			if n.Kind == merge.KindDLC && u.db != nil {
				dlc = u.db.Name(n.TitleID)
			}
			name := merge.StandardName(title, n, dlc)
			if name == "" || name == filepath.Base(n.Path) {
				continue
			}
			plan = append(plan, renaming{n, filepath.Join(filepath.Dir(n.Path), name)})
		}
	}
	return plan
}

// renameFiles gives the checked files of the Switch list their standard names,
// after showing what changes.
func (u *ui) renameFiles() {
	if u.busy {
		return
	}
	if len(u.games) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("rename.empty")))
		return
	}
	if len(u.selectedFiles()) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("list.none_checked")))
		return
	}
	plan := u.renamePlan()
	if len(plan) == 0 {
		MessageBox(Parent(App), Icon("info"), Title(T("app.title")), Msg(T("rename.none")))
		return
	}
	var lines []string
	for i, r := range plan {
		if i == 15 {
			lines = append(lines, T("rename.more", len(plan)-i))
			break
		}
		lines = append(lines, filepath.Base(r.nsp.Path)+"\n   → "+filepath.Base(r.to))
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("rename.confirm", len(plan))), Detail(strings.Join(lines, "\n")))
	if answer != "yes" {
		return
	}
	done := 0
	for _, r := range plan {
		from := r.nsp.Path
		if err := renameFile(from, r.to); err != nil {
			u.logf("log.rename_error", filepath.Base(from), err)
			continue
		}
		u.logf("log.renamed", filepath.Base(from), filepath.Base(r.to))
		for i, p := range u.paths {
			if p == from {
				u.paths[i] = r.to
			}
		}
		if v, ok := u.checked[from]; ok {
			delete(u.checked, from)
			u.checked[r.to] = v
		}
		r.nsp.Path = r.to
		done++
	}
	u.logf("log.rename_done", done, len(plan))
	u.refreshList()
}

// renameFile renames a file without replacing another one. A change of
// case only is allowed: on Windows and macOS both names are the same file,
// but on Linux "game.chd" and "Game.chd" can be two files.
func renameFile(from, to string) error {
	if err := renameFree(from, to); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// renameFree tells whether from can be renamed to without replacing
// another file (see renameFile).
func renameFree(from, to string) error {
	if st, err := os.Stat(to); err == nil {
		if fst, ferr := os.Stat(from); ferr != nil || !strings.EqualFold(from, to) || !os.SameFile(fst, st) {
			return fmt.Errorf("%s: %w", filepath.Base(to), fs.ErrExist)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
