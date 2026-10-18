# Reading the picture

`memors-scenario render`, the studio and this guide's [scenario log](log/README.md) draw every
scenario as a **swimlane picture**: one column per actor, time flowing down, one box per step.
It is the fastest way to review a scenario: who does what, when, with which result named, and
what is checked. Every picture in the log is drawn from the file itself, so it cannot drift from
what runs.

Open any page of the [implemented scenarios](log/README.md) log next to this page. For example,
[b1-promote-decline-then-accept](log/b1-promote-decline-then-accept.md) shows a dialog, a
human, a refusal and a bind used by several steps.

## The parts

| Part | Where | What it shows |
|---|---|---|
| Title and description | Top | `name` and `description` |
| Meta chips | Under the title | Decision, tiers, `covers` ids, the start date |
| Amber banner | Under the title, `decision: open` only | "pins current behaviour": the expectations are today's behaviour of a gap, not approved behaviour |
| Lanes | Columns | One per actor: agents sorted by key, then humans. The header shows the key, the kind, the client name and the default dialog answer. A step naming an undeclared actor gets an extra lane marked "⚠ not declared" |
| Left gutter | Beside each row | The step number. Where the clock moves: the date, the delta (`+1d`) and a thin rule across the lanes |
| Box | In the actor's lane | One step. The op in bold, then the arguments in file order, then the expectation chips |
| `$name` badge | Top right of a box | What the step binds |
| Dialog bubble | Next to the op | `dialog: accept` when the step overrides the answer, or the actor's default answer on a `promote` |
| `select` line | In a compact box | Which work item the bind picks |
| Chips | Bottom of a box | One per expectation (see below) |
| Edges | Right gutter | One dashed trunk per bind, from the defining badge down to every step that uses `$name`, with an arrow into each |
| Bracket | Around a row | A dashed bracket labelled `parallel` around a group's sub-steps, drawn side by side |
| Note | Under a box | The step's `note`, in italics |
| Legend | Bottom | A one-line key to the marks |

## Chips

Chips are coloured by family, so the shape of a scenario is visible at a glance:

| Family | Colour | Chips |
|---|---|---|
| Found | Green | `top $f1`, `contains $doc`, `no error`, `answers` |
| Refused, missing or absent | Orange | `excludes $f1`, `error ∋ "needs a human"`, `audit_none`, `abstains` |
| Values | Blue | `out applied = false`, `out reason ∋ "policy"`, `status facts = 2`, `count 1` |
| Side effects | Neutral | `lint[ops] has contradiction`, `audit promote by claude-code/elicitation → $f1` |

## Short and expanded text

Long arguments (a document's content, a long statement) show two lines and `… more`. How you
see the rest depends on where the picture is:

- **A standalone `.svg` file** opened in a browser (`render` without `--expand`): hover for a
  tooltip with the full text, or click `… more` to open a panel. A small script embedded in the
  file does this, and it is left out when nothing is truncated.
- **The studio**: `… more` and `less` are links. The server draws the picture again with that
  block expanded. The page runs no script, under a strict content security policy.
- **Images in documents** (this guide, GitHub): an `<img>` cannot react to clicks, so the log
  uses fully expanded pictures (`render --expand all`).

## A run on the picture

In the studio, after **Run**, the same picture shows the result:

- a box outlined green passed, a box filled red with a `✗` failed
- the failure text is printed under the chips (`out.applied: want true, got false`)
- binds show what they resolved to (`$f1 → memo://fact/01a1…`)
- a **state strip** under each row lists counters after the step (live documents, facts,
  chunks, pages, open work items), with deltas against the previous row

A scenario with validation problems still draws. Each problem is pinned as a `⚠` line on the
step it concerns.

## Ids and paths

Every drawn element carries an `id` and a `data-path`:

| Element | `id` | `data-path` |
|---|---|---|
| Step box | `step-3` (parallel: `step-3-2`) | `steps.2` (`steps.2.parallel.1`) |
| Argument | `step-3-arg-content` | `steps.2.args.content` |
| Lane | `lane-a1` | `actors.a1` |
| Bind badge, edge | `bind-f1`, `edge-f1` | the step's path |

`data-path` uses the same paths as `validate`'s messages and the source line index, so a
click on the picture can be mapped back to the line in the YAML. The planned studio editor
relies on this, and tests guarantee every `data-path` exists in the file.
