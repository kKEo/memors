package scenario

import (
	"context"
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
