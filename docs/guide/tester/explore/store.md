# Checking the store

After an experiment, check that the store is still consistent: the same properties the
[scenario invariants](../scenarios/invariants.md) check after every step, by hand.

| Command | Tells you |
|---|---|
| `memors-mcp status` | Sources, live documents and revisions, chunks, facts, queued and failed jobs, namespaces, the model in use |
| `memors-mcp verify` | Integrity: chunks, vectors, indexes. `ok: no problems found` or a list |
| `memors-mcp verify --repair` | Queues missing vectors and rebuilds broken indexes. Run `verify` again afterwards |
| `memors-mcp lint [--ns]` | Contradictions, orphan entities, missing or stale pages, expired facts |
| `memors-mcp backfill` | Embeds passages whose vectors are pending |
| `memors-mcp read <memo://…> [--history]` | One record with its provenance, or its revision chain. A forgotten record shows its tombstone and the reason |
| `memors-mcp facts ls [--as-of 2026-01-01] [--history]` | Facts as they were believed on a date, with supersession |
| `memors-mcp trust ls` | What has been promoted or demoted |
| `memors-mcp explore <name>` | The graph neighbourhood of one entity |
| `memors-mcp graph merges` | Near-duplicate names waiting for a decision |
| `memors-mcp compact [--lint]` | The work the system proposes (pages, conflicts, merges) |
| `memors-mcp submit <item> … --dry-run` | What a submission would do, without doing it |

## Checks worth doing after any experiment

1. `verify` is clean, and `status` has no failed jobs.
2. Everything you forgot is gone from `search`, `read` (tombstone only), `facts ls` and
   `explore`, also with `--as-of` a date before you forgot it.
3. `lint` reports exactly the contradictions you created, no more.
4. `read --history` of a document you re-ingested shows contiguous revisions with one live.

## Watching it live

```bash
memors-mcp ui                       # read-only web UI on loopback; prints the URL
memors-mcp ui --no-model            # the same, without loading a model
```

With `serve --http`, the live page at `/` shows connected sessions and calls in flight, and
`/live.json` is the machine-readable snapshot that MemoRS Tray reads. Its shape is pinned by a
[golden test](../gates/goldens.md).
