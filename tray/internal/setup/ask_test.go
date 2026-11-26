package setup

import (
	"context"
	"errors"
	"io"
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

// fakeAsker reports its events, then answers when released (or says it was
// stopped), recording what it was asked.
type fakeAsker struct {
	mu      sync.Mutex
	gate    chan struct{}
	asked   []string
	servers []ask.Server
	history [][]ask.Exchange
	events  []ask.Event
	answer  string
	err     error
}

func (f *fakeAsker) Ask(ctx context.Context, srv ask.Server, q string, h []ask.Exchange, progress func(ask.Event)) (ask.Answer, error) {
	f.mu.Lock()
	f.asked, f.servers, f.history = append(f.asked, q), append(f.servers, srv), append(f.history, h)
	gate, err := f.gate, f.err
	f.mu.Unlock()
	for _, e := range f.events {
		progress(e)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ask.Answer{}, ask.ErrStopped
		}
	}
	if err != nil {
		return ask.Answer{}, err
	}
	return ask.Answer{Text: f.answer, Turns: 3, Took: 4 * time.Second, Titles: map[string]string{"memo://doc/1": "The <big> answer"}}, nil
}

func askHarness(t *testing.T) (*harness, *fakeAsker) {
	t.Helper()
	h := newHarness(t)
	h.servers = map[string]menu.ServerView{connectServer: {Key: connectServer, KB: "work", URL: connectServer, Auth: "token", TokenFile: "/tok", State: menu.Running}}
	f := &fakeAsker{answer: "**Forty-two.**\n\nSee memo://doc/1 and memo://nowhere/9.", events: []ask.Event{{Text: "Let me <look>."}, {Tool: "search", Detail: "the <answer>"}, {Text: "Forty"}}}
	h.s.d.Ask = f.Ask
	h.s.d.FindClaude = func(string) (string, error) { return "/opt/fake/claude", nil }
	h.s.d.OpenMemo = func(server, path string) {
		h.mu.Lock()
		h.opened = append(h.opened, "memo:"+server+path)
		h.mu.Unlock()
	}
	return h, f
}

func askPage() string { return "/ask?" + url.Values{"server": {connectServer}}.Encode() }

func askForm(more ...string) url.Values {
	v := url.Values{"server": {connectServer}}
	for i := 0; i+1 < len(more); i += 2 {
		v.Set(more[i], more[i+1])
	}
	return v
}

// following is an Ask page that is still arriving.
type following struct {
	t    *testing.T
	body io.ReadCloser
	got  string
}

// follow posts form to path and returns the page it lands on, unread.
func (h *harness) follow(path string, form url.Values) *following {
	h.t.Helper()
	resp, err := h.c.PostForm(h.s.Origin()+path, form)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return &following{t: h.t, body: resp.Body}
}

// until reads on to want, and returns the page so far.
func (f *following) until(want string) string {
	f.t.Helper()
	buf := make([]byte, 4096)
	for !strings.Contains(f.got, want) {
		n, err := f.body.Read(buf)
		f.got += string(buf[:n])
		if err != nil {
			if !strings.Contains(f.got, want) {
				f.t.Fatalf("the page never showed %q (%v):\n%s", want, err, f.got)
			}
			break
		}
	}
	return f.got
}

// rest reads the page to its end.
func (f *following) rest() string {
	f.t.Helper()
	b, _ := io.ReadAll(f.body)
	f.got += string(b)
	return f.got
}

func lacks(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, body)
		}
	}
}

