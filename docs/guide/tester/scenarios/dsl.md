# Writing scenarios: reference

A scenario is one YAML (or JSON) file. Parsing is strict: an unknown field anywhere is an
error, so a typo never silently turns an expectation off. The types live in
`internal/eval/scenario/dsl.go`.

## The file at a glance

```yaml
# Why this scenario exists, for a reader (shown on its generated page).
name: b2-zombie-fact          # required; usually the file name without .yaml
description: an agent re-records a statement a human had forgotten with a reason
decision: open                # open | decided (default)
tiers: [ci]                   # ci (default) | nightly | binary
covers: [B2, gap-15]          # catalogue ids and gaps this file exercises
clock: 2026-01-05             # the fake clock at the first step; default 2026-01-01
actors:
  a1: {kind: agent, client: claude-code, elicitation: decline}
  h:  {kind: human}
invariants: {skip: []}        # rarely: switch invariants off for the whole file
steps:
  - at: 0d
    actor: a1
    op: ingest
    args: {…}
    bind: pager
    expect: {…}
    note: shown in the picture and the step table
```

## Header fields

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | The scenario's name in reports and pictures |
| `description` | no | One line: what it checks. Shown in the log and under the picture's title |
| `decision` | no | `decided` (default): the expectations are the wanted behaviour. `open`: they pin today's behaviour of a documented gap. See [Open and decided](decisions.md) |
| `tiers` | no | Where it runs. `ci`: hash embedder, every pull request (the default). `nightly`: a real model. `binary`: the built `memors-mcp` process. Only `ci` runs today |
| `covers` | no | Ids from the [catalogue](catalogue.md) (`B2`) and the [gaps](gaps.md) (`gap-15`). Every id must exist in `scenario-catalogue.yaml` (`TestCoversKnown`). A file named `b2-….yaml` covers `B2` implicitly |
| `clock` | no | `YYYY-MM-DD` or RFC 3339. The moment of the first step |
| `seed` | no | Reserved for generated scenarios |
| `actors` | yes | At least one |
| `invariants` | no | `{skip: [name, …]}` for the whole file. Prefer a step-level skip |
| `steps` | yes | At least one |

## Actors

```yaml
actors:
  a1:  {kind: agent, client: claude-code, elicitation: accept}
  a2:  {kind: agent, client: agent-beta}
  h:   {kind: human}
  ops: {kind: human, client: alice}
```

| Field | Applies to | Meaning |
|---|---|---|
| `kind` | all | `agent`: an MCP client session calling the ten tools. `human`: the CLI channel |
| `client` | all | For an agent, its `ClientInfo.Name`, which audit rows and the live page show. For a human, the actor name in audit. Default: the actor key |
| `elicitation` | agents | How the client answers a dialog (for example "raise this fact's trust?"): `none` (default, the client cannot show dialogs), `accept`, `decline`, `cancel` |
| `channel` | humans | Only `cli` is supported |

Each agent gets its own session, so two agents are two clients, exactly as with two
assistants connected to one server.

## Time: `clock` and `at`

- `at` is an offset from `clock`: `0d`, `2d`, `3h`, `90m`, `10s`.
- A step without `at` happens at the same moment as the previous step.
- Offsets never go backwards. `validate` reports `at 1d goes backwards (previous 48h0m0s)`.
- Time moves only through `at`: the runner sets the store's and the search service's clock
  before each step. A scenario spanning two years runs as fast as one spanning a minute.

## Steps

| Field | Meaning |
|---|---|
| `actor` | An actor key |
| `op` | What the actor does (see the two tables below) |
| `args` | The operation's arguments. For agents, exactly the tool's input. For humans, the CLI operation's |
| `bind` | Name the result so later steps can say `$name` |
| `select` | On `compact` only: which work item to bind (`{kind: conflict, subject: pager}`) |
| `elicit` | On agent steps: override the actor's dialog answer for this step (`accept`, `decline`, `cancel`) |
| `expect` | What must be observable after the step |
| `invariants` | `{skip: [name]}` after this step only |
| `note` | Free text, shown in the picture and the step table. Say why the step matters, or which gap it shows |
| `parallel` | A group of sub-steps run at once (see below) |

