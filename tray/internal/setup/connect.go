package setup

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/kKEo/memors/tray/internal/home"
	"github.com/kKEo/memors/tray/internal/menu"
)

// The "Configure your agent" pages walk through connecting one agent to one
// running server, step by step. The values come from the server as the menu
// sees it (its run file), so servers started in a terminal work too.

// connectSteps label the step indicator of these pages.
var connectSteps = []string{"Agent", "Add the server", "Check", "Start using"}

// agents are the ones the pages know, by path segment.
var agents = map[string]string{
	"claude-code":    "Claude Code",
	"claude-desktop": "Claude Desktop",
	"other":          "Other MCP client",
}

type connectData struct {
	Server  string // menu ServerView.Key
	KB      string
	Name    string // what the agent calls the server
	URL     string // the MCP endpoint
	Auth    string
	MTLS    bool
	Agent   string // a key of agents; empty on step 1
	Label   string
	Step    int
	Copied  string // what was just copied
	Opened  bool
	Problem string

	Command  string // claude mcp add …
	TokenCmd string // prints the bearer token

	DesktopFile string // claude_desktop_config.json
	DesktopJSON string // the entry to merge into it; empty when it cannot be made
	DesktopWhy  string // why not

	Back, Next string // links to the neighbouring steps
}

func (s *Server) connectChoose(w http.ResponseWriter, r *http.Request) {
	d, ok := s.connectFor(r.URL.Query().Get("server"))
	if !ok {
		s.render(w, "connect_gone.html", page{Title: "Configure your agent"})
		return
	}
	d.Step = 1
	d.Agent = r.URL.Query().Get("agent") // the one chosen before, when coming back
	s.render(w, "connect_choose.html", page{Title: "Configure your agent", Step: 1, Steps: connectSteps, Data: d})
}

func (s *Server) connectStep(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	if _, known := agents[agent]; !known {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	d, ok := s.connectFor(q.Get("server"))
	if !ok {
		s.render(w, "connect_gone.html", page{Title: "Configure your agent"})
		return
	}
	step, _ := strconv.Atoi(q.Get("step"))
	step = min(max(step, 2), len(connectSteps))
	d.Agent, d.Label, d.Step = agent, agents[agent], step
	d.Copied, d.Opened = q.Get("copied"), q.Get("opened") == "1"
	d.Problem = q.Get("problem")
	if d.Problem != "" && d.Problem != "nodesktop" && d.Problem != "open" {
		d.Problem = ""
	}
	if agent == "claude-desktop" {
		s.desktop(&d)
	}
	if step == 2 {
		d.Back = connectLink("", d.Server, url.Values{"agent": {agent}})
	} else {
		d.Back = connectLink(agent, d.Server, url.Values{"step": {strconv.Itoa(step - 1)}})
	}
	if step < len(connectSteps) {
		d.Next = connectLink(agent, d.Server, url.Values{"step": {strconv.Itoa(step + 1)}})
	}
	s.render(w, "connect_step.html", page{Title: "Configure " + d.Label, Step: step, Steps: connectSteps, Data: d})
}

// connectCopy puts one of the step's snippets on the clipboard.
func (s *Server) connectCopy(w http.ResponseWriter, r *http.Request) {
	agent, what := r.FormValue("agent"), r.FormValue("what")
	d, ok := s.connectFor(r.FormValue("server"))
	if _, known := agents[agent]; !ok || !known {
		http.Redirect(w, r, connectLink("", r.FormValue("server"), nil), http.StatusSeeOther)
		return
	}
	if agent == "claude-desktop" {
		s.desktop(&d)
	}
	text := map[string]string{"command": d.Command, "url": d.URL, "token": d.TokenCmd, "json": d.DesktopJSON}[what]
	if text == "" || (what == "token" && d.Auth != "token") {
		http.Redirect(w, r, connectLink(agent, d.Server, url.Values{"step": {"2"}}), http.StatusSeeOther)
		return
	}
	if err := s.d.Copy(text); err != nil {
		s.d.Logf("copy: %v", err)
	}
	http.Redirect(w, r, connectLink(agent, d.Server, url.Values{"step": {"2"}, "copied": {what}}), http.StatusSeeOther)
}

// connectOpen opens Claude Desktop's configuration file, creating an empty
// one when Claude Desktop is installed but has none yet.
func (s *Server) connectOpen(w http.ResponseWriter, r *http.Request) {
	server := r.FormValue("server")
	v := url.Values{"step": {"2"}}
	path, err := desktopConfigPath()
	if err == nil {
		if _, statErr := os.Stat(filepath.Dir(path)); statErr != nil {
			v.Set("problem", "nodesktop")
			http.Redirect(w, r, connectLink("claude-desktop", server, v), http.StatusSeeOther)
			return
		}
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			err = os.WriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600)
		}
	}
	if err == nil {
		err = s.d.OpenFile(path, "text")
	}
	if err != nil {
		s.d.Logf("configure agent: open %s: %v", path, err)
		v.Set("problem", "open")
	} else {
		v.Set("opened", "1")
	}
	http.Redirect(w, r, connectLink("claude-desktop", server, v), http.StatusSeeOther)
}

