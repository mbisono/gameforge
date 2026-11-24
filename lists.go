package main

import (
	"slices"
	"strings"
	"time"

	. "modernc.org/tk9.0"

	"gameforge/internal/arcade"
)

// At startup, if the user agreed (asked once), and as often as the check
// for new versions is set to run, the app looks for downloaded lists that
// have grown old (the game
// database of the Switch tab, the names of discs and ROMs, the lists of
// arcade sets and of RetroAchievements) and asks once whether to update
// them. Only lists already there are updated; nothing new is downloaded.

// oldList is a downloaded list that has grown old, with how to update it.
type oldList struct {
	name   string // as the question shows it
	update func(then func())
}

// oldLists returns the downloaded lists that have grown old: older than
// the age at which their status says so.
func (u *ui) oldLists() []oldList {
	var out []oldList
	if db := u.noIntroDB(); db != nil && len(db.Lists) > 0 && time.Since(db.Oldest()) > redumpMaxAge {
		var systems []string
		for s := range db.Lists {
			systems = append(systems, s)
		}
		slices.Sort(systems)
		out = append(out, oldList{T("lists.nointro", len(systems)), func(then func()) { u.downloadNoIntro(systems, then) }})
	}
	if db := u.redumpDB(); db != nil && len(db.Lists) > 0 && time.Since(db.Oldest()) > redumpMaxAge {
		var platforms []string
		for p := range db.Lists {
			platforms = append(platforms, p)
		}
		slices.Sort(platforms)
		out = append(out, oldList{T("lists.redump", len(platforms)), func(then func()) { u.downloadRedump(platforms, then) }})
	}
	if db := u.arcadeLists(); db != nil && len(db.Lists) > 0 && time.Since(db.Oldest()) > redumpMaxAge {
		var ids, names []string
		for _, e := range arcade.Emulators() {
			if db.Has(e.ID) {
				ids = append(ids, e.ID)
				names = append(names, e.Name)
			}
		}
		if len(ids) > 0 {
			out = append(out, oldList{T("lists.arcade", strings.Join(names, ", ")), func(then func()) { u.downloadArcade(ids, then) }})
		}
	}
	// The RetroAchievements lists need the user's key.
	if db := u.raDB(); db != nil && u.cfg.RAKey != "" {
		var consoles []int
		for c, l := range db.Lists {
			if time.Since(l.Downloaded) > redumpMaxAge {
				consoles = append(consoles, c)
			}
		}
		slices.Sort(consoles)
		if len(consoles) > 0 {
			out = append(out, oldList{T("lists.ra", len(consoles)), func(then func()) { u.downloadRA(consoles, then) }})
		}
	}
	// Last: it calls then only after a successful download.
	if u.db != nil && time.Since(u.db.Downloaded) > dbMaxAge {
		out = append(out, oldList{T("lists.titledb"), func(then func()) { u.startDownloadDB(then) }})
	}
	return out
}

// listsOn tells whether the app asks about old lists.
func (u *ui) listsOn() bool { return u.cfg.ListsCheck != nil && *u.cfg.ListsCheck }

// listsEvery is how often the app looks for old lists: as often as it is
// set to check for new versions, even with that check off.
func (u *ui) listsEvery() time.Duration {
	i := slices.IndexFunc(updateIntervals, func(v updateInterval) bool { return v.id == u.cfg.UpdateEvery })
	return updateIntervals[max(i, 0)].every
}

// askListsCheck asks once whether to look for old lists at startup.
func (u *ui) askListsCheck() {
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("lists.ask_setting")), Detail(T("lists.ask_setting.detail")))
	on := answer == "yes"
	u.cfg.ListsCheck = &on
	u.saveConfig()
}

// checkLists asks whether to update the downloaded lists that have grown
// old, if the user wants to be asked and the last question was asked long
// enough ago.
func (u *ui) checkLists() {
	if u.busy || !u.listsOn() || time.Since(u.cfg.ListsChecked) < u.listsEvery() {
		return
	}
	lists := u.oldLists()
	if len(lists) == 0 {
		return
	}
	u.cfg.ListsChecked = time.Now()
	u.saveConfig()
	var names []string
	for _, l := range lists {
		names = append(names, "• "+l.name)
	}
	answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
		Msg(T("lists.ask")), Detail(strings.Join(names, "\n")+"\n\n"+T("lists.ask.detail")))
	if answer != "yes" {
		return
	}
	u.separate()
	var next func(i int)
	next = func(i int) {
		if i < len(lists) {
			lists[i].update(func() { next(i + 1) })
		}
	}
	next(0)
}
