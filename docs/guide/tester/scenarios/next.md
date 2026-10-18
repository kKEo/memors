# What comes next

The harness is young: the [catalogue](catalogue.md) shows how many stories are still to
write. In rough order, the planned work is:

1. **Owner decisions on the [gaps](gaps.md):** keep, change, or document only.
2. **Story files** for catalogue sections A to D and F2, F5, F6 on the `ci` tier. C5, F3 and F4
   are tagged `nightly`.
3. **A seeded random-walk generator** that emits scenario files: random operation sequences
   checked against the same invariant pack, with failing walks saved as files. Plus a
   `crash_at` hook for crash scenarios (E1).
4. **`parallel` groups over HTTP:** several named sessions on `serve --http` under the race
   detector (C3, C9, B8).
5. **The nightly tier:**
   - scenarios with a real model
   - the binary executor for E1, E3 and E4
   - the F1 generator at scale, with p95 latency per tool
   - a replay corpus from `memors-mcp log replay`
6. **Editing in the studio:**
   - a text editor with live validation and re-drawing
   - saving with a conflict check
   - structural edits (add, duplicate, reorder a step; add an expectation) that keep comments
   - "record expectations from the last run", which forces `decision: open`
7. **Later:**
   - two runs (baseline and candidate model or profile) on one picture with per-step deltas
   - scenario skeletons captured from the query log or the audit table
   - a Mermaid export for pull requests

The design notes and the reasoning behind the harness are in
[`docs/eval/scenarios.md`](https://github.com/kKEo/memors/blob/master/docs/eval/scenarios.md).

## Ideas worth a scenario

If you find a behaviour while [exploring](../explore/README.md) that no scenario covers,
write the story down, even before the YAML:

1. Add a catalogue entry (or a gap, if nobody has decided what should happen).
2. Run `make scenario-docs`.
3. Open a pull request.

A one-paragraph story in the catalogue is already useful: it shows up as "planned" on the
[catalogue](catalogue.md) and in the studio's coverage view until someone implements it.
