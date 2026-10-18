// Package coverage reads a directory of scenario files and the catalogue
// (internal/eval/testdata/scenario-catalogue.yaml) and says what is covered:
// which files exist and pin which catalogue entries and gaps, which
// operations each actor kind exercises, which expectation kinds are used and
// which invariants are skipped. The studio's index and the tester guide's
// generated pages (package book) are both drawn from it.
package coverage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kKEo/memors/internal/eval/scenario"
)

// Category is a catalogue section (A. Two clocks and time).
type Category struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Intro string `yaml:"intro,omitempty"`
}

// Entry is one planned scenario in the catalogue (A1 … F6).
type Entry struct {
	ID     string `yaml:"id"`
	Title  string `yaml:"title"`
	Detail string `yaml:"detail,omitempty"`
	// Tier is where the scenario will run: ci (default), nightly or binary.
	Tier string `yaml:"tier,omitempty"`
	Gaps []int  `yaml:"gaps,omitempty"`
}

// Gap is a behaviour found by reading the code that no test pinned down
// until a scenario does; Decision is open until the owner decides.
type Gap struct {
	ID       string `yaml:"id"`
	Title    string `yaml:"title"`
	Detail   string `yaml:"detail,omitempty"`
	Where    string `yaml:"where,omitempty"`
	Decision string `yaml:"decision,omitempty"`
}

// Catalogue is the parsed catalogue file.
type Catalogue struct {
	Categories []Category `yaml:"categories"`
	Entries    []Entry    `yaml:"entries"`
	Gaps       []Gap      `yaml:"gaps"`
}

// LoadCatalogue reads a catalogue file. Unknown fields are errors, so a typo
// in the file does not silently drop text from the generated pages.
func LoadCatalogue(path string) (*Catalogue, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalogue
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, id := range c.ids() {
		k := strings.ToLower(id)
		if seen[k] {
			return nil, fmt.Errorf("%s: id %s is defined twice", path, id)
		}
		seen[k] = true
	}
	return &c, nil
}

func (c *Catalogue) ids() []string {
	var out []string
	for _, e := range c.Entries {
		out = append(out, e.ID)
	}
	for _, g := range c.Gaps {
		out = append(out, g.ID)
	}
	return out
}

// Knows reports whether id (case-insensitive) is an entry or a gap.
func (c *Catalogue) Knows(id string) bool {
	for _, k := range c.ids() {
		if strings.EqualFold(k, id) {
			return true
		}
	}
	return false
}

// Category returns the section an entry belongs to (its id's letter).
func (c *Catalogue) Category(e Entry) Category {
	for _, cat := range c.Categories {
		if strings.HasPrefix(strings.ToUpper(e.ID), strings.ToUpper(cat.ID)) {
			return cat
		}
	}
	return Category{ID: "?", Title: "Other"}
}

// File is one scenario file as the index sees it.
type File struct {
	Path string // as found under the directory
	Name string // file name: b2-zombie-fact.yaml
	Base string // without extension: b2-zombie-fact
	// Catalogue is the id implied by the file name (b2- → B2), if any.
	Catalogue string
	Title     string // the scenario's description
	Decision  string // open or decided
	Tiers     []string
	Covers    []string // as written, plus Catalogue when the file name implies one
	Actors    []string
	Steps     int
	Parallel  int
	// Lead is the file's leading comment block without the # marks: the
	// author's account of why the scenario exists.
	Lead string
	// Error is set when the file does not parse or validate; Sc may still be
	// set (a file that parses but fails validation).
	Error  string
	Issues []scenario.Issue
	Source []byte
	Sc     *scenario.Scenario
}

// OK reports whether the file parsed and validated.
func (f File) OK() bool { return f.Error == "" }

// OpRow says which scenarios call an operation as an agent and as a human.
type OpRow struct {
	Op               string
	Agent, Human     []string
	AgentNA, HumanNA bool // the op does not exist for that actor kind
}

