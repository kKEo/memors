# Probing retrieval

## Why did this rank here?

```bash
memors-mcp search "rotate api keys" --explain           # results with a one-line why each
memors-mcp explain "rotate api keys"                    # a ranking table for every hit
memors-mcp explain "rotate api keys" memo://chunk/812   # the full reasoning for one hit
```

Every result carries its **why**: which arms found it and at what rank, the fused score, the
recency factor, and the trust. The trace at the end says what was filtered out (scope,
forgotten, superseded, minimum trust), why the list ended (gap, limit or budget), and whether
anything was **degraded**, for example "no embedding model available; semantic arm skipped".
The operator guide's [search pipeline](../../operator/intro/search-pipeline.md) explains each
field.

Questions worth asking:

- **Does the same question in other words find the same thing?** Paraphrases need a real model.
- **Is a forgotten or superseded record ever returned?** Try `--granularity chunk` and
  `document` here. Facts, pages and `as_of` a past date go through the `search` tool from a
  connected client, or `memors-mcp facts ls --as-of`.
- **Do scope and trust filters ever lose results they should keep, or keep ones they should
  drop?** Try `--ns`, `--kind`, `--library`, `--version`, `--min-trust`.
- **Does an exact identifier (`SetCacheable`, an error code) come first?**
- **When nothing is relevant, does it abstain** rather than return the least-bad match?

```bash
memors-mcp profiles show            # every ranking constant and where it comes from
memors-mcp search --profile precise "…" --explain
```

## The logs

With `MEMORS_QUERY_LOG=1`, every search and tool call is logged in the knowledge-base file:

```bash
memors-mcp log tail --n 20          # recent searches: when, arms, results, latency, query
memors-mcp log calls --n 50         # recent tool calls: client, tool, outcome, error class
memors-mcp log show <id>            # one entry in full
memors-mcp metrics --since 24h      # gauges and per-tool statistics from the log
```

The call log is where an agent's experience shows: a tool that keeps failing with the same
error class, a client that never passes `scope`, searches that always abstain.

## From real questions to benchmark queries

```bash
memors-mcp log replay --n 200 > candidates.jsonl
```

Each line is a logged query, with the addresses that were returned, as an unlabelled eval
query. Label which addresses are relevant, and the questions people actually ask become
queries for [the retrieval benchmark](../gates/retrieval-eval.md) or the ground truth of a
scenario.

## Comparing models and profiles on the lab corpora

```bash
memors-mcp eval --models hash,granite-small-r2 --profiles all --corpus all
memors-mcp eval --explain-failures
```

`eval` always uses its own temporary knowledge base. See
[The retrieval benchmark](../gates/retrieval-eval.md#memors-mcp-eval-the-same-benchmark-from-the-binary).
