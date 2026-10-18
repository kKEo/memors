// Package scenariocli is the memors-scenario command: render scenario files
// as SVG, validate them, run them and tell them in plain text. It is a
// development tool for the living-system scenarios (docs/guide/tester/scenarios/),
// kept out of the memors-mcp binary.
package scenariocli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kKEo/memors/internal/eval/scenario"
	"github.com/kKEo/memors/internal/eval/scenario/book"
	"github.com/kKEo/memors/internal/eval/scenario/diagram"
)

// Serve runs the studio web server; the studio package sets it so this
// package does not import the web stack when a caller does not need it.
var Serve func(ctx context.Context, args []string, stdout, stderr io.Writer) int

// Main runs the command and returns its exit code: 0 when everything passed,
// 1 when a scenario is invalid or failed, 2 for usage errors.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "render":
		return render(rest, stdout, stderr)
	case "validate":
		return validate(rest, stdout, stderr)
	case "run":
		return run(ctx, rest, stdout, stderr)
	case "describe":
		return describe(rest, stdout, stderr)
	case "book":
		return bookCmd(rest, stdout, stderr)
	case "serve":
		if Serve == nil {
			fmt.Fprintln(stderr, "serve is not built into this binary")
			return 2
		}
		return Serve(ctx, rest, stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `memors-scenario: pictures, checks and runs of living-system scenario files

  memors-scenario render   [--out dir] [--expand all|id,id] [--mode standalone|inline] [--force] [--no-edges] <file|dir>...
  memors-scenario validate <file|dir>...
  memors-scenario run      [--json] [--trace] <file|dir>...
  memors-scenario describe <file>...
  memors-scenario book     [--dir internal/eval/testdata/scenarios] [--catalogue file] [--book docs/guide/tester] [--check]
  memors-scenario serve    [--addr 127.0.0.1:0] [--dir internal/eval/testdata/scenarios] [--catalogue file]

render writes <name>.svg next to each file (or under --out). An invalid file
is reported and skipped unless --force pins its issues on the picture.
validate prints path:line: message per problem. run executes each file
against a fresh knowledge base with the deterministic hash embedder and
prints the report. describe tells the scenario in plain text. book
regenerates the scenario pages of the tester guide (gaps, catalogue, one page
and picture per file, the SUMMARY.md entries); --check only reports what is
stale. serve opens the studio on a loopback address. Exit codes: 0 ok, 1 invalid or failed, 2 usage.
`)
}

// expand lists the scenario files behind each argument (a file or a dir).
func expand(args []string) ([]string, error) {
	var files []string
	for _, a := range args {
		st, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			more, err := scenario.ListDir(a)
			if err != nil {
				return nil, err
			}
			if len(more) == 0 {
				return nil, fmt.Errorf("%s: no scenario files (*.yaml, *.yml, *.json)", a)
			}
			files = append(files, more...)
			continue
		}
		files = append(files, a)
	}
	if len(files) == 0 {
		return nil, errors.New("a scenario file or directory is required")
	}
	return files, nil
}

// printIssues writes one line per issue in the path:line: message form.
func printIssues(w io.Writer, path string, issues []scenario.Issue) {
	for _, is := range issues {
		if is.Line > 0 {
			fmt.Fprintf(w, "%s:%d: %s\n", path, is.Line, is.Msg)
		} else {
			fmt.Fprintf(w, "%s: %s\n", path, is.Msg)
		}
	}
}

