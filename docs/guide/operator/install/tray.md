# MemoRS Tray (macOS menu bar)

MemoRS Tray is an optional macOS companion for [shared HTTP servers](clients.md). It lists the
running `memors-mcp serve --http` servers and their connected clients, opens a server's **Live**
page in a native window, and starts and stops servers. It is a separate binary in its own Go
module (`tray/`). It is the only part of the project built with cgo (AppKit and WebKit); the
memors-mcp binary stays pure Go.

User-facing behaviour is described in the user guide (*The menu-bar app*). This page covers
how it works and what to configure.

![MemoRS Tray polls GET /live.json on every running server, mints login links for its stats window and starts and stops servers. It finds servers through the run files they write, keeps settings in tray.json, logs the servers it started, and contacts GitHub for the daily update check and on the setup assistant's Install step.](../images/tray-architecture.svg)

## Install

Release assets: `memors-tray_X.Y.Z_darwin_universal.zip` and its `.sha256`, next to the
server archives. The zip holds `MemoRS Tray.app`: one universal binary (arm64 and x86-64),
macOS 12 or newer, ad-hoc signed and **not notarized**. On first open, allow it under
*System Settings › Privacy & Security*, or remove the quarantine attribute:

```bash
shasum -a 256 -c memors-tray_X.Y.Z_darwin_universal.zip.sha256
ditto -x -k memors-tray_X.Y.Z_darwin_universal.zip /Applications
xattr -dr com.apple.quarantine "/Applications/MemoRS Tray.app"
```

From source (needs the Xcode command-line tools):

```bash
make tray        # tray/memors-tray, for this machine
make tray-app    # tray/dist/MemoRS Tray.app and the release zip
make tray-test   # vet and tests of the tray module
```

## Installing memors-mcp from the app

The setup assistant installs memors-mcp on demand. On the Install step it asks the GitHub API for
the latest release (`api.github.com/repos/kKEo/memors/releases/latest`). The same request is the
update check (below). MemoRS Tray makes no other outbound connection.

Installing works like this:

1. Download `checksums.txt` and `memors-mcp_<version>_darwin_<arch>.tar.gz` for this Mac. Only
   https is accepted, redirects included.
2. Compare the archive's SHA-256 with its line in `checksums.txt`. A release without that line
   is refused.
3. Extract only the regular file `memors-mcp` from the archive, into a temporary file in the
   target folder (default `~/.local/bin`).
4. Run `memors-mcp version` on it, and only then rename it over `<folder>/memors-mcp`. A failure at
   any step leaves the previous binary untouched.
5. Set `memors_binary` in `tray.json` to the new file.

The checksum proves the download is the file the release lists. Like the manual install, it
does not protect against a compromised release. Servers that are running keep the old binary
until they restart; the last step offers to restart the ones MemoRS Tray started.

## Settings window and launch at login

The settings window, the setup assistant and the Configure Your Agent window are pages that
MemoRS Tray serves itself:

- They are served on `127.0.0.1:<random port>` and shown in a WKWebView of their own. Each
  page, like each server's stats page, is a window that joins one tab group (AppKit window
  tabbing), so one MemoRS Tray window shows on screen. Every tab keeps its own
  non-persistent cookie store.
- A window enters through a link carrying a per-process secret, passed in process. It then
  holds an HttpOnly, SameSite=Strict cookie.
- Requests with any other `Host` header, or without the cookie, are refused.
- Form posts must be same-origin.
- Saving checks that `tray.json` has not changed since the form was opened, and validates
  everything before writing.

