# The studio

The studio is a small local web app for reading, checking and running scenarios. It is the
quickest way to review a new file or to understand a failing one.

```bash
make scenario-studio            # or: go run ./cmd/memors-scenario serve --dir <dir>
```

It prints a `http://127.0.0.1:<port>/` URL. Files are read again on every request, so edit in
your editor and reload the page. Nothing is cached except the last run of each file.

## The index

- **Files**: every scenario with its catalogue id, decision, tiers, actors, step count
  (and parallel groups), `covers`, and whether it is valid. An invalid file shows its first
  problem.
- **Coverage**, the same data as the [log](log/README.md):
  - which scenarios call each operation as an agent (a tool) and as a human (the CLI)
  - which expectation kinds are used where
  - which invariants are skipped, and by whom
  - which catalogue entries and gaps no scenario covers yet

An empty cell in the operations table is a test nobody has written.

## A scenario page

`/s/<file>` shows:

- **The picture**, inline. `… more` and `less` re-draw it with a block expanded or collapsed.
  The "open as .svg" link opens the standalone file, with tooltips and click-to-expand.
- **Problems**, if any. Each links to its source line and to its step on the picture.
- **Steps**: a table with number, `at`, resolved date, actor, op, bind, expectation kinds and
  note.
- **Source**: the YAML with line anchors (`#L36`).
- **Run**: runs the file now (one run at a time, against a fresh temporary knowledge base),
  then shows the result on the picture. This is the overlay described in
  [Reading the picture](pictures.md), with a table of failures. If the file changed after the
  run, the page says the result is stale.

## The API

Two endpoints exist for the editor planned next, and work today with `curl`:

```bash
curl -s --data-binary @b2.yaml http://127.0.0.1:<port>/api/validate
# {"ok":false,"issues":[{"path":"steps.1.args","line":36,"message":"step 2: $f1 is not bound by an earlier step"}]}

curl -s --data-binary @b2.yaml 'http://127.0.0.1:<port>/api/render?mode=standalone&expand=all' > b2.svg
```

- `/api/validate` returns 200 or 422 with the problems.
- `/api/render` returns the SVG, or 422 with the problems.
- Bodies are limited to 1 MB.

## Safety

The studio binds to loopback only. It checks the `Host` header, and refuses cross-site posts:
a run needs a same-origin `Origin` or `Sec-Fetch-Site`. Every page is served with a strict
content security policy and no script. Only files directly inside `--dir` with a scenario
extension can be opened; anything else is a 404.
