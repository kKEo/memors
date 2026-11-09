package setup

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kKEo/memors/tray/internal/ask"
	"github.com/kKEo/memors/tray/internal/menu"
)

// The Ask page is a conversation with one running server's knowledge base.
// Each question runs Claude Code headless with only that server's MCP
// attached (package ask); the page reloads every two seconds while it
// thinks. The conversation lives in memory, per server, until the window
// clears it or memors-tray quits.

// maxHistory bounds the exchanges kept, and handed to claude as context.
const maxHistory = 8

// maxQuestion bounds one question, in bytes.
const maxQuestion = 4000

// conversation is one server's Ask window.
type conversation struct {
	history []exchange
	pending string // the question claude is answering now
	since   time.Time
	err     string // the last question's failure, until the next one
}

type exchange struct {
	ask.Exchange
	Took  string
	Turns int
	Asked time.Time
}

type askData struct {
	Server, KB, URL string
	History         []exchange
	Pending         string
	Since           string // how long the pending question has been running
	Err             string
	ClaudeOK        bool
	ClaudeWhy       string
	MTLS            bool
	Copied          int // 1-based index of the answer just copied
}

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

func (s *Server) askPage(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	v, ok := s.askFor(server)
	if !ok {
		s.render(w, "ask_gone.html", page{Title: "Ask the knowledge base"})
		return
	}
	d := askData{Server: server, KB: v.KB, URL: v.URL, MTLS: v.MTLS}
	if copied, err := parseIndex(r.URL.Query().Get("copied")); err == nil {
		d.Copied = copied
	}
	cfg, _, _ := s.loadConfig()
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
	d.History, d.Pending, d.Err = append([]exchange(nil), c.history...), c.pending, c.err
	if c.pending != "" {
		d.Since = time.Since(c.since).Round(time.Second).String()
	}
	s.mu.Unlock()
	refresh := 0
	if d.Pending != "" {
		refresh = 2
	}
	s.render(w, "ask.html", page{Title: "Ask " + v.KB, Refresh: refresh, Data: d})
}

// askQuestion starts answering one question in the background and sends
// the window back to the page, which shows progress until the answer is in.
func (s *Server) askQuestion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	server := r.PostForm.Get("server")
	back := "/ask?" + url.Values{"server": {server}}.Encode()
	v, ok := s.askFor(server)
	if !ok {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	question := strings.TrimSpace(r.PostForm.Get("question"))
	if question == "" {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if len(question) > maxQuestion {
		question = question[:maxQuestion]
	}
	s.mu.Lock()
	c := s.conv(server)
	if c.pending != "" { // one at a time; the page's form is hidden meanwhile
		s.mu.Unlock()
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	c.pending, c.since, c.err = question, time.Now(), ""
	history := make([]ask.Exchange, 0, len(c.history))
	for _, e := range c.history {
		history = append(history, e.Exchange)
	}
	s.mu.Unlock()
	srv := ask.Server{URL: v.URL, Auth: v.Auth, TokenFile: v.TokenFile, DefaultTokenFile: filepath.Join(s.d.Home, "http-token"), MTLS: v.MTLS}
	go s.answer(server, srv, question, history)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) answer(server string, srv ask.Server, question string, history []ask.Exchange) {
	a, err := s.d.Ask(context.Background(), srv, question, history)
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conv(server)
	if c.pending != question { // cleared meanwhile
		return
	}
	c.pending = ""
	if err != nil {
		s.d.Logf("ask %s: %v", srv.URL, err)
		c.err = err.Error()
		return
	}
	c.history = append(c.history, exchange{Exchange: ask.Exchange{Question: question, Answer: a.Text}, Took: a.Took.Round(time.Second).String(), Turns: a.Turns, Asked: time.Now()})
	if len(c.history) > maxHistory {
		c.history = c.history[len(c.history)-maxHistory:]
	}
}

// askClear forgets the conversation (and drops the answer to a pending
// question when it arrives).
func (s *Server) askClear(w http.ResponseWriter, r *http.Request) {
	server := r.FormValue("server")
	s.mu.Lock()
	delete(s.asks, server)
	s.mu.Unlock()
	http.Redirect(w, r, "/ask?"+url.Values{"server": {server}}.Encode(), http.StatusSeeOther)
}

// askCopy puts one answer on the clipboard.
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
	if text != "" {
		if err := s.d.Copy(text); err != nil {
			s.d.Logf("copy: %v", err)
		} else {
			v.Set("copied", r.FormValue("n"))
		}
	}
	http.Redirect(w, r, "/ask?"+v.Encode(), http.StatusSeeOther)
}

// parseIndex reads a 1-based answer number.
func parseIndex(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err == nil && n < 1 {
		err = strconv.ErrRange
	}
	return n, err
}
