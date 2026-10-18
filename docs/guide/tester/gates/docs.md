# Documentation checks

The guides are part of the product, so they have gates too.

## `scripts/docs-check.sh`

Runs as the first step of `make docs`, in the docs workflow, and on its own with
`make docs-check`. It fails when:

1. A relative link in any guide (`docs/guide/**`) does not resolve, or leaves `docs/guide/`.
   Links to code or other repository files must be absolute GitHub URLs, because only the
   guides are published.
2. An environment variable `MEMORS_*` read by the code is missing from the operator guide's
   environment reference.
3. A command listed in `memors-mcp`'s usage text is missing from the commands reference.
4. A `memors_*` metric registered in the code is missing from the metric catalogue.

Checks 2 to 4 make adding a setting, a command or a metric without documenting it a failing
build. Environment variables read only in tests (such as the tray's `MEMORS_TRAY_E2E_BIN`) do
not count.

## Generated pages: `TestBookCurrent`

The scenario part of this guide is partly generated from data:

| Generated page | Source |
|---|---|
| [Behaviour gaps](../scenarios/gaps.md) | `internal/eval/testdata/scenario-catalogue.yaml` |
| [Catalogue](../scenarios/catalogue.md) | the same file, plus which scenario files cover each entry |
| [Implemented scenarios](../scenarios/log/README.md) and one page and picture per file | the files in `internal/eval/testdata/scenarios/` |
| The entries under "Implemented scenarios" in `SUMMARY.md` | the same files |

`TestBookCurrent` (`internal/eval/scenario/book`) regenerates all of it in memory and fails,
naming each stale file, when the committed pages differ. It runs in the PR gate, so a scenario
cannot be added, changed or removed without its page following. To fix the failure:

```bash
make scenario-docs                          # rewrite the pages, then commit them
go run ./cmd/memors-scenario book --check   # report only; exit 1 when stale
```

Do not edit generated pages by hand. Each one starts with a comment naming its source.
Everything under `scenarios/log/` is generated, and the generator removes files there that it
no longer produces.

## Building the guides

```bash
make docs                       # docs-check, then all three books into site/
make docs-serve BOOK=tester     # live preview of one book
```

The books are plain GitBook-compatible markdown. mdBook builds them in CI. Only chapters listed
in a book's `SUMMARY.md` are published, which is why the generator keeps the scenario entries
there.
