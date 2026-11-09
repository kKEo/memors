package ask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeClaude is a script standing in for claude: it records its arguments
// and prints what the test asked for.
func fakeClaude(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestArgsIsolateClaude(t *testing.T) {
	args := Args("/x/mcp.json", "q")
	for _, want := range [][]string{
		{"-p"}, {"--setting-sources", ""}, {"--strict-mcp-config"}, {"--mcp-config", "/x/mcp.json"},
		{"--disable-slash-commands"}, {"--tools", ""}, {"--allowedTools", "mcp__memors__*"}, {"--no-session-persistence"},
	} {
		if i := slices.Index(args, want[0]); i < 0 || (len(want) > 1 && (i+1 >= len(args) || args[i+1] != want[1])) {
			t.Errorf("args lack %q: %q", want, args)
		}
	}
	if args[len(args)-1] != "q" {
		t.Errorf("the prompt must come last: %q", args)
	}
}

func TestPrompt(t *testing.T) {
	if got := Prompt("q", nil); got != "q" {
		t.Errorf("first question = %q", got)
	}
	got := Prompt("and then?", []Exchange{{Question: "what?", Answer: "this."}})
	for _, want := range []string{"Q: what?", "A: this.", "and then?"} {
		if !strings.Contains(got, want) {
			t.Errorf("follow-up prompt lacks %q:\n%s", want, got)
		}
	}
}

func TestMCPConfig(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "http-token")
	if err := os.WriteFile(tok, []byte("s3cret-s3cret-s3cret-s3cret-s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := MCPConfig(Server{URL: "http://127.0.0.1:8765/", Auth: "token", DefaultTokenFile: tok})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]struct {
			Type, URL string
			Headers   map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	m := cfg.Servers["memors"]
	if m.Type != "http" || m.URL != "http://127.0.0.1:8765/mcp" || m.Headers["Authorization"] != "Bearer s3cret-s3cret-s3cret-s3cret-s3cret" {
		t.Errorf("config = %s", b)
	}
	if b, err := MCPConfig(Server{URL: "http://127.0.0.1:1", Auth: "none"}); err != nil || strings.Contains(string(b), "headers") {
		t.Errorf("open server config = %s, %v", b, err)
	}
	if _, err := MCPConfig(Server{URL: "https://box:9", MTLS: true}); err != ErrMTLS {
		t.Errorf("mTLS server: err = %v", err)
	}
}

func TestAsk(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	bin := fakeClaude(t, `printf '%s\n' "$@" > `+log+`
cat "$(pwd)/mcp.json" >> `+log+`
echo '{"result":"The answer.","is_error":false,"num_turns":3,"total_cost_usd":0.01}'`)
	r := &Runner{Configured: func() string { return bin }}
	a, err := r.Ask(context.Background(), Server{URL: "http://127.0.0.1:8765", Auth: "none"}, "why?", nil)
	if err != nil || a.Text != "The answer." || a.Turns != 3 || a.Cost != 0.01 {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	got, _ := os.ReadFile(log)
	if !strings.Contains(string(got), "--strict-mcp-config\n") || !strings.Contains(string(got), `"url":"http://127.0.0.1:8765/mcp"`) || !strings.HasSuffix(strings.TrimSpace(string(got)), `}}}`) {
		t.Errorf("claude saw:\n%s", got)
	}
	if !strings.Contains(string(got), "why?\n") {
		t.Errorf("prompt not passed:\n%s", got)
	}
}

func TestAskErrors(t *testing.T) {
	srv := Server{URL: "http://127.0.0.1:8765"}
	r := &Runner{Configured: func() string { return fakeClaude(t, `echo '{"result":"Not logged in","is_error":true}'`) }}
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("is_error: %v", err)
	}
	r = &Runner{Configured: func() string { return fakeClaude(t, `echo "boom" >&2; exit 2`) }}
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("exit 2: %v", err)
	}
	r = &Runner{Configured: func() string { return filepath.Join(t.TempDir(), "missing") }}
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "claude_binary") {
		t.Errorf("missing binary: %v", err)
	}
}
