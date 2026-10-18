# What is tested where

## The layers

| Layer | Where | Runs in | Fails on |
|---|---|---|---|
| Package tests | `*_test.go` next to the code | PR gate | Any behaviour change a test pins |
| Contract goldens | `internal/server`, `internal/ui`, `internal/obs`, the scenario renderer | PR gate | Any change to a public surface that was not re-recorded |
| Retrieval benchmark | `internal/eval` (`TestRetrievalEval`) | PR gate | A ranking regression against `testdata/baseline.json` |
| Real-model benchmark | `nightly.yml` (`memors-mcp eval` with a real model) | Nightly | Nothing: it is a report |
| Living-system scenarios | `internal/eval/scenario` (`TestScenarios`) and the files in `internal/eval/testdata/scenarios/` | PR gate (`ci` tier) | An unmet expectation or a broken invariant |
| Fuzzing | `FuzzBuild` in the scenario renderer | Seeds in the PR gate; open-ended fuzzing by hand | A panic or an error on any input |
| Race detector | Every package | PR gate | Any data race |
| Tray module | `tray/` (its own Go module) | PR gate on macOS, release | Behaviour, plus an end-to-end run against a real `memors-mcp` |
| Docs | `scripts/docs-check.sh`, `TestBookCurrent` | Docs workflow, PR gate | Broken links, undocumented env vars, commands or metrics, stale generated pages |
| Release smoke | `ci.yml` and `release.yml` | PR gate, tags | A broken pure-Go build, a wrong archive count, `memors-mcp version` failing |
| Spikes | `spikes/<name>` (`-tags spike`) | Never in CI | Not tests: one-off experiments, see `make spike` |

## Packages and what their tests cover

| Package | Focus of its tests |
|---|---|
| `internal/kb` | The store: schema and migrations (including legacy files under `testdata/`), ingest and revisions, facts and supersession, forget and redact, trust, pages and work items, verify and repair |
| `internal/retrieve` | The search pipeline: arms, fusion, profiles, cutoffs, `as_of`, facts over time (with a fake clock) |
| `internal/embedding` | The model registry and the hash embedder used by every other test |
| `internal/compact` | Compaction proposals, conflicts, lint, submit |
| `internal/server` | The MCP surface: exactly ten tools, honest descriptions, the `tools/list` golden, trust caps on the tool channel, elicitation |
| `internal/cli` | Every subcommand, with `MEMORS_HOME`/`MEMORS_KB` pointed at a temporary directory |
| `internal/ui` | The web UI and the `/live.json` contract (golden), the Host and CSP guard |
| `internal/obs` | Metrics and their text exposition (golden) |
| `internal/eval` | The retrieval benchmark, its metrics, the paired regression gate, ablations, merge precision |
| `internal/eval/scenario` | The scenario DSL, the runner, the invariant pack, and the scenario files themselves |
| `internal/eval/scenario/diagram` | The swimlane renderer: goldens, well-formed XML, determinism, the id contract, fuzzing |
| `internal/eval/scenario/studio` | The studio web server: pages, guard, runs, API |
| `internal/eval/scenario/coverage`, `book` | Coverage over the scenario files; this guide's generated pages and their freshness |
| `tray/...` | MemoRS Tray: supervisor, setup wizard, installer, menu, discovery (see [MemoRS Tray tests](../gates/tray.md)) |

## Helpers every test uses

- **Temporary state.** Tests create their files with `t.TempDir()` and point the CLI at them
  with `t.Setenv("MEMORS_HOME", …)`. There is no shared fixture database.
- **The hash embedder.** `embedding.NewHashEmbedder(384)` (`internal/embedding/fake.go`) is
  deterministic and offline. Texts that share words get similar vectors. It has no idea of
  synonyms, so paraphrase queries are marked as needing a real model and are skipped under it.
- **Fake clocks.** `kb.Store.SetNow` and `retrieve.Service.SetNow` fix "now". Scenarios
  drive them through `clock:` and per-step `at:` offsets. No test sleeps to make time pass.
