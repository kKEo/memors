# memors-scenario

`memors-scenario` is the command-line tool for scenario files. It is a development tool, not
part of a release: run it with `go run ./cmd/memors-scenario …` from the repository root, or
build it with `go build ./cmd/memors-scenario`. Every subcommand takes files or directories
(a directory means every `*.yaml`, `*.yml` and `*.json` in it).

| Exit code | Meaning |
|---|---|
| 0 | Everything valid, passed, or up to date |
| 1 | A file is invalid, a scenario failed, or generated pages are stale |
| 2 | Usage error: unknown subcommand or flag, missing file |

## validate

```bash
go run ./cmd/memors-scenario validate internal/eval/testdata/scenarios
```

Parses each file strictly and checks it without running anything. Prints `file: ok`, or one
`file:line: message` per problem. Use it while writing; it takes milliseconds.

## describe

```bash
go run ./cmd/memors-scenario describe internal/eval/testdata/scenarios/b2-zombie-fact.yaml
```

Tells the scenario in one paragraph: steps, span, actors, then each step with its gist, its
bind and how many things it expects. It is the same text as the picture's accessible
description. Paste it into a pull request so a reviewer gets the story without reading YAML.

## render

```bash
go run ./cmd/memors-scenario render [--out dir] [--expand all|id,id] [--mode standalone|inline] [--force] [--no-edges] <file|dir>...
```

| Flag | Meaning |
|---|---|
| `--out` | Where to write `<name>.svg` (default: next to each file) |
| `--expand` | `all`, or a comma-separated list of block ids (`step-1-arg-content`) to draw in full |
| `--mode` | `standalone` (default): a file of its own, with tooltips and click-to-expand. `inline`: for embedding in an HTML page under a strict CSP (no style or script elements) |
| `--force` | Draw an invalid file anyway, with its problems pinned on the picture |
| `--no-edges` | Leave out the bind edges |

## run

```bash
go run ./cmd/memors-scenario run [--json] [--trace] <file|dir>...
```

Runs each file against a fresh temporary knowledge base with the hash embedder. It is the same
runner `TestScenarios` uses. It prints one markdown table per scenario: step, `at`, actor,
op, result, failures. The server's own log lines go to stderr, so `2>/dev/null` gives a clean
table.

- `--trace` records each step's output (strings cut at 2 KB, lists at 20 items) and the status
  counters after it.
- `--json` prints the reports as JSON, with the trace included. Each step has `Index`,
  `Path`, `At`, `Clock`, `Actor`, `Op`, `Summary`, `Failures`, `Invariants`, `Binds`,
  `Status`, `Out` and, for parallel groups, `Sub`.

```bash
go run ./cmd/memors-scenario run --json b2.yaml 2>/dev/null | jq '.[0].Steps[2].Status.facts'
```

`run` ignores `tiers`: it runs whatever you give it, with the hash embedder.

## book

```bash
go run ./cmd/memors-scenario book [--dir …] [--catalogue …] [--book docs/guide/tester] [--check]
```

Regenerates this guide's scenario pages: [Behaviour gaps](gaps.md), [Catalogue](catalogue.md),
the [implemented scenarios](log/README.md) log with one page and picture per file, and the
matching `SUMMARY.md` entries. It prints each file it writes or removes. With `--check` it
writes nothing, lists what is stale, and exits 1. `make scenario-docs` runs it with the
defaults.

## serve

```bash
go run ./cmd/memors-scenario serve [--addr 127.0.0.1:0] [--dir internal/eval/testdata/scenarios] [--catalogue file]
make scenario-studio
```

Starts [the studio](studio.md) and prints its URL. The default address picks a free loopback
port. `--catalogue` defaults to `scenario-catalogue.yaml` one level above `--dir`, when that
file exists.

## make targets

| Target | Runs |
|---|---|
| `make scenario-docs` | `book` with the defaults. Run it after changing a scenario, the catalogue or the renderer |
| `make scenario-studio` | `serve --dir $(SCENARIOS)` |
| `make golden-update` | Includes the renderer's own SVG goldens |

`SCENARIOS` defaults to `internal/eval/testdata/scenarios`.
