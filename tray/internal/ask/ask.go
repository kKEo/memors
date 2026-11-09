// Package ask answers a question about a knowledge base by running Claude
// Code headless (claude -p) with nothing but that knowledge base's MemoRS
// MCP server attached: no settings, plugins, skills, hooks or built-in
// tools, so the answer comes from the knowledge base alone.
package ask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/httpauth"
)

// ErrNotFound means no claude executable was found.
var ErrNotFound = errors.New("Claude Code (claude) not found: install it from https://claude.com/claude-code, or set claude_binary in tray.json")

// ErrMTLS: claude cannot present a client certificate from here.
var ErrMTLS = errors.New("this server asks for a client certificate (mTLS), which Claude Code cannot present from here")

// Timeout bounds one question.
const Timeout = 3 * time.Minute

// Server is the MemoRS server to ask.
type Server struct {
	URL              string // base URL, no /mcp
	Auth             string // none or token
	TokenFile        string // the server's token file; empty means DefaultTokenFile
	DefaultTokenFile string
	MTLS             bool
}

// Exchange is one earlier question and its answer; they give a follow-up
// question its context.
type Exchange struct {
	Question, Answer string
}

// Answer is what claude said.
type Answer struct {
	Text  string
	Turns int
	Cost  float64 // USD, as claude reports it
	Took  time.Duration
}

// Binary returns the claude to run: the configured path, else one on PATH
// or in a usual install directory (an app started from Finder gets a
// minimal PATH).
func Binary(configured string) (string, error) {
	userHome, _ := os.UserHomeDir()
	if configured != "" {
		p := expand(configured, userHome)
		if !executable(p) {
			return "", errors.New("claude_binary in tray.json is not an executable: " + configured)
		}
		return p, nil
	}
	var candidates []string
	if p, err := exec.LookPath("claude"); err == nil {
		candidates = append(candidates, p)
	}
	for _, dir := range []string{"~/.local/bin", "~/.claude/local", "/opt/homebrew/bin", "/usr/local/bin", "~/.npm-global/bin", "~/.bun/bin", "~/.volta/bin"} {
		candidates = append(candidates, filepath.Join(expand(dir, userHome), "claude"))
	}
	for _, p := range candidates {
		if executable(p) {
			if abs, err := filepath.Abs(p); err == nil {
				return abs, nil
			}
			return p, nil
		}
	}
	return "", ErrNotFound
}

// SystemPrompt tells claude what it is for.
const SystemPrompt = `You answer questions about one knowledge base, served by the "memors" MCP server. ` +
	`Always look in the knowledge base first with its tools (search, explore, read, status) and answer from what you find; ` +
	`never answer from general knowledge alone. When the knowledge base has nothing on the topic, say so plainly. ` +
	`Be concise and concrete, in plain text with at most light Markdown; mention memo:// identifiers when they help the reader find the source.`

// Args is the command line (after the executable) that asks prompt with
// the MCP configuration file at mcpConfig and nothing else attached.
func Args(mcpConfig, prompt string) []string {
	return []string{
		"-p", "--output-format", "json",
		"--setting-sources", "", // no user or project settings: no plugins, hooks or MCP servers from them
		"--strict-mcp-config", "--mcp-config", mcpConfig,
		"--disable-slash-commands", // no skills or commands
		"--tools", "",              // no built-in tools
		"--allowedTools", "mcp__memors__*",
		"--no-session-persistence",
		"--max-turns", "12",
		"--system-prompt", SystemPrompt,
		prompt,
	}
}

// Prompt is the question with the earlier exchanges in front of it.
func Prompt(question string, history []Exchange) string {
	if len(history) == 0 {
		return question
	}
	var b strings.Builder
	b.WriteString("Earlier in this conversation (for context; answer only the last question):\n\n")
	for _, e := range history {
		fmt.Fprintf(&b, "Q: %s\nA: %s\n\n", strings.TrimSpace(e.Question), strings.TrimSpace(e.Answer))
	}
	b.WriteString("The question to answer now:\n")
	b.WriteString(question)
	return b.String()
}

// MCPConfig is the --mcp-config content for srv.
func MCPConfig(srv Server) ([]byte, error) {
	if srv.MTLS {
		return nil, ErrMTLS
	}
	entry := map[string]any{"type": "http", "url": strings.TrimSuffix(srv.URL, "/") + "/mcp"}
	if srv.Auth == "token" {
		file := srv.TokenFile
		if file == "" {
			file = srv.DefaultTokenFile
		}
		tok, err := httpauth.Load(file)
		if err != nil {
			return nil, fmt.Errorf("read the server's token: %w", err)
		}
		entry["headers"] = map[string]string{"Authorization": "Bearer " + tok}
	}
	return json.Marshal(map[string]any{"mcpServers": map[string]any{"memors": entry}})
}

// Runner runs claude.
type Runner struct {
	// Configured is claude_binary from tray.json (may be empty).
	Configured func() string
	Env        []string // the process environment; nil means this process's
	Logf       func(format string, args ...any)
}

// Ask runs one question against srv. The MCP configuration (with the
// token, if any) goes into a private temporary directory that is also
// claude's working directory, and is removed afterwards.
func (r *Runner) Ask(ctx context.Context, srv Server, question string, history []Exchange) (Answer, error) {
	bin, err := Binary(r.configured())
	if err != nil {
		return Answer{}, err
	}
	cfg, err := MCPConfig(srv)
	if err != nil {
		return Answer{}, err
	}
	dir, err := os.MkdirTemp("", "memors-ask-")
	if err != nil {
		return Answer{}, err
	}
	defer os.RemoveAll(dir)
	cfgPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		return Answer{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, Args(cfgPath, Prompt(question, history))...)
	cmd.Dir = dir
	cmd.Env = r.Env
	cmd.Stdin = nil // /dev/null: claude must not wait for piped input
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	started := time.Now()
	runErr := cmd.Run()
	took := time.Since(started)
	if r.Logf != nil {
		r.Logf("ask %s: claude took %s (exit %v)", srv.URL, took.Round(time.Millisecond), runErr)
	}
	var res struct {
		Result  string  `json:"result"`
		IsError bool    `json:"is_error"`
		Turns   int     `json:"num_turns"`
		Cost    float64 `json:"total_cost_usd"`
	}
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &res); jsonErr != nil || (res.Result == "" && !res.IsError) {
		if ctx.Err() != nil {
			return Answer{}, fmt.Errorf("claude gave no answer within %s", Timeout)
		}
		detail := strings.TrimSpace(errb.String())
		if detail == "" {
			detail = strings.TrimSpace(out.String())
		}
		if runErr != nil {
			return Answer{}, fmt.Errorf("claude failed: %v%s", runErr, tail(detail))
		}
		return Answer{}, fmt.Errorf("claude gave an unexpected answer%s", tail(detail))
	}
	if res.IsError {
		return Answer{}, fmt.Errorf("claude: %s", strings.TrimSpace(res.Result))
	}
	return Answer{Text: strings.TrimSpace(res.Result), Turns: res.Turns, Cost: res.Cost, Took: took}, nil
}

func (r *Runner) configured() string {
	if r.Configured == nil {
		return ""
	}
	return r.Configured()
}

// tail is the last few lines of detail, for an error message.
func tail(detail string) string {
	if detail == "" {
		return ""
	}
	lines := strings.Split(detail, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return ": " + strings.Join(lines, " / ")
}

func expand(p, userHome string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok && userHome != "" {
		return filepath.Join(userHome, rest)
	}
	return p
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
