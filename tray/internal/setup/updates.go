package setup

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/kKEo/memors/tray/internal/installer"
	"github.com/kKEo/memors/tray/internal/update"
)

// The Updates page shows the installed and newest versions of MemoRS Tray
// and memors-mcp, checks again on request, and updates MemoRS Tray in
// place. memors-mcp is updated by the assistant's Install step.

// trayJob is one MemoRS Tray update, run in the background while the
// progress page reloads itself.
type trayJob struct {
	release installer.Release

	mu       sync.Mutex
	progress installer.Progress
	done     bool
	err      error
}

type trayJobView struct {
	Release installer.Release
	installer.Progress
	Running bool
	Err     string
}

func (j *trayJob) view() trayJobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := trayJobView{Release: j.release, Progress: j.progress, Running: !j.done}
	if j.err != nil {
		v.Err = j.err.Error()
	}
	return v
}

type updatesData struct {
	CheckedAt time.Time
	Err       string // the last check failed
	Release   *installer.Release

	TrayVersion   string
	TrayDev       bool // not a release build: nothing to compare
	TrayNewer     bool
	TrayHasAsset  bool
	Updatable     bool
	NotUpdatable  string // why not, when it is not
	MemorsOK      bool
	MemorsStatus  string
	MemorsVersion string
	MemorsNewer   bool
	Automatic     bool // the daily check is on
}

func (s *Server) updatesPage(w http.ResponseWriter, r *http.Request) {
	if j := s.currentTrayJob(); j != nil && j.view().Running {
		http.Redirect(w, r, "/updates/progress", http.StatusSeeOther)
		return
	}
	s.render(w, "updates.html", page{Title: "Updates", Data: s.updates(r.Context(), r.URL.Query().Get("check") == "1")})
}

// updates gathers the page; check asks GitHub again first.
func (s *Server) updates(ctx context.Context, check bool) updatesData {
	d := updatesData{TrayVersion: s.d.TrayVersion}
	if _, ok := update.Compare(d.TrayVersion, "0.0.0"); !ok {
		d.TrayDev = true
	}
	cfg, _, _ := s.loadConfig()
	d.Automatic = cfg.ChecksUpdates()
	bin, version, err := s.memors(ctx, cfg)
	d.MemorsOK, d.MemorsStatus, d.MemorsVersion = err == nil, memorsLine(bin, version, err), version
	if s.d.AppBundle != nil {
		if _, err := s.d.AppBundle(); err != nil {
			d.NotUpdatable = err.Error()
		} else {
			d.Updatable = true
		}
	}
	if s.d.Updates == nil {
		return d
	}
	var res update.Result
	if check {
		res = s.d.Updates.Check(ctx)
	} else {
		res = s.d.Updates.Last()
	}
	d.CheckedAt = res.At
	if res.Err != nil {
		d.Err = res.Err.Error()
	}
	if res.Release.Tag != "" {
		rel := res.Release
		d.Release = &rel
		d.TrayNewer = update.Newer(d.TrayVersion, rel.Tag)
		_, d.TrayHasAsset = rel.Asset(rel.TrayZipName())
		d.MemorsNewer = d.MemorsOK && update.Newer(version, rel.Tag)
	}
	return d
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/updates?check=1", http.StatusSeeOther)
}

// startTrayUpdate downloads and swaps in the newest MemoRS Tray.
func (s *Server) startTrayUpdate(w http.ResponseWriter, r *http.Request) {
	if s.d.Updates == nil || s.d.AppBundle == nil {
		http.Redirect(w, r, "/updates", http.StatusSeeOther)
		return
	}
	res := s.d.Updates.Last()
	if res.Release.Tag == "" {
		res = s.d.Updates.Check(r.Context())
	}
	if res.Release.Tag == "" || !update.Newer(s.d.TrayVersion, res.Release.Tag) {
		http.Redirect(w, r, "/updates", http.StatusSeeOther)
		return
	}
	bundle, err := s.d.AppBundle()
	if err != nil {
		http.Redirect(w, r, "/updates", http.StatusSeeOther)
		return
	}
	s.mu.Lock()
	if s.trayJob != nil && !s.trayJob.done {
		s.mu.Unlock()
		http.Redirect(w, r, "/updates/progress", http.StatusSeeOther)
		return
	}
	j := &trayJob{release: res.Release, progress: installer.Progress{Phase: installer.Downloading}}
	s.trayJob = j
	s.mu.Unlock()
	go s.runTrayUpdate(j, bundle)
	http.Redirect(w, r, "/updates/progress", http.StatusSeeOther)
}

func (s *Server) runTrayUpdate(j *trayJob, bundle string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	err := s.d.Releases.InstallTray(ctx, j.release, bundle, func(p installer.Progress) {
		j.mu.Lock()
		j.progress = p
		j.mu.Unlock()
	})
	j.mu.Lock()
	j.done, j.err = true, err
	j.mu.Unlock()
	if err != nil {
		s.d.Logf("update MemoRS Tray to %s: %v", j.release.Tag, err)
		return
	}
	s.d.Logf("installed MemoRS Tray %s at %s; relaunching", j.release.Tag, bundle)
	s.d.Notify(Event{Kind: TrayUpdated})
}

func (s *Server) currentTrayJob() *trayJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trayJob
}

func (s *Server) trayProgress(w http.ResponseWriter, r *http.Request) {
	j := s.currentTrayJob()
	if j == nil {
		http.Redirect(w, r, "/updates", http.StatusSeeOther)
		return
	}
	v := j.view()
	refresh := 0
	if v.Running {
		refresh = 1
	}
	s.render(w, "updates_progress.html", page{Title: "Updating MemoRS Tray", Refresh: refresh, Data: v})
}

// latestRelease is the newest release as the checker knows it, asked for
// again when the answer is older than releaseFor or refresh is set.
func (s *Server) latestRelease(ctx context.Context, refresh bool) (installer.Release, error) {
	if s.d.Updates == nil {
		return installer.Release{}, errors.New("no release lookup")
	}
	res := s.d.Updates.Last()
	if refresh || res.At.IsZero() || time.Since(res.At) >= releaseFor || (res.Err != nil && res.Release.Tag == "") {
		res = s.d.Updates.Check(ctx)
	}
	if res.Release.Tag == "" {
		return installer.Release{}, res.Err
	}
	return res.Release, nil
}