**Open MemoRS Tray when you log in** writes `~/Library/LaunchAgents/io.github.kkeo.memors-tray.plist`
(`RunAtLoad`, the app's own executable) and turning it off removes the file. It takes effect at
the next login. MemoRS Tray refuses while macOS runs it from a temporary copy (App Translocation:
move it to Applications first). At each start it updates the path if the app has moved. If
memo-tray's old agent (`io.github.kkeo.memo-tray.plist`) is still there, MemoRS Tray replaces it
with its own, so the setting carries over.

## How it finds servers

Every `serve --http` writes a run file, `$MEMORS_HOME/run/serve-<pid>.json` (see
[Files on disk](../config/files.md)). MemoRS Tray scans that directory every 2 seconds and
whenever its menu opens. It ignores the directory if it is not owned by you or is writable by
others. It deletes a run file only when the process is gone, or when the pid now belongs to
another program: the name is not `memors-mcp`, or it started after the file was written.

For each server it polls `GET /live.json` (see
[For companion apps](clients.md#for-companion-apps)). In token mode it reads the token from the
`token_file` named in the run file, at every poll, so a rotated token is picked up after the
server restarts. It sends the token only to loopback HTTP or to HTTPS, through no proxy, and
never follows a redirect. Servers that require a client certificate (mTLS) are listed but
cannot be polled or opened in a window.

Only servers on this machine and in this `MEMORS_HOME` are discovered. MemoRS Tray resolves
`MEMORS_HOME` like memors-mcp; apps launched from Finder see no shell variables, so that means
`~/.memors-mcp`.

## Starting and stopping

A server MemoRS Tray starts runs as:

```bash
memors-mcp serve --http 127.0.0.1:<port> [--auth token]
```

It runs with `MEMORS_HOME` and `MEMORS_KB` set. stdin is `/dev/null`, and stdout and stderr go to
`$MEMORS_HOME/logs/serve-<kb>.log` (0600, moved to `.1` above 5 MB). Variables that would change
where or how it serves are removed from its environment: `MEMORS_HTTP_*`, `MEMORS_TLS_*`,
`MEMORS_PUBLIC_URL` and `MEMORS_METRICS_ADDR`. **Stop** sends SIGTERM and kills the
process if it is still running after 10 seconds.

A server started elsewhere gets SIGTERM only, and only after MemoRS Tray has checked that the pid
is still that server: a `memors-mcp` process that started before its run file was written, and,
if it answers, one whose `/live.json` reports the same pid and instance id.

MemoRS Tray starts loopback servers only. For remote or proxied servers, keep using a service
manager.

## tray.json

`$MEMORS_HOME/tray.json` (0600) is optional. The settings window edits it. MemoRS Tray also rereads
it when you change it by hand, and adds an entry the first time you start a knowledge base from
the menu. Every write inside MemoRS Tray goes through one lock, from read to write, so the menu and
the settings pages never undo each other.

| Key | Meaning |
|---|---|
| `memors_binary` | Path to memors-mcp. Default: next to the app, then `$PATH`, `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin` |
| `claude_binary` | Path to Claude Code, for **Ask the Knowledge Base…**. Default: `$PATH`, then `~/.local/bin`, `~/.claude/local`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/.npm-global/bin`, `~/.bun/bin`, `~/.volta/bin` |
| `ask.auth` | How Claude Code signs in for **Ask the Knowledge Base…**: left out (its own login, or whatever credentials MemoRS Tray's environment holds), `api_key`, `oauth_token`, `bedrock`, `vertex` or `foundry` |
| `ask.model`, `ask.effort` | Claude Code's `--model` (an alias or a full model name) and `--effort` (`low`, `medium`, `high`, `xhigh`, `max`). Default: Claude Code's own |
| `ask.max_turns` | Steps per answer, 1–100 (default 12) |
| `ask.timeout_seconds` | Time limit of one question, 10–1800 (default 180) |
| `ask.max_budget_usd` | Stop a question at this cost (`--max-budget-usd`). Default: no limit |
| `ask.instructions` | Text added to the end of the system prompt, up to 4000 bytes |
| `ask.env` | Extra environment for Claude Code only. Applied last, so it wins over what `ask.auth` sets |
| `servers[].kb` | Knowledge-base name (`MEMORS_KB`) |
| `servers[].addr` | Loopback `host:port`. Assigned from 8765–8799 when you first start the knowledge base from the menu |
| `servers[].auth` | `none` (default) or `token` (`<MEMORS_HOME>/http-token`) |
| `servers[].autostart` | Start it when MemoRS Tray starts |
| `servers[].env`, `env` | Extra environment for that server, or for every server. Variables MemoRS Tray controls (above, plus `MEMORS_KB` and `MEMORS_HOME`) are refused |
| `stop_on_quit` | Stop the servers MemoRS Tray started when it quits (default `true`) |
| `check_updates` | Ask GitHub once a day whether a newer MemoRS Tray or memors-mcp exists (default `true`) |
| `debug` | Enable the web inspector in stats windows |
| `expert` | Show the advanced options in the settings window (default `false`). It only changes what the window shows; hidden options keep their values |

## Ask: how Claude Code runs

**Ask the Knowledge Base…** runs `claude -p` in a private temporary directory with
`--setting-sources ""`, `--strict-mcp-config`, `--disable-slash-commands`, `--tools ""` and
`--no-session-persistence`. These flags are fixed. Because no settings file is read, a model,
an `apiKeyHelper` or an `env` block in `~/.claude/settings.json` has no effect here; the `ask`
keys above replace them. They are read for every question, so a change needs no restart.

`ask.auth` sets one variable in Claude Code's environment and removes the others' (and
`ANTHROPIC_AUTH_TOKEN`), so the choice holds whatever MemoRS Tray was started with:

| `ask.auth` | Sets | Credential |
|---|---|---|
| left out | nothing; the environment passes as it is | Claude Code's login (Keychain) |
| `api_key` | `ANTHROPIC_API_KEY` | `$MEMORS_HOME/ask-api-key` |
| `oauth_token` | `CLAUDE_CODE_OAUTH_TOKEN` | `$MEMORS_HOME/ask-oauth-token`, from `claude setup-token` |
| `bedrock` | `CLAUDE_CODE_USE_BEDROCK=1` | AWS's own chain; `AWS_PROFILE`, `AWS_REGION` in `ask.env` |
| `vertex` | `CLAUDE_CODE_USE_VERTEX=1` | Google's own chain; `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID` in `ask.env` |
| `foundry` | `CLAUDE_CODE_USE_FOUNDRY=1` | `ANTHROPIC_FOUNDRY_RESOURCE` and its key in `ask.env` |

The key and the token are never written to `tray.json`. The settings window stores them in the
files above (0600, one line), never shows them again, and deletes them when another sign-in is
saved. To set one by hand, write the file yourself. A question fails with a message that names
Settings when `ask.auth` needs a file that is missing.

For an LLM gateway, leave `ask.auth` out and put `ANTHROPIC_BASE_URL` and
`ANTHROPIC_AUTH_TOKEN` in `ask.env`; that token then sits in `tray.json` (0600). A proxy
(`HTTPS_PROXY`) goes in `ask.env` too.

```json
{
  "ask": {
    "auth": "bedrock",
    "model": "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
    "effort": "low",
    "max_turns": 8,
    "max_budget_usd": 0.25,
    "env": { "AWS_PROFILE": "work", "AWS_REGION": "eu-central-1" }
  }
}
```

## Updates

With `check_updates` on, MemoRS Tray requests `releases/latest` a minute after it starts and
then every 24 hours (in memory; a restart checks again after a minute). It compares the release
tag with its own version and with `memors-mcp version`; a build without a semantic version
(`dev`, `git describe` output) is never offered an update. **Check for Updates…** in the menu
requests it again on demand, whether or not the daily check is on.

**Update MemoRS Tray** in the Updates window replaces the app in place:

1. `memors-tray_X.Y.Z_darwin_universal.zip` and its `.sha256` are downloaded; the SHA-256 must
   match before anything is unpacked.
2. The zip is unpacked with `ditto` into a hidden folder next to the installed app, and the new
   `memors-tray -version` must run.
3. The old bundle is renamed, the new one moved to its path, and the old one removed. The
   running process keeps working from memory until it quits.
4. A detached shell waits for the process to exit (which releases `tray.lock`) and opens the
   app again with `open`. Quitting stops the servers MemoRS Tray started as usual.

The app is not updated in place when it is not running from a `.app` bundle, when macOS App
Translocation runs it from a temporary copy (an app opened straight from Downloads), or when
the folder it is in is not writable; the window then links to the release page. The download
carries no quarantine attribute, so Gatekeeper does not ask again. The release's checksum proves
the download is the file the release lists, nothing more, as with the manual install.

## The stats window

The window is a WKWebView in MemoRS Tray's own process:

- It logs in with a single-use link from `POST /login-link`, so the token never reaches the
  web view, a URL or the process arguments.
- It uses a private, non-persistent website data store. The session cookie is never written
  to disk.
- JavaScript is off; memors-mcp's UI does not use any.
- It shows only the server's own origin. A clicked link elsewhere opens in the default
  browser; any other navigation (redirects off the server, `file:`, custom schemes) is refused.

**Open in Browser** passes a single-use login link to the browser through `open(1)`. Like the
link the server prints at startup, it is visible to other processes for an instant and becomes
useless once it is used.

## Files

| Path | Contents |
|---|---|
| `$MEMORS_HOME/tray.json` | Settings (above) |
| `$MEMORS_HOME/tray.lock` | Held while MemoRS Tray runs; a second copy refuses to start |
| `$MEMORS_HOME/logs/tray.log` | MemoRS Tray's own log (login codes are redacted) |
| `$MEMORS_HOME/logs/serve-<kb>.log` | Output of the servers MemoRS Tray started |
| `~/Library/LaunchAgents/io.github.kkeo.memors-tray.plist` | Present while "Open MemoRS Tray when you log in" is on |
| `~/.local/bin/memors-mcp` (or the folder you chose) | memors-mcp, when installed by the setup assistant |