// KV pairs a key (an expectation kind, an invariant, an id) with the
// scenarios (base names) that use it.
type KV struct {
	Key       string
	Scenarios []string
}

// Index is the coverage of one directory.
type Index struct {
	Files        []File
	Ops          []OpRow
	Expectations []KV
	Skipped      []KV
	// CoveredBy maps a lower-cased catalogue or gap id to the base names of
	// the valid files that cover it.
	CoveredBy     map[string][]string
	Catalogue     *Catalogue // nil when none was given
	Uncovered     []Entry
	UncoveredGaps []Gap
	// Unknown lists covers ids the catalogue does not define.
	Unknown []KV
}

// ExpectationKinds is every expectation key, in the order pages list them.
var ExpectationKinds = []string{"error", "top", "contains", "excludes", "abstains", "count", "out", "out_contains", "lint", "audit", "audit_none", "status"}

var catalogueRe = regexp.MustCompile(`^([a-fA-F])(\d+)-`)

// Build indexes every scenario file in dir. cat may be nil.
func Build(dir string, cat *Catalogue) (*Index, error) {
	paths, err := scenario.ListDir(dir)
	if err != nil {
		return nil, err
	}
	ix := &Index{CoveredBy: map[string][]string{}, Catalogue: cat}
	ops := map[string]*OpRow{}
	expect := map[string]map[string]bool{}
	skipped := map[string]map[string]bool{}
	unknown := map[string]map[string]bool{}
	mark := func(m map[string]map[string]bool, k, name string) {
		if m[k] == nil {
			m[k] = map[string]bool{}
		}
		m[k][name] = true
	}
	for _, p := range paths {
		f := readFile(p)
		ix.Files = append(ix.Files, f)
		if !f.OK() {
			continue
		}
		for _, c := range f.Covers {
			k := strings.ToLower(c)
			ix.CoveredBy[k] = addOnce(ix.CoveredBy[k], f.Base)
			if cat != nil && !cat.Knows(c) {
				mark(unknown, c, f.Base)
			}
		}
		sc := f.Sc
		if sc.Invariants != nil {
			for _, sk := range sc.Invariants.Skip {
				mark(skipped, sk, f.Base)
			}
		}
		for _, st := range sc.Steps {
			subs := []scenario.Step{st}
			if len(st.Parallel) > 0 {
				subs = st.Parallel
			}
			if st.Invariants != nil {
				for _, sk := range st.Invariants.Skip {
					mark(skipped, sk, f.Base)
				}
			}
			for _, sub := range subs {
				if sub.Invariants != nil {
					for _, sk := range sub.Invariants.Skip {
						mark(skipped, sk, f.Base)
					}
				}
				if ops[sub.Op] == nil {
					ops[sub.Op] = &OpRow{Op: sub.Op}
				}
				switch sc.Actors[sub.Actor].Kind {
				case "agent":
					ops[sub.Op].Agent = addOnce(ops[sub.Op].Agent, f.Base)
				case "human":
					ops[sub.Op].Human = addOnce(ops[sub.Op].Human, f.Base)
				}
				for _, k := range ExpectKinds(sub.Expect) {
					mark(expect, k, f.Base)
				}
			}
		}
	}
	agentOps, humanOps := map[string]bool{}, map[string]bool{}
	for _, o := range scenario.AgentOps() {
		agentOps[o] = true
		if ops[o] == nil {
			ops[o] = &OpRow{Op: o}
		}
	}
	for _, o := range scenario.HumanOps() {
		humanOps[o] = true
		if ops[o] == nil {
			ops[o] = &OpRow{Op: o}
		}
	}
	for _, o := range ops {
		o.AgentNA, o.HumanNA = !agentOps[o.Op], !humanOps[o.Op]
		ix.Ops = append(ix.Ops, *o)
	}
	sort.Slice(ix.Ops, func(i, j int) bool { return ix.Ops[i].Op < ix.Ops[j].Op })
	for _, k := range ExpectationKinds {
		ix.Expectations = append(ix.Expectations, KV{Key: k, Scenarios: keysOf(expect[k])})
	}
	ix.Skipped = kvs(skipped)
	ix.Unknown = kvs(unknown)
	if cat != nil {
		for _, e := range cat.Entries {
			if len(ix.CoveredBy[strings.ToLower(e.ID)]) == 0 {
				ix.Uncovered = append(ix.Uncovered, e)
			}
		}
		for _, g := range cat.Gaps {
			if len(ix.CoveredBy[strings.ToLower(g.ID)]) == 0 {
				ix.UncoveredGaps = append(ix.UncoveredGaps, g)
			}
		}
	}
	return ix, nil
}

