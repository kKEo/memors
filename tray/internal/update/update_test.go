package update

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kKEo/memors/tray/internal/installer"
)

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1.2.3", "1.2.3", 0, true},
		{"v1.2.3", "v1.2.4", -1, true},
		{"v1.10.0", "v1.9.9", 1, true},
		{"2.0.0", "v1.99.99", 1, true},
		{"v1.2.3-rc1", "v1.2.3", -1, true},
		{"v1.2.3-rc1", "v1.2.3-rc2", -1, true},
		{"v1.2.3+build", "v1.2.3", 0, true},
		{"dev", "v1.2.3", 0, false},
		{"memory-find-1-g3e24c3c-dirty", "v1.2.3", 0, false},
		{"v1.2", "v1.2.3", 0, false},
		{"v1.02.3", "v1.2.3", 0, false},
		{"", "v1.2.3", 0, false},
	} {
		got, ok := Compare(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d, %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
	if !Newer("v1.0.0", "v1.1.0") || Newer("v1.1.0", "v1.1.0") || Newer("dev", "v9.9.9") || Newer("1.1.0", "v1.0.0") {
		t.Error("Newer is wrong")
	}
}

type fakeReleases struct {
	calls int
	err   error
}

func (f *fakeReleases) Latest(context.Context) (installer.Release, error) {
	f.calls++
	if f.err != nil {
		return installer.Release{}, f.err
	}
	return installer.Release{Tag: "v9.9.9"}, nil
}

func TestCheckerSchedule(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rel := &fakeReleases{}
	c := &Checker{Releases: rel, Now: func() time.Time { return now }}
	if c.Due() {
		t.Error("due at launch")
	}
	now = now.Add(FirstAfter)
	if !c.Due() {
		t.Error("not due a minute after launch")
	}
	if r := c.Check(context.Background()); r.Err != nil || r.Release.Tag != "v9.9.9" || !r.At.Equal(now) {
		t.Errorf("first check: %+v", r)
	}
	if c.Due() {
		t.Error("due right after a check")
	}
	now = now.Add(Every - time.Second)
	if c.Due() {
		t.Error("due before a day passed")
	}
	now = now.Add(time.Second)
	if !c.Due() {
		t.Error("not due after a day")
	}
	// A failed check keeps the last good release and is retried a day later.
	rel.err = errors.New("offline")
	if r := c.Check(context.Background()); r.Err == nil || r.Release.Tag != "v9.9.9" {
		t.Errorf("failed check: %+v", r)
	}
	if c.Due() {
		t.Error("retrying at once after a failure")
	}
	if r := c.Last(); r.Err == nil || r.Release.Tag != "v9.9.9" || rel.calls != 2 {
		t.Errorf("Last = %+v, calls %d", r, rel.calls)
	}
}
