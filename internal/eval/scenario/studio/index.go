package studio

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kKEo/memors/internal/eval/scenario"
)

var catalogueRe = regexp.MustCompile(`^([a-fA-F])(\d+)-`)

type fileRow struct {
	Name, Title, Catalogue, Decision string
	Tiers, Covers, Actors            []string
	Steps, Parallel                  int
	Error                            string
}

type opRow struct {
	Op               string
	Agent, Human     []string
	AgentNA, HumanNA bool
}

type kvRow struct {
	Key       string
	Scenarios []string
}

type catEntry struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Gaps  []int  `yaml:"gaps,omitempty"`
}

type catalogue struct {
	Entries []catEntry `yaml:"entries"`
	Gaps    []catEntry `yaml:"gaps"`
}

type indexData struct {
	Files         []fileRow
	Ops           []opRow
	Expectations  []kvRow
	Skipped       []kvRow
	Uncovered     []catEntry
	UncoveredGaps []catEntry
	CatalogueErr  string
	HasCatalogue  bool
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	files, err := scenario.ListDir(s.dir)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	data := indexData{}
	ops := map[string]*opRow{}
	expect := map[string]map[string]bool{}
	skipped := map[string]map[string]bool{}
	covered := map[string]bool{}
	mark := func(m map[string]map[string]bool, k, name string) {
		if m[k] == nil {
			m[k] = map[string]bool{}
		}
		m[k][name] = true
	}
	for _, f := range files {
		name := filepath.Base(f)
		row := fileRow{Name: name}
		if m := catalogueRe.FindStringSubmatch(name); m != nil {
			row.Catalogue = strings.ToUpper(m[1]) + m[2]
		}
		src, err := os.ReadFile(f)
		if err != nil {
			row.Error = err.Error()
			data.Files = append(data.Files, row)
			continue
		}
		sc, err := scenario.ParseLoose(src)
		if err != nil {
			row.Error = err.Error()
			data.Files = append(data.Files, row)
			continue
		}
		if issues := scenario.Issues(sc.Validate()); len(issues) > 0 {
			row.Error = fmt.Sprintf("%d issue(s): %s", len(issues), issues[0].Error())
		}
		row.Title = sc.Description
		row.Decision = sc.Decision
		if row.Decision == "" {
			row.Decision = "decided"
		}
		row.Tiers = sc.Tiers
		if len(row.Tiers) == 0 {
			row.Tiers = []string{"ci"}
		}
		row.Covers = sc.Covers
		for k := range sc.Actors {
			row.Actors = append(row.Actors, k)
		}
		sort.Strings(row.Actors)
		row.Steps = len(sc.Steps)
		if row.Error != "" {
			data.Files = append(data.Files, row)
			continue
		}
		short := strings.TrimSuffix(name, filepath.Ext(name))
		for _, c := range sc.Covers {
			covered[strings.ToLower(c)] = true
		}
		if row.Catalogue != "" {
			covered[strings.ToLower(row.Catalogue)] = true
		}
		if sc.Invariants != nil {
			for _, sk := range sc.Invariants.Skip {
				mark(skipped, sk, short)
			}
		}
		for _, st := range sc.Steps {
			subs := []scenario.Step{st}
			if len(st.Parallel) > 0 {
				row.Parallel++
				subs = st.Parallel
			}
			if st.Invariants != nil {
				for _, sk := range st.Invariants.Skip {
					mark(skipped, sk, short)
				}
			}
			for _, sub := range subs {
				kind := sc.Actors[sub.Actor].Kind
				if ops[sub.Op] == nil {
					ops[sub.Op] = &opRow{Op: sub.Op}
				}
				switch kind {
				case "agent":
					ops[sub.Op].Agent = addOnce(ops[sub.Op].Agent, short)
				case "human":
					ops[sub.Op].Human = addOnce(ops[sub.Op].Human, short)
				}
				for _, k := range expectKinds(sub.Expect) {
					mark(expect, k, short)
				}
			}
		}
		data.Files = append(data.Files, row)
	}
	agentOps, humanOps := map[string]bool{}, map[string]bool{}
	for _, o := range scenario.AgentOps() {
		agentOps[o] = true
		if ops[o] == nil {
			ops[o] = &opRow{Op: o}
		}
	}
	for _, o := range scenario.HumanOps() {
		humanOps[o] = true
		if ops[o] == nil {
			ops[o] = &opRow{Op: o}
		}
	}
	for _, o := range ops {
		o.AgentNA, o.HumanNA = !agentOps[o.Op], !humanOps[o.Op]
		data.Ops = append(data.Ops, *o)
	}
	sort.Slice(data.Ops, func(i, j int) bool { return data.Ops[i].Op < data.Ops[j].Op })
	for _, k := range []string{"error", "top", "contains", "excludes", "abstains", "count", "out", "out_contains", "lint", "audit", "audit_none", "status"} {
		data.Expectations = append(data.Expectations, kvRow{Key: k, Scenarios: keysOf(expect[k])})
	}
	for k, m := range skipped {
		data.Skipped = append(data.Skipped, kvRow{Key: k, Scenarios: keysOf(m)})
	}
	sort.Slice(data.Skipped, func(i, j int) bool { return data.Skipped[i].Key < data.Skipped[j].Key })
	if s.catalogue != "" {
		cat, err := loadCatalogue(s.catalogue)
		if err != nil {
			data.CatalogueErr = err.Error()
		} else {
			data.HasCatalogue = true
			for _, e := range cat.Entries {
				if !covered[strings.ToLower(e.ID)] {
					data.Uncovered = append(data.Uncovered, e)
				}
			}
			for _, g := range cat.Gaps {
				if !covered[strings.ToLower(g.ID)] {
					data.UncoveredGaps = append(data.UncoveredGaps, g)
				}
			}
		}
	}
	s.render(w, "index.html", "Scenarios", data)
}

func loadCatalogue(path string) (*catalogue, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c catalogue
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func addOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func newFlagSet(stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}
