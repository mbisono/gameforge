package main

import (
	"context"
	"time"

	. "modernc.org/tk9.0"

	"gameforge/internal/update"
)

// The app can check once a day on GitHub whether a newer version is out:
// the user is asked once, and can change it in the About window. Only the
// app's name and version are sent; nothing is downloaded or installed.

// updateEvery is how often the check runs.
const updateEvery = 24 * time.Hour

// startUpdateCheck asks the first time whether to check for new versions,
// then checks if it is time to.
func (u *ui) startUpdateCheck() {
	if u.cfg.UpdateCheck == nil {
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("upd.ask")), Detail(T("upd.ask.detail")))
		on := answer == "yes"
		u.cfg.UpdateCheck = &on
		u.saveConfig()
	}
	u.checkUpdate()
}

// checkUpdate asks GitHub in the background for the newest version, if the
// check is on and the last one was tried a day ago or more. Errors (no
// network, GitHub blocked) are silent: the check is tried again a day later.
func (u *ui) checkUpdate() {
	if u.cfg.UpdateCheck == nil || !*u.cfg.UpdateCheck || time.Since(u.cfg.UpdateChecked) < updateEvery {
		return
	}
	u.cfg.UpdateChecked = time.Now()
	u.saveConfig()
	go func() {
		r, err := update.Latest(context.Background(), "GameForge/"+appVersion)
		if err != nil {
			return
		}
		PostEvent(func() {
			u.cfg.UpdateLatest, u.cfg.UpdatePage = r.Version, r.Page
			u.saveConfig()
			u.showUpdateNote()
			if update.Newer(r.Version, appVersion) {
				u.logf("upd.log", r.Version, u.updatePage())
				u.flush()
			}
		}, false)
	}()
}

// showUpdateNote shows, in the top row, the newest version found if it is
// newer than this one.
func (u *ui) showUpdateNote() {
	if u.updateNote == nil {
		return
	}
	if update.Newer(u.cfg.UpdateLatest, appVersion) {
		u.updateNote.Configure(Txt(T("upd.note", u.cfg.UpdateLatest)), Cursor("hand2"))
		setTooltip(u.updateNote, u.updatePage())
	} else {
		u.updateNote.Configure(Txt(""), Cursor(""))
	}
}

// updatePage is the web page of the newest version found.
func (u *ui) updatePage() string {
	if u.cfg.UpdatePage != "" {
		return u.cfg.UpdatePage
	}
	return repoURL + "/releases/latest"
}

// openUpdatePage opens the page of the newest version, if one was found.
func (u *ui) openUpdatePage() {
	if update.Newer(u.cfg.UpdateLatest, appVersion) {
		openURL(u.updatePage())
	}
}
