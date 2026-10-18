package scenariocli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtures = "../../testdata/scenarios"

func wellFormed(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		if _, err := dec.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("%s: not well-formed: %v", path, err)
		}
	}
}

func TestRenderValidateDescribe(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := Main(ctx, []string{"render", "--out", dir, "--expand", "all", fixtures}, &out, &errb); code != 0 {
		t.Fatalf("render: %d %s %s", code, out.String(), errb.String())
	}
	svgs, _ := filepath.Glob(filepath.Join(dir, "*.svg"))
	if len(svgs) != 3 {
		t.Fatalf("rendered %d files: %v", len(svgs), svgs)
	}
	for _, s := range svgs {
		wellFormed(t, s)
	}
	out.Reset()
	if code := Main(ctx, []string{"validate", fixtures}, &out, &errb); code != 0 || strings.Count(out.String(), ": ok") != 3 {
		t.Fatalf("validate: %d %s", code, out.String())
	}

	b, err := os.ReadFile(filepath.Join(fixtures, "b1-promote-decline-then-accept.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte(strings.Replace(string(b), "bind: f1", "bind: f9", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Main(ctx, []string{"validate", broken}, &out, &errb); code != 1 || !strings.Contains(out.String(), broken+":38: step 3: $f1 is not bound") {
		t.Fatalf("validate broken: %d %s", code, out.String())
	}
	out.Reset()
	errb.Reset()
	if code := Main(ctx, []string{"render", "--out", filepath.Join(dir, "b"), broken}, &out, &errb); code != 1 || !strings.Contains(errb.String(), ":38:") {
		t.Fatalf("render broken: %d %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "b", "broken.svg")); err == nil {
		t.Fatal("an invalid file was rendered without --force")
	}
	if code := Main(ctx, []string{"render", "--force", "--out", filepath.Join(dir, "b"), broken}, &out, &errb); code != 1 {
		t.Fatalf("render --force: %d", code)
	}
	forced, err := os.ReadFile(filepath.Join(dir, "b", "broken.svg"))
	if err != nil || !strings.Contains(string(forced), "⚠ step 3: $f1 is not bound") {
		t.Fatalf("forced rendering: %v", err)
	}

	out.Reset()
	if code := Main(ctx, []string{"describe", filepath.Join(fixtures, "b1-promote-decline-then-accept.yaml")}, &out, &errb); code != 0 || !strings.Contains(out.String(), "8 steps over 1 day") {
		t.Fatalf("describe: %d %s", code, out.String())
	}
	if code := Main(ctx, nil, &out, &errb); code != 2 {
		t.Error("no command should exit 2")
	}
	if code := Main(ctx, []string{"nope"}, &out, &errb); code != 2 {
		t.Error("unknown command should exit 2")
	}
	if code := Main(ctx, []string{"render", "--mode", "weird", fixtures}, &out, &errb); code != 2 {
		t.Error("bad mode should exit 2")
	}
	if code := Main(ctx, []string{"validate", filepath.Join(dir, "missing.yaml")}, &out, &errb); code != 2 {
		t.Error("missing file should exit 2")
	}
	if code := Main(ctx, []string{"serve"}, &out, &errb); code != 2 {
		t.Error("serve without the studio linked should exit 2")
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	src := "name: tiny\nactors:\n  a: {kind: agent}\nsteps:\n  - {at: 0d, actor: a, op: status, expect: {out: {live_documents: 0}}}\n"
	f := filepath.Join(dir, "tiny.yaml")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Main(context.Background(), []string{"run", "--json", f}, &out, &errb); code != 0 {
		t.Fatalf("run: %d %s %s", code, out.String(), errb.String())
	}
	var reps []map[string]any
	if err := json.Unmarshal(out.Bytes(), &reps); err != nil || len(reps) != 1 || reps[0]["Name"] != "tiny" {
		t.Fatalf("json report: %v %s", err, out.String())
	}
	out.Reset()
	if code := Main(context.Background(), []string{"run", f}, &out, &errb); code != 0 || !strings.Contains(out.String(), "### tiny — pass") {
		t.Fatalf("run markdown: %d %s", code, out.String())
	}
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte(strings.Replace(src, "live_documents: 0", "live_documents: 7", 1)), 0o644)
	if code := Main(context.Background(), []string{"run", bad}, &out, &errb); code != 1 {
		t.Fatalf("a failing scenario should exit 1, got %d", code)
	}
}

func TestBook(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SUMMARY.md"), []byte("# Summary\n\n- [Scenarios](s.md)\n  - [Implemented scenarios](scenarios/log/README.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--dir", fixtures, "--catalogue", "../../testdata/scenario-catalogue.yaml", "--book", root}
	var out, errb bytes.Buffer
	if code := Main(ctx, append([]string{"book", "--check"}, args...), &out, &errb); code != 1 || !strings.Contains(errb.String(), "make scenario-docs") {
		t.Fatalf("check on an empty book: %d %s %s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "scenarios")); !os.IsNotExist(err) {
		t.Fatal("--check wrote files")
	}
	out.Reset()
	if code := Main(ctx, append([]string{"book"}, args...), &out, &errb); code != 0 || !strings.Contains(out.String(), "write ") {
		t.Fatalf("book: %d %s %s", code, out.String(), errb.String())
	}
	page, err := os.ReadFile(filepath.Join(root, "scenarios/log/b2-zombie-fact.md"))
	if err != nil {
		t.Fatal(err)
	}
	// --dir outside the working tree: links fall back to the repository path.
	if !strings.Contains(string(page), "blob/master/internal/eval/testdata/scenarios/b2-zombie-fact.yaml") {
		t.Error("source link does not use the repository path")
	}
	out.Reset()
	if code := Main(ctx, append([]string{"book", "--check"}, args...), &out, &errb); code != 0 || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("check after book: %d %s", code, out.String())
	}
	if code := Main(ctx, []string{"book", "extra"}, &out, &errb); code != 2 {
		t.Errorf("book with an argument: %d", code)
	}
}