// readFile parses and validates one scenario file.
func readFile(p string) File {
	name := filepath.Base(p)
	f := File{Path: p, Name: name, Base: strings.TrimSuffix(name, filepath.Ext(name))}
	if m := catalogueRe.FindStringSubmatch(name); m != nil {
		f.Catalogue = strings.ToUpper(m[1]) + m[2]
	}
	src, err := os.ReadFile(p)
	if err != nil {
		f.Error = err.Error()
		return f
	}
	f.Source = src
	f.Lead = Lead(src)
	sc, err := scenario.ParseLoose(src)
	if err != nil {
		f.Issues = scenario.Issues(err)
		f.Error = err.Error()
		return f
	}
	sc.Path = p
	f.Sc = sc
	if issues := scenario.Issues(sc.Validate()); len(issues) > 0 {
		f.Issues = issues
		f.Error = fmt.Sprintf("%d issue(s): %s", len(issues), issues[0].Error())
	}
	f.Title = sc.Description
	f.Decision = sc.Decision
	if f.Decision == "" {
		f.Decision = "decided"
	}
	f.Tiers = sc.Tiers
	if len(f.Tiers) == 0 {
		f.Tiers = []string{"ci"}
	}
	f.Covers = append([]string(nil), sc.Covers...)
	if f.Catalogue != "" && !containsFold(f.Covers, f.Catalogue) {
		f.Covers = append([]string{f.Catalogue}, f.Covers...)
	}
	for k := range sc.Actors {
		f.Actors = append(f.Actors, k)
	}
	sort.Strings(f.Actors)
	f.Steps = len(sc.Steps)
	for _, st := range sc.Steps {
		if len(st.Parallel) > 0 {
			f.Parallel++
		}
	}
	return f
}

// Lead returns the comment block at the top of a YAML file, without the #
// marks, with lines joined into paragraphs (an empty comment line separates
// paragraphs).
func Lead(src []byte) string {
	var paras []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "#") {
			break
		}
		t = strings.TrimSpace(strings.TrimLeft(t, "#"))
		if t == "" {
			flush()
			continue
		}
		cur = append(cur, t)
	}
	flush()
	return strings.Join(paras, "\n\n")
}

// ExpectKinds names the expectation keys a step uses, in ExpectationKinds
// order.
func ExpectKinds(e *scenario.Expect) []string {
	if e == nil {
		return nil
	}
	var out []string
	add := func(ok bool, k string) {
		if ok {
			out = append(out, k)
		}
	}
	add(e.Error != nil || e.ErrorContains != "", "error")
	add(len(e.Top) > 0, "top")
	add(len(e.Contains) > 0, "contains")
	add(len(e.Excludes) > 0, "excludes")
	add(e.Abstains != nil, "abstains")
	add(e.Count != nil, "count")
	add(len(e.Out) > 0, "out")
	add(len(e.OutContains) > 0, "out_contains")
	add(e.Lint != nil, "lint")
	add(len(e.Audit) > 0, "audit")
	add(e.AuditNone, "audit_none")
	add(len(e.Status) > 0, "status")
	return out
}

func kvs(m map[string]map[string]bool) []KV {
	out := make([]KV, 0, len(m))
	for k, v := range m {
		out = append(out, KV{Key: k, Scenarios: keysOf(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func addOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
