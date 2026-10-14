// Package update knows whether a newer release of MemoRS Tray or memors-mcp
// exists: it asks GitHub for the newest release (once a day, or when
// asked), remembers the answer, and compares versions.
package update

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kKEo/memors/tray/internal/installer"
)

// Every is how often the background check runs; FirstAfter is how long
// after launch the first one runs.
const (
	Every      = 24 * time.Hour
	FirstAfter = time.Minute
	lookupWait = 15 * time.Second
)

// Releases finds releases (*installer.Client).
type Releases interface {
	Latest(ctx context.Context) (installer.Release, error)
}

// Result is the last check.
type Result struct {
	At      time.Time // zero: never checked
	Release installer.Release
	Err     error
}

// Checker caches the newest release.
type Checker struct {
	Releases Releases
	Now      func() time.Time
	Logf     func(format string, args ...any)

	mu      sync.Mutex
	last    Result
	started time.Time
	running bool
}

// Due reports whether the background check should run now: a minute after
// the first call, then every Every (a failed check is retried on the next
// tick after Every too, so a flaky network does not hammer GitHub).
func (c *Checker) Due() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Now()
	if c.started.IsZero() {
		c.started = now
	}
	if c.running {
		return false
	}
	if c.last.At.IsZero() {
		return now.Sub(c.started) >= FirstAfter
	}
	return now.Sub(c.last.At) >= Every
}

// Check asks GitHub and records the answer. Concurrent calls share one
// lookup.
func (c *Checker) Check(ctx context.Context) Result {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return c.wait()
	}
	c.running = true
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, lookupWait)
	defer cancel()
	rel, err := c.Releases.Latest(ctx)
	r := Result{At: c.Now(), Release: rel, Err: err}
	if err != nil && c.Logf != nil {
		c.Logf("update check: %v", err)
	}
	c.mu.Lock()
	if err != nil && c.last.Release.Tag != "" {
		r.Release = c.last.Release // keep the last good answer
	}
	c.last, c.running = r, false
	c.mu.Unlock()
	return r
}

// wait spins until the check in flight finishes.
func (c *Checker) wait() Result {
	for {
		time.Sleep(50 * time.Millisecond)
		c.mu.Lock()
		running, last := c.running, c.last
		c.mu.Unlock()
		if !running {
			return last
		}
	}
}

// Last is the last check's result; At is zero when there was none.
func (c *Checker) Last() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Newer reports whether latest is a newer release than installed. False
// when either is not a version (a development build, an unknown
// memors-mcp): nothing is offered then.
func Newer(installed, latest string) bool {
	c, ok := Compare(installed, latest)
	return ok && c < 0
}

// Compare orders two versions (v1.2.3, 1.2.3, 1.2.3-rc1; a pre-release is
// older than its release). ok is false when either is not a version.
func Compare(a, b string) (int, bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range 3 {
		if pa.n[i] != pb.n[i] {
			if pa.n[i] < pb.n[i] {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0, true
	case pa.pre == "":
		return 1, true
	case pb.pre == "":
		return -1, true
	}
	return strings.Compare(pa.pre, pb.pre), true
}

type version struct {
	n   [3]int
	pre string
}

func parse(s string) (version, bool) {
	var v version
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}
