# Part III: Scenario tests

The [retrieval benchmark](../gates/retrieval-eval.md) loads two corpora once and asks labelled
questions. It measures ranking well, and it is blind to everything that happens over time. A
real knowledge base is a living system:

- documents arrive and are revised over days
- agents record facts and humans correct them
- a human confirms or rejects what an agent wrote
- several agents write into the same shelves at once
- someone forgets a record and expects it gone everywhere

**Scenario tests** script exactly that. A scenario is a YAML file of steps. Named actors act on
one knowledge base under a fake clock: agents call the ten MCP tools over real in-memory
sessions, and a human uses the CLI channel. Each step can state what must be observable
afterwards: search results, outputs, audit rows, lint findings, status counters. After
**every** step, a pack of fifteen invariants checks the whole store.

```bash
go test ./internal/eval/scenario/ -run TestScenarios -count=1 -v   # what CI runs
make scenario-studio                                               # see, check and run them in a browser
```

## How it runs

- **The real code.** The runner opens a fresh store in a temporary directory and runs the real
  MCP server in-process. Agents are MCP client sessions with their own client names and
  dialog answers. The human is the same code path as `memors-mcp` CLI commands.
- **A fake clock.** `clock:` sets the first moment; each step's `at:` moves time forward
  (`2d`, `3h`). Nothing sleeps, so a scenario covering a year runs in about a second.
- **The hash embedder.** It is deterministic and needs no download, so `ci`-tier scenarios run
  in every pull request and give the same result everywhere.
- **No wall-clock assertions.** Scenarios assert behaviour (what is returned, refused,
  audited, flagged), never latencies or absolute scores.

## Where things are

| What | Where |
|---|---|
| Scenario files | `internal/eval/testdata/scenarios/*.yaml`, named after their catalogue id (`b2-zombie-fact.yaml`) |
| The catalogue and the gaps (data) | `internal/eval/testdata/scenario-catalogue.yaml` |
| DSL, runner, invariants | `internal/eval/scenario/` (`dsl.go`, `run.go`, `invariants.go`) |
| The test that runs them | `TestScenarios` in `internal/eval/scenario/scenario_test.go` |
| Pictures, studio, CLI | `internal/eval/scenario/{diagram,studio,scenariocli}`, `cmd/memors-scenario` |

## The life of a scenario

1. **Someone notices a behaviour**, by reading the code, from a bug report, or while
   exploring. If nobody has decided whether it is right, it is a
   [behaviour gap](gaps.md).
2. **A scenario pins today's behaviour** with `decision: open` and `covers: [gap-N]`. It
   passes, and the picture carries an amber "decision open" banner, so nobody mistakes it for
   approved behaviour.
3. **The owner decides** to keep the behaviour, change it, or document it only.
4. **A fix lands with the scenario flipped:** the expectation changes to the wanted behaviour,
   the run fails on the old code and passes on the new, and the file becomes
   `decision: decided`.

[Open and decided](decisions.md) has the rules. The [catalogue](catalogue.md) lists the
scenarios still to write, and the [implemented scenarios](log/README.md) log lists the ones
that exist, each with its picture.

## In this part

- [Your first scenario](first.md): write, check, draw and run one, step by step.
- [Writing scenarios: reference](dsl.md): every field, op, bind and expectation.
- [Invariants](invariants.md): the fifteen properties checked after every step.
- [Reading the picture](pictures.md): what the swimlane SVG shows.
- [memors-scenario](tools.md): the command-line tool.
- [The studio](studio.md): the local web app for reading, checking and running scenarios.
- [When a scenario fails](failures.md): reading reports and finding the cause.
- [Open and decided](decisions.md), [Behaviour gaps](gaps.md), [Catalogue](catalogue.md),
  [Implemented scenarios](log/README.md), [What comes next](next.md).
