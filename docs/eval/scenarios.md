# Living-system scenarios

*The static eval (`internal/eval`, `make eval`) loads two corpora once and runs labelled
queries: it measures ranking. This file is the second gate: scripted scenarios in which
documents arrive over days, facts are recorded and corrected, humans confirm or reject what
agents wrote, and several agents write into shared namespaces at once. Scenarios are YAML
files under `internal/eval/testdata/scenarios/`, run by `internal/eval/scenario` against the
real store and the real MCP server in-process, with a fake clock and the deterministic hash
embedder, so they run in PR CI without a model download.*

```
go test ./internal/eval/scenario/ -run TestScenarios -count=1 -v
```

One table per scenario is printed; any unmet expectation or broken invariant fails the test.

**The reference now lives in the tester guide** (`docs/guide/tester/`, published with the user
and operator guides). This file keeps the reasoning behind the harness and the design notes for
what comes next.

| Topic | Where |
|---|---|
| Overview, life of a scenario | [Scenario tests](../guide/tester/scenarios/README.md) |
| Walk-through | [Your first scenario](../guide/tester/scenarios/first.md) |
| DSL reference (fields, ops, binds, expectations, validation) | [Writing scenarios](../guide/tester/scenarios/dsl.md) |
| The invariant pack | [Invariants](../guide/tester/scenarios/invariants.md) |
| Pictures, `memors-scenario`, the studio | [Reading the picture](../guide/tester/scenarios/pictures.md), [memors-scenario](../guide/tester/scenarios/tools.md), [The studio](../guide/tester/scenarios/studio.md) |
| Failures, open vs decided | [When a scenario fails](../guide/tester/scenarios/failures.md), [Open and decided](../guide/tester/scenarios/decisions.md) |
| The behaviour gaps (formerly §4) | [Behaviour gaps](../guide/tester/scenarios/gaps.md), generated from `internal/eval/testdata/scenario-catalogue.yaml` |
| The catalogue A–F (formerly §5) | [Catalogue](../guide/tester/scenarios/catalogue.md), generated from the same file, with each entry's status |
| The scenarios in the repository and their pictures (formerly §3) | [Implemented scenarios](../guide/tester/scenarios/log/README.md), one generated page and picture per file |

The gaps and the catalogue are edited in `scenario-catalogue.yaml`, never in markdown;
`make scenario-docs` regenerates the pages and `TestBookCurrent` fails until it has run.

## 1. Why a second gate

A load-then-query benchmark cannot see sequences, actors, time or concurrency. Reading the
code with that lens surfaced fifteen behaviours that no existing test pins down (the [gaps](../guide/tester/scenarios/gaps.md)). Some
are bugs, some are decisions never taken; none of them show up as a ranking number. The
scenario harness makes each one a script that runs on every change, so that a change to the
embedding model or the retrieval pipeline is judged on the living system, not only on a
static corpus.

The harness follows practice that has worked elsewhere: deterministic simulation (seeded
actors and a fake clock against the real code, invariants after every step, failures that
reproduce from a file), stateful property testing (random operation walks saved as scenario
files when they fail), fault injection at named points, metamorphic relations for ranking
where no label exists, time-planted ground truth (facts with validity windows first, documents
generated from them), and observability as the oracle (audit rows, lint, status, counters).
It deliberately avoids golden snapshots of whole result lists, wall-clock sleeps, absolute
score assertions and model-judged win rates, and it never labels to current behaviour without
saying so (`decision: open`).

## 2. What comes next (design notes)

1. Owner decisions on the [gaps](../guide/tester/scenarios/gaps.md) (keep, change, document only), recorded as each gap's `decision` in `scenario-catalogue.yaml`.
2. Story files for A–D and F2/F5/F6 (CI tier); C5, F3, F4 tagged nightly.
3. A seeded random-walk generator that emits scenario files, an in-memory model and the same
   invariant pack; failing walks are saved as files; a `crash_at` hook for E1.
4. `parallel` groups over `serve --http` with N named sessions under the race detector.
5. The nightly tier: `memors-mcp eval --scenarios` with a real model, the binary executor for
   E1/E3/E4, the F1 generator at scale with p95 per tool, and the replay corpus from
   `memors-mcp log replay`.
6. Editing in the studio: a text editor on the scenario page with live `/api/validate` and
   `/api/render`, saving with the file hash as the conflict check, structural edits (add,
   duplicate, reorder a step; add an expectation) applied on the YAML node tree so comments
   survive, and "record expectations from the last run", which must force `decision: open`
   because it labels to current behaviour. The `Lines` index and the `data-path` attributes
   on every drawn element are the click-to-node mapping it will use.
7. Later: two runs (baseline and candidate model or profile) on one picture with per-step
   deltas, a scenario skeleton captured from `memors-mcp log replay` or the audit table, and a
   Mermaid export for pull-request descriptions.
