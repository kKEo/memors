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
| `setup` | The setup wizard (install, knowledge base, failure paths), settings (tabs, save, refuse bad input, conflicts), access control, connect pages for each client, the updates page, the Dismiss button on every page |
| `installer` | Install, refusing bad downloads, release lookup errors, the tray's own install and app bundle |
| `menu` | How each menu item renders |
| `discover` | Finding servers, refusing untrusted directories |
| `client` | Reading `/live.json` and the login link, errors, loopback only |
| small packages | `config`, `home`, `locate`, `loginitem`, `update`, `webpolicy` (including the Dismiss link), `icon` |

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

## Manual checks: windows, tabs and closing

The Objective-C window layer (`internal/macwin`) has no tests. After a change to it, build the
app (`make tray && ./tray/memors-tray`) and check by hand:

1. **Settings…** → **Dismiss** with no edits: the window closes at once. The same after
   opening each tab, and after changing a field and changing it back.
2. Tick **Open MemoRS Tray when you log in** and change a server's address, then **Dismiss**:
   a sheet asks "Discard unsaved changes?" and lists both fields by name under "Not saved
   yet:". **Keep Editing** leaves the window and the edits in place; **Dismiss** again →
   **Discard** closes it.
3. Make the same edit, then ⌘W, then the red close box: the same sheet appears each time.
4. Tick **Advanced options**, type in **Environment for servers** on the **Servers** tab and
   **Save**: the page reloads and **Dismiss** now closes
   without asking (the saved values are the new defaults).
5. The setup assistant, **Updates** and **Configure Your Agent…** pages show the button and close.
6. **Settings…** → click the **Servers** and **Ask** tabs, then **Dismiss**: closes without asking
   (choosing a tab is not an edit). Tick **Advanced options**, then **Dismiss**: the sheet asks
   and names it. Type in **API key or token** on the **Ask** tab, then **Dismiss**: the sheet
   names the field and does not show what was typed.
7. **Open Stats Window**, type in the search box, ⌘W: closes without asking (only POST forms count).

Window tabs, with two servers running:

1. Open **Settings…**, **Check for Updates…**, then **Ask the Knowledge Base…** and **Open
   Stats Window** for both servers: one window with six tabs.
2. Choose **Settings…** again: its tab comes forward, and no tab is added.
3. Edit a Settings field, switch to another tab, then click the red close box: the Settings
   tab comes forward with the "Discard unsaved changes?" sheet, and **Keep Editing** keeps it.
4. ⌃⇥ and ⌃⇧⇥ (and ⌘⇧], ⌘⇧[) switch tabs. ⌘↩ sends the question only on the Ask tab in front.
5. Stop a server: its Stats tab shows that the server has stopped. Start it again: the tab
   reloads.
6. Close every tab, move and resize nothing, open **Settings…**: the window is where it was
   and as large as it was.
7. In System Settings › Desktop & Dock, set **Prefer tabs** to *Never* and repeat step 1: still
   one window with tabs.
