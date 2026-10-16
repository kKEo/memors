// Package scenario runs living-system scenarios against the real knowledge
// base: scripted actors (agents over in-memory MCP sessions, a human through
// the CLI channel) act on one store over a fake clock; each step may carry
// expectations, and an invariant pack runs after every step. A scenario is a
// YAML (or JSON) file; the reference is docs/eval/scenarios.md.
package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario is one scripted run.
type Scenario struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Decision is "decided" when the expectations are the behaviour the owner
	// wants, or "open" when they pin current behaviour that is still under
	// discussion (a documented gap).
	Decision string `yaml:"decision,omitempty"`
	// Tiers says where the scenario runs: ci (hash embedder, every PR),
	// nightly (real model), binary (the built memors-mcp process).
	Tiers []string `yaml:"tiers,omitempty"`
	Seed  int64    `yaml:"seed,omitempty"`
	// Clock is the fake clock at the first step (YYYY-MM-DD or RFC3339);
	// default 2026-01-01. Steps advance it through their `at` offset.
	Clock      string           `yaml:"clock,omitempty"`
	Actors     map[string]Actor `yaml:"actors"`
	Invariants *InvariantOpts   `yaml:"invariants,omitempty"`
	Steps      []Step           `yaml:"steps"`
	// Path is where the file was loaded from (not part of the format).
	Path string `yaml:"-"`
}

// Actor is who performs a step.
type Actor struct {
	// Kind is agent (an MCP session calling tools) or human (the CLI channel).
	Kind string `yaml:"kind"`
	// Client is the agent's ClientInfo.Name (what audit and the live page
	// show); for a human, the actor name in audit. Default: the actor key.
	Client string `yaml:"client,omitempty"`
	// Elicitation is how the agent's client answers a dialog: none (default:
	// the client has no such capability), accept, decline or cancel. A step
	// may override it with `elicit`.
	Elicitation string `yaml:"elicitation,omitempty"`
	// Channel is the human's channel; only cli is supported.
	Channel string `yaml:"channel,omitempty"`
}

// Step is one action, or a group of actions run at once.
type Step struct {
	// At is an offset from the scenario clock: 0d, 2d, 3h, 90m, 10s. Empty
	// means the same time as the previous step. Offsets never go backwards.
	At    string         `yaml:"at,omitempty"`
	Actor string         `yaml:"actor,omitempty"`
	Op    string         `yaml:"op,omitempty"`
	Args  map[string]any `yaml:"args,omitempty"`
	// Bind names the step's result so later steps can refer to it as $name
	// (its address) or $name.chunk (a document's first passage), $name.a and
	// $name.b (a conflict item's two facts).
	Bind string `yaml:"bind,omitempty"`
	// Select picks one item from a compact result for Bind: kind, and a
	// case-insensitive substring of the subject.
	Select map[string]string `yaml:"select,omitempty"`
	// Elicit overrides the actor's elicitation answer for this step.
	Elicit     string         `yaml:"elicit,omitempty"`
	Expect     *Expect        `yaml:"expect,omitempty"`
	Invariants *InvariantOpts `yaml:"invariants,omitempty"`
	// Parallel runs these steps concurrently (each on its own actor
	// session), then applies their binds and expectations in order.
	Parallel []Step `yaml:"parallel,omitempty"`
	Note     string `yaml:"note,omitempty"`
}

// InvariantOpts switches invariants off by name (see invariants.go).
type InvariantOpts struct {
	Skip []string `yaml:"skip,omitempty"`
}

// Expect is what must be observable after a step.
type Expect struct {
	// Error: true means the op must fail (a tool error or a refused CLI op).
	Error         *bool  `yaml:"error,omitempty"`
	ErrorContains string `yaml:"error_contains,omitempty"`
	// Top, Contains and Excludes name addresses ($refs) among the step's
	// results: Top in order from rank 1, the others anywhere.
	Top      []string `yaml:"top,omitempty"`
	Contains []string `yaml:"contains,omitempty"`
	Excludes []string `yaml:"excludes,omitempty"`
	// Abstains: true means zero results with a reason; false means at least one.
	Abstains *bool `yaml:"abstains,omitempty"`
	Count    *int  `yaml:"count,omitempty"`
	// Out compares fields of the step's output by dotted path
	// (results.0.provenance.trust: user); OutContains checks substrings.
	Out         map[string]any    `yaml:"out,omitempty"`
	OutContains map[string]string `yaml:"out_contains,omitempty"`
	Lint        *LintExpect       `yaml:"lint,omitempty"`
	// Audit lists rows the step must have written; AuditNone forbids any.
	Audit     []AuditExpect `yaml:"audit,omitempty"`
	AuditNone bool          `yaml:"audit_none,omitempty"`
	// Status compares status counters (live_documents, facts, chunks,
	// sources, revisions, jobs_queued, pages.pages, pages.open_work_items,
	// graph.entities, …).
	Status map[string]any `yaml:"status,omitempty"`
}

