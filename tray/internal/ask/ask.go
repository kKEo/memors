// Package ask answers a question about a knowledge base by running Claude
// Code headless (claude -p) with nothing but that knowledge base's MemoRS
// MCP server attached: no settings, plugins, skills, hooks or built-in
// tools, so the answer comes from the knowledge base alone. How claude signs
// in, which model answers and the limits on one question come from the
// "ask" settings in tray.json (Options).
package ask

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/httpauth"
	"github.com/kKEo/memors/tray/internal/config"
)

// ErrNotFound means no claude executable was found.
var ErrNotFound = errors.New("Claude Code (claude) not found: install it from https://claude.com/claude-code, or set claude_binary in tray.json")

// ErrStopped means the question was cancelled before claude answered.
var ErrStopped = errors.New("stopped")

// ErrMTLS: claude cannot present a client certificate from here.
var ErrMTLS = errors.New("this server asks for a client certificate (mTLS), which Claude Code cannot present from here")

// Options is how one question runs: tray.json's "ask" settings, with the
// key or token read from its file. The zero value is Claude Code as it is
// logged in, with its default model.
type Options struct {
	Binary       string // claude_binary; empty means search for it
	Auth         string // "", api_key, oauth_token, bedrock, vertex or foundry
	Secret       string // the API key or OAuth token Auth needs
	Model        string
	Effort       string
	MaxTurns     int           // 0 means config.DefaultAskTurns
	Timeout      time.Duration // 0 means config.DefaultAskTimeout
	MaxBudgetUSD float64       // 0 means no limit
	Instructions string        // added to the end of SystemPrompt
	Env          map[string]string
}

// Load reads the options from tray.json under homeDir, and the key or
// token the chosen sign-in needs from its file.
func Load(homeDir string) (Options, error) {
	cfg, err := config.Load(config.Path(homeDir))
	if err != nil {
		return Options{}, err
	}
	a := cfg.AskSettings()
	o := Options{
		Binary: cfg.ClaudeBinary, Auth: a.Auth, Model: a.Model, Effort: a.Effort, MaxTurns: a.MaxTurns,
		Timeout: time.Duration(a.TimeoutSeconds) * time.Second, MaxBudgetUSD: a.MaxBudgetUSD,
		Instructions: a.Instructions, Env: a.Env,
	}
	if file := SecretFile(homeDir, a.Auth); file != "" {
		if o.Secret, err = readSecret(file); err != nil {
			return Options{}, fmt.Errorf("no %s is stored for Ask: enter it in Settings (%w)", SecretName(a.Auth), err)
		}
	}
	return o, nil
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return config.DefaultAskTimeout * time.Second
	}
	return o.Timeout
}

// SecretFile is where the key or token for a sign-in method is kept,
// readable by the owner only; empty when the method needs none.
func SecretFile(homeDir, auth string) string {
	switch auth {
	case "api_key":
		return filepath.Join(homeDir, "ask-api-key")
	case "oauth_token":
		return filepath.Join(homeDir, "ask-oauth-token")
	}
	return ""
}

// SecretName is what the secret of a sign-in method is called.
func SecretName(auth string) string {
	if auth == "oauth_token" {
		return "token"
	}
	return "API key"
}

// HasSecret reports whether a key or token is stored for auth.
func HasSecret(homeDir, auth string) bool {
	file := SecretFile(homeDir, auth)
	if file == "" {
		return false
	}
	_, err := readSecret(file)
	return err == nil
}