func TestAskConversation(t *testing.T) {
	h, f := askHarness(t)
	f.gate = make(chan struct{})
	body := h.enter(askPage())
	lacks(t, "empty page", body, "Ask <b>work</b>", `name="question"`, "only this server's MCP attached", `value="What is this knowledge base about?"`, "Ask about work…")
	if strings.Contains(body, `http-equiv="refresh"`) || strings.Contains(body, "/ask/stop") || strings.Contains(body, "<script") {
		t.Errorf("empty page:\n%s", body)
	}
	// Asking shows the question at once, then what claude does as it does
	// it: the page keeps arriving until the answer is in.
	page := h.follow("/ask", askForm("question", "  What is the <answer>?  "))
	body = page.until("Forty")
	lacks(t, "page in the making", body, `<div class="q">What is the &lt;answer&gt;?</div>`, `action="/ask/stop"`, "Asking Claude Code",
		`<div class="step"><span>Searching for <q class="what">the &lt;answer&gt;</q></span></div>`, `<div class="draft">Let me &lt;look&gt;.</div>`, `<div class="draft">Forty`)
	if strings.Contains(body, `name="question"`) {
		t.Error("the question box shows while a question runs")
	}
	// A second question meanwhile is ignored; a window opened meanwhile
	// sees the same turn from its start.
	h.follow("/ask", askForm("question", "and this?")).until("Searching for")
	close(f.gate)
	body = page.rest()
	f.mu.Lock()
	if len(f.asked) != 1 || f.asked[0] != "What is the <answer>?" || f.servers[0] != (ask.Server{URL: connectServer, Auth: "token", TokenFile: "/tok", DefaultTokenFile: h.home + "/http-token"}) {
		t.Errorf("asked %q of %+v", f.asked, f.servers)
	}
	f.gate = nil
	f.mu.Unlock()
	open := `href="/ask/open?server=` + url.QueryEscape(connectServer) + `&amp;uri=memo%3A%2F%2Fdoc%2F1"`
	lacks(t, "answered page", body, `Forty</div>`+"\n"+`<i class="end"></i></div>`, "<p><strong>Forty-two.</strong></p>", `<a `+open+`>memo://doc/1</a>`, "memo://nowhere/9.",
		open+` title="memo://doc/1 · open in the knowledge base window">The &lt;big&gt; answer</a>`, "4s · 3 steps", "Ask a follow-up…", "Copy answer", "Ask again", "Clear conversation", `name="question"`, "</body></html>")
	if strings.Count(body, "</article>") != 1 || strings.Contains(body, "nowhere%2F9") {
		t.Errorf("answered page:\n%s", body)
	}
	// The page read afresh shows the same exchange, its steps folded away.
	_, body = h.get(h.s.Origin() + askPage())
	lacks(t, "page read afresh", body, `<article class="turn" id="t1">`, "<summary>1 lookup in the knowledge base</summary>", `Searching for <q class="what">the &lt;answer&gt;</q>`, "<p><strong>Forty-two.</strong></p>", "Ask again")
	if strings.Contains(body, "/ask/stop") || strings.Contains(body, `class="draft"`) {
		t.Errorf("page read afresh:\n%s", body)
	}
	// A follow-up gets the earlier exchange as context.
	body = h.follow("/ask", askForm("question", "Why?")).rest()
	lacks(t, "follow-up", body, `id="t1"`, `<div class="q">Why?</div>`)
	f.mu.Lock()
	if len(f.history) != 2 || len(f.history[1]) != 1 || f.history[1][0].Question != "What is the <answer>?" || !strings.HasPrefix(f.history[1][0].Answer, "**Forty-two.**") {
		t.Errorf("follow-up context = %+v", f.history)
	}
	f.mu.Unlock()
	if strings.Count(body, "Ask again") != 1 {
		t.Error("only the newest answer can be asked again")
	}
	// Copy puts the answer, as written, on the clipboard.
	_, body = h.post("/ask/copy", askForm("n", "1"))
	if len(h.copied) != 1 || h.copied[0] != "**Forty-two.**\n\nSee memo://doc/1 and memo://nowhere/9." || strings.Count(body, "Copied ✓") != 1 {
		t.Errorf("copied %q", h.copied)
	}
	// A cited memory opens in the server's window; the page stays.
	for _, uri := range []string{"memo://doc/1", "memo://doc/../live", "http://elsewhere/"} {
		code, _ := h.get(h.s.Origin() + "/ask/open?" + url.Values{"server": {connectServer}, "uri": {uri}}.Encode())
		if code != http.StatusNoContent || len(h.opened) != 1 {
			t.Errorf("open %s: %d, opened %q", uri, code, h.opened)
		}
	}
	if h.opened[0] != "memo:"+connectServer+"/doc/1" {
		t.Errorf("opened %q", h.opened)
	}
	// Clear forgets it all.
	_, body = h.post("/ask/clear", askForm())
	if strings.Contains(body, "Forty-two") || !strings.Contains(body, "Ask about work…") {
		t.Errorf("after clear:\n%s", body)
	}
}

func TestAskStopAndRetry(t *testing.T) {
	h, f := askHarness(t)
	f.gate = make(chan struct{})
	h.enter(askPage())
	// Stop ends the question; what there was of the answer stays, and is
	// no context for the next one.
	page := h.follow("/ask", askForm("question", "Slow one?"))
	page.until("Forty")
	_, body := h.post("/ask/stop", askForm())
	lacks(t, "stopped page", body, `<div class="q">Slow one?</div>`, "<p>Forty</p>", "Stopped before the answer was finished.", "Try again", `name="question"`, "1 lookup in the knowledge base")
	if strings.Contains(body, "Ask again") || strings.Contains(body, "steps</span>") {
		t.Errorf("stopped page:\n%s", body)
	}
	lacks(t, "the page that was following", page.rest(), "Stopped before the answer was finished.", "</body></html>")
	// Try again asks the same question, in place of the stopped one.
	f.mu.Lock()
	f.gate = nil
	f.mu.Unlock()
	body = h.follow("/ask/retry", askForm()).rest()
	lacks(t, "retried page", body, "<p><strong>Forty-two.</strong></p>", "Ask again")
	f.mu.Lock()
	if len(f.asked) != 2 || f.asked[1] != "Slow one?" || len(f.history[1]) != 0 {
		t.Errorf("asked %q with %+v", f.asked, f.history)
	}
	f.mu.Unlock()
	if strings.Count(body, `<div class="q">`) != 1 || strings.Contains(body, "Stopped before") {
		t.Errorf("the stopped exchange is still there:\n%s", body)
	}
	// Clearing stops a question too.
	f.mu.Lock()
	f.gate = make(chan struct{})
	f.mu.Unlock()
	page = h.follow("/ask", askForm("question", "Another?"))
	page.until("Forty")
	h.post("/ask/clear", askForm())
	page.rest()
	_, body = h.get(h.s.Origin() + askPage())
	if strings.Contains(body, "Another?") || strings.Contains(body, "/ask/stop") {
		t.Errorf("after clearing a running question:\n%s", body)
	}
}

func TestAskProblems(t *testing.T) {
	h, f := askHarness(t)
	f.err = errors.New("claude: Not <logged> in")
	h.enter(askPage())
	body := h.follow("/ask", askForm("question", "q")).rest()
	lacks(t, "failed page", body, "<b>No answer.</b> claude: Not &lt;logged&gt; in", "Try again", `name="question"`)
	// Nothing to ask, or nothing to ask again: nothing happens.
	h.post("/ask/clear", askForm())
	h.post("/ask", askForm("question", "   "))
	h.post("/ask/retry", askForm())
	if _, body = h.post("/ask/stop", askForm()); !strings.Contains(body, "Ask about work…") || len(f.asked) != 1 {
		t.Errorf("asked %q:\n%s", f.asked, body)
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
