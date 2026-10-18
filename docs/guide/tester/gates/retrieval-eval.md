# The retrieval benchmark

`internal/eval` is the measurement foundation of memors-mcp. Every ranking change in the
project's history was judged by it. It loads two labelled corpora into a throwaway knowledge
base, runs every labelled query, and compares the result with a recorded baseline **query by
query**.

```bash
make eval              # go test ./internal/eval/ -run TestRetrievalEval -count=1 -v
make baseline-update   # the same, re-recording internal/eval/testdata/baseline.json
```

## What is in it

- **Two corpora**, written as Go fixtures rather than files:
  - `corpus.go`, the notes corpus: `Corpus()`, `Queries()`
  - `corpus_kb.go`, the knowledge-base corpus: documents, facts, forgotten documents, and
    their queries
- **Labelled queries** in about twenty categories, for example `exact`, `long-document`,
  `multi-hop`, `temporal`, `revocation`, `poisoning` and `abstention`. Abstention queries have
  no right answer: the right result is "nothing".
- **The hash embedder** with the `default` profile. Queries that need real semantics
  (paraphrases) are marked real-model-only and recorded as *skipped* under the hash embedder.
- **One extra check:** for every long-document query, the first relevant hit must come through
  the semantic arm.

## How a regression is judged

`PairedGate` (`internal/eval/report.go`) compares the run with `testdata/baseline.json`. It
fails the test when **any** of these happens:

| Level | Rule |
|---|---|
| One query | nDCG@10 drops by more than 0.25 (about one rank band) |
| One query | A hit becomes a miss (MRR was above 0, now 0) |
| One query | An abstention query that abstained now returns results |
| Means | recall@1, recall@5, recall@10, MRR, nDCG@10 or the abstention rate drops by more than 0.02 |
| Per category | Mean nDCG@10 drops by more than 0.02 (the abstention category is judged per query only) |

Skipped queries, and queries not in the baseline, are ignored. A new query therefore does not
count until the baseline is re-recorded.

The paired, per-query design matters. A change that helps ten queries and breaks one still
fails, because the one broken query is named. The failure message lists every regression as
query id, metric, got and baseline.

## After a deliberate ranking change

1. Run `make eval` and read the per-query table: which queries moved, and why.
2. Run `memors-mcp eval --explain-failures` for one line per missed query explaining why.
3. If the new numbers are the ones you want, run `make baseline-update` and commit
   `testdata/baseline.json` with the change. Explain the moves in the pull request.
4. For a release, the numbers go into `docs/eval/vX.Y.Z.md`: a means table and a "what this
   phase measures" table.

## Other tests in the package

- `TestAblations` prints the same corpora under alternative strategies: keyword-only,
  semantic-only, text-only, no graph, the code profile, and the agent proxy. It is for
  reading, and only fails when a strategy fails to run.
- `TestPairedGateCatchesSingleQueryLoss` is a test of the gate itself.
- The metric functions (recall, MRR, nDCG) have their own unit tests.
- `TestMergePrecision` covers merge resolution and `TestUpdateStream` covers revisions.
- The fixture checks: unique keys, queries that reference real documents, and long documents
  that really need several chunks.

## `memors-mcp eval`: the same benchmark from the binary

```bash
memors-mcp eval --models hash,granite-small-r2 --profiles default,precise,code --corpus all
memors-mcp eval --models potion --profiles all --format md > potion.md
memors-mcp eval --explain-failures
```

| Flag | Meaning |
|---|---|
| `--models` | Comma-separated model ids. `hash` is the test embedder; other ids download models |
| `--profiles` | Comma-separated ranking profiles, or `all` |
| `--corpus` | `notes`, `kb` or `all` |
| `--format` | `table`, `md` or `json` |
| `--explain-failures` | Why each missed query was missed |
| `--agent-proxy` | Adds the two-round agent-proxy strategy |
| `--rerank` | Attaches the cross-encoder reranker |

It always builds a temporary knowledge base and never touches the configured one. The
operator guide's [Measuring a change](../../operator/tuning/measuring.md) covers using it to
choose a model or a profile for real data.

## What the benchmark cannot see

It loads everything once and then asks questions. It cannot see sequences, several actors,
time passing, corrections, forgetting, or concurrency. That is what the
[scenario tests](../scenarios/README.md) are for.
