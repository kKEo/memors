package ask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	args := Args("/x/mcp.json", "q", Options{})
	for _, want := range [][]string{
		{"-p"}, {"--setting-sources", ""}, {"--strict-mcp-config"}, {"--mcp-config", "/x/mcp.json"},
		{"--disable-slash-commands"}, {"--tools", ""}, {"--allowedTools", "mcp__memors__*"}, {"--no-session-persistence"}, {"--max-turns", "12"},
	} {
		if i := slices.Index(args, want[0]); i < 0 || (len(want) > 1 && (i+1 >= len(args) || args[i+1] != want[1])) {
			t.Errorf("args lack %q: %q", want, args)
		}
	}
	if args[len(args)-1] != "q" {
		t.Errorf("the prompt must come last: %q", args)
	}
	for _, flag := range []string{"--model", "--effort", "--max-budget-usd"} {
		if slices.Contains(args, flag) {
			t.Errorf("default args carry %s: %q", flag, args)
		}
	}
}

func TestArgsFromOptions(t *testing.T) {
	args := Args("/x/mcp.json", "q", Options{Model: "sonnet", Effort: "low", MaxTurns: 5, MaxBudgetUSD: 0.5, Instructions: " -- answer in Polish\n"})
	for _, want := range [][]string{
		{"--model", "sonnet"}, {"--effort", "low"}, {"--max-turns", "5"}, {"--max-budget-usd", "0.5"},
		{"--append-system-prompt=-- answer in Polish"},
		{"--setting-sources", ""}, {"--strict-mcp-config"}, {"--tools", ""}, // still isolated
	} {
		if i := slices.Index(args, want[0]); i < 0 || (len(want) > 1 && (i+1 >= len(args) || args[i+1] != want[1])) {
			t.Errorf("args lack %q: %q", want, args)
		}
	}
	if args[len(args)-1] != "q" {
		t.Errorf("the prompt must come last: %q", args)
	}
}

