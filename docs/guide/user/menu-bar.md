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

MemoRS Tray contacts GitHub only while this assistant is open on the Install step.

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
  agents never changes.
- **Configure Your Agent…** in a running server's submenu opens a window that walks you
  through connecting an agent to that server (see below).
- **Stop**, **Restart** and **Show Log** are in the same submenu. Servers you started in a
  terminal appear too, and you can stop them from here.
- Quitting MemoRS Tray stops the servers it started. To keep them running, set
  `"stop_on_quit": false`.

## Configure your agent

**Configure Your Agent…** opens a window that guides you through connecting one agent, in four
steps:

1. **Agent**: the server's knowledge base, MCP address, sign-in and the name the agent will use
   (`memors` for the `default` knowledge base, `memors-<name>` for the others). Pick **Claude
   Code**, **Claude Desktop** or **Other MCP client**.
2. **Add the server**:
   - *Claude Code*: the `claude mcp add` command to run once in a terminal, with a **Copy**
     button.
   - *Claude Desktop*: **Open file** opens `claude_desktop_config.json` (and creates it if Claude
     Desktop has none yet), and **Copy configuration** copies the entry to add. Claude Desktop
     starts its own memors-mcp on the same knowledge base, so this works only for knowledge
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

**Settings…** in the menu opens the settings window:

- **General**: open MemoRS Tray when you log in; stop the servers it started when it quits.
- **memors-mcp**: which memors-mcp runs and its version, with **Check for updates…**; find it
  automatically or use a specific file; the embedding model for every server MemoRS Tray starts.
- **Servers**: each knowledge base MemoRS Tray can start, with its address, whether it needs a
  token, and whether it starts with MemoRS Tray; add or remove one (removing never deletes the
  knowledge base itself).
- **Advanced**: extra environment for the servers, and the Web Inspector for MemoRS Tray's
  windows. Links open `tray.json` and the logs folder.

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

Logs live in `~/.memors-mcp/logs/`: one file per server (`serve-<name>.log`) and `tray.log`.
