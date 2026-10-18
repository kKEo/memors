# Open and decided

Every scenario says whether its expectations are **wanted** behaviour or merely **current**
behaviour:

| `decision` | Meaning | On the picture |
|---|---|---|
| `decided` (default) | The expectations are the behaviour the owner wants. A failure is a regression | No banner |
| `open` | The expectations pin what the code does **today** for a documented [gap](gaps.md) that nobody has decided yet. They are a tripwire, not an endorsement | Amber "decision open" banner |

This distinction keeps the suite honest. Without it, a scenario written by observing the code
would quietly turn every existing bug into specified behaviour. That is "labelling to current
behaviour", the main trap of tests written after the fact.

## The workflow for a gap

1. **Document the gap** in `internal/eval/testdata/scenario-catalogue.yaml` under `gaps`, with
   `title`, `detail`, `where` (files and functions) and `decision: open`. Run
   `make scenario-docs`; it appears on [Behaviour gaps](gaps.md).
2. **Pin it**: a scenario with `decision: open` and `covers: [gap-N]` that asserts today's
   behaviour. Put a `note` on the step that will change when the gap is decided, for example
   `has_not: [resurrected]` in [b2-zombie-fact](log/b2-zombie-fact.md).
3. **The owner decides**, and the gap's `decision` becomes one of:
   - `keep`: today's behaviour is right. The scenario becomes `decided` unchanged.
   - `change`: a fix is wanted (next step).
   - `document`: the behaviour stays and is documented in the user or operator guide. The
     scenario becomes `decided`, and links the documentation in its comment.
4. **A fix lands with its scenario flipped.** The expectation changes to the wanted
   behaviour, which fails on the old code and passes on the new. Then
   `decision: open` → `decided`, and any invariant skips the gap needed are removed in the same
   change.

## Rules

- **Never write an `open` scenario without a gap**, and never a gap without `detail` and
  `where`. An open decision must be findable.
- **An `open` scenario that starts failing is a decision point**, not a test to update. Find
  out whether someone fixed the gap (then flip it properly) or broke something nearby.
- **A recorded expectation is `open` until someone decides.** This includes expectations
  copied from a run's output, and the planned "record from run" in the studio, which will
  force `decision: open` on the file.
- **Skips belong to `open` scenarios**, at the narrowest level, with a note. See
  [Invariants](invariants.md#skipping-an-invariant).