### Agent operations: the ten MCP tools

`ingest`, `search`, `read`, `remember`, `forget`, `promote`, `explore`, `compact`, `submit`,
`status`.

The arguments are the tool's own input schema. The server validates them, so a wrong name
fails the step with the tool's validation message. A tool error fails the step unless the step
expects it (`error: true` or `error_contains`). The tool channel's trust cap applies: an agent
cannot promote, demote, forget or supersede a `user` or `curated` record. Such a call returns
a "needs a human" error and writes no audit row.

### Human operations: the CLI channel

| Op | Arguments | Notes |
|---|---|---|
| `ingest` | `namespace`, `content`, `context`, `document`, `trust`, `entities`, `source: {uri, title, kind, library, version, origin, tags}` | Trust defaults to `user`. `curated` must be written explicitly |
| `remember` | `namespace`, `statement`, `about`, `supersedes`, `evidence_uri`, `origin`, `trust`, `valid_from`, `valid_to` | Trust defaults to `user` |
| `forget` | `uri`, `reason`, `redact` | |
| `promote`, `demote` | `uri`, `to` | |
| `merge`, `reject` | `id`, or `a` and `b` (the two names) | Accept or reject an open merge candidate |
| `compact` | `namespace`, `kinds` | Proposes work items, like `memors-mcp compact` |
| `submit` | `item_id`, `title`, `content`, `keep`, `accept`, `skip`, `reason`, `dry_run` | Resolves a work item |
| `lint` | `namespace` | Output: `findings` |
| `verify` | `repair` | Output: `problems`, `repaired`, `clean` |
| `backfill` | | Output: `embedded` |
| `status` | | The status counters |
| `search` | `query`, `granularity` (default `document`), `mode`, `limit`, `scope`, `as_of`, `exclude_ids` | With the explain format |
| `read` | `uri` | A forgotten record gives `{tombstone: true, reason, forgotten_at}` |
| `history` | `uri` | Output: `entries` |
| `facts` | `namespace`, `as_of`, `history` | Output: `facts` |

`namespace` defaults to `default` where it applies.

## Binds and references

`bind: name` stores what the step produced. Any later string in `args` or `expect` may then
use `$name`, and the runner substitutes it before the step runs.

| Bound by | `$name` | Also |
|---|---|---|
| `ingest` | the document's `memo://doc/…` | `$name.chunk`: the first passage's `memo://chunk/…`. `$name.id`: the bare document id |
| `remember` | the fact's `memo://fact/…` | |
| `submit` | the page's address | |
| `compact` with `select` | the chosen work item's id | `$name.kind`. For a conflict, `$name.a` and `$name.b`: the two facts |
| other ops | the output's `uri`, if any | |

`select` matches `kind` exactly and `subject` as a case-insensitive substring of the item's
subject or entity name, or of a conflict's statements. When nothing matches, the step fails
with `no work item matches select …`.

A reference to a name that no earlier step binds is a validation error. A step's own `bind`
may be used in its own `expect`, for example `audit: [{op: remember, target: $f1}]`.

## Parallel groups

```yaml
  - at: 2d
    note: two agents ingest at once
    parallel:
      - {actor: a1, op: ingest, args: {…}, bind: d1}
      - {actor: a2, op: ingest, args: {…}, bind: d2, expect: {…}}
```

- The sub-steps run concurrently, each on its own actor's session. Their binds and
  expectations are then applied in order.
- A group carries only `at`, `note`, `invariants` and `parallel`. Sub-steps have no `at` and no
  nested `parallel`.
- Binds made inside a group are visible to the steps after it.
- Audit expectations of a sub-step see every row the group wrote, because the rows cannot be
  told apart by timing. Use `actor` in the audit expectation to pin whose row it is.
