# Contract and golden tests

A golden test compares an output with a file committed in `testdata/`. It catches *any*
change, including ones nobody meant to make. The goldens guard the surfaces other programs
depend on: what an MCP client sees, what the tray and dashboards read, what Prometheus
scrapes.

| Test | Golden file | Guards | Re-record with |
|---|---|---|---|
| `TestListToolsGolden` (`internal/server`) | `internal/server/testdata/tools.golden.json` | The `tools/list` response: names, descriptions, input and output schemas of the ten tools | `make golden-update` |
| `TestLiveJSONGolden` (`internal/ui`) | `internal/ui/testdata/live.golden.json` | The `/live.json` snapshot that MemoRS Tray and the live page read | `make golden-update` |
| `TestPromTextGolden` (`internal/obs`) | `internal/obs/testdata/metrics.golden.txt` | The Prometheus text exposition | `UPDATE_GOLDEN=1 go test ./internal/obs/ -run TestPromTextGolden` |
| `TestGolden` (`internal/eval/scenario/diagram`) | `diagram/testdata/*.short.svg`, `*.inline.svg` | The scenario pictures | `make golden-update` |
| `TestBookCurrent` (`internal/eval/scenario/book`) | `docs/guide/tester/scenarios/…` | This guide's generated scenario pages | `make scenario-docs` |

Next to the goldens, two server tests guard the contract in words rather than bytes:

- `TestToolSurfaceIsExactlyTenTools`: adding or removing a tool is a deliberate, documented
  decision, never a side effect.
- `TestDescriptionsAreHonest`: every tool has a title and an output schema, and no
  description carries struct-tag leftovers or privacy promises the system cannot keep.
- `TestAnnotations`: every tool carries its MCP annotations (read-only, destructive and so on).

## Changing a contract on purpose

1. Make the change.
2. Run the update command from the table.
3. Read the diff of the golden file with `git diff`, as carefully as the code diff. Every
   changed line must be one you meant to change.
4. Rules for the specific surfaces:
   - **`/live.json`** may only *gain* fields without bumping `live.Schema`. Removing or renaming
     a field is a breaking change for MemoRS Tray.
   - **Tool descriptions** are read by models. Wording changes there change agent behaviour, so
     treat them like code.
5. Commit the golden together with the change, and say why in the pull request.

## When a golden fails and you did not mean to change anything

The failure prints the first difference. Common causes:

- A map iterated without sorting, so the order changes between runs.
- A timestamp or a random id leaked into the output.
- A dependency upgrade changed a schema or a default.

Fix the cause. Do not re-record a golden to make a test pass.