// LintExpect runs lint on a namespace and checks finding kinds. An entry
// may be a kind (contradiction) or kind:$ref to pin the finding's address.
type LintExpect struct {
	Namespace string   `yaml:"namespace,omitempty"`
	Has       []string `yaml:"has,omitempty"`
	HasNot    []string `yaml:"has_not,omitempty"`
}

// AuditExpect matches one audit row written during the step.
type AuditExpect struct {
	Op      string `yaml:"op"`
	Actor   string `yaml:"actor,omitempty"`
	Channel string `yaml:"channel,omitempty"`
	Target  string `yaml:"target,omitempty"`
}

var (
	agentOps = set("ingest", "search", "read", "remember", "forget", "promote", "explore", "compact", "submit", "status")
	humanOps = set("ingest", "remember", "forget", "promote", "demote", "merge", "reject", "compact", "submit", "lint", "verify", "backfill", "status", "search", "read", "history", "facts")
	tiers    = set("ci", "nightly", "binary")
	answers  = set("none", "accept", "decline", "cancel")
	refRe    = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_-]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?`)
	atRe     = regexp.MustCompile(`^(\d+)([dhms])$`)
)

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Load reads and validates one scenario file (YAML or JSON).
func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sc, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	sc.Path = path
	return sc, nil
}

// LoadDir loads every *.yaml, *.yml and *.json file in dir, sorted by name.
func LoadDir(dir string) ([]*Scenario, error) {
	var files []string
	for _, pat := range []string{"*.yaml", "*.yml", "*.json"} {
		m, err := filepath.Glob(filepath.Join(dir, pat))
		if err != nil {
			return nil, err
		}
		files = append(files, m...)
	}
	sort.Strings(files)
	var out []*Scenario
	for _, f := range files {
		sc, err := Load(f)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// Parse decodes and validates a scenario.
func Parse(b []byte) (*Scenario, error) {
	var sc Scenario
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&sc); err != nil {
		return nil, err
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}

// HasTier reports whether the scenario runs in tier t (a scenario with no
// tiers runs in ci).
func (sc *Scenario) HasTier(t string) bool {
	if len(sc.Tiers) == 0 {
		return t == "ci"
	}
	for _, x := range sc.Tiers {
		if x == t {
			return true
		}
	}
	return false
}

// ClockStart is the fake clock at the first step.
func (sc *Scenario) ClockStart() (time.Time, error) {
	if sc.Clock == "" {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil
	}
	return parseTime(sc.Clock)
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("time %q: want YYYY-MM-DD or RFC3339", s)
	}
	return t.UTC(), nil
}

// ParseAt turns a step offset (2d, 3h, 90m, 10s) into a duration.
func ParseAt(s string) (time.Duration, error) {
	m := atRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("at %q: want <n>d, <n>h, <n>m or <n>s", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute, "s": time.Second}[m[2]]
	return time.Duration(n) * unit, nil
}

// Validate checks the script before anything runs: actors exist and have a
// kind, ops fit the actor's kind, offsets parse and never go backwards,
// tiers and answers are known, and every $ref was bound by an earlier step.
func (sc *Scenario) Validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if strings.TrimSpace(sc.Name) == "" {
		fail("name is required")
	}
	switch sc.Decision {
	case "", "open", "decided":
	default:
		fail("decision %q: want open or decided", sc.Decision)
	}
	for _, t := range sc.Tiers {
		if !tiers[t] {
			fail("tier %q: want ci, nightly or binary", t)
		}
	}
	if _, err := sc.ClockStart(); err != nil {
		fail("clock: %v", err)
	}
	if len(sc.Actors) == 0 {
		fail("at least one actor is required")
	}
	for name, a := range sc.Actors {
		switch a.Kind {
		case "agent":
			if a.Elicitation != "" && !answers[a.Elicitation] {
				fail("actor %s: elicitation %q: want none, accept, decline or cancel", name, a.Elicitation)
			}
			if a.Channel != "" {
				fail("actor %s: channel applies to human actors only", name)
			}
		case "human":
			if a.Channel != "" && a.Channel != "cli" {
				fail("actor %s: channel %q: only cli is supported", name, a.Channel)
			}
			if a.Elicitation != "" {
				fail("actor %s: elicitation applies to agent actors only", name)
			}
		default:
			fail("actor %s: kind %q: want agent or human", name, a.Kind)
		}
	}
	if len(sc.Steps) == 0 {
		fail("at least one step is required")
	}
	bound := map[string]bool{}
	var last time.Duration
	for i, st := range sc.Steps {
		where := fmt.Sprintf("step %d", i+1)
		if st.At != "" {
			d, err := ParseAt(st.At)
			if err != nil {
				fail("%s: %v", where, err)
			} else if d < last {
				fail("%s: at %s goes backwards (previous %s)", where, st.At, last)
			} else {
				last = d
			}
		}
		if len(st.Parallel) > 0 {
			if st.Op != "" || st.Actor != "" || st.Bind != "" || st.Expect != nil {
				fail("%s: a parallel group carries only its sub-steps (and at, note, invariants)", where)
			}
			var binds []string
			for j, sub := range st.Parallel {
				if sub.At != "" || len(sub.Parallel) > 0 {
					fail("%s.%d: sub-steps have no at and no nested parallel", where, j+1)
				}
				errs = append(errs, sc.validateStep(fmt.Sprintf("%s.%d", where, j+1), sub, bound)...)
				if sub.Bind != "" {
					binds = append(binds, sub.Bind)
				}
			}
			for _, b := range binds {
				bound[b] = true
			}
			continue
		}
		errs = append(errs, sc.validateStep(where, st, bound)...)
		if st.Bind != "" {
			bound[st.Bind] = true
		}
	}
	return errors.Join(errs...)
}

func (sc *Scenario) validateStep(where string, st Step, bound map[string]bool) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	a, ok := sc.Actors[st.Actor]
	if !ok {
		fail("%s: unknown actor %q", where, st.Actor)
		return errs
	}
	switch a.Kind {
	case "agent":
		if !agentOps[st.Op] {
			fail("%s: op %q is not a tool an agent can call", where, st.Op)
		}
		if st.Elicit != "" {
			if !answers[st.Elicit] || st.Elicit == "none" {
				fail("%s: elicit %q: want accept, decline or cancel", where, st.Elicit)
			} else if a.Elicitation == "" || a.Elicitation == "none" {
				fail("%s: elicit needs an actor whose elicitation is not none", where)
			}
		}
	case "human":
		if !humanOps[st.Op] {
			fail("%s: op %q is not a CLI operation", where, st.Op)
		}
		if st.Elicit != "" {
			fail("%s: elicit applies to agent steps only", where)
		}
	}
	if len(st.Select) > 0 && st.Op != "compact" {
		fail("%s: select applies to compact steps only", where)
	}
	for _, ref := range refsIn(st.Args) {
		if !bound[ref] {
			fail("%s: $%s is not bound by an earlier step", where, ref)
		}
	}
	if st.Expect != nil {
		e := st.Expect
		var strs []string
		strs = append(strs, e.Top...)
		strs = append(strs, e.Contains...)
		strs = append(strs, e.Excludes...)
		for _, v := range e.Out {
			if s, ok := v.(string); ok {
				strs = append(strs, s)
			}
		}
		for _, a := range e.Audit {
			strs = append(strs, a.Target)
			if a.Op == "" {
				fail("%s: an audit expectation needs op", where)
			}
		}
		if e.Lint != nil {
			strs = append(strs, e.Lint.Has...)
			strs = append(strs, e.Lint.HasNot...)
		}
		for _, s := range strs {
			for _, ref := range refsInString(s) {
				if !bound[ref] && ref != st.Bind {
					fail("%s: $%s is not bound by an earlier step", where, ref)
				}
			}
		}
		if e.AuditNone && len(e.Audit) > 0 {
			fail("%s: audit_none and audit together", where)
		}
	}
	return errs
}

// refsIn collects the $names used anywhere in a value tree.
func refsIn(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = append(out, refsInString(x)...)
	case []any:
		for _, e := range x {
			out = append(out, refsIn(e)...)
		}
	case map[string]any:
		for _, e := range x {
			out = append(out, refsIn(e)...)
		}
	}
	return out
}

func refsInString(s string) []string {
	var out []string
	for _, m := range refRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
