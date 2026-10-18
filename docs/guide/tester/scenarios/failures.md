# When a scenario fails

Scenarios are deterministic: the same file on the same code gives the same result on every
machine. A failure is never "flaky". Either the code changed behaviour, or the file is wrong.

## Where failures show up

| You ran | You see |
|---|---|
| `go test ./internal/eval/scenario/ -run TestScenarios` | `--- FAIL: TestScenarios/<file>` with the report table. Add `-v` to see tables of passing files too |
| `memors-scenario run <file>` | The report table; exit code 1 |
| The studio's **Run** | Red boxes with the failure under the chips, and a failure table under the picture |
| `memors-scenario validate` | `file:line: message`. The file was never run |

## The report

```
### b1-promote-decline-then-accept — FAIL (decision: decided)

| # | at | actor | op | result | failures |
|---|---|---|---|---|---|
| 1 | 0d | a1 | ingest | memo://doc/01a1… |  |
| 2 | 0d | a1 | remember | memo://fact/01a1… |  |
| 3 | 0d | a1 | promote | memo://fact/01a1… | out.applied: want true, got false |
```

- **result** is the step's gist: the address it produced, `N results`, `applied=false`, or
  `error: …`.
- **failures** lists every unmet expectation, then every broken invariant
  (`invariant forgotten-never-served: …`). In a parallel group, each failure is prefixed with
  `actor/op:`.

A failing step does not stop the run. Later steps still run, so one cause can show up as
several failures. Read from the top: **the first failing step is the one to explain.**

## Three kinds of failure

**1. An expectation did not hold.** The message says what was wanted and what came back:

| Message | Usually means |
|---|---|
| `top[0]: want memo://doc/A, got memo://doc/B` | Ranking changed. Check the query shares words with the passage (hash embedder) |
| `results lack memo://… (got …)` | The record is not returned: filtered by scope, trust or `as_of`, or forgotten |
| `results must not contain memo://…` | Something that should be hidden is served (forget, scope, supersession) |
| `expected abstention …; got 2 results` | The search should have found nothing and did not |
| `out.applied: want false, got true` | A field of the output differs. Run with `--json` to see the whole output |
| `expected an error, the op succeeded` | A refusal the scenario relies on did not happen, often a trust-cap regression |
| `audit lacks {op … channel …}; rows: …` | The audit row is missing or carries other values. The message lists what was written |
| `lint lacks contradiction (findings: …)` | Lint did not report what it should |
| `status.facts: want 2, got 3` | A counter is off. Compare with the state strip in the studio |

**2. The op itself failed.** `failed: …` with the tool's or the store's error. A tool error
that starts with `validating "arguments"` means the step passes an argument the tool does not
take. Compare `args` with the tool's input schema
(`internal/server/testdata/tools.golden.json`). For example, `search` takes
`scope: {namespaces: [ops]}`, not `namespace: ops`.

**3. An invariant broke.** `invariant <name>: <message>`. The step did something that left the
store in a state that must never exist. This is the most serious kind: see
[Invariants](invariants.md) for what each one protects. Do not skip the invariant to make the
test pass, unless the scenario documents that exact gap with `decision: open`.

## A debugging recipe

1. **Reproduce one file:**
   `go run ./cmd/memors-scenario run internal/eval/testdata/scenarios/<file> 2>/dev/null`.
2. **See it:** open the file in the studio and press **Run**. The red box, the resolved binds
   and the state strip usually explain the failure.
3. **See everything the step returned:**

   ```bash
   go run ./cmd/memors-scenario run --json <file> 2>/dev/null | jq '.[0].Steps[2]'
   ```

   `Out` is the step's output, `Status` the counters after it, `Binds` what it bound.
4. **Read the server log** (stderr of `run`): each tool call with its outcome and error class,
   each search with its resolved mode, outcome and cutoff.
5. **Cut the scenario down:** copy it to a scratch directory, and delete steps after the first
   failure and expectations unrelated to it. A failing three-step file is the best bug report.
6. **Decide what changed.** If the code is right and the scenario encoded old behaviour,
   update the expectation in the same change and say so in the pull request. If the scenario
   is `decided`, that is a behaviour change the owner must agree to. If the code is wrong, fix
   the code.

## A failing `open` scenario

A `decision: open` scenario pins today's behaviour of a gap. If it starts failing, the
behaviour changed, perhaps on purpose (someone fixed the gap) or perhaps as a side effect.
Either way it needs a decision: see [Open and decided](decisions.md).
