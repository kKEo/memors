package scenario

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scenarioDir = "../testdata/scenarios"

// TestScenarios runs every ci-tier scenario file against a fresh knowledge
// base with the hash embedder and fails on any unmet expectation or broken
// invariant. A scenario with decision: open pins current behaviour; when
// it fails, the behaviour changed and the file (or the decision) must move.
func TestScenarios(t *testing.T) {
	scenarios, err := LoadDir(scenarioDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no scenario files under %s", scenarioDir)
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			if !sc.HasTier("ci") {
				t.Skipf("tiers %v", sc.Tiers)
			}
			rep, err := Run(context.Background(), sc, Options{Logf: t.Logf})
			if err != nil {
				t.Fatal(err)
			}
			t.Log("\n" + rep.Markdown())
			for _, f := range rep.Failures() {
				t.Error(f)
			}
		})
	}
}

func TestValidateRejectsBadScripts(t *testing.T) {
	cases := map[string]string{
		"unknown actor":   "name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: b, op: status}]\n",
		"bad op":          "name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: demote}]\n",
		"human tool":      "name: x\nactors: {h: {kind: human}}\nsteps: [{actor: h, op: explore}]\n",
		"unbound ref":     "name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: read, args: {uri: $nope}}]\n",
		"backwards at":    "name: x\nactors: {a: {kind: agent}}\nsteps: [{at: 2d, actor: a, op: status}, {at: 1d, actor: a, op: status}]\n",
		"bad at":          "name: x\nactors: {a: {kind: agent}}\nsteps: [{at: tomorrow, actor: a, op: status}]\n",
		"elicit no actor": "name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: promote, elicit: accept}]\n",
		"unknown field":   "name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: status, expects: {}}]\n",
		"bad tier":        "name: x\ntiers: [weekly]\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: status}]\n",
		"select on read":  "name: x\nactors: {a: {kind: agent}}\nsteps: [{actor: a, op: status, select: {kind: page}}]\n",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	good := "name: ok\nclock: 2026-01-01\nactors:\n  a: {kind: agent, client: c, elicitation: decline}\n  h: {kind: human}\nsteps:\n  - {at: 0d, actor: a, op: ingest, args: {content: hi, namespace: n, source: {uri: u}}, bind: d}\n  - {at: 1d, actor: h, op: read, args: {uri: $d}, expect: {out: {tombstone: false}}}\n  - at: 1d\n    parallel:\n      - {actor: a, op: search, args: {query: hi}}\n      - {actor: h, op: status}\n"
	if _, err := Parse([]byte(good)); err != nil {
		t.Fatalf("good script rejected: %v", err)
	}
}

func TestRefsAndLookup(t *testing.T) {
	r := &Runner{binds: map[string]map[string]string{"d": {"": "memo://doc/1", "chunk": "memo://chunk/7"}}}
	got, err := r.resolveString("see $d.chunk and $d")
	if err != nil || got != "see memo://chunk/7 and memo://doc/1" {
		t.Fatalf("resolve: %q %v", got, err)
	}
	if _, err := r.resolveString("$x"); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound ref: %v", err)
	}
	if _, err := r.resolveString("$d.nope"); err == nil {
		t.Fatal("unknown field should fail")
	}
	out := map[string]any{"results": []any{map[string]any{"uri": "memo://fact/1", "provenance": map[string]any{"trust": "user"}}}}
	if v := lookup(out, "results.0.provenance.trust"); v != "user" {
		t.Fatalf("lookup: %v", v)
	}
	if v := lookup(out, "results.3.uri"); v != nil {
		t.Fatalf("out of range lookup: %v", v)
	}
	if !sameValue(1, float64(1)) || sameValue("1", float64(1)) || !sameValue(nil, nil) {
		t.Fatal("sameValue")
	}
	if d, _ := ParseAt("90m"); d.Minutes() != 90 {
		t.Fatal("ParseAt")
	}
	if filepath.Base(scenarioDir) != "scenarios" {
		t.Fatal("scenario dir")
	}
}

