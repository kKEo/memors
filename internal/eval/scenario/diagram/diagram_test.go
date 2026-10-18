package diagram

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kKEo/memors/internal/eval/scenario"
)

var update = flag.Bool("update", false, "rewrite the golden SVG files")

const fixtures = "../../testdata/scenarios"

func load(t *testing.T, name string) *scenario.Scenario {
	t.Helper()
	sc, err := scenario.Load(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func inlineOpts(expand string) Options {
	return Options{Mode: Inline, Expand: ParseExpand(expand), ExpandHref: func(ids []string) string { return "?expand=" + strings.Join(ids, ",") }}
}

func render(t *testing.T, sc *scenario.Scenario, opts Options) []byte {
	t.Helper()
	b, err := Render(sc, opts)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wellFormed parses the SVG as XML and returns every id and data-path.
func wellFormed(t *testing.T, svg []byte) (ids, paths []string) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(svg))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return ids, paths
		}
		if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "id":
					ids = append(ids, a.Value)
				case "data-path":
					paths = append(paths, a.Value)
				}
			}
		}
	}
}

func TestGolden(t *testing.T) {
	files, err := scenario.ListDir(fixtures)
	if err != nil || len(files) == 0 {
		t.Fatalf("fixtures: %v %d", err, len(files))
	}
	for _, f := range files {
		sc := load(t, filepath.Base(f))
		base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		for variant, opts := range map[string]Options{"short": {Mode: Standalone}, "inline": inlineOpts("")} {
			got := render(t, sc, opts)
			golden := filepath.Join("testdata", base+"."+variant+".svg")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%s: %v (run with -update)", golden, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from the rendering (run `go test ./internal/eval/scenario/diagram -run TestGolden -update` after a deliberate change)", golden)
			}
		}
	}
}

func TestWellFormedAndContract(t *testing.T) {
	files, _ := scenario.ListDir(fixtures)
	for _, f := range files {
		sc := load(t, filepath.Base(f))
		for name, opts := range map[string]Options{"short": {Mode: Standalone}, "all": {Mode: Standalone, Expand: ParseExpand("all")}, "inline": inlineOpts(""), "inline-all": inlineOpts("all")} {
			svg := render(t, sc, opts)
			ids, paths := wellFormed(t, svg)
			seen := map[string]bool{}
			for _, id := range ids {
				if seen[id] {
					t.Errorf("%s %s: duplicate id %q", f, name, id)
				}
				seen[id] = true
			}
			for _, p := range paths {
				if sc.LineOf(p) == 0 {
					t.Errorf("%s %s: data-path %q resolves to no source line", f, name, p)
				}
			}
			for _, must := range []string{`id="step-1"`, `data-path="steps.0"`, `id="header"`, `role="img"`, `<desc id="d">`} {
				if !strings.Contains(string(svg), must) {
					t.Errorf("%s %s: missing %s", f, name, must)
				}
			}
		}
	}
}

func TestDeterministic(t *testing.T) {
	sc := load(t, "c2-two-agents-disagree.yaml")
	a, b := render(t, sc, Options{Mode: Standalone}), render(t, sc, Options{Mode: Standalone})
	if !bytes.Equal(a, b) {
		t.Fatal("two renderings differ")
	}
	var args, out []string
	for i := 0; i < 12; i++ {
		args = append(args, fmt.Sprintf("k%02d: v%d", i, i))
	}
	for i := 0; i < 6; i++ {
		out = append(out, fmt.Sprintf("results.%d.uri: x", i))
	}
	src := "name: many\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: a, op: search, args: {" + strings.Join(args, ", ") + "}, expect: {out: {" + strings.Join(out, ", ") + "}}}\n"
	syn, err := scenario.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	syn.Lines = nil // no source order: keys must still come out in one order
	for i := 0; i < 5; i++ {
		if x := render(t, syn, Options{}); !bytes.Equal(x, render(t, syn, Options{})) {
			t.Fatal("synthetic rendering is not deterministic")
		}
	}
}

