# Exporting

## Markdown with provenance

```bash
memors-mcp export --md ./kb-export            # all namespaces
memors-mcp export --md ./kb-export --ns handbook
```

The layout is `<namespace>/<kind>/<slug>-<shortid>.md`, plus an `_index.md` per namespace.
Curated pages go to `<namespace>/pages/`. The root gets `README.md`, `SUMMARY.md`, `book.toml`
and `.gitbook.yaml`, which make the folder a book (below). Each exported file has YAML front
matter:

```yaml
---
memo_uri: memo://doc/01a1017e-ec2c-777a-b64a-4b67d1ac8f97
title: Deploy checklist
kind: doc
namespace: default
revision: 1
content_hash: sha256:cf966c40e2f66a5d595ab316004e2875ff5f70aa51679c31669852fc121d7326
fetched_at: 2026-10-03T11:20:57Z
trust: user
origin: user-said

---
```

Empty fields are left out. `source_uri`, `library`, `version` and `context` appear when they
are set. The blank line before the closing `---` is deliberate: mdBook has no front-matter
support and would otherwise read the block as a heading; YAML and Obsidian ignore it.

The export opens as an Obsidian vault. Re-ingesting it produces **zero** new revisions, because
the content hashes match; `ingest <dir>` skips `_index.md` and, when a `book.toml` sits beside
them, `README.md` and `SUMMARY.md`. Facts, history, graph and logs are not included.

## Browse it as a book

The export is a valid [mdBook](https://rust-lang.github.io/mdBook/) source and a GitBook space:
`SUMMARY.md` lists one chapter per namespace, with the documents grouped by kind and the agent
written pages last under *Pages (derived by agents)*.

```bash
memors-mcp export --md ./kb-export
mdbook build ./kb-export && open ./kb-export/book/index.html
```

Every page renders its front matter as a provenance block above the text, so a reader sees the
trust level, the build time and `built_from` before the content, and stale pages are marked
`(stale)` in the table of contents. To publish with GitBook, point a space at the folder; it reads
`.gitbook.yaml` and the front matter natively. Nothing rebuilds the book for you: run the export
again after the knowledge base changes.

## An index for agents

```bash
memors-mcp export --index > kb-index.md                        # ≤ 8 KB, one line per document
memors-mcp export --index --library grpc/grpc-go@v1.64.0 --max-bytes 4096
```

Each line has the title, address, kind, version and trust, and facts are summarised. Lines that do
not fit the budget are counted in a footer. Paste the output into `CLAUDE.md` or `AGENTS.md` so
an agent knows what exists before it searches. The same content is the MCP resource
`memo://index`.

## Raw data

The file is standard SQLite, so any tool can read it. For example, every live fact as CSV:

```bash
sqlite3 -csv -header ~/.memors-mcp/kb/my-project.db \
  "SELECT id, namespace, statement, trust, valid_from, valid_to FROM facts WHERE invalidated_at IS NULL AND deleted_at IS NULL"
```

Column names: [schema.md](https://github.com/kKEo/memors/blob/master/docs/schema.md).
