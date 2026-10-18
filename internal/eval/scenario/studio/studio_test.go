package studio

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtures = "../../testdata/scenarios"

// newStudio copies the fixtures into a temp dir, adds a broken file and
// returns the handler with the loopback host allowed.
func newStudio(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	files, err := filepath.Glob(filepath.Join(fixtures, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(fixtures, "b1-promote-decline-then-accept.yaml"))
	_ = os.WriteFile(filepath.Join(dir, "x9-broken.yaml"), []byte(strings.Replace(string(b), "bind: f1", "bind: f9", 1)), 0o644)
	s := New(dir, WithCatalogue("../../testdata/scenario-catalogue.yaml"))
	s.AllowHost("127.0.0.1:1")
	return s, s.Handler(), dir
}

type resp struct {
	code int
	hdr  http.Header
	body string
}

func do(t *testing.T, h http.Handler, method, path, host, body string, hdr map[string]string) resp {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return resp{code: res.StatusCode, hdr: res.Header, body: string(b)}
}

func get(t *testing.T, h http.Handler, path string) (int, string, http.Header) {
	t.Helper()
	r := do(t, h, http.MethodGet, path, "127.0.0.1:1", "", nil)
	return r.code, r.body, r.hdr
}

func wellFormed(t *testing.T, svg string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := dec.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
	}
}

func TestIndexListsFilesAndCoverage(t *testing.T) {
	_, h, _ := newStudio(t)
	code, body, hdr := get(t, h, "/")
	if code != 200 || hdr.Get("Content-Security-Policy") != csp {
		t.Fatalf("index: %d %q", code, hdr.Get("Content-Security-Policy"))
	}
	for _, must := range []string{
		`href="/s/b1-promote-decline-then-accept.yaml"`, `href="/s/b2-zombie-fact.yaml"`, `href="/s/c2-two-agents-disagree.yaml"`,
		`href="/s/x9-broken.yaml"`, `<span class="warn">invalid</span>`, "$f1 is not bound",
		`<span class="badge open">open</span>`, `<span class="badge">gap-15</span>`,
		`<code>ingest</code></td><td>b1-promote-decline-then-accept, b2-zombie-fact, c2-two-agents-disagree</td>`,
		`<code>lint</code></td><td><span class="badge na">not a tool</span></td><td>c2-two-agents-disagree</td>`,
		`<code>explore</code></td><td><span class="cell-empty">—</span></td>`,
		`title="Expiry during the run">A1</span>`, `title="A tool-channel revision of a curated source stays curated">gap-1</span>`,
	} {
		if !strings.Contains(body, must) {
			t.Errorf("index lacks %s", must)
		}
	}
	for _, mustNot := range []string{`>B1</span>`, `>B2</span>`, `>C2</span>`, `>gap-15</span>`} {
		if strings.Contains(strings.SplitN(body, "Catalogue entries without a scenario", 2)[1], mustNot) {
			t.Errorf("covered entry listed as uncovered: %s", mustNot)
		}
	}
}

func TestScenarioPage(t *testing.T) {
	_, h, _ := newStudio(t)
	code, body, hdr := get(t, h, "/s/b1-promote-decline-then-accept.yaml")
	if code != 200 || hdr.Get("Content-Security-Policy") != csp {
		t.Fatalf("page: %d", code)
	}
	for _, must := range []string{"<svg ", `id="step-1"`, `data-path="steps.0"`, `id="L7"`, `<a href="/s/b1-promote-decline-then-accept.yaml?expand=step-1-arg-content"><text class="more"`, `href="/s/b1-promote-decline-then-accept.yaml/diagram.svg"`, "<h2>Steps</h2>", `<code>$doc</code>`} {
		if !strings.Contains(body, must) {
			t.Errorf("page lacks %s", must)
		}
	}
	svg := body[strings.Index(body, "<svg "):strings.Index(body, "</svg>")]
	for _, bad := range []string{"<style", "<script", ` style="`} {
		if strings.Contains(svg, bad) {
			t.Errorf("inline svg contains %s", bad)
		}
	}
	// the short form keeps the full text as a tooltip (<title>) but draws only two lines
	if strings.Contains(svg, "before logging.</text>") || !strings.Contains(svg, "before logging.\n</title>") {
		t.Error("the short form should keep the whole content as a tooltip and not draw it")
	}
	code, body, _ = get(t, h, "/s/b1-promote-decline-then-accept.yaml?expand=step-1-arg-content")
	svg = body[strings.Index(body, "<svg "):strings.Index(body, "</svg>")]
	if code != 200 || !strings.Contains(svg, "before logging.</text>") || !strings.Contains(svg, `>less<`) {
		t.Error("expand did not show the full block")
	}
	code, body, _ = get(t, h, "/s/x9-broken.yaml")
	if code != 200 || !strings.Contains(body, `<a href="#L38">line 38</a>`) || !strings.Contains(body, `<a href="#step-3"><code>steps.2.args</code></a>`) || !strings.Contains(body, `class="step invalid" id="step-3"`) {
		t.Errorf("broken file page: %d", code)
	}
}

