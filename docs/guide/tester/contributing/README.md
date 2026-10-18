# Part V: Adding tests

## Which kind of test?

| You are changing or protecting… | Write |
|---|---|
| One function's behaviour, an edge case, an error path | A **package test** next to the code |
| What a client, the tray or Prometheus sees (tool schemas, `/live.json`, metric names) | Update the **golden** and review the diff ([Contract and golden tests](../gates/goldens.md)) |
| Ranking: which result comes first for a query | A **labelled query** in `internal/eval` (`corpus.go` or `corpus_kb.go`), then `make baseline-update` ([The retrieval benchmark](../gates/retrieval-eval.md)) |
| A sequence over time, several actors, trust and confirmation, forgetting, concurrency | A **scenario** ([Your first scenario](../scenarios/first.md)) |
| A property of the store that must always hold | An **invariant** ([Invariants](../scenarios/invariants.md#adding-an-invariant)) |
| A parser or anything that reads outside input | A **fuzz target** ([Fuzzing](../gates/fuzz-race.md)) |
| Behaviour nobody has decided yet | A **gap** plus a `decision: open` scenario ([Open and decided](../scenarios/decisions.md)) |
| A question like "is model X better?" | Not a test: a spike (`spikes/<name>`, `make spike NAME=…`) or `memors-mcp eval` |

## Conventions

- **Isolate state.** Use `t.TempDir()` for files and `t.Setenv("MEMORS_HOME", …)` for the CLI.
  Never read or write `~/.memors-mcp`.
- **No downloads, no network.** Use `embedding.NewHashEmbedder(384)`. A test that needs a real
  model is a nightly concern, not a unit test.
- **No sleeps.** Control time with `SetNow` on the store and the search service, or with
  `clock`/`at` in a scenario. Wait for goroutines with channels or `sync.WaitGroup`.
- **Assert behaviour, not numbers that drift.** Membership, order of the top results,
  abstention, refusals, audit rows. Not absolute scores or latencies. The benchmark judges
  quality with its paired gate.
- **Make it fail first.** Break the code (or the expectation) once and watch the test fail with
  a readable message.
- **Name what is being protected.** A test name like `TestForgetHidesFromAsOf` beats
  `TestForget2`. A scenario's leading comment explains its story.
- **Goldens are reviewed, not regenerated.** Never run an update command just to turn a test
  green.
- **Run under `-race`** before pushing: `make check`.

## Pull-request checklist

- [ ] `make check` passes locally.
- [ ] New behaviour has a test, and that test failed before the change.
- [ ] Golden or baseline changes are deliberate and explained in the description.
- [ ] A new scenario has `covers:`, a leading comment, and the right `decision`.
      `make scenario-docs` has been run and its pages committed.
- [ ] A behaviour you found but did not fix is recorded as a [gap](../scenarios/gaps.md) or a
      [catalogue](../scenarios/catalogue.md) entry, not left in a comment.
- [ ] Docs changed with the code: `make docs-check` passes. New env vars, commands or metrics
      are in the operator guide.
