package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	. "modernc.org/tk9.0"

	"gameforge/internal/i18n"
	"gameforge/internal/merge"
	"gameforge/internal/titledb"
)

// A database older than this gets a hint to update it.
const dbMaxAge = 14 * 24 * time.Hour

// dbPath is the cache file, next to the configuration file (the program
// folder, or the user configuration folder if that is not writable).
func (u *ui) dbPath() string { return filepath.Join(filepath.Dir(u.cfg.Path()), titledb.FileName) }

// loadDB reads the local game database, if there is one; the network is
// used only when the user asks for it. Its state is shown next to the
// online menu of the Switch tab, not in the log.
func (u *ui) loadDB() {
	db, err := titledb.Load(u.dbPath())
	u.dbErr = nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		u.dbErr = err
	}
	if err == nil {
		u.db = db
		u.refreshList()
	}
	u.showDBStatus()
}

// showDBStatus shows the state of the game database in the Switch tab.
func (u *ui) showDBStatus() {
	if u.dbStatus == nil {
		return
	}
	var text string
	switch {
	case u.db != nil && time.Since(u.db.Downloaded) > dbMaxAge:
		text = T("db.status_old", u.db.DataDate(), int(time.Since(u.db.Downloaded).Hours()/24))
	case u.db != nil:
		text = T("db.status", u.db.DataDate())
	case u.dbErr != nil:
		text = T("db.status_error")
	default:
		text = T("db.status_none")
	}
	u.dbStatus.Configure(Txt(text))
}

// startDownloadDB downloads the game database in the background and then,
// if it succeeded, runs then on the GUI thread.
func (u *ui) startDownloadDB(then func()) {
	if u.busy {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelMerge = cancel
	u.setBusy(true)
	enable(u.cancelBtn, true)
	u.logf("db.download_start")
	old := u.db

	go func() {
		defer cancel()
		db, err := titledb.Download(ctx, old, func(file string, done, total int64) {
			totalText := "?"
			if total > 0 {
				u.setProgress(float64(done) / float64(total))
				totalText = humanSize(total)
			}
			u.setStatus(T("status.downloading", file, humanSize(done), totalText))
		})
		var saveErr error
		if err == nil && ctx.Err() == nil {
			saveErr = db.Save(u.dbPath())
		}
		var result i18n.Message
		switch {
		case ctx.Err() != nil:
			result = i18n.M("log.cancelled")
		case err != nil:
			result = i18n.M("db.download_error", err)
		case saveErr != nil:
			// Still used in this session; the next start reports it missing.
			result = i18n.M("db.save_error", saveErr)
		case old != nil && db.ETags == old.ETags:
			result = i18n.M("db.unchanged", db.DataDate())
		default:
			result = i18n.M("db.updated", db.Games(), db.DataDate())
		}
		u.println(result)
		u.setProgress(0)
		u.setStatus(result.String())
		// Decided here: the deferred cancel runs before the event below.
		ok := err == nil && ctx.Err() == nil
		PostEvent(func() {
			u.flush()
			u.cancelMerge = nil
			enable(u.cancelBtn, false)
			u.setBusy(false)
			if ok {
				u.db, u.dbErr = db, nil
				u.showDBStatus()
				u.refreshList()
				if then != nil {
					then()
				}
			}
		}, false)
	}()
}

// checkOnline compares the listed games with the game database, offering
// to download it first if there is none.
func (u *ui) checkOnline() {
	if u.busy {
		return
	}
	if u.db == nil {
		u.logf("db.not_found", u.dbPath())
		u.flush()
		answer := MessageBox(Parent(App), Icon("question"), Type("yesno"), Title(T("app.title")),
			Msg(T("db.ask_download", u.dbPath())))
		if answer == "yes" {
			u.startDownloadDB(u.reportOnline)
		}
		return
	}
	u.reportOnline()
}

// reportOnline writes what each game lacks, compared with the game
// database, to the log.
func (u *ui) reportOnline() {
	db := u.db
	if len(u.games) == 0 {
		u.logf("online.empty")
		u.flush()
		return
	}
	u.logf("online.start", db.DataDate())
	var newer, missing, outdated, unknown int
	for _, g := range u.games {
		s := g.Online(db)
		name := fmt.Sprintf("%s [%016X]", g.Title(), g.AppID)
		if !s.Known {
			unknown++
			u.logf("online.unknown", name)
			continue
		}
		var have any = i18n.M("online.no_update_file")
		if s.Have > 0 {
			have = fmt.Sprintf("v%d", s.Have)
			if v, t, ok := g.UpdateVersion(); ok && v == s.Have && t != nil {
				have = versionText(t.Control, v)
			}
		}
		switch {
		case s.NewerUpdate():
			newer++
			u.logf("online.update_new", name, s.Latest.Version, s.Latest.Date, have)
		case s.HasUpdates:
			u.logf("online.update_ok", name, have)
		default:
			u.logf("online.no_updates", name)
		}
		for _, id := range s.Missing {
			missing++
			u.logf("online.dlc_missing", id, dlcName(db, id))
		}
		for _, d := range s.Outdated {
			outdated++
			u.logf("online.dlc_old", d.ID, dlcName(db, d.ID), d.Have, d.Latest.Version, d.Latest.Date)
		}
	}
	summary := i18n.M("online.summary", len(u.games), newer, missing, outdated, unknown)
	u.println(summary)
	u.setStatus(summary.String())
	u.flush()
}

func dlcName(db *titledb.DB, id uint64) string {
	if n := db.Name(id); n != "" {
		return n
	}
	return "?"
}

// onlineTag summarizes a game's online status for the file list.
func onlineTag(s merge.OnlineStatus) string {
	if !s.Known {
		return T("online.tag_unknown")
	}
	var issues []string
	if s.NewerUpdate() {
		issues = append(issues, T("online.tag_update", s.Latest.Version, s.Latest.Date))
	}
	if n := len(s.Missing); n > 0 {
		issues = append(issues, T("online.tag_missing", n))
	}
	if n := len(s.Outdated); n > 0 {
		issues = append(issues, T("online.tag_old", n))
	}
	if len(issues) == 0 {
		return T("online.tag_ok")
	}
	return strings.Join(issues, " • ")
}
