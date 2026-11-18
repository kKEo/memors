package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kKEo/memors/tray/internal/ask"
	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/home"
)

// serverRow is one knowledge base in the settings table.
type serverRow struct {
	KB, Addr, Auth string
	Autostart      bool
}

type settingsData struct {
	Rev          string
	Saved        bool
	Notice       string
	Errors       []string
	Login        bool
	StopOnQuit   bool
	CheckUpdates bool
	TrayVersion  string
	MemorsMode   string // auto or path
	MemorsPath   string
	MemorsStatus string
	MemorsOK     bool
	Model        string
	Models       []Model
	DefaultModel string
	Servers      []serverRow
	Env          string
	Debug        bool
	ConfigPath   string
	LogsDir      string

	// The Ask section.
	Back            string // the server whose Ask window led here, for the way back
	ClaudePath      string
	ClaudeStatus    string
	ClaudeOK        bool
	AskAuth         string
	AskAuths        []choice
	AskSecretStored bool
	AskSecretName   string
	AskModel        string
	AskEffort       string
	AskEfforts      []string
	AskTurns        string
	AskTimeout      string
	AskBudget       string
	AskInstructions string
	AskEnv          string
}

// choice is one option of a select.
type choice struct{ Value, Label string }

// askAuths are the ways Claude Code can sign in, as the form names them.
var askAuths = []choice{
	{"", "Claude Code's own sign-in"},
	{"api_key", "Anthropic API key"},
	{"oauth_token", "Long-lived token (claude setup-token)"},
	{"bedrock", "Amazon Bedrock"},
	{"vertex", "Google Vertex AI"},
	{"foundry", "Microsoft Foundry"},
}

// askAuthLabel is the form's name for a sign-in method.
func askAuthLabel(auth string) string {
	for _, c := range askAuths {
		if c.Value == auth {
			return c.Label
		}
	}
	return auth
}

// askSummary is one line on how Ask runs Claude Code.
func askSummary(a config.Ask) string {
	parts := []string{askAuthLabel(a.Auth)}
	if a.Model != "" {
		parts = append(parts, "model "+a.Model)
	} else {
		parts = append(parts, "default model")
	}
	if a.Effort != "" {
		parts = append(parts, a.Effort+" effort")
	}
	return strings.Join(parts, " · ")
}