func TestExpandReflows(t *testing.T) {
	sc := load(t, "b1-promote-decline-then-accept.yaml")
	short := Build(sc, inlineOpts(""))
	full := Build(sc, inlineOpts("step-1-arg-content"))
	if full.Height <= short.Height {
		t.Fatalf("expanding did not grow the picture: %d vs %d", full.Height, short.Height)
	}
	delta := full.Rows[0].H - short.Rows[0].H
	if delta <= 0 || full.Rows[0].Boxes[0].Rect.H-short.Rows[0].Boxes[0].Rect.H != delta {
		t.Fatalf("row 1 did not grow with its box: %d", delta)
	}
	for i := 1; i < len(short.Rows); i++ {
		if full.Rows[i].Y-short.Rows[i].Y != delta || full.Rows[i].H != short.Rows[i].H {
			t.Errorf("row %d: shifted by %d (want %d), height %d vs %d", i+1, full.Rows[i].Y-short.Rows[i].Y, delta, full.Rows[i].H, short.Rows[i].H)
		}
	}
	if full.Header.H != short.Header.H || full.Rows[0].Y != short.Rows[0].Y {
		t.Error("the header or the first row moved")
	}
	svg := string(render(t, sc, inlineOpts("step-1-arg-content")))
	if !strings.Contains(svg, ">less<") || !strings.Contains(svg, "id=\"step-1-arg-content\" data-path=\"steps.0.args.content\">\n<title>") {
		t.Error("expanded block should offer less and keep its title")
	}
	if !strings.Contains(svg, "Put the auth interceptor") {
		t.Error("expanded content missing")
	}
}

func TestInlineIsCSPClean(t *testing.T) {
	sc := load(t, "b2-zombie-fact.yaml")
	svg := string(render(t, sc, inlineOpts("")))
	for _, bad := range []string{"<style", "<script", ` style="`} {
		if strings.Contains(svg, bad) {
			t.Errorf("inline output contains %q", bad)
		}
	}
	if regexp.MustCompile(`\son[a-z]+="`).MatchString(svg) {
		t.Error("inline output has an event handler attribute")
	}
	if !strings.Contains(svg, `<a href="?expand=step-1-arg-content"><text class="more"`) {
		t.Error("… more should be a link in inline mode")
	}
	std := string(render(t, sc, Options{Mode: Standalone}))
	for _, must := range []string{"<style>", "<script><![CDATA[", `class="popover" id="pop-step-1-arg-content" display="none"`, `data-expand="step-1-arg-content"`} {
		if !strings.Contains(std, must) {
			t.Errorf("standalone output lacks %s", must)
		}
	}
	all := string(render(t, sc, Options{Mode: Standalone, Expand: ParseExpand("all")}))
	if strings.Contains(all, "<script") || strings.Contains(all, "popover") || strings.Contains(all, "… more") {
		t.Error("a fully expanded standalone file should be plain SVG")
	}
}

func TestEdges(t *testing.T) {
	d := Build(load(t, "b1-promote-decline-then-accept.yaml"), Options{})
	got := map[string]Edge{}
	for _, e := range d.Edges {
		got[e.Bind] = e
		if e.FromRow >= e.ToRow {
			t.Errorf("edge %s: from row %d to row %d", e.Bind, e.FromRow, e.ToRow)
		}
		if len(e.Stubs) != len(e.Uses) || e.From.X == 0 {
			t.Errorf("edge %s: geometry %+v", e.Bind, e)
		}
		if e.Slot > 1 {
			t.Errorf("edge %s: slot %d", e.Bind, e.Slot)
		}
	}
	if len(got["doc"].Uses) != 4 || len(got["f1"].Uses) != 3 {
		t.Errorf("uses: doc %v f1 %v", got["doc"].Uses, got["f1"].Uses)
	}
	if got["doc"].FromBox != "step-1" || got["f1"].Uses[0] != "step-3" {
		t.Errorf("boxes: %+v %+v", got["doc"], got["f1"])
	}
	if nd := Build(load(t, "b1-promote-decline-then-accept.yaml"), Options{NoEdges: true}); len(nd.Edges) != 0 || nd.Width >= d.Width {
		t.Errorf("NoEdges: %d edges, width %d vs %d", len(nd.Edges), nd.Width, d.Width)
	}
}