func TestParseLines(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(scenarioDir, "b1-promote-decline-then-accept.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"name": 7, "covers": 11, "actors.h": 15, "steps.0": 17, "steps.0.args.content": 22, "steps.1.args.statement": 32, "steps.2.args": 38, "steps.6.expect.error_contains": 74}
	for p, l := range want {
		if got := sc.Lines[p].Line; got != l {
			t.Errorf("Lines[%s] = %d, want %d", p, got, l)
		}
	}
	if sc.LineOf("steps.1.args.nope") != 32 || sc.LineOf("steps.9") != 16 || sc.LineOf("nope.x") != 0 {
		t.Errorf("LineOf fallback: %d %d %d", sc.LineOf("steps.1.args.nope"), sc.LineOf("steps.9"), sc.LineOf("nope.x"))
	}
	if StepPath(2, -1) != "steps.2" || StepPath(4, 1) != "steps.4.parallel.1" {
		t.Error("StepPath")
	}
}

func TestIssuesCarryLines(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(scenarioDir, "b1-promote-decline-then-accept.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(b), "bind: f1", "bind: f9", 1)
	_, err = Parse([]byte(src))
	issues := Issues(err)
	if len(issues) < 2 {
		t.Fatalf("issues: %v (%v)", issues, err)
	}
	if issues[0].Path != "steps.2.args" || issues[0].Line != 38 || !strings.Contains(issues[0].Msg, "$f1 is not bound") {
		t.Errorf("first issue: %+v", issues[0])
	}
	if !strings.Contains(err.Error(), "(line 38)") {
		t.Errorf("error text lacks the line: %v", err)
	}
	// yaml's own errors name a line too
	_, err = Parse([]byte("name: x\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: a, op: status, expects: {}}\n"))
	if is := Issues(err); len(is) != 1 || is[0].Line != 4 {
		t.Errorf("unknown field: %+v", is)
	}
	// ParseLoose keeps an invalid file renderable
	sc, err := ParseLoose([]byte("name: x\nactors: {a: {kind: agent}}\nsteps:\n  - {actor: b, op: status}\n"))
	if err != nil || sc.Lines["steps.0"].Line != 4 {
		t.Errorf("ParseLoose: %v %+v", err, sc)
	}
	if Issues(nil) != nil {
		t.Error("Issues(nil)")
	}
}

func TestRunTrace(t *testing.T) {
	src := "name: trace\nactors:\n  a: {kind: agent, client: c}\n  h: {kind: human}\nsteps:\n" +
		"  - {at: 0d, actor: a, op: ingest, args: {namespace: n, content: \"# T\\n\\nSome text about T.\", source: {uri: https://t.example/1, kind: doc, origin: web}}, bind: d}\n" +
		"  - at: 1d\n    parallel:\n      - {actor: a, op: search, args: {query: text about T}}\n      - {actor: h, op: status}\n"
	sc, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), sc, Options{Trace: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Steps) != 2 {
		t.Fatalf("steps: %d", len(rep.Steps))
	}
	s0 := rep.Steps[0]
	if s0.Path != "steps.0" || !strings.HasPrefix(s0.Binds[""], "memo://doc/") || !strings.HasPrefix(s0.Binds["chunk"], "memo://chunk/") {
		t.Errorf("step 0: path %q binds %v", s0.Path, s0.Binds)
	}
	if n, _ := asFloat(s0.Status["live_documents"]); n != 1 {
		t.Errorf("status after step 0: %v", s0.Status)
	}
	if s0.Out["uri"] != s0.Binds[""] {
		t.Errorf("out: %v", s0.Out)
	}
	s1 := rep.Steps[1]
	if len(s1.Sub) != 2 || s1.Sub[1].Path != "steps.1.parallel.1" || s1.Sub[1].Op != "status" || s1.Sub[0].Out == nil {
		t.Errorf("parallel sub-reports: %+v", s1.Sub)
	}
	for _, f := range rep.Failures() {
		t.Error(f)
	}
	long := strings.Repeat("ą", 3000)
	if c := capValue(map[string]any{"s": long, "l": make([]any, 30)}, 2048, 20).(map[string]any); len(c["s"].(string)) > 2048+len("…") || len(c["l"].([]any)) != 20 {
		t.Errorf("capValue: %d %d", len(c["s"].(string)), len(c["l"].([]any)))
	}
}