// connectFor describes how to reach the server; false when it is not
// running (any more).
func (s *Server) connectFor(server string) (connectData, bool) {
	if server == "" || s.d.Connection == nil {
		return connectData{}, false
	}
	v, ok := s.d.Connection(server)
	if !ok || v.URL == "" {
		return connectData{}, false
	}
	token := filepath.Join(s.d.Home, "http-token")
	return connectData{
		Server: server, KB: v.KB, Name: menu.ServerName(v.KB), URL: v.URL + "/mcp", Auth: authName(v.Auth), MTLS: v.MTLS,
		Command:  menu.ClaudeAddCommand(v.KB, v.URL, v.Auth, v.TokenFile, token),
		TokenCmd: menu.TokenCommand(v.TokenFile, token),
	}, true
}

// desktop fills in Claude Desktop's entry. Its configuration file takes
// only servers it starts itself, over stdio: it runs its own memors-mcp on
// the same knowledge base, so that knowledge base has to be on this Mac.
func (s *Server) desktop(d *connectData) {
	if p, err := desktopConfigPath(); err == nil {
		d.DesktopFile = p
	}
	kbs, _ := home.KBs(s.d.Home)
	if !slices.Contains(kbs, d.KB) {
		d.DesktopWhy = "The knowledge base " + d.KB + " is not on this Mac, and Claude Desktop can only start servers on this Mac. Use another agent, or set up the knowledge base here."
		return
	}
	cfg, _, _ := s.loadConfig()
	bin, err := s.d.FindMemors(cfg.MemorsBinary)
	if err != nil {
		d.DesktopWhy = "memors-mcp was not found (" + err.Error() + "). Install it with Set Up memors-mcp… in the menu bar."
		return
	}
	env := map[string]string{"MEMORS_KB": d.KB}
	if srv := cfg.Find(d.KB); srv != nil {
		for k, v := range srv.Env {
			env[k] = v
		}
	}
	if def, err := defaultHome(); err != nil || def != s.d.Home {
		env["MEMORS_HOME"] = s.d.Home
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		d.Name: map[string]any{"command": bin, "env": env},
	}}, "", "  ")
	if err != nil {
		d.DesktopWhy = err.Error()
		return
	}
	d.DesktopJSON = string(b)
}

// desktopConfigPath is where Claude Desktop keeps its MCP servers on macOS
// (a variable for tests).
var desktopConfigPath = func() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
}

// defaultHome is MEMORS_HOME when it is not set.
func defaultHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".memors-mcp"), nil
}

// connectLink is the page for agent (step 1 when agent is empty).
func connectLink(agent, server string, extra url.Values) string {
	v := url.Values{"server": {server}}
	for k, vs := range extra {
		v[k] = vs
	}
	p := "/connect"
	if agent != "" {
		p += "/" + agent
	}
	return p + "?" + v.Encode()
}
