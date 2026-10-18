# Changelog

All notable changes. Tags follow semantic versioning; each tag has an eval report in
`docs/eval/` from `v0.6.0` on.

## [Unreleased]

- **Scenario pictures and `memors-scenario`.** A scenario file can now be drawn as an SVG swimlane
  picture (`internal/eval/scenario/diagram`): a lane per actor, time flowing down, one box per step
  with arguments and expectation chips, `$bind` edges to the steps that use them, dialog bubbles,
  parallel brackets, an amber banner for `decision: open`, and expandable long text (click in a
  standalone file, a link in the studio). `cmd/memors-scenario` (a development tool, not in the
  release) has `render`, `validate` (with file lines), `run` (`--json`, `--trace`) and `describe`;
  `make scenario-svg` keeps `docs/eval/scenarios/*.svg` current and `TestDocsCurrent` checks them.
  Supporting changes: `scenario.Parse` indexes source positions (`Lines`, `LineOf`), `Validate`
  returns `*Issue` values with paths and lines (`Issues`, `StepPath`, `ParseLoose`), the DSL gains
  `covers:` (catalogue ids and gaps a file exercises), and `StepReport` carries `Path`, `Binds`,
  per-sub-step `Sub` entries and, with `Options.Trace`, `Status` and a capped `Out`.
- **Living-system scenarios** (`internal/eval/scenario`, `docs/eval/scenarios.md`): a second
  eval gate next to the static corpus. A scenario is a YAML file in
  `internal/eval/testdata/scenarios/` scripting agents (one in-memory MCP session each, with
  its own client name and a scripted elicitation answer) and a human (the CLI channel) acting
  on one knowledge base over a fake clock; each step may expect results, output fields, lint
  findings, audit rows or status counters, and an invariant pack (one live revision per
  source, forgotten records never served, no tool call above the agent trust cap, raw rows
  untouched by compaction, …) runs after every step. `TestScenarios` runs every `ci`-tier
  file with the hash embedder. Three pilots ship: elicitation declined then accepted plus a
  refused tool forget (B1), the zombie fact (B2, `decision: open`), and two agents' conflict
  resolved by a third (C2). The catalogue of scenarios still to write and fifteen behaviour
  gaps found while reading the code, each awaiting an owner decision, are in the document.
  Supporting changes: `retrieve.Service.SetNow`, `server.Server.Connect`, `eval.RawChecksum`
  (the compaction checksum, exported); `gopkg.in/yaml.v3` is a new dependency.
- **MemoRS Tray checks for updates and updates itself.** A minute after it starts and then once
  a day it asks GitHub for the newest release; when a newer MemoRS Tray or memors-mcp exists, the
  top of the menu offers it, and **Check for Updates…** asks on demand. The new **Updates** window
  shows both versions and **Update MemoRS Tray** downloads the release zip, verifies its
  `.sha256`, swaps the app in place and relaunches it (or links to the release page where it
  cannot replace itself). `check_updates: false` in `tray.json`, or the Settings checkbox, turns
  the daily check off. memors-mcp is updated by the assistant's Install step, as before.
- **MemoRS Tray: Configure Your Agent…** replaces **Copy MCP URL** and **Copy "claude mcp add"
  Command** in a running server's submenu. It opens a step-by-step window: choose the agent
  (Claude Code, Claude Desktop or another MCP client), add the server (the command, the Claude
  Desktop configuration entry, or the address and token header, each with a Copy button), check
  the connection and start with the `ask` prompt. The setup assistant's last step links to it.
- **`ask` MCP prompt**: turns a conversation into a chat answered from the knowledge base. The
  client's model searches passages and facts, reads for depth, cites every claim by its `memo://`
  address and says when the knowledge base has nothing; the server still runs no LLM. One
  optional argument, a namespace; the questions follow in the conversation (Claude Code:
  `/mcp__memors__ask`). Counted in `memors_mcp_prompt_gets_total`. The operator guide's
  `memors:search_abstain_ratio:rate15m` rule now counts passage and document searches only,
  since `ask` adds a fact search to every question.