func TestDiagramSVG(t *testing.T) {
	_, h, _ := newStudio(t)
	code, body, hdr := get(t, h, "/s/c2-two-agents-disagree.yaml/diagram.svg?expand=all")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "image/svg+xml") || hdr.Get("Content-Security-Policy") != svgCSP {
		t.Fatalf("svg: %d %q %q", code, hdr.Get("Content-Type"), hdr.Get("Content-Security-Policy"))
	}
	wellFormed(t, body)
	if !strings.Contains(body, "a majority of an odd count; five is the production setting") {
		t.Error("expand=all did not expand")
	}
	code, body, _ = get(t, h, "/s/b2-zombie-fact.yaml/diagram.svg")
	if code != 200 || !strings.Contains(body, "<script><![CDATA[") {
		t.Error("standalone svg should carry its script when something is truncated")
	}
}

func TestGuard(t *testing.T) {
	_, h, _ := newStudio(t)
	if res := do(t, h, http.MethodGet, "/", "evil.example:1", "", nil); res.code != 403 {
		t.Errorf("foreign host: %d", res.code)
	}
	if res := do(t, h, http.MethodPost, "/s/b2-zombie-fact.yaml/run", "127.0.0.1:1", "", map[string]string{"Origin": "http://evil.example"}); res.code != 403 {
		t.Errorf("foreign origin: %d", res.code)
	}
	if res := do(t, h, http.MethodPost, "/s/b2-zombie-fact.yaml/run", "127.0.0.1:1", "", map[string]string{"Sec-Fetch-Site": "cross-site"}); res.code != 403 {
		t.Errorf("cross-site: %d", res.code)
	}
	if res := do(t, h, http.MethodPost, "/", "127.0.0.1:1", "", nil); res.code != 405 {
		t.Errorf("POST /: %d", res.code)
	}
	for _, p := range []string{"/s/nope.yaml", "/s/..%2Fscenario-catalogue.yaml", "/s/.hidden.yaml", "/s/notes.txt", "/s/nope.yaml/diagram.svg"} {
		if code, _, _ := get(t, h, p); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if res := do(t, h, http.MethodPost, "/s/nope.yaml/run", "127.0.0.1:1", "", nil); res.code != 404 {
		t.Errorf("run missing: %d", res.code)
	}
}

func TestRunOverlay(t *testing.T) {
	_, h, dir := newStudio(t)
	res := do(t, h, http.MethodPost, "/s/b1-promote-decline-then-accept.yaml/run", "127.0.0.1:1", "", map[string]string{"Origin": "http://127.0.0.1:1", "Sec-Fetch-Site": "same-origin"})
	if res.code != 303 || res.hdr.Get("Location") != "/s/b1-promote-decline-then-accept.yaml?run=1" {
		t.Fatalf("run: %d %q", res.code, res.hdr.Get("Location"))
	}
	code, body, _ := get(t, h, res.hdr.Get("Location"))
	if code != 200 {
		t.Fatalf("after run: %d", code)
	}
	for _, must := range []string{`class="step pass" id="step-1"`, `class="step pass" id="step-8"`, "$doc → memo://doc/", "state: docs 1 (+1)", "<h2>Last run</h2>", `<span class="ok">pass</span>`} {
		if !strings.Contains(body, must) {
			t.Errorf("run page lacks %s", must)
		}
	}
	if strings.Contains(body, "the file changed since") {
		t.Error("fresh run marked stale")
	}
	// the standalone picture carries the overlay too
	_, svg, _ := get(t, h, "/s/b1-promote-decline-then-accept.yaml/diagram.svg?run=1")
	if !strings.Contains(svg, `class="step pass" id="step-1"`) {
		t.Error("diagram.svg?run=1 lacks the overlay")
	}
	// a changed file marks the run stale
	p := filepath.Join(dir, "b1-promote-decline-then-accept.yaml")
	b, _ := os.ReadFile(p)
	_ = os.WriteFile(p, append(b, '\n'), 0o644)
	_, body, _ = get(t, h, "/s/b1-promote-decline-then-accept.yaml")
	if !strings.Contains(body, "the file changed since") || !strings.Contains(body, "show it on the picture") {
		t.Error("stale run not flagged")
	}
	// an invalid file reports the error instead of running
	res = do(t, h, http.MethodPost, "/s/x9-broken.yaml/run", "127.0.0.1:1", "", nil)
	if res.code != 303 {
		t.Fatalf("run broken: %d", res.code)
	}
	_, body, _ = get(t, h, "/s/x9-broken.yaml?run=1")
	if !strings.Contains(body, "run error:") || !strings.Contains(body, "$f1 is not bound") {
		t.Error("broken run should report the validation error")
	}
}

func TestAPI(t *testing.T) {
	_, h, _ := newStudio(t)
	res := do(t, h, http.MethodPost, "/api/validate", "127.0.0.1:1", "name: x\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: a, op: status, expects: {}}\n", nil)
	var out struct {
		OK     bool
		Issues []struct {
			Path    string
			Line    int
			Message string
		}
	}
	if err := json.Unmarshal([]byte(res.body), &out); err != nil || res.code != 422 || out.OK || len(out.Issues) != 1 || out.Issues[0].Line != 4 {
		t.Fatalf("validate unknown field: %d %v %+v", res.code, err, out)
	}
	res = do(t, h, http.MethodPost, "/api/validate", "127.0.0.1:1", "name: x\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: a, op: read, args: {uri: $nope}}\n", nil)
	_ = json.Unmarshal([]byte(res.body), &out)
	if res.code != 422 || len(out.Issues) != 1 || out.Issues[0].Path != "steps.0.args" || out.Issues[0].Line != 4 {
		t.Fatalf("validate unbound: %d %+v", res.code, out)
	}
	res = do(t, h, http.MethodPost, "/api/validate", "127.0.0.1:1", "name: x\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: a, op: status}\n", nil)
	_ = json.Unmarshal([]byte(res.body), &out)
	if res.code != 200 || !out.OK {
		t.Fatalf("validate ok: %d %+v", res.code, out)
	}
	res = do(t, h, http.MethodPost, "/api/render?expand=all", "127.0.0.1:1", "name: x\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: a, op: read, args: {uri: $nope}}\n", nil)
	if res.code != 200 || !strings.HasPrefix(res.hdr.Get("Content-Type"), "image/svg+xml") || !strings.Contains(res.body, "⚠ step 1: $nope is not bound") {
		t.Fatalf("render with issues: %d %s", res.code, res.body[:min(len(res.body), 200)])
	}
	wellFormed(t, res.body)
	res = do(t, h, http.MethodPost, "/api/render", "127.0.0.1:1", "name: [", nil)
	if res.code != 422 {
		t.Errorf("render garbage: %d", res.code)
	}
	res = do(t, h, http.MethodPost, "/api/validate", "127.0.0.1:1", strings.Repeat("x", 1<<20+1), nil)
	if res.code != 413 {
		t.Errorf("oversize body: %d", res.code)
	}
	if code, body, _ := get(t, h, "/static/studio.css"); code != 200 || !strings.Contains(body, ".popover") || !strings.Contains(body, "pre.source") {
		t.Error("stylesheet")
	}
}

func TestStepID(t *testing.T) {
	for in, want := range map[string]string{"steps.2": "step-3", "steps.2.args": "step-3", "steps.4.parallel.1": "step-5-2", "steps.4.parallel.1.op": "step-5-2", "name": "header", "actors.a": "header"} {
		if got := stepID(in); got != want {
			t.Errorf("stepID(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestCommandRefusesBadDir(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Command(t.Context(), []string{"--dir", filepath.Join(t.TempDir(), "nope")}, &out, &errb); code != 2 {
		t.Errorf("bad dir: %d", code)
	}
}
