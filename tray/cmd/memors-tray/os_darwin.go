//go:build darwin

package main

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// lockSingle keeps a second memors-tray from starting: it holds an exclusive
// lock on path for the life of the process.
func lockSingle(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("already running")
		}
		return nil, err
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}

// loginCode matches the single-use code in a login link, which must never
// reach a log.
var loginCode = regexp.MustCompile(`code=[^&\s"]+`)

// openLog returns a logger writing to path (0600, rotated above 5 MB), or
// to stderr when the file cannot be opened.
func openLog(path string) func(format string, args ...any) {
	l := log.New(os.Stderr, "", log.LstdFlags)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
			_ = os.Rename(path, path+".1")
		}
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			l = log.New(f, "", log.LstdFlags)
		}
	}
	return func(format string, args ...any) {
		l.Print(loginCode.ReplaceAllString(fmt.Sprintf(format, args...), "code=REDACTED"))
	}
}

// openURL opens an http(s) URL in the default browser.
func openURL(u string) error {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return fmt.Errorf("not a web address: %q", u)
	}
	return exec.Command("/usr/bin/open", u).Run()
}

// openFile shows a log in Console, a text file in the default editor, or a
// folder in Finder.
func openFile(path, how string) error {
	switch how {
	case "log":
		if _, err := os.Stat(path); err != nil {
			return errors.New("no log yet")
		}
		if err := exec.Command("/usr/bin/open", "-a", "Console", path).Run(); err == nil {
			return nil
		}
		return exec.Command("/usr/bin/open", "-t", path).Run()
	case "text":
		return exec.Command("/usr/bin/open", "-t", path).Run()
	default:
		return exec.Command("/usr/bin/open", path).Run()
	}
}

// relaunch opens the app at bundle once this process has exited: a
// detached shell waits for the pid to go away, so the single-instance lock
// is free by then.
func relaunch(bundle string) error {
	quoted := "'" + strings.ReplaceAll(bundle, "'", `'\''`) + "'"
	script := fmt.Sprintf("while kill -0 %d 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open %s", os.Getpid(), quoted)
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap it if it exits before we do
	return nil
}

// copyText puts text on the clipboard.
func copyText(text string) error {
	cmd := exec.Command("/usr/bin/pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
