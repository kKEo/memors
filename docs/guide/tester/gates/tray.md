# MemoRS Tray tests

MemoRS Tray (`tray/`) is the macOS menu-bar app. It is its own Go module and the only cgo
build in the repository, so its tests run separately:

```bash
make tray-test        # cd tray && go vet ./... && go test ./... -race -count=1
```

## What the suite covers

| Package | Tests cover |
|---|---|
| `app` | Agents appearing and leaving, unreachable servers, start/stop/restart, autostart, shutdown, a bad config shown to the user, first-run behaviour, the update check and its menu, relaunch after an update |
| `supervisor` | Starting and stopping the server, crashes, killing a stubborn process, the child's environment, log rotation, stopping a server the tray did not start |
| `setup` | The setup wizard (install, knowledge base, failure paths), settings (save, refuse bad input, conflicts), access control, connect pages for each client, the updates page |
| `installer` | Install, refusing bad downloads, release lookup errors, the tray's own install and app bundle |
| `menu` | How each menu item renders |
| `discover` | Finding servers, refusing untrusted directories |
| `client` | Reading `/live.json` and the login link, errors, loopback only |
| small packages | `config`, `home`, `locate`, `loginitem`, `update`, `webpolicy`, `icon` |

Most supervisor tests do not start a real server. The test binary re-executes itself as a fake
`memors-mcp serve --http` (`TRAY_FAKE_MEMORS`, `TRAY_FAKE_MODE` in `supervisor_test.go`), so
crashes and slow shutdowns can be scripted.

## Tests switched on by environment variables

| Variable | Enables | Network |
|---|---|---|
| `MEMORS_TRAY_E2E_BIN=<path to memors-mcp>` | `TestE2ERealServer`: the supervisor against a real built server, with `MEMORS_MODEL=not-a-model` so nothing downloads and search runs keyword-only | No |
| `MEMORS_TRAY_LIVE_GITHUB=1` | `TestLiveGitHub` (installer) and `TestLiveWizard` (setup): download the real latest release | Yes |

```bash
make build && cd tray && MEMORS_TRAY_E2E_BIN="$PWD/../memors-mcp" go test ./internal/supervisor/ -run E2E -count=1 -v
```

CI runs the end-to-end test on every pull request. The live GitHub tests are for a person to
run before a release.

## Checks that are not Go tests

CI also checks that the tray binary does not link the server's dependencies (SQLite, the model
runtime, the MCP SDK). It builds the app bundle, verifies its code signature and that it
contains both `x86_64` and `arm64`, and runs it with `-version`.
