# Fuzzing and the race detector

## The race detector

Every package runs under `-race` in the PR gate (`make test-race`). The race detector finds
unsynchronised access to shared memory *while the test runs*. It only sees races that the
test exercises, so tests that use goroutines (the HTTP server, the tray supervisor, `parallel`
scenario groups) are the ones that make it useful.

- A race report is a failing test. Fix the cause; never add a sleep.
- Run a suspect test many times: `go test ./internal/<pkg>/ -race -count=50 -run <Test>`.
- Releases run the suite without `-race`, because it already passed on the merge commit.

## Fuzzing

Go's native fuzzing feeds generated inputs to a function and keeps any input that makes it
fail. The repository has one fuzz target:

| Target | Package | Property |
|---|---|---|
| `FuzzBuild` | `internal/eval/scenario/diagram` | Any bytes that parse as a scenario can be validated and rendered, standalone and inline, without an error or a panic |

In the normal test run, `FuzzBuild` only runs its **seed corpus** (the scenario files, plus a
one-step script) as a regular test. To actually fuzz:

```bash
go test ./internal/eval/scenario/diagram/ -run '^$' -fuzz FuzzBuild -fuzztime 60s
```

When fuzzing finds a failure, Go writes the input under
`internal/eval/scenario/diagram/testdata/fuzz/FuzzBuild/`. That file then runs on every
`go test` like a seed. Commit it together with the fix, so the bug stays fixed.

Good candidates for new fuzz targets are parsers and anything that takes text from outside:
the scenario DSL (`scenario.ParseLoose`), front matter in `ingest`, query parsing.
