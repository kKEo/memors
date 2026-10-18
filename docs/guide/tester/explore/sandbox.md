# A sandbox knowledge base

Never explore on a knowledge base that matters. memors-mcp keeps everything under
`MEMORS_HOME` (default `~/.memors-mcp`), so pointing it elsewhere gives you a clean,
disposable copy of the whole system:

```bash
export MEMORS_HOME=$(mktemp -d)       # everything goes here: kb/<name>.db, models, tokens
export MEMORS_KB=sandbox              # the file is $MEMORS_HOME/kb/sandbox.db
export MEMORS_QUERY_LOG=1             # keep a log of searches and tool calls (see Probing retrieval)
```

Use a terminal you close afterwards, or `unset` the variables, so later commands go back to
your real knowledge base.

## With or without a model

| Setup | How | Good for |
|---|---|---|
| No model | `export MEMORS_MODEL=not-a-model` | Fast, offline. Search is keyword-only and says `degraded: keyword-only`. Vectors queue for `backfill`. Trust, forgetting, facts, audit and lint all behave normally |
| A real model | Leave `MEMORS_MODEL` unset (default `granite-small-r2`) or pick one from `memors-mcp model ls`. `memors-mcp model smoke` checks it loads | Ranking, paraphrases, semantic search: what users actually get |

The first real-model run downloads the model into `$MEMORS_HOME`. Copy a model directory in to
avoid downloading again, or keep one `MEMORS_HOME` per model.

## Seeding it

```bash
printf '# Rotating keys\n\nRotate the API keys every ninety days from the vault console.\n' > keys.md
memors-mcp ingest keys.md --ns ops --title "Rotating keys"
memors-mcp ingest ./some-docs/ --ns docs                  # a directory
memors-mcp remember "API keys rotate every 90 days" --ns ops --about keys
memors-mcp ls
```

Realistic material finds more than lorem ipsum: a few of your own notes, a project's
README, two versions of the same document, a note that contradicts a document.

## Acting as an agent

The CLI is the **human** channel (trust `user`). To see what an **agent** sees (trust `agent`,
the tool channel's limits, dialogs), connect a real MCP client to the sandbox. Start your
assistant with the same `MEMORS_HOME` and `MEMORS_KB`, or run one shared server:

```bash
memors-mcp serve --http 127.0.0.1:8765      # MCP at /mcp, the live UI at /
```

Then point a client at it (see [Connecting MCP clients](../../operator/install/clients.md)).
Two clients connected at once is how most concurrency and actor-name questions are found.

## Starting over

```bash
rm -rf "$MEMORS_HOME"
```

Or keep the directory as evidence: the `.db` file is the whole state, and attaching it to a
bug report reproduces exactly what you saw.