func TestParallel(t *testing.T) {
	src := "name: ok\nclock: 2026-01-01\nactors:\n  a: {kind: agent, client: c, elicitation: decline}\n  h: {kind: human}\nsteps:\n  - {at: 0d, actor: a, op: ingest, args: {content: hi, namespace: n, source: {uri: u}}, bind: d}\n  - {at: 1d, actor: h, op: read, args: {uri: $d}, expect: {out: {tombstone: false}}}\n  - at: 1d\n    note: both at once\n    parallel:\n      - {actor: a, op: search, args: {query: hi}}\n      - {actor: h, op: status}\n"
	sc, err := scenario.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	d := Build(sc, Options{})
	r := d.Rows[2]
	if !r.Parallel || r.Bracket == nil || len(r.Boxes) != 2 || r.Boxes[0].Lane == r.Boxes[1].Lane || r.Boxes[0].ID != "step-3-1" || r.Boxes[1].Path != "steps.2.parallel.1" {
		t.Fatalf("parallel row: %+v", r)
	}
	if r.Boxes[0].Rect.Y != r.Boxes[1].Rect.Y || r.Boxes[0].Rect.Y != r.Y+bracketH {
		t.Errorf("sub-boxes should share the row top: %+v %+v", r.Boxes[0].Rect, r.Boxes[1].Rect)
	}
	if r.Note.Full != "both at once" || r.Bracket.H < r.Boxes[0].Rect.H {
		t.Errorf("bracket/note: %+v %+v", r.Bracket, r.Note)
	}
	svg := string(render(t, sc, Options{}))
	wellFormed(t, []byte(svg))
	if !strings.Contains(svg, ">parallel<") || !strings.Contains(svg, `id="step-3-2"`) {
		t.Error("parallel group not drawn")
	}
}

func TestOverlay(t *testing.T) {
	sc := load(t, "b1-promote-decline-then-accept.yaml")
	rep := &scenario.Report{Name: sc.Name, Steps: []scenario.StepReport{
		{Index: 1, Op: "ingest", Binds: map[string]string{"": "memo://doc/abc", "chunk": "memo://chunk/1"}, Status: map[string]any{"live_documents": float64(1), "facts": float64(0), "chunks": float64(1), "pages": map[string]any{"pages": float64(0), "open_work_items": float64(0)}}},
		{Index: 2, Op: "remember", Binds: map[string]string{"": "memo://fact/f1"}, Status: map[string]any{"live_documents": float64(1), "facts": float64(1), "chunks": float64(1), "pages": map[string]any{"pages": float64(0), "open_work_items": float64(1)}}},
		{Index: 3, Op: "promote", Failures: []string{"out applied: want false, got true"}, Invariants: []string{"tool-channel-cap: 1 audit rows where a tool call acted above the agent trust cap"}},
	}}
	ov := OverlayFrom(rep)
	if ov.OK || ov.Steps["step-3"].State != "fail" || ov.Steps["step-1"].State != "pass" {
		t.Fatalf("overlay: %+v", ov)
	}
	if c := ov.Steps["step-2"].Counters; len(c) != 5 || c[1].Key != "facts" || c[1].Delta != 1 || c[4].Delta != 1 {
		t.Errorf("counters: %+v", c)
	}
	svg := string(render(t, sc, Options{Mode: Standalone, Overlay: ov}))
	wellFormed(t, []byte(svg))
	for _, must := range []string{`class="step fail" id="step-3"`, `class="step pass" id="step-1"`, "✗ out applied: want false,", "✗ invariant tool-channel-cap", "$doc → memo://doc/abc", "state: docs 1 (+1)", "facts 1 (+1)", "open items 1 (+1)"} {
		if !strings.Contains(svg, must) {
			t.Errorf("overlay rendering lacks %q", must)
		}
	}
	if OverlayFrom(nil) != nil {
		t.Error("nil report")
	}
}

