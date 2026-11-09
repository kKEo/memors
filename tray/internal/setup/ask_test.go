package setup

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kKEo/memors/tray/internal/ask"
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
