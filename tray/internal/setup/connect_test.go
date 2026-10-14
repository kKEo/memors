package setup

import (
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/menu"
)

const connectServer = "http://127.0.0.1:8766"

// connectHarness has one running token-protected server for knowledge base
// work, on disk, and a Claude Desktop folder of its own.
func connectHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.servers = map[string]menu.ServerView{connectServer: {Key: connectServer, KB: "work", URL: connectServer, Auth: "token", State: menu.Running}}
	if err := os.MkdirAll(filepath.Join(h.home, "kb"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "kb", "work.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(t.TempDir(), "Claude", "claude_desktop_config.json")
	old := desktopConfigPath
	desktopConfigPath = func() (string, error) { return desktop, nil }
	t.Cleanup(func() { desktopConfigPath = old })
	return h, desktop
}

func connectPage(agent string, step int) string {
	return "/connect/" + agent + "?" + url.Values{"server": {connectServer}, "step": {strconv.Itoa(step)}}.Encode()
}

func TestConnectChooseAgent(t *testing.T) {
	h, _ := connectHarness(t)
	body := h.enter("/connect?" + url.Values{"server": {connectServer}}.Encode())
	for _, want := range []string{"Configure your agent", "<b>work</b>", "http://127.0.0.1:8766/mcp", "bearer token", "memors-work",
		`<li class="current">Agent</li>`, "Add the server</li>", "Claude Code", "Claude Desktop", "Other MCP client",
		`href="/connect/claude-desktop?server=http%3a%2f%2f127.0.0.1%3a8766&amp;step=2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("step 1 lacks %q", want)
		}
	}
	if strings.Contains(body, "Knowledge base</li>") {
		t.Error("step 1 shows the wizard's steps")
	}
	// A server that went away, or was never there.
	_, body = h.get(h.s.Origin() + "/connect?server=" + url.QueryEscape("http://127.0.0.1:9"))
	if !strings.Contains(body, "This server is not running") {
		t.Errorf("unknown server:\n%s", body)
	}
	if code, _ := h.get(h.s.Origin() + "/connect/nope?server=x"); code != http.StatusNotFound {
		t.Errorf("unknown agent: %d", code)
	}
}

func TestConnectClaudeCode(t *testing.T) {
	h, _ := connectHarness(t)
	want := `claude mcp add --transport http memors-work 'http://127.0.0.1:8766/mcp' --header "Authorization: Bearer $(memors-mcp http-token)"`
	body := h.enter(connectPage("claude-code", 2))
	for _, s := range []string{html.EscapeString(want), "--scope user", "claude mcp remove memors-work", `<li class="done">Agent</li>`, `<li class="current">Add the server</li>`} {
		if !strings.Contains(body, s) {
			t.Errorf("step 2 lacks %q", s)
		}
	}
	_, body = h.post("/connect/copy", url.Values{"server": {connectServer}, "agent": {"claude-code"}, "what": {"command"}})
	if len(h.copied) != 1 || h.copied[0] != want || !strings.Contains(body, "Copied ✓") {
		t.Errorf("copied %q", h.copied)
	}
	_, body = h.get(h.s.Origin() + connectPage("claude-code", 3))
	if !strings.Contains(body, "/mcp</code>") || !strings.Contains(body, "Open Stats Window") {
		t.Errorf("step 3:\n%s", body)
	}
	_, body = h.get(h.s.Origin() + connectPage("claude-code", 4))
	if !strings.Contains(body, "/mcp__memors-work__ask") || !strings.Contains(body, "Configure another agent") || strings.Contains(body, ">Next<") {
		t.Errorf("step 4:\n%s", body)
	}
}

func TestConnectClaudeDesktop(t *testing.T) {
	h, desktop := connectHarness(t)
	if err := (&config.Config{Servers: []config.Server{{KB: "work", Addr: "127.0.0.1:8766", Env: map[string]string{"MEMORS_MODEL": "potion"}}}}).Save(config.Path(h.home)); err != nil {
		t.Fatal(err)
	}
	body := h.enter(connectPage("claude-desktop", 2))
	for _, s := range []string{`&#34;memors-work&#34;: {`, `&#34;command&#34;: &#34;/opt/fake/memors-mcp&#34;`, `&#34;MEMORS_KB&#34;: &#34;work&#34;`,
		`&#34;MEMORS_MODEL&#34;: &#34;potion&#34;`, `&#34;MEMORS_HOME&#34;: &#34;` + h.home, "quit Claude Desktop completely"} {
		if !strings.Contains(body, s) {
			t.Errorf("step 2 lacks %q\n%s", s, body)
		}
	}
	h.post("/connect/copy", url.Values{"server": {connectServer}, "agent": {"claude-desktop"}, "what": {"json"}})
	if len(h.copied) != 1 || !strings.HasPrefix(h.copied[0], "{\n  \"mcpServers\": {\n    \"memors-work\": {") {
		t.Errorf("copied %q", h.copied)
	}

	// No Claude Desktop: nothing is created.
	_, body = h.post("/connect/open", url.Values{"server": {connectServer}})
	if !strings.Contains(body, "does not seem to be installed") || len(h.opened) != 0 {
		t.Errorf("open without Claude Desktop: %v\n%s", h.opened, body)
	}
	if err := os.MkdirAll(filepath.Dir(desktop), 0o700); err != nil {
		t.Fatal(err)
	}
	_, body = h.post("/connect/open", url.Values{"server": {connectServer}})
	if b, err := os.ReadFile(desktop); err != nil || !strings.Contains(string(b), `"mcpServers"`) {
		t.Errorf("config file not created: %q %v", b, err)
	}
	if len(h.opened) != 1 || h.opened[0] != "text:"+desktop || !strings.Contains(body, "Opened ✓") {
		t.Errorf("opened %v", h.opened)
	}
	// An existing file is left alone.
	if err := os.WriteFile(desktop, []byte(`{"mine":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.post("/connect/open", url.Values{"server": {connectServer}})
	if b, _ := os.ReadFile(desktop); string(b) != `{"mine":1}` {
		t.Errorf("existing file changed: %q", b)
	}

	// A knowledge base on another machine cannot be started by Claude Desktop.
	h.servers["https://kb.example:443"] = menu.ServerView{Key: "https://kb.example:443", KB: "remote", URL: "https://kb.example:443"}
	_, body = h.get(h.s.Origin() + "/connect/claude-desktop?" + url.Values{"server": {"https://kb.example:443"}, "step": {"2"}}.Encode())
	if !strings.Contains(body, "is not on this Mac") || strings.Contains(body, "mcpServers") {
		t.Errorf("remote knowledge base:\n%s", body)
	}
}

func TestConnectOther(t *testing.T) {
	h, _ := connectHarness(t)
	body := h.enter(connectPage("other", 2))
	for _, s := range []string{"http://127.0.0.1:8766/mcp", "Streamable HTTP", "Authorization: Bearer &lt;token&gt;", "memors-mcp http-token"} {
		if !strings.Contains(body, s) {
			t.Errorf("step 2 lacks %q", s)
		}
	}
	h.post("/connect/copy", url.Values{"server": {connectServer}, "agent": {"other"}, "what": {"url"}})
	h.post("/connect/copy", url.Values{"server": {connectServer}, "agent": {"other"}, "what": {"token"}})
	h.post("/connect/copy", url.Values{"server": {connectServer}, "agent": {"other"}, "what": {"bogus"}})
	if len(h.copied) != 2 || h.copied[0] != "http://127.0.0.1:8766/mcp" || h.copied[1] != "memors-mcp http-token" {
		t.Errorf("copied %q", h.copied)
	}
	// Without a token there is no header step and nothing token-related to copy.
	h.servers[connectServer] = menu.ServerView{Key: connectServer, KB: "work", URL: connectServer, Auth: "none"}
	_, body = h.get(h.s.Origin() + connectPage("other", 2))
	if strings.Contains(body, "Authorization") {
		t.Errorf("header shown without a token:\n%s", body)
	}
	h.post("/connect/copy", url.Values{"server": {connectServer}, "agent": {"other"}, "what": {"token"}})
	if len(h.copied) != 2 {
		t.Errorf("copied a token command without a token: %q", h.copied)
	}
}

func TestConnectAccessControl(t *testing.T) {
	h, _ := connectHarness(t)
	if code, _ := h.get(h.s.Origin() + connectPage("claude-code", 2)); code != http.StatusForbidden {
		t.Errorf("without the cookie: %d", code)
	}
	h.enter("/connect?server=x")
	req, _ := http.NewRequest("POST", h.s.Origin()+"/connect/copy", strings.NewReader(url.Values{"server": {connectServer}, "agent": {"claude-code"}, "what": {"command"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := h.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || len(h.copied) != 0 {
		t.Errorf("cross-site copy: %d, copied %q", resp.StatusCode, h.copied)
	}
}

func TestWizardDoneLinksToConnect(t *testing.T) {
	h := newHarness(t)
	if err := (&config.Config{Servers: []config.Server{{KB: "work", Addr: "127.0.0.1:8765"}}}).Save(config.Path(h.home)); err != nil {
		t.Fatal(err)
	}
	body := h.enter("/wizard/done?kb=work")
	if !strings.Contains(body, "Configure Your Agent…</b> in the menu bar once the server runs") || !strings.Contains(body, `<li class="current">Connect</li>`) {
		t.Errorf("before the server runs:\n%s", body)
	}
	h.servers = map[string]menu.ServerView{"http://127.0.0.1:8765": {Key: "http://127.0.0.1:8765", KB: "work", URL: "http://127.0.0.1:8765"}}
	_, body = h.get(h.s.Origin() + "/wizard/done?kb=work")
	if !strings.Contains(body, `href="/connect?server=http%3A%2F%2F127.0.0.1%3A8765"`) {
		t.Errorf("no link once it runs:\n%s", body)
	}
}
