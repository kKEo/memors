package setup

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kKEo/memors/tray/internal/ask"
	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/menu"
)

// fakeAsker answers when released, recording what it was asked.
type fakeAsker struct {
	mu      sync.Mutex
	gate    chan struct{}
	asked   []string
	servers []ask.Server
	history [][]ask.Exchange
	answer  string
	err     error
}

func (f *fakeAsker) Ask(_ context.Context, srv ask.Server, q string, h []ask.Exchange) (ask.Answer, error) {
	f.mu.Lock()
	f.asked, f.servers, f.history = append(f.asked, q), append(f.servers, srv), append(f.history, h)
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	if f.err != nil {
		return ask.Answer{}, f.err
	}
	return ask.Answer{Text: f.answer, Turns: 3, Took: 4 * time.Second}, nil
}

func askHarness(t *testing.T) (*harness, *fakeAsker) {
	t.Helper()
	h := newHarness(t)
	h.servers = map[string]menu.ServerView{connectServer: {Key: connectServer, KB: "work", URL: connectServer, Auth: "token", TokenFile: "/tok", State: menu.Running}}
	f := &fakeAsker{answer: "Forty-two.\n\nSee memo://doc/1."}
	h.s.d.Ask = f.Ask
	h.s.d.FindClaude = func(string) (string, error) { return "/opt/fake/claude", nil }
	return h, f
}

func askPage() string { return "/ask?" + url.Values{"server": {connectServer}}.Encode() }

