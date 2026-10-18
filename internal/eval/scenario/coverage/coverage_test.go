package coverage

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	dir = "../../testdata/scenarios"
	cat = "../../testdata/scenario-catalogue.yaml"
)

func load(t *testing.T) *Catalogue {
	t.Helper()
	c, err := LoadCatalogue(cat)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogue(t *testing.T) {
	c := load(t)
	if len(c.Categories) != 6 || len(c.Entries) < 45 || len(c.Gaps) != 15 {
		t.Fatalf("catalogue: %d categories, %d entries, %d gaps", len(c.Categories), len(c.Entries), len(c.Gaps))
	}
	for _, e := range c.Entries {
		if e.Title == "" || e.Detail == "" {
			t.Errorf("%s: title and detail are required", e.ID)
		}
		if c.Category(e).ID == "?" {
			t.Errorf("%s: no category", e.ID)
		}
		switch e.Tier {
		case "", "ci", "nightly", "binary":
		default:
			t.Errorf("%s: tier %q", e.ID, e.Tier)
		}
		for _, g := range e.Gaps {
			if !c.Knows("gap-" + strconv.Itoa(g)) {
				t.Errorf("%s: gap %d is not defined", e.ID, g)
			}
		}
	}
	for _, g := range c.Gaps {
		if g.Title == "" || g.Detail == "" || g.Where == "" {
			t.Errorf("%s: title, detail and where are required", g.ID)
		}
		switch g.Decision {
		case "open", "keep", "change", "document":
		default:
			t.Errorf("%s: decision %q", g.ID, g.Decision)
		}
	}
}

func TestLoadCatalogueRejectsTyposAndDuplicates(t *testing.T) {
	d := t.TempDir()
	for name, body := range map[string]string{
		"typo.yaml": "entries:\n  - {id: A1, titel: x}\n",
		"dup.yaml":  "entries:\n  - {id: A1, title: x}\ngaps:\n  - {id: a1, title: y}\n",
	} {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCatalogue(p); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestBuild(t *testing.T) {
	ix, err := Build(dir, load(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Files) < 3 {
		t.Fatalf("files: %d", len(ix.Files))
	}
	for _, f := range ix.Files {
		if !f.OK() {
			t.Errorf("%s: %s", f.Name, f.Error)
		}
	}
	if got := ix.CoveredBy["gap-15"]; !reflect.DeepEqual(got, []string{"b2-zombie-fact"}) {
		t.Errorf("gap-15 covered by %v", got)
	}
	if got := ix.CoveredBy["c2"]; len(got) != 1 {
		t.Errorf("C2 covered by %v", got)
	}
	if len(ix.Unknown) != 0 {
		t.Errorf("unknown covers ids: %v", ix.Unknown)
	}
	var ingest *OpRow
	for i := range ix.Ops {
		if ix.Ops[i].Op == "ingest" {
			ingest = &ix.Ops[i]
		}
	}
	if ingest == nil || len(ingest.Agent) == 0 || ingest.AgentNA {
		t.Errorf("ingest row: %+v", ingest)
	}
	for _, o := range ix.Ops {
		if o.Op == "demote" && !o.AgentNA {
			t.Error("demote is not a tool")
		}
	}
	if len(ix.Expectations) != len(ExpectationKinds) {
		t.Errorf("expectations: %d rows", len(ix.Expectations))
	}
	for _, e := range ix.Uncovered {
		if e.ID == "B2" {
			t.Error("B2 is covered")
		}
	}
}

func TestLead(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(dir, "b2-zombie-fact.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	lead := Lead(b)
	if !strings.HasPrefix(lead, "B2 — Zombie fact.") || !strings.HasSuffix(lead, "the one that will flip.") || strings.Contains(lead, "#") || strings.Contains(lead, "\n") {
		t.Errorf("lead: %q", lead)
	}
	if got := Lead([]byte("# one\n#\n# two\n# three\nname: x\n# not lead\n")); got != "one\n\ntwo three" {
		t.Errorf("paragraphs: %q", got)
	}
	if Lead([]byte("name: x\n")) != "" {
		t.Error("no comment, no lead")
	}
}

// TestCoversKnown keeps `covers:` honest: every id a scenario names must be
// defined in the catalogue, or the coverage view and the tester guide count
// it nowhere.
func TestCoversKnown(t *testing.T) {
	ix, err := Build(dir, load(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range ix.Unknown {
		t.Errorf("covers id %q (in %s) is not in %s", u.Key, strings.Join(u.Scenarios, ", "), cat)
	}
}

func TestUnknownCoversAreReported(t *testing.T) {
	d := t.TempDir()
	src, err := os.ReadFile(filepath.Join(dir, "b2-zombie-fact.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src = []byte(strings.Replace(string(src), "covers: [B2, gap-15]", "covers: [B2, Z9]", 1))
	if err := os.WriteFile(filepath.Join(d, "b2-zombie-fact.yaml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(d, load(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Unknown) != 1 || ix.Unknown[0].Key != "Z9" {
		t.Errorf("unknown: %+v", ix.Unknown)
	}
}
