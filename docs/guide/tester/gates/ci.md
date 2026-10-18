# Continuous integration

Four GitHub Actions workflows live in `.github/workflows/`.

| Workflow | Trigger | Blocks | What it runs |
|---|---|---|---|
| `ci.yml` | Push to `master`, every pull request | Merge | Tests, lint, build, release dry run, tray |
| `nightly.yml` | Daily at 03:17 UTC, or by hand | Nothing | The benchmark with a real model, as a report |
| `release.yml` | A `v*` tag | The release | Tests, GoReleaser, registry publish, tray app |
| `docs.yml` | Changes under `docs/guide/`, `scripts/docs-check.sh` | Pages deploy | `make docs` (docs-check first), deploy to GitHub Pages |

## `ci.yml`: the merge gate

**Job `test`** runs on `ubuntu-latest` and `macos-latest` with `fail-fast: false`, so a
failure on one platform still reports the other. Windows is disabled in the matrix for now.
The Go version comes from `go.mod`. In order:

1. `test -z "$(gofmt -l .)"`: every file is gofmt-clean.
2. `go vet ./...`
3. `go test ./... -race -count=1`: every package, including the retrieval benchmark, the
   contract goldens, the `ci`-tier scenarios and the generated-docs freshness test.
4. A pure-Go build (`CGO_ENABLED=0`) with the version stamped in.
5. A smoke run: `./memors-mcp version`.

**Job `lint`** runs golangci-lint and checks that `server.json`'s description is at most 100
characters (the MCP registry's limit).

**Job `release-config`** runs GoReleaser in snapshot mode and asserts that exactly five
archives come out.

**Job `tray`** (macOS) runs:

- gofmt on `tray/`, then `make tray-test`.
- A dependency check: the tray must not link the server's SQLite, model runtime or MCP SDK.
- An end-to-end test against a freshly built `memors-mcp`.
- The app bundle build, with the signature and the universal-binary architectures verified.

No job in `ci.yml` downloads a model.

## `nightly.yml`: the real model

Runs `memors-mcp eval --models hash,granite-small-r2 --corpus all --format md` with the model
cache kept between runs, and uploads the markdown as the `eval-report` artifact. It is
informational: compare it with the previous night's report when retrieval or a model changed.
Scenarios on the `nightly` tier are not run yet (see [What comes next](../scenarios/next.md)).

## `release.yml`: tags

- Runs `go test ./... -count=1` without the race detector (the race run already passed on the
  merge commit).
- Then GoReleaser, and the `server.json` publish to the MCP registry.
- A second job builds and uploads the MemoRS Tray app after `make tray-test`.

## Reproducing a CI failure locally

| CI step failed | Run locally |
|---|---|
| gofmt | `make fmt` (lists the files), then `gofmt -w <file>` |
| vet | `make vet` |
| tests | `go test ./<package>/ -race -count=1 -v -run <Test>` |
| lint | `make lint` |
| release-config | `make snapshot`, then count the archives in `dist/` |
| tray | `make tray-test` on macOS; for the E2E step see [MemoRS Tray tests](tray.md) |
| docs | `make docs-check`, then `make docs` with mdBook installed |

A failure only on `macos-latest` or only on `ubuntu-latest` usually involves file paths,
line endings or timing. Run the test with `-count=20` on the platform you have. A
deterministic test must pass all twenty times.