func waitFor(t *testing.T, h *harness, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body := h.get(h.s.Origin() + askPage())
		if strings.Contains(body, want) || time.Now().After(deadline) {
			if !strings.Contains(body, want) {
				t.Fatalf("page never showed %q:\n%s", want, body)
			}
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAskConversation(t *testing.T) {
	h, f := askHarness(t)
	f.gate = make(chan struct{})
	body := h.enter(askPage())
	for _, want := range []string{"Ask <b>work</b>", `name="question"`, "only this server's MCP attached"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty page lacks %q", want)
		}
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("an idle page reloads itself")
	}
	// Asking shows the pending question and reloads until the answer is in.
	_, body = h.post("/ask", url.Values{"server": {connectServer}, "question": {"  What is the answer?  "}})
	for _, want := range []string{"What is the answer?", "Searching the knowledge base", `http-equiv="refresh" content="2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("pending page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `name="question"`) {
		t.Error("the form shows while a question runs")
	}
	// A second question meanwhile is ignored.
	h.post("/ask", url.Values{"server": {connectServer}, "question": {"and this?"}})
	close(f.gate)
	body = waitFor(t, h, "Forty-two.")
	f.mu.Lock()
	if len(f.asked) != 1 || f.asked[0] != "What is the answer?" || f.servers[0] != (ask.Server{URL: connectServer, Auth: "token", TokenFile: "/tok", DefaultTokenFile: h.home + "/http-token"}) {
		t.Errorf("asked %q of %+v", f.asked, f.servers)
	}
	f.mu.Unlock()
	for _, want := range []string{"4s · 3 steps", "Follow-up question", "Copy answer", "Clear conversation"} {
		if !strings.Contains(body, want) {
			t.Errorf("answered page lacks %q:\n%s", want, body)
		}
	}
	// A follow-up gets the earlier exchange as context.
	f.gate = nil
	h.post("/ask", url.Values{"server": {connectServer}, "question": {"Why?"}})
	waitFor(t, h, "Why?</div>\n<pre")
	f.mu.Lock()
	if len(f.history) != 2 || len(f.history[1]) != 1 || f.history[1][0].Question != "What is the answer?" {
		t.Errorf("follow-up context = %+v", f.history)
	}
	f.mu.Unlock()
	// Copy puts the answer on the clipboard.
	_, body = h.post("/ask/copy", url.Values{"server": {connectServer}, "n": {"1"}})
	if len(h.copied) != 1 || h.copied[0] != "Forty-two.\n\nSee memo://doc/1." || !strings.Contains(body, "Copied ✓") {
		t.Errorf("copied %q", h.copied)
	}
	// Clear forgets it all.
	_, body = h.post("/ask/clear", url.Values{"server": {connectServer}})
	if strings.Contains(body, "Forty-two") || !strings.Contains(body, "Your question") {
		t.Errorf("after clear:\n%s", body)
	}
}

func TestAskProblems(t *testing.T) {
	h, f := askHarness(t)
	f.err = errors.New("claude: Not logged in")
	h.enter(askPage())
	h.post("/ask", url.Values{"server": {connectServer}, "question": {"q"}})
	body := waitFor(t, h, "claude: Not logged in")
	if !strings.Contains(body, `name="question"`) {
		t.Error("the form is gone after a failure")
	}
	// Claude Code missing: a warning, no form.
	h.s.d.FindClaude = func(string) (string, error) { return "", ask.ErrNotFound }
	_, body = h.get(h.s.Origin() + askPage())
	if !strings.Contains(body, "Claude Code (claude) not found") || strings.Contains(body, `name="question"`) {
		t.Errorf("without claude:\n%s", body)
	}
	// A server that is gone.
	_, body = h.get(h.s.Origin() + "/ask?server=" + url.QueryEscape("http://127.0.0.1:9"))
	if !strings.Contains(body, "This server is not running") {
		t.Errorf("unknown server:\n%s", body)
	}
	// An mTLS server: explained, no form.
	h.servers[connectServer] = menu.ServerView{Key: connectServer, KB: "work", URL: connectServer, MTLS: true, State: menu.Running}
	h.s.d.FindClaude = func(string) (string, error) { return "/opt/fake/claude", nil }
	_, body = h.get(h.s.Origin() + askPage())
	if !strings.Contains(body, "client certificate") || strings.Contains(body, `name="question"`) {
		t.Errorf("mTLS server:\n%s", body)
	}
}

func TestAskSettings(t *testing.T) {
	h, _ := askHarness(t)
	claude := filepath.Join(t.TempDir(), "claude")
	h.s.d.FindClaude = func(configured string) (string, error) {
		if configured != "" && configured != claude {
			return "", errors.New("claude_binary in tray.json is not an executable: " + configured)
		}
		return "/opt/fake/claude", nil
	}
	// The Ask window says how Claude Code runs and links to its settings.
	body := h.enter(askPage())
	for _, want := range []string{"Claude Code&#39;s own sign-in · default model", `href="/settings?back=` + strings.ToLower(url.QueryEscape(connectServer)) + `#ask"`} {
		if !strings.Contains(body, want) {
			t.Errorf("ask page lacks %q:\n%s", want, body)
		}
	}
	_, body = h.get(h.s.Origin() + "/settings?" + url.Values{"back": {connectServer}}.Encode())
	for _, want := range []string{`id="ask"`, "Back to Ask", "Claude Code at /opt/fake/claude", `name="ask_max_turns" value="12"`, `name="ask_timeout" value="180"`, "Only for the API key"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	// An API key sign-in needs its key.
	form := func(body string, more url.Values) url.Values {
		f := url.Values{"rev": {rev(t, body)}, "back": {connectServer}, "stop_on_quit": {"on"}, "check_updates": {"on"}}
		for k, v := range more {
			f[k] = v
		}
		return f
	}
	code, got := h.post("/settings", form(body, url.Values{"ask_auth": {"api_key"}}))
	if code != http.StatusUnprocessableEntity || !strings.Contains(got, "enter the API key") {
		t.Errorf("API key sign-in without a key: %d", code)
	}
	code, got = h.post("/settings", form(body, url.Values{
		"claude_path": {claude}, "ask_auth": {"api_key"}, "ask_secret": {" sk-ant-secret "}, "ask_model": {"sonnet"}, "ask_effort": {"low"},
		"ask_max_turns": {"6"}, "ask_timeout": {"180"}, "ask_budget": {"$0.50"}, "ask_instructions": {"Answer in Polish.\r\n"},
		"ask_env": {"HTTPS_PROXY=http://proxy:3128\n# note\n"},
	}))
	if code != 200 || !strings.Contains(got, "Settings saved.") || !strings.Contains(got, "Back to Ask") {
		t.Fatalf("save: %d\n%s", code, got)
	}
	if strings.Contains(got, "sk-ant-secret") {
		t.Error("the settings page shows the key")
	}
	for _, want := range []string{"An API key is stored", `<option value="api_key" selected>`, `name="ask_model" value="sonnet"`, `<option value="low" selected>`, `name="ask_budget" value="0.5"`, "Answer in Polish.</textarea>", "HTTPS_PROXY=http://proxy:3128</textarea>"} {
		if !strings.Contains(got, want) {
			t.Errorf("saved settings page lacks %q", want)
		}
	}
	c := h.cfg()
	want := config.Ask{Auth: "api_key", Model: "sonnet", Effort: "low", MaxTurns: 6, MaxBudgetUSD: 0.5, Instructions: "Answer in Polish.", Env: map[string]string{"HTTPS_PROXY": "http://proxy:3128"}}
	if c.ClaudeBinary != claude || c.Ask == nil || !reflect.DeepEqual(*c.Ask, want) {
		t.Errorf("saved ask = %+v (claude %q)", c.Ask, c.ClaudeBinary)
	}
	raw, _ := os.ReadFile(config.Path(h.home))
	if strings.Contains(string(raw), "sk-ant-secret") {
		t.Error("the key is in tray.json")
	}
	if o, err := ask.Load(h.home); err != nil || o.Secret != "sk-ant-secret" {
		t.Errorf("stored key: %+v, %v", o, err)
	}
	_, body = h.get(h.s.Origin() + askPage())
	if !strings.Contains(body, "Anthropic API key · model sonnet · low effort") {
		t.Errorf("ask page does not name the new setup:\n%s", body)
	}
	// Saving again with the key field empty keeps the key and the Claude location.
	_, body = h.get(h.s.Origin() + "/settings")
	h.post("/settings", form(body, url.Values{"claude_path": {claude}, "ask_auth": {"api_key"}, "ask_max_turns": {"12"}}))
	if c := h.cfg(); c.ClaudeBinary != claude || c.Ask == nil || c.Ask.Auth != "api_key" || c.Ask.MaxTurns != 0 || !ask.HasSecret(h.home, "api_key") {
		t.Errorf("second save: %+v", c.Ask)
	}
	// Another sign-in deletes the key; all defaults leave no ask block.
	_, body = h.get(h.s.Origin() + "/settings")
	h.post("/settings", form(body, url.Values{"ask_auth": {"bedrock"}, "ask_env": {"AWS_PROFILE=work"}}))
	if c := h.cfg(); c.Ask == nil || c.Ask.Auth != "bedrock" || c.Ask.Env["AWS_PROFILE"] != "work" || ask.HasSecret(h.home, "api_key") {
		t.Errorf("after choosing Bedrock: %+v (key kept: %v)", c.Ask, ask.HasSecret(h.home, "api_key"))
	}
	_, body = h.get(h.s.Origin() + "/settings")
	h.post("/settings", form(body, nil))
	if c := h.cfg(); c.Ask != nil || c.ClaudeBinary != "" {
		t.Errorf("defaults: ask = %+v", c.Ask)
	}
	// Bad values are refused, and nothing is stored.
	_, body = h.get(h.s.Origin() + "/settings")
	for _, c := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"ask_auth": {"password"}}, "auth must be one of"},
		{url.Values{"ask_model": {"--dangerously-skip-permissions"}}, "not a model name"},
		{url.Values{"ask_effort": {"heroic"}}, "effort must be one of"},
		{url.Values{"ask_max_turns": {"many"}}, "not a whole number"},
		{url.Values{"ask_max_turns": {"0"}}, "max_turns must be between"},
		{url.Values{"ask_timeout": {"5"}}, "timeout_seconds must be between"},
		{url.Values{"ask_budget": {"cheap"}}, "not an amount"},
		{url.Values{"ask_env": {"no equals"}}, "Ask, environment for Claude Code, line 1"},
		{url.Values{"ask_env": {"BAD NAME=1"}}, "not a variable name"},
		{url.Values{"ask_auth": {"oauth_token"}, "ask_secret": {"two words"}}, "Ask, token: it contains spaces"},
		{url.Values{"claude_path": {"/does/not/exist"}}, "Claude Code location"},
	} {
		code, got := h.post("/settings", form(body, c.form))
		if code != http.StatusUnprocessableEntity || !strings.Contains(got, c.want) {
			t.Errorf("%v: %d, want an error containing %q", c.form, code, c.want)
		}
	}
	if c := h.cfg(); c.Ask != nil || ask.HasSecret(h.home, "oauth_token") {
		t.Errorf("a refused form was stored: %+v", c.Ask)
	}
}
