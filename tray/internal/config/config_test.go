package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.json")
	c, err := Load(path)
	if err != nil || len(c.Servers) != 0 || !c.StopsOnQuit() {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	no := false
	c.Servers = []Server{{KB: "crportal", Addr: "127.0.0.1:8765", Auth: "token", Autostart: true, Env: map[string]string{"MEMORS_MODEL": "potion"}}}
	c.StopOnQuit = &no
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o, want 600", fi.Mode().Perm())
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := back.Find("crportal"); s == nil || s.Addr != "127.0.0.1:8765" || s.Auth != "token" || !s.Autostart || s.Env["MEMORS_MODEL"] != "potion" || back.StopsOnQuit() {
		t.Errorf("round trip = %+v", back)
	}
	if err := os.WriteFile(path, []byte(`{"servers":[{"kb":"x","addr":"0.0.0.0:1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("invalid file loaded: %v", err)
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"ok", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:8765"}, {KB: "b", Addr: "[::1]:8766", Auth: "token"}, {KB: "c", Addr: "localhost:8767", Auth: "none"}}}, ""},
		{"bad name", Config{Servers: []Server{{KB: "a/b", Addr: "127.0.0.1:1"}}}, "knowledge-base name"},
		{"twice", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:1"}, {KB: "a", Addr: "127.0.0.1:2"}}}, "twice"},
		{"same addr", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:1"}, {KB: "b", Addr: "127.0.0.1:1"}}}, "used twice"},
		{"remote", Config{Servers: []Server{{KB: "a", Addr: "192.168.1.2:1"}}}, "loopback"},
		{"no port", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1"}}}, "addr"},
		{"bad port", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:99999"}}}, "bad port"},
		{"bad auth", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:1", Auth: "basic"}}}, "none or token"},
		{"reserved env", Config{Env: map[string]string{"MEMORS_HTTP_AUTH": "none"}}, "set by MemoRS Tray"},
		{"reserved server env", Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:1", Env: map[string]string{"MEMORS_KB": "b"}}}}, "set by MemoRS Tray"},
		{"bad env name", Config{Env: map[string]string{"A=B": "x"}}, "variable name"},
		{"ask ok", Config{Ask: &Ask{Auth: "bedrock", Model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", Effort: "high", MaxTurns: 100, TimeoutSeconds: 10, MaxBudgetUSD: 2, Env: map[string]string{"AWS_PROFILE": "work"}}}, ""},
		{"ask model with a context suffix", Config{Ask: &Ask{Model: "sonnet[1m]"}}, ""},
		{"ask auth", Config{Ask: &Ask{Auth: "password"}}, "auth must be one of"},
		{"ask model is a flag", Config{Ask: &Ask{Model: "--settings"}}, "not a model name"},
		{"ask model with a space", Config{Ask: &Ask{Model: "sonnet --x"}}, "not a model name"},
		{"ask effort", Config{Ask: &Ask{Effort: "extreme"}}, "effort must be one of"},
		{"ask turns", Config{Ask: &Ask{MaxTurns: 101}}, "max_turns"},
		{"ask negative turns", Config{Ask: &Ask{MaxTurns: -1}}, "max_turns"},
		{"ask timeout", Config{Ask: &Ask{TimeoutSeconds: 5}}, "timeout_seconds"},
		{"ask budget", Config{Ask: &Ask{MaxBudgetUSD: -1}}, "max_budget_usd"},
		{"ask instructions", Config{Ask: &Ask{Instructions: strings.Repeat("x", MaxAskInstructions+1)}}, "instructions"},
		{"ask env name", Config{Ask: &Ask{Env: map[string]string{"A B": "x"}}}, "variable name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
				t.Errorf("Validate = %v, want %q", err, c.want)
			}
		})
	}
}

func TestAddrFor(t *testing.T) {
	c := &Config{Servers: []Server{{KB: "a", Addr: "127.0.0.1:8765"}}}
	if addr, added, err := c.AddrFor("a", func(string) bool { return false }); err != nil || added || addr != "127.0.0.1:8765" {
		t.Errorf("configured: %q %v %v", addr, added, err)
	}
	busy := map[string]bool{"127.0.0.1:8766": true}
	addr, added, err := c.AddrFor("b", func(a string) bool { return !busy[a] })
	if err != nil || !added || addr != "127.0.0.1:8767" || c.Find("b") == nil || c.Find("b").Addr != addr {
		t.Errorf("assigned: %q %v %v %+v", addr, added, err, c.Servers)
	}
	if _, _, err := c.AddrFor("c", func(string) bool { return false }); err == nil {
		t.Error("no free port should be an error")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("assigned config invalid: %v", err)
	}
}

// Writers in different goroutines never lose each other's changes.
func TestUpdateIsSerialised(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.json")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Update(path, func(c *Config) error {
				c.Servers = append(c.Servers, Server{KB: fmt.Sprintf("kb%d", i), Addr: fmt.Sprintf("127.0.0.1:%d", 9000+i)})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c, err := Load(path); err != nil || len(c.Servers) != 20 {
		t.Errorf("after 20 concurrent updates: %d servers, %v", len(c.Servers), err)
	}
}

func TestUpdateAndSaveIf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(path, func(c *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("a broken file was not replaced: %v", err)
	}
	rev := Revision(path)
	if _, err := Update(path, func(c *Config) error { return nil }); err != nil || Revision(path) != rev {
		t.Errorf("a no-op update rewrote the file (%v)", err)
	}
	if _, err := Update(path, func(c *Config) error { c.Servers = []Server{{KB: "x", Addr: "8.8.8.8:1"}}; return nil }); err == nil {
		t.Error("an invalid result was saved")
	}
	if Revision(path) != rev {
		t.Error("a refused update touched the file")
	}
	if ok, err := SaveIf(path, &Config{Debug: true}, "stale"); ok || err != nil {
		t.Errorf("SaveIf with a stale revision: %v %v", ok, err)
	}
	if ok, err := SaveIf(path, &Config{Debug: true}, rev); !ok || err != nil {
		t.Errorf("SaveIf with the current revision: %v %v", ok, err)
	}
	if Revision(filepath.Join(t.TempDir(), "none")) != "none" {
		t.Error("Revision of a missing file")
	}
}
