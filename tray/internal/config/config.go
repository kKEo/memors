// Package config is memors-tray's settings file, <MEMORS_HOME>/tray.json: the
// servers it starts (knowledge base, address, auth), extra environment for
// them, and where memors-mcp is installed.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/kKEo/memors/tray/internal/home"
)

// Config is tray.json.
type Config struct {
	// MemorsBinary is the memors-mcp executable; empty means search for it.
	MemorsBinary string `json:"memors_binary,omitempty"`
	// Env is extra environment for every server memors-tray starts. Apps
	// started from Finder do not see your shell's variables, so settings
	// such as MEMORS_MODEL go here.
	Env     map[string]string `json:"env,omitempty"`
	Servers []Server          `json:"servers,omitempty"`
	// StopOnQuit stops the servers memors-tray started when it quits
	// (default true).
	StopOnQuit *bool `json:"stop_on_quit,omitempty"`
	// CheckUpdates asks GitHub once a day whether a newer MemoRS Tray or
	// memors-mcp exists (default true).
	CheckUpdates *bool `json:"check_updates,omitempty"`
	// Debug enables the web inspector in stats windows.
	Debug bool `json:"debug,omitempty"`
	// ClaudeBinary is the Claude Code executable the Ask window runs;
	// empty means search for it.
	ClaudeBinary string `json:"claude_binary,omitempty"`
	// Ask is how the Ask window runs Claude Code; nil means the defaults.
	Ask *Ask `json:"ask,omitempty"`
}

// Ask is how the Ask window runs Claude Code: how it signs in, which
// model answers, and the limits on one question. The zero value is
// Claude Code as it is logged in, with its default model.
type Ask struct {
	// Auth is how Claude Code signs in: empty (its own login, or whatever
	// credentials MemoRS Tray's environment holds), api_key, oauth_token,
	// bedrock, vertex or foundry. The key or token itself is not kept
	// here but in a file of its own under MEMORS_HOME.
	Auth string `json:"auth,omitempty"`
	// Model is claude's --model: an alias (sonnet, opus, haiku) or a full
	// model name; empty means Claude Code's default.
	Model string `json:"model,omitempty"`
	// Effort is claude's --effort; empty means the model's default.
	Effort string `json:"effort,omitempty"`
	// MaxTurns bounds the steps of one answer (default DefaultAskTurns).
	MaxTurns int `json:"max_turns,omitempty"`
	// TimeoutSeconds bounds one question (default DefaultAskTimeout).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// MaxBudgetUSD stops one question at this cost; 0 means no limit.
	MaxBudgetUSD float64 `json:"max_budget_usd,omitempty"`
	// Instructions are added to the end of the system prompt.
	Instructions string `json:"instructions,omitempty"`
	// Env is extra environment for Claude Code (AWS_PROFILE,
	// ANTHROPIC_BASE_URL, ...); it wins over what Auth sets.
	Env map[string]string `json:"env,omitempty"`
}

// The Ask defaults and bounds.
const (
	DefaultAskTurns    = 12
	MaxAskTurns        = 100
	DefaultAskTimeout  = 180 // seconds
	MinAskTimeout      = 10
	MaxAskTimeout      = 1800
	MaxAskInstructions = 4000 // bytes
)

// AskAuths are the values of Ask.Auth besides empty.
var AskAuths = []string{"api_key", "oauth_token", "bedrock", "vertex", "foundry"}

// AskEfforts are the values of Ask.Effort besides empty.
var AskEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// AskSettings is c.Ask, never nil.
func (c *Config) AskSettings() Ask {
	if c.Ask == nil {
		return Ask{}
	}
	return *c.Ask
}

// IsZero reports whether a is all defaults (and so left out of tray.json).
func (a Ask) IsZero() bool {
	return a.Auth == "" && a.Model == "" && a.Effort == "" && a.MaxTurns == 0 && a.TimeoutSeconds == 0 &&
		a.MaxBudgetUSD == 0 && a.Instructions == "" && len(a.Env) == 0
}