- Fixed: a `scope.version` search also returned older revisions ingested at the same version.
  It now returns the newest revision of each source for that version.
- Fixed: when `exclude_ids` removed every match, search returned no results and an empty
  `reason`. It now says so, with the abstention class `excluded`.
- Fixed: a call to a tool name the server does not have created new `tool` label values in the
  tool-call metrics; it is now counted as `tool="unknown"`.
- **Renamed: the project is now memors** (`github.com/kKEo/memors`, MCP registry
  `io.github.kKEo/memors`, guides at `kkeo.github.io/memors/`), **the server binary is
  `memors-mcp`** (`cmd/memors-mcp`, archives `memors-mcp_<version>_<os>_<arch>`) and **the menu-bar
  app is MemoRS Tray** (`MemoRS Tray.app`, executable `memors-tray`, release asset
  `memors-tray_<version>_darwin_universal.zip`). Breaking, with no fallback to the old names:
  - environment variables `MEMO_*` → `MEMORS_*`;
  - base directory `~/.memo-mcp` → `~/.memors-mcp` (move it) and model cache
    `~/.cache/memo-mcp/models` → `~/.cache/memors-mcp/models`;
  - metrics `memo_*` → `memors_*` (recording rules and alerts in the operator guide follow);
  - MemoRS Tray's bundle ID and LaunchAgent are `io.github.kkeo.memors-tray`, adopting memo-tray's
    "open at login" agent on first start; the `tray.json` key `memo_binary` is now `memors_binary`;
  - the web UI session cookie is renamed (one more login per browser);
  - the deprecated `JOURNAL_TOKEN` / `JOURNAL_PATH` aliases are removed.

  Unchanged: knowledge-base files (the SQLite application id stays `MEMO`), `memo://` addresses,
  tool names, the export format. Steps in the operator guide, *Upgrading from memo-mcp*.
- Documentation: two GitBook-compatible guides under `docs/guide/`: a plain-English user guide
  (quick start, everyday use, trust, privacy, use cases) and an operator guide (install,
  configuration reference, tuning, lifecycle, metric catalogue, Prometheus rules and alerts,
  backup, troubleshooting). Built with mdBook (`make docs`, `make docs-serve`) and deployed to
  GitHub Pages from `master` by `.github/workflows/docs.yml`.
- `scripts/docs-check.sh`: fails when a `MEMORS_*` variable, a command or a metric in the code is
  missing from the operator guide, or a guide link is broken.
