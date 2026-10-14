package setup

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memors/tray/internal/installer"
)

func TestUpdatesPage(t *testing.T) {
	h := newHarness(t)
	body := h.enter("/updates")
	for _, want := range []string{"<h1>Updates</h1>", "Not checked yet", "v1.4.0", "memors-mcp 1.4.0 at", "once a day"} {
		if !strings.Contains(body, want) {
			t.Errorf("before any check, page lacks %q", want)
		}
	}
	if strings.Contains(body, "is available") || strings.Contains(body, "Update MemoRS Tray") {
		t.Errorf("updates offered before any check:\n%s", body)
	}
	code, body := h.post("/updates/check", nil)
	if code != 200 {
		t.Fatalf("check: %d", code)
	}
	for _, want := range []string{"<b>v9.9.9</b>", "Checked just now", "release notes", `href="https://github.com/kKEo/memors/releases/tag/v9.9.9"`,
		"v1.4.0 · <b>v9.9.9 is available</b>", `action="/updates/tray"`, "Update MemoRS Tray", "9.9.9 is available", "Update memors-mcp…", "swaps it in"} {
		if !strings.Contains(body, want) {
			t.Errorf("after the check, page lacks %q\n%s", want, body)
		}
	}
	// Up to date: nothing is offered.
	h.rel.latestErr = nil
	h.s.d.TrayVersion = "v9.9.9"
	_, body = h.get(h.s.Origin() + "/updates")
	if !strings.Contains(body, "v9.9.9 · up to date") || strings.Contains(body, "Update MemoRS Tray") {
		t.Errorf("up to date:\n%s", body)
	}
	// A development build is never offered an update.
	h.s.d.TrayVersion = "dev"
	_, body = h.get(h.s.Origin() + "/updates")
	if !strings.Contains(body, "development build") || strings.Contains(body, "Update MemoRS Tray") {
		t.Errorf("dev build:\n%s", body)
	}
	// Where it cannot replace itself, the release page is offered instead.
	h.s.d.TrayVersion = "v1.4.0"
	h.bundleErr = errors.New("cannot update MemoRS Tray in place: macOS runs it from a temporary copy")
	_, body = h.get(h.s.Origin() + "/updates")
	if strings.Contains(body, `action="/updates/tray"`) || !strings.Contains(body, "temporary copy") || !strings.Contains(body, "Download v9.9.9") {
		t.Errorf("not updatable:\n%s", body)
	}
	if code, _ := h.post("/updates/tray", nil); code != 200 || len(h.rel.bundles) != 0 {
		t.Errorf("update started although not updatable: %d %v", code, h.rel.bundles)
	}
	// A release without the tray archive yet.
	h.bundleErr = nil
	h.rel.noTray = true
	h.post("/updates/check", nil)
	_, body = h.get(h.s.Origin() + "/updates")
	if strings.Contains(body, `action="/updates/tray"`) || !strings.Contains(body, "no MemoRS Tray archive yet") {
		t.Errorf("no tray asset:\n%s", body)
	}
	// A failed check keeps the last answer and says so.
	h.rel.noTray = false
	h.rel.latestErr = errors.New("offline")
	_, body = h.post("/updates/check", nil)
	if !strings.Contains(body, "Could not look up the latest release: offline") || !strings.Contains(body, "Showing the last answer") || !strings.Contains(body, "<b>v9.9.9</b>") {
		t.Errorf("failed check:\n%s", body)
	}
}

func TestUpdateTray(t *testing.T) {
	h := newHarness(t)
	h.rel.gate = make(chan struct{})
	h.enter("/updates?check=1")
	code, body := h.post("/updates/tray", nil)
	if code != 200 || !strings.Contains(body, "Updating MemoRS Tray to v9.9.9") || !strings.Contains(body, `<progress value="25"`) || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("progress page: %d\n%s", code, body)
	}
	if !slices.Equal(h.rel.bundles, []string{"/Applications/MemoRS Tray.app"}) {
		t.Errorf("InstallTray bundles = %v", h.rel.bundles)
	}
	// Meanwhile the Updates page sends you to the progress.
	if _, body := h.get(h.s.Origin() + "/updates"); !strings.Contains(body, "Updating MemoRS Tray") {
		t.Errorf("updates page while running:\n%s", body)
	}
	// A second start does not run twice.
	h.post("/updates/tray", nil)
	if len(h.rel.bundles) != 1 {
		t.Errorf("started twice: %v", h.rel.bundles)
	}
	close(h.rel.gate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, body = h.get(h.s.Origin() + "/updates/progress")
		if strings.Contains(body, "v9.9.9 is installed") || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "v9.9.9 is installed") || !strings.Contains(body, "relaunches in a moment") {
		t.Fatalf("done page:\n%s", body)
	}
	if !slices.Equal(h.kinds(), []EventKind{TrayUpdated}) {
		t.Errorf("events = %v", h.kinds())
	}
}

func TestUpdateTrayFailure(t *testing.T) {
	h := newHarness(t)
	h.rel.trayErr = installer.ErrChecksum
	h.enter("/updates?check=1")
	h.post("/updates/tray", nil)
	deadline := time.Now().Add(5 * time.Second)
	var body string
	for {
		_, body = h.get(h.s.Origin() + "/updates/progress")
		if strings.Contains(body, "did not finish") || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "did not finish") || !strings.Contains(body, "does not match the release") || !strings.Contains(body, "MemoRS Tray stays as it was") {
		t.Fatalf("failure page:\n%s", body)
	}
	if len(h.kinds()) != 0 {
		t.Errorf("events after a failure: %v", h.kinds())
	}
}

func TestSettingsCheckUpdates(t *testing.T) {
	h := newHarness(t)
	body := h.enter("/settings")
	if !strings.Contains(body, `name="check_updates" checked`) || !strings.Contains(body, "MemoRS Tray v1.4.0") || !strings.Contains(body, `href="/updates"`) {
		t.Fatalf("settings page:\n%s", body)
	}
	h.post("/settings", url.Values{"rev": {rev(t, body)}, "login": {"on"}, "stop_on_quit": {"on"}, "memors_mode": {"auto"}, "servers": {"0"}})
	if h.cfg().ChecksUpdates() {
		t.Error("unchecking the box did not turn the daily check off")
	}
	if _, body := h.get(h.s.Origin() + "/updates"); !strings.Contains(body, "newest release when you ask") {
		t.Errorf("updates page with the daily check off:\n%s", body)
	}
	body = h.enter("/settings")
	h.post("/settings", url.Values{"rev": {rev(t, body)}, "check_updates": {"on"}, "stop_on_quit": {"on"}, "memors_mode": {"auto"}, "servers": {"0"}})
	if !h.cfg().ChecksUpdates() {
		t.Error("checking the box did not turn the daily check on")
	}
}