// CheckSecret says what is wrong with a key or token as typed.
func CheckSecret(secret string) error {
	switch {
	case secret == "":
		return errors.New("it is empty")
	case len(secret) > 4096:
		return errors.New("it is too long")
	case strings.ContainsFunc(secret, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return errors.New("it contains spaces or control characters")
	}
	return nil
}

// StoreSecret keeps the key or token for auth, replacing the one stored.
func StoreSecret(homeDir, auth, secret string) error {
	file := SecretFile(homeDir, auth)
	if file == "" {
		return fmt.Errorf("sign-in %q takes no key or token", auth)
	}
	if err := CheckSecret(secret); err != nil {
		return err
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(homeDir, ".ask-*.tmp") // 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// ForgetSecrets deletes the stored keys and tokens, except the one for
// keep: a secret does not outlive the sign-in method that used it.
func ForgetSecrets(homeDir, keep string) error {
	var errs []error
	for _, auth := range config.AskAuths {
		if file := SecretFile(homeDir, auth); file != "" && auth != keep {
			if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func readSecret(file string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(b))
	if err := CheckSecret(secret); err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	return secret, nil
}

// The variables that select how claude signs in. Choosing a sign-in
// method removes the others' from claude's environment, so the choice
// holds whatever MemoRS Tray itself was started with.
var authVars = map[string]string{
	"api_key":     "ANTHROPIC_API_KEY",
	"oauth_token": "CLAUDE_CODE_OAUTH_TOKEN",
	"bedrock":     "CLAUDE_CODE_USE_BEDROCK",
	"vertex":      "CLAUDE_CODE_USE_VERTEX",
	"foundry":     "CLAUDE_CODE_USE_FOUNDRY",
}

// Env is claude's environment: base, changed for o's sign-in method, with
// o.Env on top. With no method chosen base is passed on as it is.
func Env(base []string, o Options) []string {
	set := map[string]string{}
	var drop []string
	if own, ok := authVars[o.Auth]; ok {
		for _, name := range authVars {
			drop = append(drop, name)
		}
		drop = append(drop, "ANTHROPIC_AUTH_TOKEN")
		if o.Secret != "" {
			set[own] = o.Secret
		} else {
			set[own] = "1"
		}
	}
	for k, v := range o.Env {
		set[k] = v
	}
	if len(set) == 0 {
		return base
	}
	env := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[name]; !replaced && !slices.Contains(drop, name) {
			env = append(env, kv)
		}
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		env = append(env, k+"="+set[k])
	}
	return env
}

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
	// Titles names the memo:// addresses the knowledge base's tools
	// returned while claude worked, where a result gave a title.
	Titles map[string]string
}

// Event is something claude did on the way to an answer, reported while
// it works: a knowledge-base tool it called, or the next piece of the text
// it is writing.
type Event struct {
	Tool   string // the tool called (search, read, explore, status); empty for text
	Detail string // what the tool was asked: the query or the address
	Text   string // more of the text claude is writing
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
	`Be concise and concrete. Write Markdown: short paragraphs, and a list, a table or a code block where it makes the answer easier to read. ` +
	`Cite your sources: after a statement, give the memo:// address it comes from, exactly as the tools returned it, so the reader can open it.`

// Args is the command line (after the executable) that asks prompt with
// the MCP configuration file at mcpConfig and nothing else attached, as o
// says. The isolation flags are not configurable.
func Args(mcpConfig, prompt string, o Options) []string {
	turns := o.MaxTurns
	if turns <= 0 {
		turns = config.DefaultAskTurns
	}
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", // one JSON event per line, as claude works
		"--include-partial-messages", // the answer's text as it is written
		"--setting-sources", "",      // no user or project settings: no plugins, hooks or MCP servers from them
		"--strict-mcp-config", "--mcp-config", mcpConfig,
		"--disable-slash-commands", // no skills or commands
		"--tools", "",              // no built-in tools
		"--allowedTools", "mcp__memors__*",
		"--no-session-persistence",
		"--max-turns", strconv.Itoa(turns),
		"--system-prompt", SystemPrompt,
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	if o.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(o.MaxBudgetUSD, 'f', -1, 64))
	}
	if text := strings.TrimSpace(o.Instructions); text != "" {
		args = append(args, "--append-system-prompt="+text) // one argument: the text may start with a dash
	}
	return append(args, prompt)
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
	// Options are read for every question (Load), so a change in Settings
	// applies to the next one; nil means the defaults.
	Options func() (Options, error)
	Env     []string // the process environment; nil means this process's
	Logf    func(format string, args ...any)
}

// Ask runs one question against srv, calling progress (when not nil) for
// each thing claude does meanwhile. Cancelling ctx stops claude; the error
// is then ErrStopped. The MCP configuration (with the token, if any) goes
// into a private temporary directory that is also claude's working
// directory, and is removed afterwards.
func (r *Runner) Ask(ctx context.Context, srv Server, question string, history []Exchange, progress func(Event)) (Answer, error) {
	var o Options
	if r.Options != nil {
		var err error
		if o, err = r.Options(); err != nil {
			return Answer{}, err
		}
	}
	bin, err := Binary(o.Binary)
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
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, o.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, Args(cfgPath, Prompt(question, history), o)...)
	cmd.Dir = dir
	base := r.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = Env(base, o)
	cmd.Stdin = nil                 // /dev/null: claude must not wait for piped input
	cmd.WaitDelay = 2 * time.Second // a child that keeps the pipe open does not hold up the answer
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Answer{}, err
	}
	started := time.Now()
	var res result
	runErr := cmd.Start()
	if runErr == nil {
		res = readStream(stdout, progress)
		runErr = cmd.Wait()
	}
	took := time.Since(started)
	if r.Logf != nil {
		r.Logf("ask %s: claude took %s (exit %v; sign-in %s, model %s)", srv.URL, took.Round(time.Millisecond), runErr, orDefault(o.Auth), orDefault(o.Model))
	}
	if parent.Err() != nil {
		return Answer{}, ErrStopped
	}
	if !res.seen || (res.Result == "" && !res.IsError) {
		if ctx.Err() != nil {
			return Answer{}, fmt.Errorf("claude gave no answer within %s", o.timeout())
		}
		detail := strings.TrimSpace(errb.String())
		if detail == "" {
			detail = strings.TrimSpace(res.other)
		}
		if runErr != nil {
			return Answer{}, fmt.Errorf("claude failed: %v%s", runErr, tail(detail))
		}
		return Answer{}, fmt.Errorf("claude gave an unexpected answer%s", tail(detail))
	}
	if res.IsError {
		return Answer{}, fmt.Errorf("claude: %s", strings.TrimSpace(res.Result))
	}
	return Answer{Text: strings.TrimSpace(res.Result), Turns: res.Turns, Cost: res.Cost, Took: took, Titles: res.titles}, nil
}