// maxServers bounds the rows read back from a posted form.
const maxServers = 100

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	cfg, rev, err := s.loadConfig()
	d := s.settingsFrom(r.Context(), cfg)
	d.Rev = rev
	d.Saved = r.URL.Query().Get("saved") == "1"
	d.Back = s.backTo(r.URL.Query().Get("back"))
	if err != nil {
		d.Errors = append(d.Errors, err.Error()+" (saving this form replaces it)")
	}
	s.render(w, "settings.html", page{Title: "Settings", Data: d})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	cur, rev, _ := s.loadConfig()
	if r.PostForm.Get("rev") != rev {
		d := s.settingsFrom(r.Context(), cur)
		d.Rev, d.Back = rev, s.backTo(r.PostForm.Get("back"))
		d.Notice = "tray.json changed while this form was open (MemoRS Tray adds a server when you start a new knowledge base). Nothing was saved: check the values below and save again."
		w.WriteHeader(http.StatusConflict)
		s.render(w, "settings.html", page{Title: "Settings", Data: d})
		return
	}
	next, errs := s.fromForm(r.PostForm, cur)
	auth := next.AskSettings().Auth
	secret := strings.TrimSpace(r.PostForm.Get("ask_secret"))
	if file := ask.SecretFile(s.d.Home, auth); file == "" {
		secret = "" // this sign-in takes none
	} else if secret != "" {
		if err := ask.CheckSecret(secret); err != nil {
			errs = append(errs, fmt.Sprintf("Ask, %s: %v", ask.SecretName(auth), err))
		}
	} else if !ask.HasSecret(s.d.Home, auth) {
		errs = append(errs, fmt.Sprintf("Ask: enter the %s for the sign-in %q", ask.SecretName(auth), askAuthLabel(auth)))
	}
	if want := r.PostForm.Get("login") == "on"; want != s.d.Login.Enabled() {
		var err error
		if want {
			err = s.d.Login.Enable()
		} else {
			err = s.d.Login.Disable()
		}
		if err != nil {
			errs = append(errs, "Launch at login: "+err.Error())
		}
	}
	if len(errs) > 0 {
		d := s.settingsFrom(r.Context(), next)
		d.Rev, d.Errors, d.Back = rev, errs, s.backTo(r.PostForm.Get("back"))
		d.MemorsMode, d.MemorsPath = r.PostForm.Get("memors_mode"), r.PostForm.Get("memors_path")
		d.Env = r.PostForm.Get("env")
		d.ClaudePath, d.AskEnv = r.PostForm.Get("claude_path"), r.PostForm.Get("ask_env")
		d.AskTurns, d.AskTimeout, d.AskBudget = r.PostForm.Get("ask_max_turns"), r.PostForm.Get("ask_timeout"), r.PostForm.Get("ask_budget")
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, "settings.html", page{Title: "Settings", Data: d})
		return
	}
	if secret != "" { // before tray.json names the sign-in that needs it
		if err := ask.StoreSecret(s.d.Home, auth, secret); err != nil {
			http.Error(w, "saving the "+ask.SecretName(auth)+": "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	saved, err := config.SaveIf(config.Path(s.d.Home), next, rev)
	if err != nil {
		http.Error(w, "saving tray.json: "+err.Error(), http.StatusInternalServerError)
		return
	}
	q := url.Values{}
	if back := s.backTo(r.PostForm.Get("back")); back != "" {
		q.Set("back", back)
	}
	if !saved { // changed between the check above and now
		http.Redirect(w, r, "/settings?"+q.Encode(), http.StatusSeeOther)
		return
	}
	if err := ask.ForgetSecrets(s.d.Home, auth); err != nil {
		s.d.Logf("settings: %v", err)
	}
	s.d.Notify(Event{Kind: ConfigSaved})
	q.Set("saved", "1")
	http.Redirect(w, r, "/settings?"+q.Encode(), http.StatusSeeOther)
}

// backTo is server when it names a running server with an Ask window,
// else empty.
func (s *Server) backTo(server string) string {
	if _, ok := s.askFor(server); !ok {
		return ""
	}
	return server
}

// fromForm builds the configuration a posted settings form describes.
// Per-server environment, which the form does not show, is kept; new
// servers come in through addServer.
func (s *Server) fromForm(f url.Values, cur *config.Config) (*config.Config, []string) {
	var errs []string
	next := &config.Config{Debug: f.Get("debug") == "on"}
	if f.Get("stop_on_quit") != "on" {
		no := false
		next.StopOnQuit = &no
	}
	if f.Get("check_updates") != "on" {
		no := false
		next.CheckUpdates = &no
	}
	if f.Get("memors_mode") == "path" {
		p := expandHome(strings.TrimSpace(f.Get("memors_path")))
		if !filepath.IsAbs(p) {
			errs = append(errs, "memors-mcp location: give the full path to the memors-mcp file, or choose Find automatically")
		} else if _, err := s.d.FindMemors(p); err != nil {
			errs = append(errs, "memors-mcp location: "+err.Error())
		}
		next.MemorsBinary = p
	}

	if p := strings.TrimSpace(f.Get("claude_path")); p != "" {
		p = expandHome(p)
		if !filepath.IsAbs(p) {
			errs = append(errs, "Claude Code location: give the full path to the claude file, or leave it empty to find it automatically")
		} else if s.d.FindClaude != nil {
			if _, err := s.d.FindClaude(p); err != nil {
				errs = append(errs, "Claude Code location: "+p+" is not an executable")
			}
		}
		next.ClaudeBinary = p
	}
	a, askErrs := askFromForm(f)
	errs = append(errs, askErrs...)
	if !a.IsZero() {
		next.Ask = &a
	}

	env, envErrs := parseEnv(f.Get("env"))
	errs = append(errs, envErrs...)
	if m := strings.TrimSpace(f.Get("model")); m != "" {
		env["MEMORS_MODEL"] = m
	}
	if len(env) > 0 {
		next.Env = env
	}

	n, _ := strconv.Atoi(f.Get("servers"))
	for i := range min(n, maxServers) {
		kb := f.Get(fmt.Sprintf("kb_%d", i))
		if kb == "" || f.Get(fmt.Sprintf("remove_%d", i)) == "on" {
			continue
		}
		srv := config.Server{KB: kb, Addr: strings.TrimSpace(f.Get(fmt.Sprintf("addr_%d", i))),
			Auth: authOf(f.Get(fmt.Sprintf("auth_%d", i))), Autostart: f.Get(fmt.Sprintf("autostart_%d", i)) == "on"}
		if old := cur.Find(kb); old != nil {
			srv.Env = old.Env
		}
		next.Servers = append(next.Servers, srv)
	}
	if err := next.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	return next, errs
}

// askFromForm reads the Ask section. A limit typed as its default is
// left out of tray.json.
func askFromForm(f url.Values) (config.Ask, []string) {
	var errs []string
	a := config.Ask{
		Auth:         strings.TrimSpace(f.Get("ask_auth")),
		Model:        strings.TrimSpace(f.Get("ask_model")),
		Effort:       strings.TrimSpace(f.Get("ask_effort")),
		Instructions: strings.TrimSpace(strings.ReplaceAll(f.Get("ask_instructions"), "\r\n", "\n")),
	}
	number := func(field, what string, def int) int {
		v := strings.TrimSpace(f.Get(field))
		if v == "" {
			return 0
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, "Ask, "+what+": "+strconv.Quote(v)+" is not a whole number")
			return 0
		}
		if n == def {
			return 0
		}
		if n == 0 {
			return -1 // out of range, for Validate to say so
		}
		return n
	}
	a.MaxTurns = number("ask_max_turns", "steps per answer", config.DefaultAskTurns)
	a.TimeoutSeconds = number("ask_timeout", "time limit", config.DefaultAskTimeout)
	if v := strings.TrimPrefix(strings.TrimSpace(f.Get("ask_budget")), "$"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n != n {
			errs = append(errs, "Ask, cost limit: "+strconv.Quote(v)+" is not an amount in US dollars")
		} else {
			a.MaxBudgetUSD = n
		}
	}
	env, envErrs := parseLines(f.Get("ask_env"), "Ask, environment for Claude Code")
	errs = append(errs, envErrs...)
	if len(env) > 0 {
		a.Env = env
	}
	return a, errs
}

// settingsFrom fills the form from a configuration.
func (s *Server) settingsFrom(ctx context.Context, cfg *config.Config) settingsData {
	d := settingsData{
		Login:        s.d.Login.Enabled(),
		StopOnQuit:   cfg.StopsOnQuit(),
		CheckUpdates: cfg.ChecksUpdates(),
		TrayVersion:  s.d.TrayVersion,
		MemorsMode:   "auto",
		MemorsPath:   cfg.MemorsBinary,
		Model:        cfg.Env["MEMORS_MODEL"],
		Debug:        cfg.Debug,
		ConfigPath:   config.Path(s.d.Home),
		LogsDir:      home.LogDir(s.d.Home),
	}
	if cfg.MemorsBinary != "" {
		d.MemorsMode = "path"
	}
	bin, version, err := s.memors(ctx, cfg)
	d.MemorsOK = err == nil
	d.MemorsStatus = memorsLine(bin, version, err)
	if d.MemorsOK {
		d.Models, _ = s.d.Models(ctx, bin)
		for _, m := range d.Models {
			if m.Default {
				d.DefaultModel = m.ID
			}
		}
	}
	for _, srv := range cfg.Servers {
		d.Servers = append(d.Servers, serverRow{KB: srv.KB, Addr: srv.Addr, Auth: authName(srv.Auth), Autostart: srv.Autostart})
	}
	var lines []string
	for k, v := range cfg.Env {
		if k != "MEMORS_MODEL" {
			lines = append(lines, k+"="+v)
		}
	}
	slices.Sort(lines)
	d.Env = strings.Join(lines, "\n")

	a := cfg.AskSettings()
	d.ClaudePath, d.ClaudeOK = cfg.ClaudeBinary, true
	if s.d.FindClaude != nil {
		if bin, err := s.d.FindClaude(cfg.ClaudeBinary); err != nil {
			d.ClaudeOK, d.ClaudeStatus = false, err.Error()
		} else {
			d.ClaudeStatus = "Claude Code at " + bin
		}
	}
	d.AskAuth, d.AskAuths = a.Auth, askAuths
	d.AskSecretName = ask.SecretName(a.Auth)
	d.AskSecretStored = ask.HasSecret(s.d.Home, a.Auth)
	d.AskModel, d.AskEffort, d.AskEfforts = a.Model, a.Effort, config.AskEfforts
	d.AskTurns, d.AskTimeout = strconv.Itoa(a.Turns()), strconv.Itoa(a.Timeout())
	if a.MaxBudgetUSD > 0 {
		d.AskBudget = strconv.FormatFloat(a.MaxBudgetUSD, 'f', -1, 64)
	}
	d.AskInstructions = a.Instructions
	lines = nil
	for k, v := range a.Env {
		lines = append(lines, k+"="+v)
	}
	slices.Sort(lines)
	d.AskEnv = strings.Join(lines, "\n")
	return d
}

func (s *Server) openFromSettings(w http.ResponseWriter, r *http.Request) {
	var err error
	switch r.FormValue("what") {
	case "config":
		path := config.Path(s.d.Home)
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			err = (&config.Config{}).Save(path)
		}
		if err == nil {
			err = s.d.OpenFile(path, "text")
		}
	case "logs":
		dir := home.LogDir(s.d.Home)
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = s.d.OpenFile(dir, "folder")
		}
	}
	if err != nil {
		s.d.Logf("settings: open %s: %v", r.FormValue("what"), err)
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// loadConfig reads tray.json and a revision string that changes whenever
// the file does. A broken file comes back as an empty configuration plus
// its error.
func (s *Server) loadConfig() (*config.Config, string, error) {
	path := config.Path(s.d.Home)
	rev := config.Revision(path)
	cfg, err := config.Load(path)
	if err != nil {
		return &config.Config{}, rev, err
	}
	return cfg, rev, nil
}

// memors finds memors-mcp the way the menu does, and asks its version.
func (s *Server) memors(ctx context.Context, cfg *config.Config) (bin, version string, err error) {
	bin, err = s.d.FindMemors(cfg.MemorsBinary)
	if err == nil {
		version, _ = s.d.MemorsVersion(ctx, bin)
	}
	return bin, version, err
}

func memorsLine(bin, version string, err error) string {
	if err != nil {
		return err.Error()
	}
	if version == "" {
		version = "(version unknown)"
	}
	return "memors-mcp " + version + " at " + bin
}

// parseEnv reads the servers' environment (parseLines), where the
// embedding model has a field of its own.
func parseEnv(text string) (map[string]string, []string) {
	env, errs := parseLines(text, "Environment")
	if _, ok := env["MEMORS_MODEL"]; ok {
		delete(env, "MEMORS_MODEL")
		errs = append(errs, "Environment: choose the embedding model with the field above, not MEMORS_MODEL here")
	}
	return env, errs
}

// parseLines reads KEY=value lines; blank lines and # comments are
// skipped. what names the field in an error.
func parseLines(text, what string) (map[string]string, []string) {
	env := map[string]string{}
	var errs []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			errs = append(errs, fmt.Sprintf("%s, line %d: write NAME=value", what, i+1))
			continue
		}
		env[k] = strings.TrimSpace(v)
	}
	return env, errs
}

func authOf(v string) string {
	if v == "token" {
		return "token"
	}
	return ""
}

func authName(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// expandHome turns a leading ~/ into the home directory.
func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}
