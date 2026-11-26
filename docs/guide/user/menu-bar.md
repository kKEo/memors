# The menu-bar app (macOS)

If you run memors-mcp as one shared server (`memors-mcp serve --http`, see
[Looking inside](looking-inside.md)), **MemoRS Tray** puts it in your menu bar. At a glance you
see which agents are connected right now, you can open the server's live page in a window of
its own, and you can start and stop servers without a terminal.

## Installing it

1. Download `memors-tray_X.Y.Z_darwin_universal.zip` from the
   [releases page](https://github.com/kKEo/memors/releases), unzip it and move
   **MemoRS Tray.app** to Applications.
2. Open it. The first time, macOS says it cannot check the developer: the app is not
   notarized. Open **System Settings › Privacy & Security** and click **Open Anyway**. (Or, in a
   terminal: `xattr -dr com.apple.quarantine "/Applications/MemoRS Tray.app"`.)
3. If memors-mcp is not installed yet, the **setup assistant** opens by itself (below). Otherwise
   MemoRS Tray finds it in the usual places (`~/.local/bin`, `/opt/homebrew/bin`,
   `/usr/local/bin`, `~/go/bin`).

## The setup assistant

**Install or Update memors-mcp…** in the menu (or **Install memors-mcp…** at the top, when it is
missing) walks you through four steps:

1. **Welcome**: whether memors-mcp is installed, and which version.
2. **Install**: MemoRS Tray looks up the newest release on GitHub and downloads the one for your
   Mac. Before installing it checks the download against the checksums published with the
   release and makes sure the program runs. The default folder is `~/.local/bin`. If anything
   goes wrong, the memors-mcp you had stays as it was. After an update, **Restart them** restarts
   the servers MemoRS Tray started, so they run the new version.
3. **Knowledge base**: a name (pick an existing one or type a new one), the embedding model,
   whether a token is required, and whether the server starts with MemoRS Tray. **Save and start**
   starts it.
4. **Connect**: the MCP address and the `claude mcp add` line for Claude Code, with a **Copy**
   button. Once the server runs, **Configure your agent** opens the step-by-step guide for other
   agents (see below).

MemoRS Tray contacts GitHub on this Install step and for the daily update check (below).

## What the menu shows

```
crportal · 127.0.0.1:8765 · running      ▸
    ● claude-code 2.1.4 — search (3s)
    ○ cursor 1.2 — idle 12m
default · stopped                        ▸
Start Server                             ▸
Settings                                 ▸
Quit MemoRS Tray (stops 1 server)
```

- One line per server: its memory's name, its address and its state. The submenu (▸) has
  everything you can do with it.
- Under each server, one line per connected agent:
  - **●** the agent is busy (the call it is running is shown) or connected and active;
  - **○** its connection is open but it has been quiet.
- The number next to the menu-bar icon counts the **●** agents.

An agent disappears from the list when it disconnects. One that stays quiet for more than 30
minutes is dropped by the server and shows up again with its next request.

## Starting and stopping servers

- **Start Server ▸** lists your memories. Pick one and MemoRS Tray starts a server for it on
  `127.0.0.1:8765`, or the next free port. It remembers the address, so the URL you give your
  agents never changes. **Add Server…** at the bottom of that list opens the form that sets up
  a server for a knowledge base that does not exist yet, with its own address and access setting.
- **Ask the Knowledge Base…** in a running server's submenu opens a window where you type a
  question and get an answer from that knowledge base (see below).
- **Configure Your Agent…** in a running server's submenu opens a window that walks you
  through connecting an agent to that server (see below).
- **Stop**, **Restart** and **Show Log** are in the same submenu. Servers you started in a
  terminal appear too, and you can stop them from here.
- Quitting MemoRS Tray stops the servers it started. To keep them running, set
  `"stop_on_quit": false`.

## Ask the knowledge base

**Ask the Knowledge Base…** opens a conversation window for one running server. Type a
question and press **Ask** (or ⌘↩); on an empty conversation you can also pick one of the
suggested questions. Follow-up questions see the earlier answers.

While the answer is on its way the window shows what is happening: each search and each
memory that is read, then the text as it is written. **Stop** ends it; what there was of the
answer stays, and **Try again** asks the same question once more.

An answer is formatted text, and it cites the memories it relies on. The `memo://` addresses
in it, and the **Sources** under it, are links: a click opens that memory in the server's own
window. Under each answer, the line of lookups unfolds to show what was searched and read,
**Copy answer** puts the answer on the clipboard, and **Ask again** (on the newest answer)
replaces it with a new one. If a question fails, the reason takes the answer's place, with
**Try again**. **Clear conversation** starts over.

Under the hood MemoRS Tray runs Claude Code in headless mode with nothing but that server's
MCP attached: no other MCP servers, skills, plugins, hooks or built-in tools. Claude can only
search and read the knowledge base, so the answer comes from your memory, not from the web or
your files. Each question is one Claude Code run, billed to the Claude account you are logged
in to, unless you choose another sign-in (below).

It needs Claude Code installed; the window says so when it is not found. MemoRS Tray looks on
`$PATH` and in the usual install folders; **Claude Code location** in Settings points it
elsewhere. Servers that require a client certificate (mTLS) cannot be asked this way.

### Choosing the account and the model

The line under the question box shows how Claude Code runs, for example *Claude Code's own
sign-in · default model*. **Ask settings…** next to it opens the **Ask** tab of Settings;
**‹ Back to Ask** returns to the conversation. What you save applies to the next question.
The last three items below show once **Advanced options** is ticked (see
[Settings](#settings)).

- **Sign in with**: *Claude Code's own sign-in* (the account you logged in to with `claude`),
  an *Anthropic API key*, a *long-lived token* (the one `claude setup-token` prints), or a
  cloud provider: *Amazon Bedrock*, *Google Vertex AI*, *Microsoft Foundry*.
- **API key or token**: type it once. MemoRS Tray keeps it in a file only you can read, never
  shows it again, and deletes it when you save another sign-in. Leave the field empty to keep
  the stored one.
- **Model** and **Effort**: an alias such as `sonnet` or `haiku`, or a full model name; a
  smaller model and a lower effort answer faster and cost less.
- **Extra instructions**: added to what MemoRS Tray tells Claude, for example the language to
  answer in.
- **Claude Code location**: the `claude` file to run, when MemoRS Tray does not find it.
- **Steps per answer**, **Time limit** and **Cost limit per question**: when a question stops.
  The defaults are 12 steps, 180 seconds and no cost limit.
- **Environment for Claude Code**: `NAME=value` lines for Claude Code only. A cloud provider
  reads its profile, region or project from here.

Your own Claude Code settings (`~/.claude/settings.json`) are not read by this window, so a
model or a provider you set there has to be set here as well.

## Configure your agent

**Configure Your Agent…** opens a window that guides you through connecting one agent, in four
steps:

1. **Agent**: the server's knowledge base, MCP address, sign-in and the name the agent will use
   (`memors` for the `default` knowledge base, `memors-<name>` for the others). Pick **Claude
   Code**, **Claude Desktop** or **Other MCP client**.
2. **Add the server**:
   - *Claude Code*: the `claude mcp add` command to run once in a terminal, with a **Copy**
     button. An optional second part sets up the memors-hooks plugin, which makes Claude use
     the knowledge base without being asked: the commands that install it, and the command
     that writes the project's `.memors-kb` file so the plugin reads this knowledge base
     rather than `default`. The plugin's own server refuses to start while this one runs;
     disable it in `/mcp`.
   - *Claude Desktop*: **Open file** opens `claude_desktop_config.json` (and creates it if Claude
     Desktop has none yet), and **Copy configuration** copies the entry to add. Claude Desktop
     starts its own memors-mcp on the same knowledge base (the entry sets
     `MEMORS_SERVE_SHARED=1` to allow a second engine), so this works only for knowledge
     bases on this Mac, and its activity does not show in the stats window. Restart Claude
     Desktop afterwards.
   - *Other*: the MCP address (Streamable HTTP) and, when the server needs a token, the
     `Authorization` header and the command that prints the token.
3. **Check**: where the agent lists its connected servers, and what to look at when it does not
   connect.
4. **Start using it**: the `ask` prompt (in Claude Code, `/mcp__<name>__ask`), and how to teach
   the agent to search on its own.

The token itself is never shown or copied, only the `memors-mcp http-token` command that prints
it. Choosing the item for another server switches the same window to that server.

## The stats window

**Open Stats Window** shows the server's **Live** page in a window of its own: connected
clients, calls in flight, per-tool timings and ingest progress, refreshed every 2 seconds. The
other pages (search, documents, facts) work in the window too. Links that leave the server open
in your normal browser.

If the server asks for a token, the window logs in by itself. There is nothing to paste.

## Settings

**Settings…** in the menu opens the settings window. It has three tabs, and one **Save**
button for all of them:

- **General**: open MemoRS Tray when you log in; stop the servers it started when it quits;
  check for updates once a day, with **Updates…** (below); which memors-mcp runs and its
  version, with **Check for updates…**; the embedding model for every server MemoRS Tray
  starts.
- **Servers**: each knowledge base MemoRS Tray can start, with its address, whether it needs a
  token, and whether it starts with MemoRS Tray. **Add server…** opens a form; **Save & Run**
  saves the server, starts it and shows the `claude mcp add` command to connect Claude Code
  (plus a link to the guide for other agents). Removing a server never deletes the knowledge base
  itself.
- **Ask**: how Claude Code signs in, the model and extra instructions (see
  [Ask the knowledge base](#ask-the-knowledge-base)).

**Advanced options**, at the right of the tabs, shows the options most people never change;
the choice is remembered when you save:

- *General*: the Web Inspector for MemoRS Tray's windows; finding memors-mcp automatically or
  using a specific file.
- *Servers*: extra environment for the servers.
- *Ask*: where Claude Code is, the limits on one question, and extra environment for Claude
  Code.

An option that is hidden keeps its value and still applies. Under the tabs, links open
`tray.json` and the logs folder.

Every MemoRS Tray window has a **Dismiss** button in its header; ⌘W and the close box do the
same. If a form has changes you have not saved, the window asks first and lists the fields
you changed (their names, never what you typed): **Keep Editing** returns to the form,
**Discard** closes it and drops the changes. A window with nothing changed closes at once.

The window shows what is saved in `~/.memors-mcp/tray.json`. You can also edit the file by hand:

```json
{
  "servers": [
    { "kb": "crportal", "addr": "127.0.0.1:8765", "autostart": true },
    { "kb": "work", "addr": "127.0.0.1:8766", "auth": "token" }
  ],
  "env": { "MEMORS_MODEL": "potion" },
  "stop_on_quit": true
}
```

- `autostart` starts that server whenever MemoRS Tray starts.
- `auth: "token"` keeps other people on a shared Mac out (see the operator guide).
- `env` passes settings to every server MemoRS Tray starts. Apps opened from the Dock do not see
  your terminal's variables, so put settings such as `MEMORS_MODEL` here.
- `memors_binary` is the memors-mcp to run; the setup assistant sets it to what it installed.
- `claude_binary` is the Claude Code executable **Ask the Knowledge Base…** runs, when it is
  not on `$PATH` or in a usual install folder.
- `ask` holds the Ask settings, for example
  `"ask": { "model": "haiku", "effort": "low", "max_turns": 8 }`. The operator guide lists
  every key.

Logs live in `~/.memors-mcp/logs/`: one file per server (`serve-<name>.log`) and `tray.log`.

## Updates

MemoRS Tray asks GitHub for the newest release a minute after it starts and then once a day.
When a newer MemoRS Tray or memors-mcp exists, the top of the menu says so: **Update MemoRS
Tray to vX.Y.Z…** or **Update memors-mcp to X.Y.Z…**. Either opens the **Updates** window, as
does **Check for Updates…** at the bottom of the menu, which asks GitHub again first. The window
shows both installed versions next to the newest release, with:

- **Update MemoRS Tray**: downloads the new app, checks it against the `.sha256` published with
  the release, makes sure it starts, swaps it in where MemoRS Tray is installed, and relaunches
  it. Servers MemoRS Tray started stop with it (unless *stop on quit* is off) and the ones set
  to start with it start again. If MemoRS Tray cannot replace itself, for example because macOS
  runs it from a temporary copy (open it from Applications, not from the Downloads folder), the
  window offers the release page instead.
- **Update memors-mcp…**: the setup assistant's Install step, as before.

A build without a release version (a development build) is never offered an update. To stop
the daily check, turn off **Check for updates once a day** in Settings; **Check for Updates…**
still works then.
