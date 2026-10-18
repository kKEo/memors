package book

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	fixtures  = "../../testdata/scenarios"
	catalogue = "../../testdata/scenario-catalogue.yaml"
	bookRoot  = "../../../../docs/guide/tester"
)

func generate(t *testing.T, dir string) *Output {
	t.Helper()
	out, err := Generate(Config{Dir: dir, Catalogue: catalogue})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestBookCurrent fails when the committed scenario pages of the tester
// guide no longer match the scenario files or the catalogue.
func TestBookCurrent(t *testing.T) {
	changes, err := Plan(bookRoot, generate(t, fixtures))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		state := "stale or missing"
		if c.Content == nil {
			state = "no longer generated"
		}
		t.Errorf("docs/guide/tester/%s is %s: run `make scenario-docs`", c.Path, state)
	}
}

func TestDeterministic(t *testing.T) {
	a, b := generate(t, fixtures), generate(t, fixtures)
	if len(a.Files) != len(b.Files) {
		t.Fatal("file sets differ")
	}
	for p, x := range a.Files {
		if !bytes.Equal(x, b.Files[p]) {
			t.Errorf("%s differs between two runs", p)
		}
	}
}

var (
	linkRe   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	anchorRe = regexp.MustCompile(`<a id="([^"]+)"></a>`)
)

// TestGenerateLinks checks every relative link in the generated pages: the
// target is generated or is a page of the book, and an anchor into a
// generated page exists there.
func TestGenerateLinks(t *testing.T) {
	out := generate(t, fixtures)
	anchors := map[string]map[string]bool{}
	for p, b := range out.Files {
		anchors[p] = map[string]bool{}
		for _, m := range anchorRe.FindAllSubmatch(b, -1) {
			anchors[p][string(m[1])] = true
		}
	}
	for p, b := range out.Files {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		for _, m := range linkRe.FindAllSubmatch(b, -1) {
			target := string(m[1])
			if strings.HasPrefix(target, "https://") {
				if !strings.HasPrefix(target, RepoURL) {
					t.Errorf("%s: unexpected external link %s", p, target)
				}
				continue
			}
			file, frag, _ := strings.Cut(target, "#")
			dest := p
			if file != "" {
				dest = path.Clean(path.Join(path.Dir(p), file))
			}
			if _, ok := out.Files[dest]; ok {
				if frag != "" && !anchors[dest][frag] {
					t.Errorf("%s: %s has no anchor #%s", p, dest, frag)
				}
				continue
			}
			if _, err := os.Stat(filepath.Join(bookRoot, filepath.FromSlash(dest))); err != nil {
				t.Errorf("%s: link %s resolves to %s, which is neither generated nor in the book", p, target, dest)
			}
		}
	}
}

func TestPages(t *testing.T) {
	out := generate(t, fixtures)
	page := string(out.Files[LogDir+"/b2-zombie-fact.md"])
	for _, want := range []string{
		"# b2-zombie-fact\n",
		"- **Decision:** open:",
		"[gap 15](../gaps.md#gap-15)",
		"## Why it exists\n\nB2 — Zombie fact.",
		"![Swimlane picture of b2-zombie-fact](b2-zombie-fact.svg)",
		"| 6 | Jan 7 (2d) | `a1` | `remember` | The pager escalates to the CEO after five minutes.<br>*gap: nothing links",
		"<summary>b2-zombie-fact.yaml",
		"```yaml\n# B2 — Zombie fact.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("b2 page lacks %q", want)
		}
	}
	if svg := out.Files[LogDir+"/b2-zombie-fact.svg"]; !bytes.HasPrefix(svg, []byte("<svg")) || bytes.Contains(svg, []byte("<script")) {
		t.Error("the picture is not the fully expanded standalone SVG")
	}
	gaps := string(out.Files["scenarios/gaps.md"])
	if !strings.Contains(gaps, `<a id="gap-15"></a>`) || !strings.Contains(gaps, "- **Pinned by:** [b2-zombie-fact](log/b2-zombie-fact.md)") {
		t.Error("gap 15 is not shown as pinned by b2")
	}
	cat := string(out.Files["scenarios/catalogue.md"])
	if !strings.Contains(cat, "## C. Many agents writing notes") || !strings.Contains(cat, "- **Status:** implemented by [c2-two-agents-disagree](log/c2-two-agents-disagree.md)") {
		t.Error("catalogue lacks the C section or C2's status")
	}
	index := string(out.Files[LogDir+"/README.md"])
	if !strings.Contains(index, " scenario files** · ") {
		t.Error("log index lacks the totals line")
	}
	for _, e := range out.Entries {
		if !strings.Contains(index, "| ["+e.Title+"]("+e.Title+".md) |") {
			t.Errorf("log index lacks a row for %s", e.Title)
		}
	}
}