- Documentation: twelve diagrams (SVG, following the reader's light or dark setting). The user
  guide shows how memors-mcp fits in, a copy per session against one shared server, a corrected
  fact's history, trust levels, and shelves against separate memories. The operator guide and
  the README show the processes and files, the data layers, the search pipeline, the ingest
  write path, the two clocks, the `serve --http` setups and MemoRS Tray. The operator guide now
  says that `as_of` filters on recorded time only, as the code does; valid time is shown, and
  `lint` flags expired facts.
- CI: the Windows test job is disabled for now; release archives for Windows are still built.
- Web UI ingest console (`/ingest`, `/ingest/{id}`): every `memors-mcp ingest` batch and MCP
  `ingest` call is recorded as a run with live progress (running / done / failed / stalled) and
  stats: documents written, unchanged and failed, chunks, embedded and pending vectors, bytes,
  duration and docs/s, plus one row per document. The page reloads itself while a run is in
  progress (no JS; the CSP is unchanged). `/status` lists the last five runs. Schema migration 5
  adds `ingest_runs` and `ingest_run_items`.
- `memors-mcp serve --http 127.0.0.1:8765` (`MEMORS_HTTP_ADDR`): one long-running MCP server over
  streamable HTTP at `/mcp` for any number of clients (`claude mcp add --transport http`), with
  the web UI and `/metrics` on the same port. Loopback only unless `--allow-remote`. Only in
  this mode the UI gains a **Live** page: active clients, calls in flight (an ingest links to
  its run), background backfill/reindex state, and per-tool calls, errors and p50/p95 latency
  plus ingest throughput since start. `/ingest` also lists ingest calls still in flight.
- `serve --http` authentication: a bearer token (`<MEMORS_HOME>/http-token`, 0600, printed by the
  new `memors-mcp http-token [--rotate]`) protects `/mcp`, the UI and `/metrics`, and is on by
  default whenever the server is reachable from elsewhere. Browsers log in with a single-use
  link printed at startup (or by pasting the token) and get an HttpOnly, SameSite=Strict
  session cookie. HTTPS with `--tls-cert`/`--tls-key`, optional mTLS with `--tls-client-ca`,
  `--behind-proxy` for a TLS-terminating proxy, and `--public-url`. Plain HTTP on a
  non-loopback address and unauthenticated exposure are refused.
- Fix MCP registry publishing: `server.json` no longer sets `registryBaseUrl` on the MCPB package.
- MemoRS Tray setup assistant and settings window. **Install or Update memors-mcp…** (and
  **Install memors-mcp…**, opened by itself on first launch when memors-mcp is missing) downloads
  the latest release for this Mac from GitHub, checks it against the release's `checksums.txt`,
  tries it and installs it atomically (default `~/.local/bin`), then sets up a knowledge base
  (name, embedding model, token, start with MemoRS Tray) and shows the `claude mcp add` line.
  **Settings…** edits `tray.json` in a form: open at login (LaunchAgent), stop servers on quit,
  memors-mcp location and version, embedding model, servers (address, token, autostart, add and
  remove), extra environment. MemoRS Tray contacts GitHub only while the install step is open.
- **MemoRS Tray**, an optional macOS menu-bar app (`tray/`, its own Go module; the only cgo build,
  memors-mcp stays pure Go): lists the running `serve --http` servers from their run files with
  the agents connected to each (● active, ○ quiet; the count next to the icon), opens a
  server's **Live** page in a native window that logs itself in, and starts, stops and restarts
  servers per knowledge base (`<MEMORS_HOME>/tray.json`, logs in `<MEMORS_HOME>/logs/`). Released
  as `memors-tray_<version>_darwin_universal.zip` (unsigned) by a macOS job after the server
  archives; `make tray`, `make tray-app`, `make tray-test`.
- `GET /live.json` on `serve --http`: the Live page's clients and calls in flight as JSON
  (`schema` 1, plus the server's instance id and pid) for companion apps, and
  `POST /login-link` (token mode, `Authorization: Bearer` only) returning a single-use login
  link so an app can open the UI in its own window without handing the token to a browser.
- `serve --http` advertises itself to local apps in `<MEMORS_HOME>/run/serve-<pid>.json` (0600,
  directory 0700) while it serves: URL, bound address, auth mode, token file path (never the
  token), knowledge base, version and the instance id `/live.json` reports. The file is
  removed when the server stops.
- Fix the **Live** page listing a phantom `unknown` client for 30 minutes next to every go-sdk
  client: the `server/discover` probe such clients send before `initialize` no longer counts as a
  session, in the page or in `memors_mcp_sessions_total`. Clients now show their version and a
  state: *connected* (holds its event stream), *open* (session open, no stream) or *left*;
  sessions are shown by a short hashed handle instead of a prefix of the session id, which
  grants access to the session.

## [1.4.0] — 2026-10-03 — measuring the server itself (P10)

- `internal/obs`: stdlib metrics registry, Prometheus text format, runtime subset, loopback
  metrics server, structured-logging setup.
- `memo-mcp serve --metrics-addr` / `MEMO_METRICS_ADDR`; UI `/metrics`; `memo-mcp metrics`.
- Metrics for every MCP request and tool call, every search and arm, every audited write,
  embeddings, graph cache, and table-count gauges.
- `log/slog` on stderr with `MEMO_LOG_FORMAT` and `MEMO_LOG_LEVEL`; one line per tool call and
  per search; all ad-hoc stderr prints replaced.
- The MCP `logging` capability is no longer advertised (deprecated; decision D-O).
- Migration 4 `call_log`; `memo-mcp log calls`; `log tail` and the UI `/log` page show tool
  calls next to searches; pruning covers both.
- Privacy sentence amended: local metrics and logs are not telemetry.

## [1.3.0] — 2026-10-03 — a knowledge base you can read (P9)

- `memo-mcp ui`: read-only web face on loopback, server-rendered, no JavaScript. Search with
  the explain and trace tables, documents and passages with provenance and history, the facts
  timeline with `as_of`, entity neighbourhoods, pages with sources, status, lint, the query
  log, the eval report. GET only; Host-header allowlist; `--allow-remote` required for a
  non-loopback address.
- A test asserts the UI's numbers equal the retrieval service's for the same query.
- `memo-mcp migrate`; read-only commands refuse a file at an older schema with that advice.
- `memo-mcp graph rebuild [--ns]` re-extracts mentions for files written before 1.1.

## [1.2.0] — 2026-10-03 — compaction and pages (P8)

- Migration 3: `pages`, `page_sources`, `page_vecs`, `pages_fts`, `work_items`.
- Pages are written only by the calling agent: `compact` proposes work items (page, stale,
  conflict, merge, duplicate) with full payloads; `submit` stores pages as derived
  (`is_inference`) with their sources, resolves conflicts under the trust rule, records merge
  decisions; `dry_run` shows the diff, the omission check and the corruption check.
- Staleness: a revised or forgotten source marks dependent pages stale with the reason.
- `lint`: contradictions, orphan entities, missing pages, stale pages, expired facts.
- `search granularity=page`; `read` and the `memo://page/{id}` resource; `status.pages`.
- CLI `compact`, `submit`, `lint`, `pages ls`; `export --md` writes `<ns>/pages/`.
- Optional `compact --executor ollama` (loopback, constrained JSON); nothing else depends on it.
- Eval: lint recall on five planted defect kinds, omission and corruption checks, raw-row
  checksum. Ten tools.

## [1.1.0] — 2026-10-03 — entities and graph, as an index (P7)

- Migration 2: `entities`, `entity_aliases`, `mentions`, `merge_candidates`, `edges`.
- Heuristic entity extraction on ingest; client-supplied `entities[]`/`relations[]`; fact
  subjects linked from `about[]`.
- Deterministic entity resolution with a review queue (`memo-mcp graph merges|merge|reject`).
- Entity arm and graph arm (personalised PageRank, product scoring), routed into `auto` for
  multi-entity or relational questions; `mode=graph`; `why.graph`, `trace.entities`.
- Tool `explore` (eight tools), resource `memo://entity/{id}`, `status.graph`; CLI `explore`.
- Eval: multi-hop slice, graph-tax column, update-stream and merge-precision tests; ablation
  profiles `text-only`, `no-graph`.
- Fixed: the fact arm had no weight in the default profile; a one-word overlap could match a
  fact.

## [1.0.0] — 2026-10-02

The 1.0 contract: tool names and parameters, the `memo://` address scheme, the explain field
names, the export front matter (`docs/architecture.md`).

- CI matrix on ubuntu, macos and windows; lint as a gate; PR CI never downloads a model.
- Nightly real-model eval workflow with a cached model directory.
- GoReleaser release pipeline: five archives, version from the tag, checksums.
- `server.json` and registry publishing through GitHub OIDC on tag.
- `docs/architecture.md`, this changelog, README rewrite.

## [0.9.0] — 2026-10-02 — the agent surface (P5)

- Every tool description carries an example.
- `search` returns `truncated` and `narrow_hint` in every format; the text mirror says it.
- `response_format: detailed` shows the passage with its neighbours.
- `promote` asks the human through a multi round-trip elicitation (SEP-2322); accepted answers
  apply with audit channel `elicitation`, otherwise the CLI command is returned.
- MCP resources mirror the read tools (`memo://doc|chunk|source|fact/{id}`,
  `memo://ns/{namespace}/index`, `memo://index`) with cache hints.
- `memo-mcp export --index`, `memo-mcp log replay`, `SKILL.md`.

## [0.8.0] — 2026-10-02 — provenance, trust and time (P4)

- Facts: `remember` (add-only, `supersedes` keeps history), `forget` (tombstones, `redact`),
  `promote`; CLI `remember`, `forget`, `facts ls --as-of`, `trust ls|promote|demote`,
  `read --history`.
- `as_of` on search and facts; forgotten records hidden even under `as_of`.
- Fact arm in fusion ("facts as extra keys"); `granularity: fact` with trust-first tiebreak.
- Trust transitions enforced by channel; `ErrNeedsHuman` carries the CLI command.
- Stopwords dropped from keyword queries. Eval slices: knowledge-update, temporal, conflict,
  revocation, write-loss, poisoning.

## [0.7.0] — 2026-10-02 — the lab (P3)

- Eval v2: knowledge-base corpus, categories, cost columns, paired per-query gate, abstention
  rate, `memo-mcp eval`.
- Embedding model registry (`memo-mcp model ls|smoke|pull|use`, `MEMO_MODEL`); vectors for
  several models coexist; `memo-mcp reindex`.
- Default model: granite-embedding-small-english-r2 (OD-6), chosen in the bake-off.
- Ranking profiles with derivations (`memo-mcp profiles show`, `$MEMO_HOME/profiles.json`,
  `MEMO_PROFILE`); per-model relevance bands.
- Optional cross-encoder reranker (`MEMO_RERANK=1`), not promoted (failed the gate).

## [0.6.0] — 2026-10-02 — search that explains itself (P2)

- `internal/retrieve`: keyword, exact and semantic arms, weighted RRF, chunk-to-document
  aggregation, recency for notes and conversations, gap cutoff, semantic-only floor, token
  budget, `exclude_ids`.
- The explain contract (`why`, `trace`) shared by CLI and MCP; structured abstention.
- Typed MCP tools `ingest`, `search`, `read`, `status` with output schemas and annotations;
  the six journal tools removed.
- Opt-in query log (`MEMO_QUERY_LOG=1`; `log tail|show|prune`).

## [0.5.0] — 2026-10-02 — the store (P1)

- New knowledge-base schema (`docs/schema.md`): sources, documents with revisions, chunks with
  three indexes, facts, models, jobs, audit. `PRAGMA application_id`; v0 journal files refused.
- Markdown-aware chunker; batch embedder interface; embedding outside the transaction with a
  backfill queue.
- CLI `ingest`, `read`, `ls`, `verify`, `export --md`, `backfill`, `status`.

## [0.4.0] — 2026-10-02 — reset the map (P0)

- Repository renamed to `github.com/kKEo/memory-find`; binary stays `memo-mcp`.
- go-sdk v1.8.0 (protocol 2026-07-28); version from the tag; CLI skeleton; `MEMO_KB`,
  `MEMO_HOME`; deprecated `JOURNAL_*` aliases.
- `.golangci.yml`, minimal CI, `signal.NotifyContext`; stats crash on fractional means fixed.
- Spikes S1–S4 (go-sdk, vector storage, FTS5, embedding models).

## [0.3.0] — 2026-09-04 — review Phases 0–1 (retroactive tag)

- Hardening of the journal-era server and the first eval harness and baseline.

## Before 0.3.0 (2026-05 to 2026-06)

- Go rewrite of obra/private-journal-mcp: six journal tools, MiniLM embeddings, FTS5, `--stats`.
