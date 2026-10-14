package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MemoRS Tray updates itself the same way it installs memors-mcp: the
// release's zip is downloaded and checked against the .sha256 published
// next to it, unpacked beside the installed app, tried out, and only then
// swapped in. The running process keeps working from the renamed old
// bundle until it relaunches.

// ErrNotUpdatable means MemoRS Tray cannot replace itself where it runs
// from; the message says why.
var ErrNotUpdatable = errors.New("cannot update MemoRS Tray in place")

// TrayZipName is the MemoRS Tray archive attached to a release.
func (r Release) TrayZipName() string {
	return fmt.Sprintf("memors-tray_%s_darwin_universal.zip", r.Version())
}

// AppBundle is the MemoRS Tray.app that exe (this process's executable)
// runs from, when it can be replaced in place: a .app bundle outside App
// Translocation, in a folder this user can write to.
func AppBundle(exe string) (string, error) {
	exe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	// <bundle>.app/Contents/MacOS/<exe>
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if filepath.Base(filepath.Dir(exe)) != "MacOS" || filepath.Ext(bundle) != ".app" {
		return "", fmt.Errorf("%w: it is not running from an app bundle (%s)", ErrNotUpdatable, exe)
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		return "", fmt.Errorf("%w: macOS runs it from a temporary copy; move MemoRS Tray.app to Applications and open it from there", ErrNotUpdatable)
	}
	probe, err := os.CreateTemp(filepath.Dir(bundle), ".memors-tray-probe-*")
	if err != nil {
		return "", fmt.Errorf("%w: %s is not writable", ErrNotUpdatable, filepath.Dir(bundle))
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return bundle, nil
}

// InstallTray downloads rel's MemoRS Tray zip, checks it against its
// .sha256, unpacks it next to bundle, makes sure the new app runs, and
// swaps it in at bundle's path. The process keeps running from the old
// bundle (renamed) and must relaunch to run the new version.
func (c *Client) InstallTray(ctx context.Context, rel Release, bundle string, progress func(Progress)) error {
	if progress == nil {
		progress = func(Progress) {}
	}
	zipAsset, ok := rel.Asset(rel.TrayZipName())
	if !ok {
		return fmt.Errorf("release %s has no MemoRS Tray archive (%s)", rel.Tag, rel.TrayZipName())
	}
	sums, ok := rel.Asset(rel.TrayZipName() + ".sha256")
	if !ok {
		return fmt.Errorf("release %s has no %s.sha256; refusing to install unverified files", rel.Tag, rel.TrayZipName())
	}
	want, err := c.checksum(ctx, sums, zipAsset.Name)
	if err != nil {
		return err
	}

	parent := filepath.Dir(bundle)
	work, err := os.MkdirTemp(parent, ".memors-tray-update-*")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotUpdatable, err)
	}
	defer os.RemoveAll(work)
	zipPath := filepath.Join(work, zipAsset.Name)
	got, err := c.download(ctx, zipAsset, zipPath, progress)
	if err != nil {
		return err
	}
	progress(Progress{Phase: Verifying})
	if got != want {
		return ErrChecksum
	}

	progress(Progress{Phase: Installing})
	fresh, err := unzipApp(ctx, zipPath, work)
	if err != nil {
		return err
	}
	if err := trayRuns(ctx, fresh); err != nil {
		return fmt.Errorf("the downloaded MemoRS Tray does not run on this Mac: %w", err)
	}
	old := filepath.Join(parent, "."+filepath.Base(bundle)+".old")
	_ = os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		return fmt.Errorf("%w: %v", ErrNotUpdatable, err)
	}
	if err := os.Rename(fresh, bundle); err != nil {
		_ = os.Rename(old, bundle) // put the old one back
		return err
	}
	_ = os.RemoveAll(old) // the next update removes it if the running process still holds it
	return nil
}

// unzipApp unpacks the one .app in the zip into dir with ditto, which
// keeps the bundle's structure and attributes, and returns its path.
func unzipApp(ctx context.Context, zipPath, dir string) (string, error) {
	out := filepath.Join(dir, "unpacked")
	cmd := exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", zipPath, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("unpacking the archive: %v: %s", err, strings.TrimSpace(string(b)))
	}
	des, err := os.ReadDir(out)
	if err != nil {
		return "", err
	}
	app := ""
	for _, de := range des {
		if de.IsDir() && filepath.Ext(de.Name()) == ".app" {
			if app != "" {
				return "", errors.New("the archive holds more than one app")
			}
			app = filepath.Join(out, de.Name())
		}
	}
	if app == "" {
		return "", errors.New("the archive holds no app")
	}
	return app, nil
}

// trayRuns makes sure the unpacked app starts: `memors-tray -version` must
// report a version.
func trayRuns(ctx context.Context, app string) error {
	ctx, cancel := context.WithTimeout(ctx, versionWait)
	defer cancel()
	exe := filepath.Join(app, "Contents", "MacOS", "memors-tray")
	out, err := exec.CommandContext(ctx, exe, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.HasPrefix(string(out), "memors-tray ") {
		return errors.New("it does not report a memors-tray version")
	}
	return nil
}