func render(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory for the .svg files (default: next to each file)")
	exp := fs.String("expand", "", "blocks shown in full: all, or ids separated by commas")
	mode := fs.String("mode", "standalone", "standalone (a file of its own) or inline (for an HTML page under a strict CSP)")
	force := fs.Bool("force", false, "render an invalid file with its issues pinned on the picture")
	noEdges := fs.Bool("no-edges", false, "leave the bind edges out")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files, err := expand(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	opts := diagram.Options{Expand: diagram.ParseExpand(*exp), NoEdges: *noEdges}
	switch *mode {
	case "standalone":
		opts.Mode = diagram.Standalone
	case "inline":
		opts.Mode = diagram.Inline
	default:
		fmt.Fprintf(stderr, "mode %q: want standalone or inline\n", *mode)
		return 2
	}
	if *out != "" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	code := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		sc, err := scenario.ParseLoose(b)
		if err != nil {
			printIssues(stderr, f, scenario.Issues(err))
			code = 1
			continue
		}
		sc.Path = f
		o := opts
		if issues := scenario.Issues(sc.Validate()); len(issues) > 0 {
			printIssues(stderr, f, issues)
			code = 1
			if !*force {
				continue
			}
			o.Issues = issues
		}
		svg, err := diagram.Render(sc, o)
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		dir := *out
		if dir == "" {
			dir = filepath.Dir(f)
		}
		target := filepath.Join(dir, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))+".svg")
		if err := os.WriteFile(target, svg, 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "%s → %s\n", f, target)
	}
	return code
}

func validate(args []string, stdout, stderr io.Writer) int {
	files, err := expand(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	code := 0
	for _, f := range files {
		if _, err := scenario.Load(f); err != nil {
			printIssues(stdout, f, scenario.Issues(err))
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "%s: ok\n", f)
	}
	return code
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	trace := fs.Bool("trace", false, "record each step's output and status counters")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files, err := expand(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	code := 0
	var reports []*scenario.Report
	for _, f := range files {
		sc, err := scenario.Load(f)
		if err != nil {
			printIssues(stderr, f, scenario.Issues(err))
			code = 1
			continue
		}
		rep, err := scenario.Run(ctx, sc, scenario.Options{Trace: *trace || *asJSON})
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", f, err)
			code = 1
			continue
		}
		if !rep.OK() {
			code = 1
		}
		if *asJSON {
			reports = append(reports, rep)
			continue
		}
		fmt.Fprintln(stdout, rep.Markdown())
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return code
}

func describe(args []string, stdout, stderr io.Writer) int {
	files, err := expand(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	code := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		sc, err := scenario.ParseLoose(b)
		if err != nil {
			printIssues(stderr, f, scenario.Issues(err))
			code = 1
			continue
		}
		fmt.Fprintln(stdout, diagram.Narrate(sc))
	}
	return code
}

func bookCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("book", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", book.DefaultDir, "the scenario files")
	cat := fs.String("catalogue", book.DefaultCatalogue, "the catalogue data file")
	root := fs.String("book", book.DefaultBook, "the tester guide's root (holds SUMMARY.md)")
	check := fs.Bool("check", false, "write nothing; list stale files and exit 1 when there are any")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "book takes no arguments (got %q)\n", fs.Args())
		return 2
	}
	cfg := book.Config{Dir: *dir, Catalogue: *cat}
	// Links to the files on GitHub need repository paths; a relative path
	// inside the working tree is one, anything else keeps the default.
	if p := filepath.ToSlash(filepath.Clean(*dir)); !filepath.IsAbs(*dir) && !strings.HasPrefix(p, "..") {
		cfg.SourcePath = p
	}
	if p := filepath.ToSlash(filepath.Clean(*cat)); !filepath.IsAbs(*cat) && !strings.HasPrefix(p, "..") {
		cfg.CataloguePath = p
	}
	out, err := book.Generate(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	changes, err := book.Plan(*root, out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, c := range changes {
		verb := "write"
		if c.Content == nil {
			verb = "remove"
		}
		if *check {
			verb = "stale (" + verb + ")"
		}
		fmt.Fprintf(stdout, "%s %s\n", verb, filepath.Join(*root, filepath.FromSlash(c.Path)))
	}
	if *check {
		if len(changes) > 0 {
			fmt.Fprintln(stderr, "the tester guide's scenario pages are stale: run `make scenario-docs`")
			return 1
		}
		fmt.Fprintln(stdout, "book: up to date")
		return 0
	}
	if err := book.Apply(*root, changes); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(changes) == 0 {
		fmt.Fprintln(stdout, "book: up to date")
	}
	return 0
}
