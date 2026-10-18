# Part II: Automated gates

What runs without anyone asking, where it runs, and how to read and fix a failure:

- [Continuous integration](ci.md): the four workflows, what blocks a merge, and how to
  reproduce a CI failure on your machine.
- [Contract and golden tests](goldens.md): the public surfaces pinned byte for byte, and how
  to change one on purpose.
- [The retrieval benchmark](retrieval-eval.md): the labelled corpora, the paired regression
  gate and its thresholds, and `memors-mcp eval`.
- [Fuzzing and the race detector](fuzz-race.md).
- [MemoRS Tray tests](tray.md): the macOS module's own suite, with its end-to-end and live tests.
- [Documentation checks](docs.md): `docs-check.sh` and the freshness of generated pages.

The scenario harness is also part of the PR gate. It has its own part:
[Scenario tests](../scenarios/README.md).
