# Invariants

Expectations check what one step was meant to do. **Invariants** check that nothing else broke.
After every step of every scenario (after a parallel group as a whole), the runner checks the
whole store against fifteen properties. They must hold whatever happened before, in any
order. A broken invariant fails the step with `invariant <name>: <message>`.

The pack lives in `internal/eval/scenario/invariants.go`. The names are stable, because they
are what `invariants: {skip: […]}` refers to.

| # | Name | Must hold | Failure message |
|---|---|---|---|
| 1 | `one-live-revision` | Every source has at most one live revision (not forgotten, not superseded) | `N sources with more than one live revision` |
| 2 | `contiguous-revisions` | A source's revision numbers are exactly 1…n | `N sources whose revision numbers are not 1..n` |
| 3 | `acyclic-supersession` | Following `superseded_by` from any fact never loops | `fact supersession cycle through memo://fact/…` |
| 4 | `invalidated-has-successor` | A fact marked invalidated names the fact that replaced it | `N invalidated facts with no successor recorded` |
| 5 | `no-chunks-for-forgotten` | A forgotten document has no indexed passages left | `N chunks of forgotten documents still indexed` |
| 6 | `forgotten-never-served` | Every forgotten fact and document, searched for by its own statement or title, live and under `as_of`, never comes back | `forgotten memo://… is still served (as_of=…)` |
| 7 | `tool-channel-cap` | No audit row shows a tool call that promoted or demoted, or that forgot, redacted, superseded or resolved a `user` or `curated` record | `N audit rows where a tool call acted above the agent trust cap` |
| 8 | `audit-per-mutation` | A step that changed something wrote an audit row: an ingest that made a revision, a remember, forget, demote, merge or reject, an applied promote, an applied submit | `<op> succeeded and wrote no audit row` |
| 9 | `raw-unchanged-by-compaction` | `compact` and `submit` never change a raw source, document or chunk row | `<op> changed a raw source, document or chunk row` |
| 10 | `page-sources-exist` | Every passage a page cites still exists | `N page sources pointing at missing chunks` |
| 11 | `stale-consistent` | A live page whose source revision was superseded or forgotten is marked stale | `N pages not marked stale although a source revision is gone` |
| 12 | `one-open-item-per-subject` | At most one open work item per kind and subject | `N subjects with more than one open work item of a kind` |
| 13 | `mentions-live` | Every graph mention points at an existing passage | `N mentions pointing at missing chunks` |
| 14 | `status-matches-sql` | The status counters agree with a direct count of live documents and chunks | `status says … live documents and … chunks; SQL says …` |
| 15 | `verify-clean` | `verify` (the same check as `memors-mcp verify`) finds no problem | `verify: <problems>` |

## Why these fifteen

Each invariant is a promise memors-mcp makes to its users, stated over the database rather than
over one response:

- **Revisions** (1, 2): a document has one current text and a clean history.
- **Facts over time** (3, 4): supersession is a history, never a loop or a dead end.
- **Forgetting** (5, 6): forgotten means unreachable, through every search path, including
  "what did we know back then".
- **Trust** (7, 8): agents cannot override humans, and every change is accountable.
- **Compaction** (9, 10, 11, 12): pages are derived data. They never rewrite the raw record,
  never cite what is gone, admit when they are stale, and do not pile up duplicate work.
- **Self-consistency** (13, 14, 15): the graph, the counters and the integrity checker agree
  with the tables.

`forgotten-never-served` is the most expensive: it runs real searches for every forgotten
record after every step. It is also the one most likely to catch a regression in a new search
arm.

## Skipping an invariant

```yaml
invariants: {skip: [stale-consistent]}          # whole file
steps:
  - …
    invariants: {skip: [verify-clean]}          # after this step only
```

A skip is only acceptable in a `decision: open` scenario that documents a gap in which the
invariant is *known* to break. For example, a scenario pinning [gap 13](gaps.md#gap-13)
(passages left without vectors after a crash) may skip `verify-clean` after the crash step.
Rules:

- Skip at the narrowest level: one step, not the whole file.
- Add a `note` on the step saying which gap the skip documents.
- When the gap is fixed, remove the skip in the same change.

Skips are visible: the [implemented scenarios](log/README.md) log lists every skipped
invariant and the files that skip it, and each scenario page lists its own.

## Adding an invariant

An invariant belongs in the pack when it is true for **every** state the system can reach, not
only after particular steps. Add it to `Invariants` in `invariants.go` with a stable name and
a message that counts or names the offending rows. Then run all scenarios. If an existing
scenario breaks it, you have found a bug or a gap. Document it; do not weaken the invariant.
Then add the invariant to the table on this page.
