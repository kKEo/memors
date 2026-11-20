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
them, `README.md` and `SUMMARY.md`. It also skips the files under `pages/` (front matter
`kind: page`) and reports them as `skipped (derived page)`: a page is rebuilt from its sources,
never ingested. Facts, history, graph and logs are not included.

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
again after the knowledge base changes. Export into an empty folder each time: the export
never deletes, so a page that was retitled or removed since the last run would leave its old
file behind.

In an agent-written page, every `memo://` address of a record that is in the export becomes a
link to that record's file: a passage or a document leads to the document, a page to the page,
an entity to its page. Addresses inside fenced blocks, and of records that are not exported,
stay as text. Documents are never touched; they are exported byte for byte.

### A namespace with an outline

A namespace that has an [outline](../config/commands.md) is laid out as its book instead:

```text
- namespace
  - Overview                  the overview page, once written
  - Chapter title             the chapter's page, or a draft entry while it is unwritten
    - Member page             the entity pages of the chapter, in outline order
  - Not in a chapter          pages no chapter covers
  - Sources                   every document, grouped by kind, as an appendix
```

The reader goes down one level at a time. The overview ends with the list of chapters. A
chapter ends with **In this chapter**: its member pages and member documents. These lists are
written at export time and are not part of the stored page. The namespace's `_index.md` starts
with the same contents, and marks chapters that are not written yet and pages that are stale.

Pages gain two front-matter fields: `chapter` (the chapter an entity page belongs to) and
`built_from_pages` (the pages a chapter or overview page was written from). A namespace
without an outline is laid out as before.

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
