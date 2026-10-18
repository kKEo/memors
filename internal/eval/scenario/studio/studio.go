// Package studio is the scenario studio: a loopback web server that draws
// the scenario files of one directory as pictures, shows their source with
// validation issues pinned to lines, runs them against a fresh knowledge
// base and paints the outcome onto the picture, and lists what the files
// cover of the catalogue. Files are read on every request, so editing one
// in any editor and reloading is the editing loop for now; the editor that
// comes next will use /api/validate and /api/render.
package studio

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/eval/scenario"
	"github.com/kKEo/memors/internal/eval/scenario/coverage"
	"github.com/kKEo/memors/internal/eval/scenario/diagram"
	"github.com/kKEo/memors/internal/ui"
)

//go:embed templates/*.html
var templateFS embed.FS

const (
	csp    = "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'"
	svgCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(ya?ml|json)$`)

// Server serves one directory of scenario files.
type Server struct {
	dir        string
	catalogue  string
	tmpl       *template.Template
	hosts      map[string]bool
	runTimeout time.Duration
	embedder   embedding.Embedder

	mu   sync.Mutex // runs
	runs map[string]*runResult
	exec sync.Mutex // one run at a time
}

type runResult struct {
	Report  *scenario.Report
	Overlay *diagram.Overlay
	Hash    string
	At      time.Time
	Err     string
}

// Option configures New.
type Option func(*Server)

// WithCatalogue names the catalogue data file the coverage view reads.
func WithCatalogue(path string) Option { return func(s *Server) { s.catalogue = path } }

// WithRunTimeout bounds one run (default 60 s).
func WithRunTimeout(d time.Duration) Option { return func(s *Server) { s.runTimeout = d } }

// WithEmbedder replaces the deterministic hash embedder for runs.
func WithEmbedder(e embedding.Embedder) Option { return func(s *Server) { s.embedder = e } }

// New builds the studio for dir.
func New(dir string, opts ...Option) *Server {
	s := &Server{dir: filepath.Clean(dir), hosts: map[string]bool{}, runs: map[string]*runResult{}, runTimeout: 60 * time.Second}
	for _, o := range opts {
		o(s)
	}
	funcs := template.FuncMap{
		"join":   strings.Join,
		"stepID": stepID,
		"dt":     func(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") },
	}
	s.tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))
	return s
}

// AllowHost accepts a Host header value.
func (s *Server) AllowHost(h string) { s.hosts[strings.ToLower(h)] = true }

// AllowListener accepts the Host header values that reach ln.
func (s *Server) AllowListener(ln net.Listener) {
	for _, h := range ui.ListenerHosts(ln) {
		s.AllowHost(h)
	}
}

// Handler returns the routes behind the guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /s/{name}", s.scenarioPage)
	mux.HandleFunc("GET /s/{name}/diagram.svg", s.scenarioSVG)
	mux.HandleFunc("POST /s/{name}/run", s.runScenario)
	mux.HandleFunc("POST /api/validate", s.apiValidate)
	mux.HandleFunc("POST /api/render", s.apiRender)
	mux.HandleFunc("GET /static/studio.css", s.css)
	return s.guard(mux)
}

// guard checks the Host header (DNS rebinding), refuses cross-site POSTs
// and sets the security headers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.hosts) > 0 && !s.hosts[strings.ToLower(r.Host)] {
			http.Error(w, "unexpected Host header (DNS rebinding defence); open the studio by the address it printed", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || (len(s.hosts) > 0 && !s.hosts[strings.ToLower(u.Host)]) {
					http.Error(w, "cross-origin request refused", http.StatusForbidden)
					return
				}
			}
			if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" && sf != "none" {
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type page struct {
	Title string
	Dir   string
	Data  any
	Error string
}

func (s *Server) render(w http.ResponseWriter, name, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, page{Title: title, Dir: s.dir, Data: data}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) fail(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	s.render(w, "error.html", "Error", err.Error())
}

func (s *Server) css(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = io.WriteString(w, pageCSS+"\n"+diagram.CSS)
}

// file resolves a scenario name to a path inside dir.
func (s *Server) file(name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", os.ErrNotExist
	}
	p := filepath.Join(s.dir, name)
	if filepath.Dir(p) != s.dir {
		return "", os.ErrNotExist
	}
	return p, nil
}

// stepID maps a Lines path to the picture's element id.
func stepID(path string) string {
	var i, j int
	switch {
	case strings.HasPrefix(path, "steps."):
		rest := strings.TrimPrefix(path, "steps.")
		if n, _ := fmt.Sscanf(rest, "%d.parallel.%d", &i, &j); n == 2 {
			return fmt.Sprintf("step-%d-%d", i+1, j+1)
		}
		if n, _ := fmt.Sscanf(rest, "%d", &i); n == 1 {
			return fmt.Sprintf("step-%d", i+1)
		}
	}
	return "header"
}

// --- scenario page ---

type srcLine struct {
	N    int
	Text string
}

type stepRow struct {
	Index, Line        int
	Path, At, Clock    string
	Actor, Op, Bind    string
	Expect, Note, Kind string
}

type runStep struct {
	Index                int
	At, Actor, Op, State string
	Summary, Binds       string
	Failures             []string
}

type runView struct {
	OK    bool
	At    string
	Err   string
	Steps []runStep
}

type scenarioData struct {
	Name, Desc, Decision string
	Covers               []string
	SVG                  template.HTML
	Issues               []scenario.Issue
	Source               []srcLine
	Steps                []stepRow
	ExpandAllHref        string
	CollapseHref         string
	SVGQuery             string
	RunHref              string
	ShowRun              bool
	Run                  *runView
	RunStale             bool
}

func (s *Server) scenarioPage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.file(name)
	if err != nil {
		s.fail(w, http.StatusNotFound, fmt.Errorf("no scenario %q", name))
		return
	}
	src, err := os.ReadFile(path)
	if err != nil {
		s.fail(w, http.StatusNotFound, err)
		return
	}
	expand := r.URL.Query().Get("expand")
	showRun := r.URL.Query().Get("run") == "1"
	href := func(expand string, run bool) string {
		q := url.Values{}
		if expand != "" {
			q.Set("expand", expand)
		}
		if run {
			q.Set("run", "1")
		}
		if len(q) == 0 {
			return "/s/" + name
		}
		return "/s/" + name + "?" + q.Encode()
	}
	data := scenarioData{Name: name, ExpandAllHref: href("all", showRun), CollapseHref: href("", showRun), RunHref: href(expand, true), ShowRun: showRun}
	if q := (url.Values{}); true {
		if expand != "" {
			q.Set("expand", expand)
		}
		if showRun {
			q.Set("run", "1")
		}
		if len(q) > 0 {
			data.SVGQuery = "?" + q.Encode()
		}
	}
	for i, l := range strings.Split(strings.TrimRight(string(src), "\n"), "\n") {
		data.Source = append(data.Source, srcLine{N: i + 1, Text: l})
	}
	sc, err := scenario.ParseLoose(src)
	if err != nil {
		data.Issues = scenario.Issues(err)
		s.render(w, "scenario.html", name, data)
		return
	}
	data.Issues = scenario.Issues(sc.Validate())
	data.Desc = sc.Description
	data.Decision = sc.Decision
	data.Covers = sc.Covers
	opts := diagram.Options{Mode: diagram.Inline, Expand: diagram.ParseExpand(expand), Issues: data.Issues,
		ExpandHref: func(ids []string) string { return href(strings.Join(ids, ","), showRun) }}
	run := s.lastRun(name)
	if run != nil {
		data.Run = runViewOf(run)
		data.RunStale = run.Hash != hashOf(src)
		if showRun && run.Overlay != nil {
			opts.Overlay = run.Overlay
		}
	}
	svg, err := diagram.Render(sc, opts)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	data.SVG = template.HTML(svg) //nolint:gosec // the renderer escapes everything it writes
	data.Steps = stepRows(sc)
	s.render(w, "scenario.html", name, data)
}

func stepRows(sc *scenario.Scenario) []stepRow {
	var rows []stepRow
	start, _ := sc.ClockStart()
	var cur time.Duration
	add := func(i, j int, st scenario.Step, kind string) {
		path := scenario.StepPath(i, j)
		row := stepRow{Index: i + 1, Path: path, Line: sc.LineOf(path), At: st.At, Clock: start.Add(cur).Format("2006-01-02"), Actor: st.Actor, Op: st.Op, Bind: st.Bind, Note: st.Note, Kind: kind}
		if j >= 0 {
			row.Path = path
		}
		row.Expect = strings.Join(coverage.ExpectKinds(st.Expect), ", ")
		rows = append(rows, row)
	}
	for i, st := range sc.Steps {
		if st.At != "" {
			if d, err := scenario.ParseAt(st.At); err == nil {
				cur = d
			}
		}
		if len(st.Parallel) > 0 {
			add(i, -1, st, "parallel")
			for j, sub := range st.Parallel {
				add(i, j, sub, "sub")
			}
			continue
		}
		add(i, -1, st, "")
	}
	return rows
}

func runViewOf(rr *runResult) *runView {
	v := &runView{At: rr.At.Local().Format("15:04:05"), Err: rr.Err}
	if rr.Report == nil {
		return v
	}
	v.OK = rr.Report.OK()
	for _, st := range rr.Report.Steps {
		v.Steps = append(v.Steps, runStepOf(st, ""))
		for _, sub := range st.Sub {
			v.Steps = append(v.Steps, runStepOf(sub, fmt.Sprintf("%d.", st.Index)))
		}
	}
	return v
}

func runStepOf(st scenario.StepReport, prefix string) runStep {
	rs := runStep{Index: st.Index, At: st.At, Actor: st.Actor, Op: prefix + st.Op, State: "pass", Summary: st.Summary}
	if !st.OK() {
		rs.State = "fail"
	}
	rs.Failures = append(rs.Failures, st.Failures...)
	for _, f := range st.Invariants {
		rs.Failures = append(rs.Failures, "invariant "+f)
	}
	if addr := st.Binds[""]; addr != "" {
		rs.Binds = addr
	}
	return rs
}

func (s *Server) scenarioSVG(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.file(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	src, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sc, err := scenario.ParseLoose(src)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	opts := diagram.Options{Mode: diagram.Standalone, Expand: diagram.ParseExpand(r.URL.Query().Get("expand")), Issues: scenario.Issues(sc.Validate())}
	if r.URL.Query().Get("run") == "1" {
		if rr := s.lastRun(name); rr != nil {
			opts.Overlay = rr.Overlay
		}
	}
	svg, err := diagram.Render(sc, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", svgCSP)
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = w.Write(svg)
}

// --- runs ---

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func (s *Server) lastRun(name string) *runResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[name]
}

func (s *Server) runScenario(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.file(name)
	if err != nil {
		s.fail(w, http.StatusNotFound, fmt.Errorf("no scenario %q", name))
		return
	}
	src, err := os.ReadFile(path)
	if err != nil {
		s.fail(w, http.StatusNotFound, err)
		return
	}
	rr := &runResult{Hash: hashOf(src), At: time.Now()}
	sc, err := scenario.Parse(src)
	if err != nil {
		rr.Err = err.Error()
	} else {
		sc.Path = path
		s.exec.Lock()
		ctx, cancel := context.WithTimeout(r.Context(), s.runTimeout)
		rep, err := scenario.Run(ctx, sc, scenario.Options{Trace: true, Embedder: s.embedder})
		cancel()
		s.exec.Unlock()
		if err != nil {
			rr.Err = err.Error()
		} else {
			rr.Report = rep
			rr.Overlay = diagram.OverlayFrom(rep)
		}
	}
	s.mu.Lock()
	s.runs[name] = rr
	s.mu.Unlock()
	http.Redirect(w, r, "/s/"+name+"?run=1", http.StatusSeeOther)
}

// --- API for the editor ---

type apiIssue struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

type apiResult struct {
	OK     bool       `json:"ok"`
	Issues []apiIssue `json:"issues"`
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "body too large or unreadable", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return b, true
}

func toAPI(issues []scenario.Issue) []apiIssue {
	out := make([]apiIssue, 0, len(issues))
	for _, is := range issues {
		out = append(out, apiIssue{Path: is.Path, Line: is.Line, Message: is.Msg})
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) apiValidate(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	sc, err := scenario.ParseLoose(b)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiResult{Issues: toAPI(scenario.Issues(err))})
		return
	}
	issues := scenario.Issues(sc.Validate())
	if len(issues) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, apiResult{Issues: toAPI(issues)})
		return
	}
	writeJSON(w, http.StatusOK, apiResult{OK: true, Issues: []apiIssue{}})
}

func (s *Server) apiRender(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	sc, err := scenario.ParseLoose(b)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiResult{Issues: toAPI(scenario.Issues(err))})
		return
	}
	opts := diagram.Options{Mode: diagram.Inline, Expand: diagram.ParseExpand(r.URL.Query().Get("expand")), Issues: scenario.Issues(sc.Validate())}
	if r.URL.Query().Get("mode") == "standalone" {
		opts.Mode = diagram.Standalone
	}
	svg, err := diagram.Render(sc, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if opts.Mode == diagram.Standalone {
		w.Header().Set("Content-Security-Policy", svgCSP)
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = w.Write(svg)
}

// --- serve command ---

// Command is the `memors-scenario serve` entry point.
func Command(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet(stderr)
	addr := fs.String("addr", "127.0.0.1:0", "loopback address to listen on")
	dir := fs.String("dir", "internal/eval/testdata/scenarios", "directory of scenario files")
	cat := fs.String("catalogue", "", "catalogue data file (default: <dir>/../scenario-catalogue.yaml when it exists)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if st, err := os.Stat(*dir); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "--dir %s: not a directory\n", *dir)
		return 2
	}
	if *cat == "" {
		if p := filepath.Join(*dir, "..", "scenario-catalogue.yaml"); fileExists(p) {
			*cat = p
		}
	}
	srv := New(*dir, WithCatalogue(*cat))
	ln, err := ui.Listen(*addr, false)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	srv.AllowListener(ln)
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(stdout, "scenario studio: http://%s/ (files under %s; ctrl-c to stop)\n", ln.Addr(), *dir)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdownCtx)
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
