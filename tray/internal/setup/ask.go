package setup

import (
	"context"
	"errors"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/memomd"
	"github.com/kKEo/memors/tray/internal/ask"
	"github.com/kKEo/memors/tray/internal/menu"
)

// The Ask page is a conversation with one running server's knowledge base.
// Each question runs Claude Code headless with only that server's MCP
// attached (package ask). The page has no script, so a question that is
// being answered is shown by not finishing the response: the page is sent
// as far as the question, then each thing claude does is appended as it
// happens, then the answer, then the box for the next question. Reloading
// meanwhile replays what has happened and carries on. The conversation
// lives in memory, per server, until the window clears it or memors-tray
// quits.

// maxHistory bounds the exchanges kept, and handed to claude as context.
const maxHistory = 8

// maxQuestion bounds one question, in bytes.
const maxQuestion = 4000

// maxEvents bounds what is kept of one answer in the making.
const maxEvents = 4000

// conversation is one server's Ask window.
type conversation struct {
	history []exchange
	live    *liveTurn // the question claude is answering now
}

// exchange is a question and what came of it: an answer, a failure, or
// what there was when it was stopped.
type exchange struct {
	ask.Exchange               // Answer is markdown, as claude wrote it
	HTML         template.HTML // the answer, rendered
	Sources      []source      // the memories the answer cites
	Steps        []step        // what claude did in the knowledge base
	Took         string
	Turns        int
	Err          string // why there is no answer
	Stopped      bool   // stopped from the window; Answer is what there was
}

// Answered reports whether the exchange is context for a follow-up.
func (e exchange) Answered() bool { return e.Err == "" && !e.Stopped }

// source is a memory an answer cites.
type source struct {
	URI, Title, Href string
}

// Label is what the source's link says.
func (s source) Label() string {
	if s.Title != "" {
		return s.Title
	}
	return strings.TrimPrefix(s.URI, "memo://")
}

// step is one call to a knowledge-base tool.
type step struct {
	Tool, Detail string
}

// Verb says what the step does; Detail, when there is one, follows it.
func (s step) Verb() string {
	switch s.Tool {
	case "search":
		if s.Detail == "" {
			return "Searching the knowledge base"
		}
		return "Searching for"
	case "read":
		if s.Detail == "" {
			return "Reading a memory"
		}
		return "Reading"
	case "explore":
		if s.Detail == "" {
			return "Exploring connections"
		}
		return "Exploring"
	case "status":
		return "Checking what the knowledge base holds"
	}
	return "Using " + s.Tool
}

// Quoted reports whether Detail is words (a search), not an address.
func (s step) Quoted() bool { return s.Tool == "search" }

// liveTurn is a question on its way to an answer.
type liveTurn struct {
	question string
	cancel   context.CancelFunc
	events   []ask.Event
	changed  chan struct{} // closed, and replaced, at every event
	done     chan struct{} // closed once the exchange is in the history
}

// starters are the questions an empty conversation offers; any knowledge
// base can answer them.
var starters = []string{
	"What is this knowledge base about?",
	"What was added most recently?",
	"Which topics have the most material?",
}

type askData struct {
	Server, KB string
	History    []exchange
	Live       string // the question being answered
	Starters   []string
	ClaudeOK   bool
	ClaudeWhy  string
	MTLS       bool
	Copied     int    // 1-based index of the answer just copied
	Setup      string // how Claude Code runs: sign-in, model
}

// CanAsk reports whether the page offers the question box.
func (d askData) CanAsk() bool { return d.ClaudeOK && !d.MTLS }

// Last is the 1-based index of the newest exchange.
func (d askData) Last() int { return len(d.History) }

// askFor describes server as the Ask page needs it; false when the
// server is not running (any more).
func (s *Server) askFor(server string) (menu.ServerView, bool) {
	if server == "" || s.d.Connection == nil || s.d.Ask == nil {
		return menu.ServerView{}, false
	}
	v, ok := s.d.Connection(server)
	if !ok || v.URL == "" {
		return menu.ServerView{}, false
	}
	return v, true
}

// conv returns server's conversation, creating it. Call with s.mu held.
func (s *Server) conv(server string) *conversation {
	if s.asks == nil {
		s.asks = map[string]*conversation{}
	}
	c := s.asks[server]
	if c == nil {
		c = &conversation{}
		s.asks[server] = c
	}
	return c
}

func askURL(server string) string { return "/ask?" + url.Values{"server": {server}}.Encode() }