// Turns is the step limit of one answer.
func (a Ask) Turns() int {
	if a.MaxTurns == 0 {
		return DefaultAskTurns
	}
	return a.MaxTurns
}

// Timeout is the time limit of one question, in seconds.
func (a Ask) Timeout() int {
	if a.TimeoutSeconds == 0 {
		return DefaultAskTimeout
	}
	return a.TimeoutSeconds
}

var askModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]-]*$`)

// Validate checks the Ask settings.
func (a Ask) Validate() error {
	if a.Auth != "" && !slices.Contains(AskAuths, a.Auth) {
		return fmt.Errorf("ask: auth must be one of %s (or left out), not %q", strings.Join(AskAuths, ", "), a.Auth)
	}
	if a.Model != "" && (len(a.Model) > 200 || !askModel.MatchString(a.Model)) {
		return fmt.Errorf("ask: model %q is not a model name", a.Model)
	}
	if a.Effort != "" && !slices.Contains(AskEfforts, a.Effort) {
		return fmt.Errorf("ask: effort must be one of %s (or left out), not %q", strings.Join(AskEfforts, ", "), a.Effort)
	}
	if a.MaxTurns != 0 && (a.MaxTurns < 1 || a.MaxTurns > MaxAskTurns) {
		return fmt.Errorf("ask: max_turns must be between 1 and %d", MaxAskTurns)
	}
	if a.TimeoutSeconds != 0 && (a.TimeoutSeconds < MinAskTimeout || a.TimeoutSeconds > MaxAskTimeout) {
		return fmt.Errorf("ask: timeout_seconds must be between %d and %d", MinAskTimeout, MaxAskTimeout)
	}
	if a.MaxBudgetUSD < 0 || a.MaxBudgetUSD > 1000 {
		return errors.New("ask: max_budget_usd must be between 0 and 1000")
	}
	if len(a.Instructions) > MaxAskInstructions {
		return fmt.Errorf("ask: instructions are longer than %d bytes", MaxAskInstructions)
	}
	for k := range a.Env {
		if !envName.MatchString(k) {
			return fmt.Errorf("ask: env: %q is not a variable name", k)
		}
	}
	return nil
}

// Server is one knowledge base memors-tray can start.
type Server struct {
	KB        string            `json:"kb"`
	Addr      string            `json:"addr"`           // loopback host:port
	Auth      string            `json:"auth,omitempty"` // none (default) or token
	Autostart bool              `json:"autostart,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// Path is the settings file under MEMORS_HOME.
func Path(homeDir string) string { return filepath.Join(homeDir, "tray.json") }

// Load reads path; a missing file is an empty configuration.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return &c, nil
}

