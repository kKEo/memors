package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const goodTray = "#!/bin/sh\necho 'memors-tray v9.9.9'\n"

// appZip is a zip holding MemoRS Tray.app with exe as its executable.
func appZip(t *testing.T, exe string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string, mode os.FileMode) {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("MemoRS Tray.app/Contents/Info.plist", "<plist/>", 0o644)
	add("MemoRS Tray.app/Contents/MacOS/memors-tray", exe, 0o755)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func trayFake(t *testing.T, zipBytes []byte) *fakeGitHub {
	t.Helper()
	f := newFake(t, tgz(t, entry{"memors-mcp", goodBinary, 0}))
	f.trayZip = zipBytes
	sum := sha256.Sum256(zipBytes)
	f.traySum = hex.EncodeToString(sum[:]) + "  memors-tray_9.9.9_darwin_universal.zip\n"
	return f
}

// installedApp is an old MemoRS Tray.app in its own Applications folder.
func installedApp(t *testing.T) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), "Applications", "MemoRS Tray.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "MacOS", "memors-tray"), []byte("#!/bin/sh\necho 'memors-tray v1.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestInstallTray(t *testing.T) {
	f := trayFake(t, appZip(t, goodTray))
	bundle := installedApp(t)
	c := f.client()
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var phases []Phase
	err = c.InstallTray(context.Background(), rel, bundle, func(p Progress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(phases, []Phase{Downloading, Verifying, Installing}) {
		t.Errorf("phases %v", phases)
	}
	b, err := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "memors-tray"))
	if err != nil || string(b) != goodTray {
		t.Errorf("new app not in place: %q %v", b, err)
	}
	des, _ := os.ReadDir(filepath.Dir(bundle))
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	if !slices.Equal(names, []string{"MemoRS Tray.app"}) {
		t.Errorf("Applications holds %v, want only the app", names)
	}
}

func TestInstallTrayRefusesBadDownloads(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(f *fakeGitHub)
		want  string
	}{
		{"wrong checksum", func(f *fakeGitHub) {
			f.traySum = strings.Repeat("0", 64) + "  memors-tray_9.9.9_darwin_universal.zip\n"
		}, ErrChecksum.Error()},
		{"no checksum", func(f *fakeGitHub) { f.traySum = "" }, "refusing to install unverified files"},
		{"does not run", func(f *fakeGitHub) { f.trayZip = appZip(t, "#!/bin/sh\necho nope\n"); f.traySum = "" }, "does not run on this Mac"},
		{"no app", func(f *fakeGitHub) { f.trayZip = []byte("not a zip"); f.traySum = "" }, "unpacking the archive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := trayFake(t, appZip(t, goodTray))
			c.setup(f)
			if f.traySum == "" { // recompute for a replaced archive, drop for the "no checksum" case
				if c.name != "no checksum" {
					sum := sha256.Sum256(f.trayZip)
					f.traySum = hex.EncodeToString(sum[:]) + "  memors-tray_9.9.9_darwin_universal.zip\n"
				}
			}
			bundle := installedApp(t)
			cl := f.client()
			rel, err := cl.Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = cl.InstallTray(context.Background(), rel, bundle, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if b, _ := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "memors-tray")); !strings.Contains(string(b), "v1.0.0") {
				t.Errorf("the old app was touched: %q", b)
			}
			des, _ := os.ReadDir(filepath.Dir(bundle))
			if len(des) != 1 {
				t.Errorf("leftovers next to the app: %v", des)
			}
		})
	}
}

func TestAppBundle(t *testing.T) {
	bundle := installedApp(t)
	bundle, _ = filepath.EvalSymlinks(bundle) // /var is /private/var
	exe := filepath.Join(bundle, "Contents", "MacOS", "memors-tray")
	if got, err := AppBundle(exe); err != nil || got != bundle {
		t.Errorf("AppBundle = %q, %v", got, err)
	}
	loose := filepath.Join(t.TempDir(), "memors-tray")
	if err := os.WriteFile(loose, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AppBundle(loose); !errors.Is(err, ErrNotUpdatable) || !strings.Contains(err.Error(), "not running from an app bundle") {
		t.Errorf("loose binary: %v", err)
	}
	trans := filepath.Join(t.TempDir(), "AppTranslocation", "x", "d", "MemoRS Tray.app", "Contents", "MacOS")
	if err := os.MkdirAll(trans, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trans, "memors-tray"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AppBundle(filepath.Join(trans, "memors-tray")); !errors.Is(err, ErrNotUpdatable) || !strings.Contains(err.Error(), "temporary copy") {
		t.Errorf("translocated: %v", err)
	}
	if err := os.Chmod(filepath.Dir(bundle), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(bundle), 0o755) })
	if _, err := AppBundle(exe); !errors.Is(err, ErrNotUpdatable) || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("read-only folder: %v", err)
	}
}