func (s *Server) askPage(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	v, ok := s.askFor(server)
	if !ok {
		s.render(w, "ask_gone.html", page{Title: "Ask the knowledge base"})
		return
	}
	d := askData{Server: server, KB: v.KB, MTLS: v.MTLS, Starters: starters}
	if copied, err := parseIndex(r.URL.Query().Get("copied")); err == nil {
		d.Copied = copied
	}
	cfg, _, _ := s.loadConfig()
	d.Setup = askSummary(cfg.AskSettings())
	if s.d.FindClaude != nil {
		if _, err := s.d.FindClaude(cfg.ClaudeBinary); err != nil {
			d.ClaudeWhy = err.Error()
		} else {
			d.ClaudeOK = true
		}
	} else {
		d.ClaudeOK = true
	}
	s.mu.Lock()
	c := s.conv(server)
	lt := c.live
	d.History = append([]exchange(nil), c.history...)
	if lt != nil {
		d.Live = lt.question
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p := page{Title: "Ask " + v.KB, Data: d}
	if !s.part(w, "ask_top", p) {
		return
	}
	if lt != nil {
		if !s.follow(w, r, lt) {
			return // the window went away; claude carries on
		}
		s.mu.Lock()
		d.History, d.Live = nil, ""
		if cur := s.asks[server]; cur == c && c.live != lt && len(c.history) > 0 {
			d.History = append([]exchange(nil), c.history...)
		}
		s.mu.Unlock()
		p.Data = d
		if !s.part(w, "ask_landed", p) {
			return
		}
	}
	s.part(w, "ask_bottom", p)
}

// part writes one piece of the page.
func (s *Server) part(w io.Writer, name string, p page) bool {
	if err := s.tmpl.ExecuteTemplate(w, name, p); err != nil {
		s.d.Logf("render %s: %v", name, err)
		return false
	}
	return true
}

// follow appends what claude does for lt to the page as it happens, and
// returns once the turn is over; false when the window stopped reading.
func (s *Server) follow(w http.ResponseWriter, r *http.Request, lt *liveTurn) bool {
	rc := http.NewResponseController(w)
	next, draft := 0, false
	for {
		s.mu.Lock()
		events := lt.events[next:]
		changed := lt.changed
		s.mu.Unlock()
		next += len(events)
		var b strings.Builder
		for _, e := range events {
			if e.Tool != "" {
				if draft {
					b.WriteString("</div>\n")
					draft = false
				}
				writeStep(&b, step{Tool: e.Tool, Detail: e.Detail})
				continue
			}
			if !draft {
				b.WriteString(`<div class="draft">`)
				draft = true
			}
			b.WriteString(html.EscapeString(e.Text))
		}
		if b.Len() > 0 {
			if _, err := io.WriteString(w, b.String()); err != nil {
				return false
			}
		}
		select {
		case <-lt.done:
			s.mu.Lock()
			rest := len(lt.events) - next
			s.mu.Unlock()
			if rest > 0 {
				continue // write what arrived last, then finish
			}
			if draft {
				if _, err := io.WriteString(w, "</div>\n"); err != nil {
					return false
				}
			}
			return true
		default:
		}
		if err := rc.Flush(); err != nil {
			return false
		}
		select {
		case <-changed:
		case <-lt.done:
		case <-r.Context().Done():
			return false
		}
	}
}

func writeStep(b *strings.Builder, st step) {
	b.WriteString(`<div class="step"><span>` + html.EscapeString(st.Verb()))
	if st.Detail != "" {
		tag := "b"
		if st.Quoted() {
			tag = "q"
		}
		b.WriteString(" <" + tag + ` class="what">` + html.EscapeString(st.Detail) + "</" + tag + ">")
	}
	b.WriteString("</span></div>\n")
}

// askQuestion starts answering one question in the background and sends
// the window back to the page, which follows it until the answer is in.
func (s *Server) askQuestion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	server := r.PostForm.Get("server")
	if v, ok := s.askFor(server); ok {
		s.begin(server, v, r.PostForm.Get("question"), false)
	}
	http.Redirect(w, r, askURL(server), http.StatusSeeOther)
}

// askRetry asks the newest question again, in place of what came of it:
// after a failure or a stop, or for another answer.
func (s *Server) askRetry(w http.ResponseWriter, r *http.Request) {
	server := r.FormValue("server")
	if v, ok := s.askFor(server); ok {
		s.begin(server, v, "", true)
	}
	http.Redirect(w, r, askURL(server), http.StatusSeeOther)
}