// result is the last line of claude's output, and what was gathered on
// the way to it.
type result struct {
	Result  string  `json:"result"`
	IsError bool    `json:"is_error"`
	Turns   int     `json:"num_turns"`
	Cost    float64 `json:"total_cost_usd"`

	seen   bool              // a result line arrived
	titles map[string]string // memo:// address to title, from tool results
	other  string            // output that was not an event, for an error message
}

// streamLine is one event of claude's stream-json output, as far as Ask
// reads it.
type streamLine struct {
	Type  string `json:"type"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"tool_use_result"`
}

// toolPrefix is how claude names the tools of the "memors" MCP server.
const toolPrefix = "mcp__memors__"

// readStream follows claude's output to its end, reporting tool calls and
// text to progress, and returns the result line. A plain JSON result (an
// older claude, or one that failed before it started) is read as well.
func readStream(out io.Reader, progress func(Event)) result {
	var res result
	emit := func(e Event) {
		if progress != nil {
			progress(e)
		}
	}
	partial := false // the current message's text arrived in pieces already
	rd := bufio.NewReaderSize(out, 64<<10)
	for {
		raw, err := rd.ReadBytes('\n')
		if line := bytes.TrimSpace(raw); len(line) > 0 {
			var l streamLine
			if json.Unmarshal(line, &l) != nil {
				if len(res.other) < 4<<10 {
					res.other += string(line) + "\n"
				}
			} else {
				switch l.Type {
				case "stream_event":
					switch {
					case l.Event.Type == "message_start":
						partial = false
					case l.Event.Type == "content_block_delta" && l.Event.Delta.Type == "text_delta" && l.Event.Delta.Text != "":
						partial = true
						emit(Event{Text: l.Event.Delta.Text})
					}
				case "assistant":
					for _, b := range blocks(l.Message.Content) {
						switch b.Type {
						case "tool_use":
							emit(Event{Tool: strings.TrimPrefix(b.Name, toolPrefix), Detail: toolDetail(b.Input)})
						case "text":
							if !partial && b.Text != "" {
								emit(Event{Text: b.Text})
							}
						}
					}
				case "user":
					for _, b := range blocks(l.Message.Content) {
						if b.Type == "tool_result" {
							res.noteTitles(b.Content)
						}
					}
					res.noteTitles(l.ToolUseResult)
				case "result", "":
					titles, other := res.titles, res.other
					if json.Unmarshal(line, &res) == nil {
						res.seen = true
					}
					res.titles, res.other = titles, other
				}
			}
		}
		if err != nil {
			return res
		}
	}
}

// block is one part of a message's content.
type block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
}

func blocks(content json.RawMessage) []block {
	var bs []block
	_ = json.Unmarshal(content, &bs) // a plain string has no blocks
	return bs
}

// toolDetail is the one thing worth showing about a tool call: what was
// searched for, or the address that was read.
func toolDetail(input json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	for _, key := range []string{"query", "uri", "entity", "topic", "name"} {
		if s, ok := in[key].(string); ok && strings.TrimSpace(s) != "" {
			s = strings.Join(strings.Fields(s), " ")
			if r := []rune(s); len(r) > 120 {
				s = string(r[:120]) + "…"
			}
			return s
		}
	}
	return ""
}

// maxTitles bounds the titles kept for one answer.
const maxTitles = 500

// noteTitles remembers the title of every memo:// address in a tool
// result: any object with a "uri" and a "title", however deep, also when
// the result is JSON carried in a text block.
func (r *result) noteTitles(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	r.walkTitles(v, 0)
}

func (r *result) walkTitles(v any, depth int) {
	if depth > 12 {
		return
	}
	switch v := v.(type) {
	case string:
		if t := strings.TrimSpace(v); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			var inner any
			if json.Unmarshal([]byte(t), &inner) == nil {
				r.walkTitles(inner, depth+1)
			}
		}
	case []any:
		for _, e := range v {
			r.walkTitles(e, depth+1)
		}
	case map[string]any:
		uri, _ := v["uri"].(string)
		title, _ := v["title"].(string)
		if title = strings.TrimSpace(title); strings.HasPrefix(uri, "memo://") && title != "" && len(r.titles) < maxTitles {
			if r.titles == nil {
				r.titles = map[string]string{}
			}
			if _, ok := r.titles[uri]; !ok {
				r.titles[uri] = title
			}
		}
		for _, e := range v {
			r.walkTitles(e, depth+1)
		}
	}
}

func orDefault(s string) string {
	if s == "" {
		return "default"
	}
	return s
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
