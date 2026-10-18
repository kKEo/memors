# Your first scenario

This walk-through writes a five-step scenario, checks it, draws it, runs it, and breaks it on
purpose. It takes about fifteen minutes. Work in a scratch directory first:

```bash
mkdir -p /tmp/scn
```

## 1. The story

Write the story down before any YAML. Who acts, what happens over time, and what must be
observable at the end:

> An agent ingests a runbook and finds it. The next day a human forgets the runbook with a
> reason. The agent's next search must come back empty, and the tombstone must still say why.

## 2. The file

Save this as `/tmp/scn/x1-forget-hides-a-document.yaml`:

```yaml
# X1 — A human forgets a document an agent relied on. The agent finds the
# runbook, the human forgets it with a reason, and the agent's next search
# must come back empty while the tombstone still says why.
name: x1-forget-hides-a-document
description: a forgotten document is never served again, and its tombstone keeps the reason
decision: decided
tiers: [ci]
clock: 2026-04-01
actors:
  bot: {kind: agent, client: claude-code}
  ann: {kind: human}
steps:
  - at: 0d
    actor: bot
    op: ingest
    args:
      namespace: ops
      content: |
        # Rotating the API keys

        Rotate the API keys every ninety days from the vault console.
      source: {uri: https://ops.example/keys, title: Rotating the API keys, kind: doc}
    bind: runbook
    expect:
      out: {revision: 1}
      audit: [{op: ingest, actor: claude-code, channel: tool}]

  - actor: bot
    op: search
    args: {query: rotate the API keys, scope: {namespaces: [ops]}}
    expect: {top: [$runbook]}

  - at: 1d
    actor: ann
    op: forget
    args: {uri: $runbook, reason: replaced by the new secrets policy}
    expect:
      audit: [{op: forget, channel: cli, target: $runbook}]

  - actor: bot
    op: search
    args: {query: rotate the API keys, scope: {namespaces: [ops]}}
    expect: {abstains: true, excludes: [$runbook]}

  - actor: ann
    op: read
    args: {uri: $runbook}
    expect:
      out: {tombstone: true}
      out_contains: {reason: secrets policy}
```

What each part does:

- **The leading comment** is the author's account of why the file exists. The generated
  scenario page shows it under "Why it exists", so write it for a reader.
- **`actors`**: `bot` is an MCP session whose client calls itself `claude-code`. Audit rows
  will name it so. `ann` is a human on the CLI channel.
- **`bind: runbook`** names the ingested document. Later steps say `$runbook` and get its
  `memo://doc/…` address.
- **`at`** moves the fake clock: day 0, then day 1. A step without `at` happens at the same
  moment as the previous one.
- **`args`** are exactly the tool's arguments (for agents) or the CLI command's (for humans).
  A wrong argument name is a tool error, as step 2 would show with `namespace:` instead of
  `scope:`.
- **`expect`** lists what must be true after the step. Anything not listed is not checked,
  but the [invariants](invariants.md) still run.

## 3. Check it

```bash
go run ./cmd/memors-scenario validate /tmp/scn
```

`validate` parses the file strictly: an unknown field, a step using a `$name` nobody bound
yet, an op the actor kind cannot perform, or time going backwards is reported as
`file:line: message`. It does not run anything, so it takes milliseconds.

## 4. Read it back

```bash
go run ./cmd/memors-scenario describe /tmp/scn/x1-forget-hides-a-document.yaml
go run ./cmd/memors-scenario render --out /tmp/scn /tmp/scn
open /tmp/scn/x1-forget-hides-a-document.svg
```

`describe` tells the scenario in one paragraph. It is useful in a pull request description.
`render` draws the swimlane picture: one lane per actor, time flowing down, a box per step
with its arguments and expectation chips, and a dashed edge from `$runbook` to every step that
uses it. In the standalone file, click "… more" on a long argument to see it in full.
[Reading the picture](pictures.md) explains every mark.

## 5. Run it

```bash
go run ./cmd/memors-scenario run /tmp/scn/x1-forget-hides-a-document.yaml 2>/dev/null
```

```
### x1-forget-hides-a-document — pass (decision: decided)

| # | at | actor | op | result | failures |
|---|---|---|---|---|---|
| 1 | 0d | bot | ingest | memo://doc/01a14e81-… |  |
| 2 |  | bot | search | 1 results |  |
| 3 | 1d | ann | forget | memo://doc/01a14e81-… |  |
| 4 |  | bot | search | 0 results |  |
| 5 |  | ann | read |  |  |
```

The server's own log lines go to stderr (hidden above with `2>/dev/null`). Exit code 0 means
every expectation held and every invariant held after every step.

Or open it in the studio, which shows the picture next to the source and paints a run onto
it:

```bash
go run ./cmd/memors-scenario serve --dir /tmp/scn
```

## 6. Break it on purpose

A test you have never seen fail proves little. Try each change, run again, and read the
failure:

| Change | What you should see |
|---|---|
| In step 2, `scope: {namespaces: [ops]}` → `namespace: ops` | Step 2 fails with the tool's own argument validation error |
| In step 4, `abstains: true` → `abstains: false` | `expected results, got an abstention (no arm matched)` |
| In step 5, `secrets policy` → `old policy` | `out.reason: "replaced by the new secrets policy" does not contain "old policy"` |
| `bind: runbook` → `bind: doc` | `validate` prints one line per use, starting with `x1-….yaml:31: step 2: $runbook is not bound by an earlier step` |

[When a scenario fails](failures.md) covers reading failures in detail.

## 7. Make it part of CI

1. Pick its catalogue id from the [catalogue](catalogue.md), or add an entry to
   `internal/eval/testdata/scenario-catalogue.yaml`. Name the file after it
   (`d2-revoked-but-still-reachable.yaml`).
2. Add `covers:` with the catalogue id and any [gaps](gaps.md) it pins.
3. Choose `decision`: `decided` if the expectations are the behaviour the owner wants, `open`
   if they pin today's behaviour of a gap ([Open and decided](decisions.md)).
4. Move it to `internal/eval/testdata/scenarios/` and run:

   ```bash
   go test ./internal/eval/scenario/ -run TestScenarios -count=1
   make scenario-docs      # its page, picture and SUMMARY entry in this guide
   ```

5. Commit the file and the regenerated pages together. The PR gate fails if the pages are
   missing (`TestBookCurrent`), or if `covers` names an id the catalogue does not define
   (`TestCoversKnown`).