// Save writes c to path atomically, readable by the owner only.
func (c *Config) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tray-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeMu serialises read-modify-write cycles on tray.json: the menu (an
// address for a newly started knowledge base) and the settings pages write
// it from different goroutines.
var writeMu sync.Mutex

// Update applies change to the settings in path and saves the result,
// holding writeMu from read to write, so concurrent writers never undo each
// other. An unreadable file is replaced. Nothing is written when change
// leaves the settings as they were, or when the result is invalid.
func Update(path string, change func(c *Config) error) (*Config, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	c, loadErr := Load(path)
	if loadErr != nil {
		c = &Config{}
	}
	before, _ := json.Marshal(c)
	if err := change(c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if after, _ := json.Marshal(c); loadErr == nil && string(after) == string(before) {
		return c, nil
	}
	return c, c.Save(path)
}

// SaveIf saves c in place of the settings in path if they are still at
// revision rev (see Revision); it reports whether it did.
func SaveIf(path string, c *Config, rev string) (bool, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	if Revision(path) != rev {
		return false, nil
	}
	return true, c.Save(path)
}

// Revision changes whenever the file at path does; "none" when there is
// no file.
func Revision(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

// StopsOnQuit reports whether quitting stops the servers memors-tray started.
func (c *Config) StopsOnQuit() bool { return c.StopOnQuit == nil || *c.StopOnQuit }

// ChecksUpdates reports whether the daily update check is on.
func (c *Config) ChecksUpdates() bool { return c.CheckUpdates == nil || *c.CheckUpdates }

// Find returns the entry for a knowledge base, or nil.
func (c *Config) Find(kb string) *Server {
	for i := range c.Servers {
		if c.Servers[i].KB == kb {
			return &c.Servers[i]
		}
	}
	return nil
}

// FirstPort and LastPort bound the addresses AddrFor hands out.
const (
	FirstPort = 8765
	LastPort  = 8799
)

// AddrFor returns the address for kb, assigning the first free port from
// FirstPort on when kb has none yet; added reports a new entry, which the
// caller saves, so the knowledge base keeps its MCP URL from then on.
// free reports whether nothing listens on an address.
func (c *Config) AddrFor(kb string, free func(addr string) bool) (addr string, added bool, err error) {
	if s := c.Find(kb); s != nil {
		return s.Addr, false, nil
	}
	used := map[string]bool{}
	for _, s := range c.Servers {
		used[s.Addr] = true
	}
	for port := FirstPort; port <= LastPort; port++ {
		a := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if !used[a] && free(a) {
			c.Servers = append(c.Servers, Server{KB: kb, Addr: a})
			return a, true, nil
		}
	}
	return "", false, fmt.Errorf("no free port in %d-%d; set an address for %s in tray.json", FirstPort, LastPort, kb)
}

// PortFree reports whether addr can be bound right now.
func PortFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Reserved reports whether memors-tray sets an environment variable itself
// (or removes it) for the servers it starts, so tray.json may not.
func Reserved(name string) bool {
	switch name {
	case "MEMORS_KB", "MEMORS_HOME", "MEMORS_PUBLIC_URL", "MEMORS_METRICS_ADDR":
		return true
	}
	return strings.HasPrefix(name, "MEMORS_HTTP_") || strings.HasPrefix(name, "MEMORS_TLS_")
}

// Validate checks names, addresses and environment.
func (c *Config) Validate() error {
	if err := validEnv(c.Env); err != nil {
		return err
	}
	if c.Ask != nil {
		if err := c.Ask.Validate(); err != nil {
			return err
		}
	}
	kbs, addrs := map[string]bool{}, map[string]bool{}
	for _, s := range c.Servers {
		if !home.ValidName(s.KB) {
			return fmt.Errorf("server %q: not a valid knowledge-base name", s.KB)
		}
		if kbs[s.KB] {
			return fmt.Errorf("server %q listed twice", s.KB)
		}
		kbs[s.KB] = true
		host, port, err := net.SplitHostPort(s.Addr)
		if err != nil {
			return fmt.Errorf("server %q: addr %q: %w", s.KB, s.Addr, err)
		}
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("server %q: addr %q: bad port", s.KB, s.Addr)
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("server %q: addr %q: MemoRS Tray starts loopback servers only", s.KB, s.Addr)
		}
		if addrs[s.Addr] {
			return fmt.Errorf("server %q: addr %s used twice", s.KB, s.Addr)
		}
		addrs[s.Addr] = true
		if s.Auth != "" && s.Auth != "none" && s.Auth != "token" {
			return fmt.Errorf("server %q: auth must be none or token, not %q", s.KB, s.Auth)
		}
		if err := validEnv(s.Env); err != nil {
			return fmt.Errorf("server %q: %w", s.KB, err)
		}
	}
	return nil
}

func validEnv(env map[string]string) error {
	for k := range env {
		if !envName.MatchString(k) {
			return fmt.Errorf("env: %q is not a variable name", k)
		}
		if Reserved(k) {
			return fmt.Errorf("env: %s is set by MemoRS Tray itself", k)
		}
	}
	return nil
}