func TestEnv(t *testing.T) {
	base := []string{"PATH=/bin", "ANTHROPIC_API_KEY=old", "CLAUDE_CODE_OAUTH_TOKEN=tok", "ANTHROPIC_AUTH_TOKEN=gw", "CLAUDE_CODE_USE_VERTEX=1", "AWS_REGION=us-east-1"}
	if got := Env(base, Options{}); !slices.Equal(got, base) {
		t.Errorf("no sign-in chosen: the environment must pass as it is, got %q", got)
	}
	for _, c := range []struct {
		o    Options
		want []string
	}{
		{Options{Auth: "api_key", Secret: "sk-new"}, []string{"PATH=/bin", "AWS_REGION=us-east-1", "ANTHROPIC_API_KEY=sk-new"}},
		{Options{Auth: "oauth_token", Secret: "oat"}, []string{"PATH=/bin", "AWS_REGION=us-east-1", "CLAUDE_CODE_OAUTH_TOKEN=oat"}},
		{Options{Auth: "bedrock", Env: map[string]string{"AWS_REGION": "eu-central-1", "AWS_PROFILE": "work"}},
			[]string{"PATH=/bin", "AWS_PROFILE=work", "AWS_REGION=eu-central-1", "CLAUDE_CODE_USE_BEDROCK=1"}},
		// The extra environment wins, even over what the sign-in removes.
		{Options{Auth: "foundry", Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "mine"}},
			[]string{"PATH=/bin", "AWS_REGION=us-east-1", "ANTHROPIC_AUTH_TOKEN=mine", "CLAUDE_CODE_USE_FOUNDRY=1"}},
		{Options{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw"}}, append(slices.Clone(base), "ANTHROPIC_BASE_URL=https://gw")},
	} {
		if got := Env(base, c.o); !slices.Equal(got, c.want) {
			t.Errorf("Env(%+v)\n got %q\nwant %q", c.o, got, c.want)
		}
	}
}

func TestSecrets(t *testing.T) {
	home := t.TempDir()
	if HasSecret(home, "api_key") || SecretFile(home, "bedrock") != "" || SecretFile(home, "") != "" {
		t.Fatal("a fresh home has no secrets, and only keys and tokens have files")
	}
	for _, bad := range []string{"", "two words", "line\nbreak"} {
		if err := StoreSecret(home, "api_key", bad); err == nil {
			t.Errorf("stored %q", bad)
		}
	}
	if err := StoreSecret(home, "bedrock", "x"); err == nil {
		t.Error("stored a secret for a sign-in that takes none")
	}
	if err := StoreSecret(home, "api_key", "sk-ant-1"); err != nil {
		t.Fatal(err)
	}
	if err := StoreSecret(home, "oauth_token", "oat-1"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(SecretFile(home, "api_key"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, %v", fi, err)
	}
	// Load hands the secret of the configured sign-in to the runner.
	cfg := `{"claude_binary":"/x/claude","ask":{"auth":"api_key","model":"haiku","effort":"low","max_turns":4,"timeout_seconds":30,"max_budget_usd":0.25,"instructions":"Briefly.","env":{"HTTPS_PROXY":"http://p:1"}}}`
	if err := os.WriteFile(filepath.Join(home, "tray.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if o.Binary != "/x/claude" || o.Auth != "api_key" || o.Secret != "sk-ant-1" || o.Model != "haiku" || o.Effort != "low" || o.MaxTurns != 4 ||
		o.Timeout != 30*time.Second || o.MaxBudgetUSD != 0.25 || o.Instructions != "Briefly." || o.Env["HTTPS_PROXY"] != "http://p:1" {
		t.Errorf("options = %+v", o)
	}
	// Forgetting keeps only the secret in use.
	if err := ForgetSecrets(home, "api_key"); err != nil || !HasSecret(home, "api_key") || HasSecret(home, "oauth_token") {
		t.Errorf("after forgetting the others: %v", err)
	}
	if err := ForgetSecrets(home, ""); err != nil || HasSecret(home, "api_key") {
		t.Errorf("after forgetting all: %v", err)
	}
	if _, err := Load(home); err == nil || !strings.Contains(err.Error(), "enter it in Settings") {
		t.Errorf("a sign-in without its key: %v", err)
	}
	if o, err := Load(t.TempDir()); err != nil || o.Auth != "" || o.Secret != "" {
		t.Errorf("no tray.json: %+v, %v", o, err)
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
	bin := fakeClaude(t, `echo "key=$ANTHROPIC_API_KEY extra=$ASK_EXTRA" > `+log+`
printf '%s\n' "$@" >> `+log+`
cat "$(pwd)/mcp.json" >> `+log+`
echo '{"result":"The answer.","is_error":false,"num_turns":3,"total_cost_usd":0.01}'`)
	r := &Runner{Env: []string{"PATH=" + os.Getenv("PATH"), "ANTHROPIC_API_KEY=inherited"}, Options: func() (Options, error) {
		return Options{Binary: bin, Auth: "api_key", Secret: "sk-chosen", Model: "sonnet", Env: map[string]string{"ASK_EXTRA": "yes"}}, nil
	}}
	a, err := r.Ask(context.Background(), Server{URL: "http://127.0.0.1:8765", Auth: "none"}, "why?", nil)
	if err != nil || a.Text != "The answer." || a.Turns != 3 || a.Cost != 0.01 {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	got, _ := os.ReadFile(log)
	for _, want := range []string{"key=sk-chosen extra=yes\n", "--model\nsonnet\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("claude did not see %q:\n%s", want, got)
		}
	}
	if !strings.Contains(string(got), "--strict-mcp-config\n") || !strings.Contains(string(got), `"url":"http://127.0.0.1:8765/mcp"`) || !strings.HasSuffix(strings.TrimSpace(string(got)), `}}}`) {
		t.Errorf("claude saw:\n%s", got)
	}
	if !strings.Contains(string(got), "why?\n") {
		t.Errorf("prompt not passed:\n%s", got)
	}
}

func TestAskErrors(t *testing.T) {
	srv := Server{URL: "http://127.0.0.1:8765"}
	run := func(bin string) *Runner {
		return &Runner{Options: func() (Options, error) { return Options{Binary: bin}, nil }}
	}
	r := run(fakeClaude(t, `echo '{"result":"Not logged in","is_error":true}'`))
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("is_error: %v", err)
	}
	r = run(fakeClaude(t, `echo "boom" >&2; exit 2`))
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("exit 2: %v", err)
	}
	r = run(filepath.Join(t.TempDir(), "missing"))
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "claude_binary") {
		t.Errorf("missing binary: %v", err)
	}
	// The time limit is the configured one.
	r = &Runner{Options: func() (Options, error) {
		return Options{Binary: fakeClaude(t, `exec sleep 30`), Timeout: 200 * time.Millisecond}, nil
	}}
	if _, err := r.Ask(context.Background(), srv, "q", nil); err == nil || !strings.Contains(err.Error(), "no answer within 200ms") {
		t.Errorf("timeout: %v", err)
	}
	// Options that cannot be read stop the question.
	r = &Runner{Options: func() (Options, error) { return Options{}, os.ErrPermission }}
	if _, err := r.Ask(context.Background(), srv, "q", nil); err != os.ErrPermission {
		t.Errorf("unreadable options: %v", err)
	}
}