// begin starts a question, unless one is being answered. With again the
// question is the newest exchange's, which it replaces.
func (s *Server) begin(server string, v menu.ServerView, question string, again bool) {
	s.mu.Lock()
	c := s.conv(server)
	if c.live != nil { // one at a time; the page has no question box meanwhile
		s.mu.Unlock()
		return
	}
	if again {
		if len(c.history) == 0 {
			s.mu.Unlock()
			return
		}
		question = c.history[len(c.history)-1].Question
		c.history = c.history[:len(c.history)-1]
	}
	question = strings.TrimSpace(question)
	if len(question) > maxQuestion {
		question = strings.ToValidUTF8(question[:maxQuestion], "")
	}
	if question == "" {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	lt := &liveTurn{question: question, cancel: cancel, changed: make(chan struct{}), done: make(chan struct{})}
	c.live = lt
	var history []ask.Exchange
	for _, e := range c.history {
		if e.Answered() {
			history = append(history, e.Exchange)
		}
	}
	s.mu.Unlock()
	srv := ask.Server{URL: v.URL, Auth: v.Auth, TokenFile: v.TokenFile, DefaultTokenFile: filepath.Join(s.d.Home, "http-token"), MTLS: v.MTLS}
	go s.answer(ctx, server, c, lt, srv, history)
}

func (s *Server) answer(ctx context.Context, server string, c *conversation, lt *liveTurn, srv ask.Server, history []ask.Exchange) {
	defer lt.cancel()
	a, err := s.d.Ask(ctx, srv, lt.question, history, func(e ask.Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(lt.events) >= maxEvents && e.Tool == "" {
			return
		}
		lt.events = append(lt.events, e)
		close(lt.changed)
		lt.changed = make(chan struct{})
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	defer close(lt.done)
	if s.asks[server] != c || c.live != lt { // cleared meanwhile
		return
	}
	c.live = nil
	ex := exchange{Exchange: ask.Exchange{Question: lt.question}}
	text := ""
	for _, e := range lt.events {
		if e.Tool != "" {
			ex.Steps = append(ex.Steps, step{Tool: e.Tool, Detail: e.Detail})
			text = ""
		} else {
			text += e.Text
		}
	}
	switch {
	case err == nil:
		ex.Took, ex.Turns = a.Took.Round(time.Second).String(), a.Turns
		s.fill(&ex, server, a.Text, a.Titles)
	case errors.Is(err, ask.ErrStopped):
		ex.Stopped = true
		s.fill(&ex, server, strings.TrimSpace(text), nil) // what there was of the answer
	default:
		s.d.Logf("ask %s: %v", srv.URL, err)
		ex.Err = err.Error()
	}
	c.history = append(c.history, ex)
	if len(c.history) > maxHistory {
		c.history = c.history[len(c.history)-maxHistory:]
	}
}

// fill renders an answer and lists the memories it cites, each linked to
// its page on the server.
func (s *Server) fill(ex *exchange, server, text string, titles map[string]string) {
	if text == "" {
		return
	}
	href := func(uri string) string { return memoLink(server, uri) }
	ex.Answer, ex.HTML = text, memomd.Render(text, href)
	for _, uri := range memomd.URIs(text) {
		if h := href(uri); h != "" {
			ex.Sources = append(ex.Sources, source{URI: uri, Title: titles[uri], Href: h})
		}
	}
}

// memoAddr matches the memo:// addresses the server has a page for.
var memoAddr = regexp.MustCompile(`^memo://(doc|chunk|source|fact|entity|page)/([A-Za-z0-9_-]+)$`)

// memoLink is the link that shows a memo:// address in server's own
// window; "" when the server has no page for it.
func memoLink(server, uri string) string {
	if !memoAddr.MatchString(uri) {
		return ""
	}
	return "/ask/open?" + url.Values{"server": {server}, "uri": {uri}}.Encode()
}

// askOpen shows a cited memory in the server's window. It answers with no
// content, so the conversation stays as it is.
func (s *Server) askOpen(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	m := memoAddr.FindStringSubmatch(r.URL.Query().Get("uri"))
	if _, ok := s.askFor(server); ok && m != nil && s.d.OpenMemo != nil {
		s.d.OpenMemo(server, "/"+m[1]+"/"+m[2])
	}
	w.WriteHeader(http.StatusNoContent)
}

// askStop stops the question being answered, and waits a moment for
// claude to go so the page comes back with what there was.
func (s *Server) askStop(w http.ResponseWriter, r *http.Request) {
	server := r.FormValue("server")
	s.mu.Lock()
	var lt *liveTurn
	if c := s.asks[server]; c != nil {
		lt = c.live
	}
	s.mu.Unlock()
	if lt != nil {
		lt.cancel()
		select {
		case <-lt.done:
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}
	http.Redirect(w, r, askURL(server), http.StatusSeeOther)
}

// askClear forgets the conversation, and stops the question being
// answered.
func (s *Server) askClear(w http.ResponseWriter, r *http.Request) {
	server := r.FormValue("server")
	s.mu.Lock()
	if c := s.asks[server]; c != nil && c.live != nil {
		c.live.cancel()
	}
	delete(s.asks, server)
	s.mu.Unlock()
	http.Redirect(w, r, askURL(server), http.StatusSeeOther)
}

// askCopy puts one answer on the clipboard, and comes back to it.
func (s *Server) askCopy(w http.ResponseWriter, r *http.Request) {
	server := r.FormValue("server")
	v := url.Values{"server": {server}}
	i, err := parseIndex(r.FormValue("n"))
	s.mu.Lock()
	c := s.conv(server)
	text := ""
	if err == nil && i >= 1 && i <= len(c.history) {
		text = c.history[i-1].Answer
	}
	s.mu.Unlock()
	back := "/ask?" + v.Encode()
	if text != "" {
		if err := s.d.Copy(text); err != nil {
			s.d.Logf("copy: %v", err)
		} else {
			v.Set("copied", strconv.Itoa(i))
			back = "/ask?" + v.Encode() + "#t" + strconv.Itoa(i)
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// parseIndex reads a 1-based answer number.
func parseIndex(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err == nil && n < 1 {
		err = strconv.ErrRange
	}
	return n, err
}
