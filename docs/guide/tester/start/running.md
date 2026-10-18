# Running the tests

## What you need

- **Go**, the version named in `go.mod`. CI uses the same file.
- **make**.
- **golangci-lint** is optional. `make lint` runs it through `go run` when it is not installed,
  which is slower the first time.
- **mdBook** is optional, and needed only to build these guides (`brew install mdbook`).
- **macOS** is needed only for MemoRS Tray tests (`make tray-test`), the one cgo build.

Nothing else is required. The tests create their own SQLite files in temporary directories,
use the deterministic hash embedder instead of a model, and never touch `~/.memors-mcp`.

## The make targets

| Command | What it runs | When |
|---|---|---|
| `make check` | gofmt, `go vet`, golangci-lint, then all tests under the race detector | Before every push. This is the local copy of the PR gate |
| `make test` | `go test ./... -count=1` | Quick run without the race detector |
| `make test-race` | `go test ./... -race -count=1` | The test half of the PR gate |
| `make cover` | All packages that have tests, with `coverage.out` and a total | Looking for untested code |
| `make eval` | The retrieval benchmark, verbose, with its per-query table | After touching retrieval, ranking or chunking |
| `make golden-update` | Re-records the contract goldens | After a deliberate contract change. Review the diff |
| `make baseline-update` | Re-records the retrieval baseline | After a deliberate ranking change. Review the diff |
| `make scenario-docs` | Regenerates this guide's scenario pages | After changing a scenario file or the catalogue |
| `make scenario-studio` | The scenario studio on a loopback address | Reading, checking and running scenarios |
| `make tray-test` | Vet and race tests of the MemoRS Tray module | macOS, when touching `tray/` |
| `make docs-check` | Link and reference checks over the guides | When editing docs |

## Narrowing a run

```bash
go test ./internal/retrieve/ -count=1                            # one package
go test ./internal/eval/scenario/ -run TestScenarios -count=1 -v # one test, with its output
go test ./internal/eval/scenario/ -run 'TestScenarios/b2' -v     # one sub-test (one scenario file)
go test ./internal/server/ -run TestListToolsGolden -count=1     # one contract
```

- `-count=1` turns off Go's test cache. Use it whenever you want a test to actually run again.
- `-v` prints log lines and, for scenarios and the benchmark, their report tables.
- `-race` is slower, but it is what CI runs. A data race is a failing test, not a warning.
- `-run` takes a regular expression over test names. `/` separates sub-tests.

## How long it takes

On a recent laptop, `go test ./... -race -count=1` takes a few minutes. Most of that is
`internal/eval`, the retrieval benchmark (about two and a half minutes under the race
detector). Without `-race`, the whole suite is much faster. Every other package finishes in
seconds.

## When a test fails

1. Re-run just that package with `-count=1 -v`. Every test in the gate is deterministic: a fake
   clock, the hash embedder and no network. A test that passes on a re-run without a code change
   has found a bug (often a race), and is not "flaky".
2. Read the failure message. It names a file, a query id, a scenario step or a golden diff.
3. Use the page for that kind of test:
   - [goldens](../gates/goldens.md)
   - [the benchmark](../gates/retrieval-eval.md)
   - [scenarios](../scenarios/failures.md)
   - [docs](../gates/docs.md)
