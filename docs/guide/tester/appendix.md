# Appendix: words and commands

## Words

| Word | Meaning here |
|---|---|
| **Abstention** | A search that correctly returns nothing, with a reason, because nothing is relevant |
| **Actor** | Who performs a scenario step: an `agent` (an MCP session) or a `human` (the CLI channel) |
| **Arm** | One retrieval method inside search (keyword, semantic, exact, fact, entity, graph). Results from arms are fused |
| **Audit row** | The record of a change: op, actor, channel, target. Every mutation writes one; a refusal writes none |
| **Baseline** | `internal/eval/testdata/baseline.json`: the recorded per-query results the benchmark compares against |
| **Bind** | A name a scenario step gives its result (`bind: f1`), used later as `$f1` |
| **Catalogue** | The list of scenario stories to write, with ids A1 to F6 |
| **Channel** | How a change arrived: `tool` (an agent), `cli` (a human), `elicitation` (a human answering a dialog) |
| **Decision** | `open` (pins today's behaviour of a gap) or `decided` (wanted behaviour) |
| **Elicitation** | A dialog the server asks the client to show a human, for example to raise trust |
| **Gap** | A behaviour found by reading the code that no test pinned and nobody decided |
| **Golden** | A committed file an output must match byte for byte |
| **Hash embedder** | The deterministic, offline test embedder: shared words give similar vectors |
| **Invariant** | A property of the whole store checked after every scenario step |
| **Paired gate** | The benchmark's rule that compares each query with its own baseline, not only the averages |
| **Scenario** | A YAML file of steps by actors over a fake clock, with expectations |
| **Studio** | The local web app for scenarios (`make scenario-studio`) |
| **Tier** | Where a scenario runs: `ci`, `nightly`, `binary` |
| **Tombstone** | What remains of a forgotten record: its address, when it was forgotten, and the reason |
| **Trust** | `agent` < `user` < `curated`. Agents may only change `agent` records |

The [user guide's glossary](../user/glossary.md) and the
[operator guide's glossary](../operator/appendix/glossary.md) define the product's own terms.

## Commands

```bash
# the gate
make check                                   # gofmt, vet, lint, race tests: what CI runs
go test ./<pkg>/ -run <Test> -count=1 -v     # one test, uncached, with output

# contracts and baselines
make golden-update                           # re-record goldens (review the diff!)
UPDATE_GOLDEN=1 go test ./internal/obs/ -run TestPromTextGolden
make eval                                    # retrieval benchmark with its table
make baseline-update                         # re-record the baseline (review the diff!)
memors-mcp eval --models hash,granite-small-r2 --profiles all --explain-failures

# scenarios
go test ./internal/eval/scenario/ -run TestScenarios -count=1 -v
go run ./cmd/memors-scenario validate|describe|render|run|book|serve …
make scenario-studio
make scenario-docs                           # regenerate this guide's scenario pages

# fuzzing, races, tray, docs
go test ./internal/eval/scenario/diagram/ -run '^$' -fuzz FuzzBuild -fuzztime 60s
go test ./<pkg>/ -race -count=50 -run <Test>
make tray-test
make docs-check && make docs

# exploring
export MEMORS_HOME=$(mktemp -d) MEMORS_KB=sandbox MEMORS_QUERY_LOG=1
memors-mcp search "…" --explain; memors-mcp explain "…" [memo://…]
memors-mcp log tail|calls|replay; memors-mcp status; memors-mcp verify; memors-mcp lint
```