func TestSpliceSummary(t *testing.T) {
	src := "# Summary\n\n- [Part](p/README.md)\n  - [Implemented scenarios](scenarios/log/README.md)\n    - [old](scenarios/log/old.md)\n    - [gone](scenarios/log/gone.md)\n  - [What comes next](scenarios/next.md)\n- [Appendix](appendix.md)\n"
	got, err := SpliceSummary([]byte(src), []SummaryEntry{{"a", "scenarios/log/a.md"}, {"b", "scenarios/log/b.md"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "# Summary\n\n- [Part](p/README.md)\n  - [Implemented scenarios](scenarios/log/README.md)\n    - [a](scenarios/log/a.md)\n    - [b](scenarios/log/b.md)\n  - [What comes next](scenarios/next.md)\n- [Appendix](appendix.md)\n"
	if string(got) != want {
		t.Errorf("splice:\n%s\nwant:\n%s", got, want)
	}
	again, _ := SpliceSummary(got, []SummaryEntry{{"a", "scenarios/log/a.md"}, {"b", "scenarios/log/b.md"}})
	if string(again) != want {
		t.Error("splicing twice is not idempotent")
	}
	none, _ := SpliceSummary(got, nil)
	if strings.Contains(string(none), "log/a.md") || !strings.Contains(string(none), "[What comes next]") {
		t.Errorf("empty splice: %s", none)
	}
	if _, err := SpliceSummary([]byte("# Summary\n- [x](x.md)\n"), nil); err == nil {
		t.Error("a SUMMARY.md without the anchor must be an error")
	}
}

// TestPlanAndApply runs the generator against a scratch book: a first run
// writes everything, a second finds nothing to do, and removing a scenario
// removes its page, its picture and its SUMMARY line.
func TestPlanAndApply(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	files, _ := filepath.Glob(filepath.Join(fixtures, "*.yaml"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	summary := "# Summary\n\n- [Scenarios](scenarios/README.md)\n  - [Implemented scenarios](scenarios/log/README.md)\n"
	if err := os.WriteFile(filepath.Join(root, "SUMMARY.md"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() []Change {
		t.Helper()
		changes, err := Plan(root, generate(t, dir))
		if err != nil {
			t.Fatal(err)
		}
		if err := Apply(root, changes); err != nil {
			t.Fatal(err)
		}
		return changes
	}
	if c := run(); len(c) < 3+2*len(files)+1 {
		t.Fatalf("first run: %d changes", len(c))
	}
	if c := run(); len(c) != 0 {
		t.Fatalf("second run: %+v", c)
	}
	if err := os.Remove(filepath.Join(dir, "b2-zombie-fact.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, LogDir, "notes.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	run()
	for _, p := range []string{"b2-zombie-fact.md", "b2-zombie-fact.svg"} {
		if _, err := os.Stat(filepath.Join(root, LogDir, p)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed", p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, LogDir, "notes.txt")); err != nil {
		t.Error("a file the generator does not own was removed")
	}
	s, _ := os.ReadFile(filepath.Join(root, "SUMMARY.md"))
	if strings.Contains(string(s), "b2-zombie-fact") || !strings.Contains(string(s), "    - [c2-two-agents-disagree](scenarios/log/c2-two-agents-disagree.md)") {
		t.Errorf("SUMMARY.md after removal:\n%s", s)
	}
}