func TestInvalidStillRenders(t *testing.T) {
	src := "name: broken\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: b, op: status}\n  - {actor: a, op: read, args: {uri: $nope}}\n"
	sc, err := scenario.ParseLoose([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	issues := scenario.Issues(sc.Validate())
	if len(issues) != 2 {
		t.Fatalf("issues: %+v", issues)
	}
	d := Build(sc, Options{Issues: issues})
	if len(d.Lanes) != 2 || !d.Lanes[1].Unknown || d.Lanes[1].Key != "b" {
		t.Fatalf("lanes: %+v", d.Lanes)
	}
	if len(d.Rows[0].Boxes[0].Issues) != 1 || len(d.Rows[1].Boxes[0].Issues) != 1 {
		t.Errorf("issues not pinned: %+v %+v", d.Rows[0].Boxes[0].Issues, d.Rows[1].Boxes[0].Issues)
	}
	svg := string(render(t, sc, Options{Issues: issues}))
	wellFormed(t, []byte(svg))
	if !strings.Contains(svg, `class="step invalid" id="step-1"`) || !strings.Contains(svg, "⚠ step 1: unknown actor") {
		t.Error("issue not drawn on the step")
	}
	empty := Build(&scenario.Scenario{}, Options{})
	if len(empty.Lanes) != 1 || empty.Width == 0 {
		t.Errorf("empty scenario: %+v", empty)
	}
}

func FuzzBuild(f *testing.F) {
	files, _ := scenario.ListDir(fixtures)
	for _, fl := range files {
		b, _ := os.ReadFile(fl)
		f.Add(b)
	}
	f.Add([]byte("name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: status}]\n"))
	f.Fuzz(func(t *testing.T, src []byte) {
		sc, err := scenario.ParseLoose(src)
		if err != nil {
			return
		}
		issues := scenario.Issues(sc.Validate())
		for _, opts := range []Options{{Mode: Standalone, Issues: issues}, inlineOpts("all")} {
			if _, err := Render(sc, opts); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestNarrate(t *testing.T) {
	s := Narrate(load(t, "c2-two-agents-disagree.yaml"))
	for _, must := range []string{"10 steps over 3 days from 2026-03-01", "a1 (agent, agent-alpha)", "h (human)", "1. [0d] a1 ingest", "→ $doc", "5. [2d] a3 compact", "in parallel"} {
		if !strings.Contains(s, must) && must != "in parallel" {
			t.Errorf("narration lacks %q: %s", must, s)
		}
	}
	if w := wrap("alpha beta gamma", 10); len(w) != 2 || w[0] != "alpha beta" {
		t.Errorf("wrap: %v", w)
	}
	if w := wrap("abcdefghijkl", 5); len(w) != 3 || w[2] != "kl" {
		t.Errorf("hard wrap: %v", w)
	}
	if w := wrap("a\n\n\nb\n\n", 10); len(w) != 4 {
		t.Errorf("paragraphs: %q", w)
	}
	if !ParseExpand("all").Has("x") || !ParseExpand("a, b").Has("b") || strings.Join(ParseExpand("a,b").Toggle("a"), ",") != "b" || strings.Join(ParseExpand("a").Toggle("b"), ",") != "a,b" {
		t.Error("Expand")
	}
	x := &ids{}
	if x.get("step", "1") != "step-1" || x.get("step", "1") != "step-1-2" || x.get("a/b c") != "a-b-c" || x.get("") != "x" {
		t.Error("ids")
	}
}