- Invariants run once, after the whole group. Skip them on the group, not on a sub-step.
- A sub-step's failures are reported on the group's row, prefixed with `actor/op:`.
- Under `go test -race` a group also exercises the server's concurrency.

## Expectations

| Key | Passes when | Failure message (example) |
|---|---|---|
| `top: [$a, $b]` | Results start with these, in order. A result matches by its uri, document uri or chunk uri | `top[0]: want memo://doc/…, got memo://doc/…` |
| `contains: [$a]` | Each is somewhere in the results (search results, facts, history entries, items, findings) | `results lack memo://… (got …)` |
| `excludes: [$a]` | None of them is in the results | `results must not contain memo://…` |
| `abstains: true` | Zero results and a reason | `expected abstention (zero results with a reason); got 2 results, reason ""` |
| `abstains: false` | At least one result | `expected results, got an abstention (no arm matched)` |
| `count: n` | Exactly n results | `want 1 results, got 2` |
| `out: {path: value}` | A field of the step's output, by dotted path (`applied: false`, `results.0.provenance.trust: user`) | `out.applied: want true, got false` |
| `out_contains: {path: text}` | A string field contains the text | `out.reason: "…" does not contain "old policy"` |
| `error: true` | The op fails (a tool error or a refused CLI op) | `expected an error, the op succeeded` |
| `error_contains: text` | The op fails and the error contains the text | `error "…" does not contain "…"` |
| `lint: {namespace, has: [kind], has_not: [kind]}` | Lint after the step reports, or does not report, these finding kinds. `kind:$ref` pins the finding's address. `namespace` defaults to the step's own | `lint lacks contradiction (findings: …)`, `lint must not report resurrected` |
| `audit: [{op, actor, channel, target}]` | The step wrote these audit rows (`op` required, the rest optional) | `audit lacks {op promote actor "" channel "elicitation" target "memo://fact/…"}; rows: …` |
| `audit_none: true` | The step wrote no audit row. A refusal must not write one | `expected no audit rows, got {…}` |
| `status: {facts: 2, pages.open_work_items: 0}` | Status counters after the step, by dotted path | `status.facts: want 2, got 3` |

Counter names for `status` include `live_documents`, `sources`, `revisions`, `chunks`,
`facts`, `jobs_queued`, `jobs_failed`, `pages.pages`, `pages.stale_pages`,
`pages.open_work_items`, `graph.entities`, `graph.mentions` and `graph.edges`. Run a scenario
with `--json` to see every counter after every step.

Expectations are checked only where written. Everything else is left to the invariants,
which run after every step whatever the expectations say.

## Validation

`memors-scenario validate`, the studio and `TestScenarios` all reject a file before running it
when:

- a field is unknown, or YAML is malformed (with yaml's own line number)
- `name`, `actors` or `steps` is missing
- `decision`, a tier, an actor `kind`, `elicitation` or `channel` has a value not listed above
- a step names an unknown actor, or an op its actor kind cannot perform
  (`op "demote" is not a tool an agent can call`)
- `elicit` is used on a human, or on an agent whose `elicitation` is `none`
- `select` is used on anything but `compact`
- a `$name` is used before any step binds it
- `at` is malformed or goes backwards
- an audit expectation has no `op`, or `audit_none` and `audit` are used together
- a parallel group carries an op, actor, bind or expect of its own, or a sub-step has `at`

Every problem is reported with its line: `b2-zombie-fact.yaml:36: step 2: $f1 is not bound by
an earlier step`.

## Style

- **One story per file.** The catalogue entry is the unit. If you need "and also", write a
  second file.
- **Expect what the story is about.** Fewer, sharper expectations beat asserting every field.
  The invariants catch collateral damage.
- **Use `note` on the step that matters**, especially the one an `open` scenario will flip.
- **Never assert scores, latencies or whole result lists.** Assert membership (`top`,
  `contains`, `excludes`), abstention, outputs and side effects.
- **Prefer realistic text.** The hash embedder matches on shared words, so a query has to share
  words with the passage it should find.
