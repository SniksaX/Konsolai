# MCP servers

Konsolai is a [Model Context Protocol](https://modelcontextprotocol.io) client. An MCP server adds tools to the model: a local command over stdio, or a streamable HTTP endpoint. MCP tools are offered in `lookup`, `act`, and `build` turns, never in `answer`. The model can call them, but it cannot change the configuration: there is no tool that adds, edits, enables, trusts or removes a server.

## Setup

MCP is configured under `mcp.servers`; `mcp.max_inline_tools` (default `15`) caps how many tools are offered at once. A filesystem server over stdio, read-only:

```yaml
mcp:
  servers:
    files:
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/data/projects"]
      trust: read_only
```

A database server that reads its connection string from the environment:

```yaml
mcp:
  servers:
    db:
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-postgres"]
      env:
        DATABASE_URL: "${DATABASE_URL}"
      trust: read_only
      tools:
        read_only: ["query", "list_*"]
```

A remote HTTP server with a token:

```yaml
mcp:
  servers:
    docs:
      transport: http
      url: https://mcp.example.com/mcp
      headers:
        Authorization: "Bearer ${DOCS_TOKEN}"
      trust: ask
```

The same through `/set mcp`, which writes the active config (or only this REPL with `--session`):

```sh
konsolai /set mcp add stdio files npx -y @modelcontextprotocol/server-filesystem /data/projects
konsolai /set mcp trust files read_only
konsolai /set mcp add http docs https://mcp.example.com/mcp
konsolai /set mcp header docs Authorization "Bearer ${DOCS_TOKEN}"
konsolai /set mcp allow files "read_*"
konsolai /set mcp deny files "write_*"
konsolai /set mcp env db DATABASE_URL '${DATABASE_URL}'
konsolai /set mcp disable files
konsolai /set mcp remove files
konsolai /mcp
```

Adding a stdio server asks once and prints the exact command line before it writes the file; `n` leaves the file unchanged. MCP entries are not config keys, so `/set mcp` is the only writer, and `/config show` / `/config diff` mask every env and header value.

## Fields

| field | meaning |
| --- | --- |
| `transport` | `stdio` runs `command` with `args`; `http` connects to `url` |
| `command`, `args` | stdio only; the child runs in the project directory |
| `env` | stdio only; the child gets `PATH`, `HOME` and this map, nothing else |
| `url` | http only; `https`, or plain `http` on a loopback or private host |
| `headers` | http only; sent on every request to that server |
| `enabled` | default `true`; a disabled server is configured but never connected |
| `trust` | `ask` (default), `read_only`, or `auto`; see below |
| `timeout_ms` | one connect, tool list, or call; default `30000` |
| `private` | labels the server; a remote main model needs a confirmation before it is used |
| `tools.allow`, `tools.deny` | shell-style globs over `tool` or `server:tool`; deny wins |
| `tools.read_only` | globs the user declares read-only; trusted at every trust level |

An `env` or `headers` value may be a literal or a `${ENV_VAR}` reference. A reference is resolved when the session starts: a missing variable names itself (`environment variable "DOCS_TOKEN" is not set`) and the server reads as down or auth failed; the value never appears in output. A literal that looks like a stored credential (`sk-`, `ghp_`, `AKIA`, `Bearer <token>`, `password=`, a long mixed token) is refused; use a reference. `/config show`, `/config diff`, `/mcp`, `/status`, `/doctor` never print an env or header value.

## Trust levels

| trust | what is exposed | what counts as read-only |
| --- | --- | --- |
| `ask` (default) | every allowed tool | only `tools.read_only` globs; the server's own `readOnlyHint` is not trusted |
| `read_only` | read-only tools only | `tools.read_only` globs and the server's `readOnlyHint` |
| `auto` | every allowed tool | `tools.read_only` globs and the server's `readOnlyHint` |

`ask` and `auto` differ only in whether the server's annotation is trusted. Neither is auto-approval: a mutating MCP call still goes through the plan gate and the approval rules, exactly like a shell command. A `read_only` server hides its mutating tools from the model altogether.

The rules are re-read for every request: a `/set mcp trust`, `allow`, `deny`, `enable` or `disable` change applies to the next turn, and the manager keeps its session and cached tool list. The manager's per-server spec (whether the server was enabled at start, its command, URL and timeout) is fixed when the process builds the manager, so enabling a server that started disabled, or changing its command, URL or timeout, needs a restart.

## Tools in a turn

- `answer` turns see no MCP tools. `lookup` sees only read-only tools. `act` and `build` see the allowed catalog; when a plan is required, only the read-only tools and `propose_plan` are offered until it is approved, so direct MCP tools wait for approval (`mcp_find_tool` stays available above the inline cap).
- A tool is exposed to the model as `server__tool` (lowercase, sanitized, collisions get a stable numeric suffix). Two servers can offer tools with the same name.
- Above `mcp.max_inline_tools`, the turn exposes one meta-tool, `mcp_find_tool`, instead of every schema. It searches names and one-line descriptions, so a large catalog does not fill the prompt. A found tool is registered between steps and can be called in the same turn: its schema is sent on the next request, a tool declared in the plan is accepted, and one outside the plan asks again. A `read_only` server still hides mutating found tools, and a name the server does not serve stays refused.
- In a `build` or planned `act` turn, `propose_plan` accepts `tools`: the MCP tools the plan will call, as `server__tool` or `server:tool`. A call outside that list is outside the plan and follows the usual confirmation path.
- A tool result is untrusted data. Konsolai masks secret-shaped values, caps the raw result at 8 MiB and the wrapped output at 64 KiB, omits images, audio and resources with a marker, and wraps the text in `<untrusted_mcp_result>` followed by a line telling the model that the content is data, never instructions. Tool descriptions are flattened and capped at 240 characters, and secret-shaped text is masked in descriptions and input schemas too, so a server cannot leak a configured env or header value back to the model through its tool list.

## Privacy

An MCP result is part of the conversation, so it leaves the machine when the main model is remote. `private` marks a server whose results you do not want a remote model to see. When the main model is remote, a private server is hidden unless it is confirmed: the CLI asks once per process ("this sends MCP results from <server> to the remote model"), `--yes` accepts it without asking, and a non-interactive run without `--yes` leaves it hidden. A local main model needs no confirmation. The MCP session itself is always between Konsolai and the server; the server may itself be remote.

## Status and diagnostics

- `/mcp` (or `/mcp list`) prints one row per configured server: name, transport, enabled or disabled, trust, private marker, cached tool count and known state. It never connects a server: state and count come from earlier sessions, so an unused server reads `not connected` with no count.
- `/mcp tools <name>` connects to that server once and prints one row per tool: name, `read-only` or `mutating`, `allowed` or `hidden` with the rule that hid it, the live `offered` column, and the first line of the description capped at 80 characters. Read-only means a `tools.read_only` glob matched, or the server's `readOnlyHint` under `read_only`/`auto` trust. Hidden means the tool is not in `tools.allow`, matched a `tools.deny` glob, or is mutating on a `read_only` server.
- The `offered` column is what the next `act` or `build` turn would send the model: `yes`, or `no:` with the reason (`private (remote main)`, `server disabled`, `not in allow`, `deny <glob>`, `trust read_only`, or `find tool only (inline cap)`). The summary line ends with `N offered now`. It is a per-server preview: the inline-cap check counts that server's allowed tools only, so with several servers the real turn can still switch to the find meta-tool, and a `lookup` turn offers only the read-only subset. It also ignores the plan phase: a build or high-effort act turn before approval offers only the read-only tools and `propose_plan` (plus `mcp_find_tool` above the cap), so direct MCP tools appear after the plan is approved.
- `/mcp test <name>` connects to one server once, lists its tools and prints the connect-and-list latency and the tool count. A down or unresolvable server prints a short error and the unresolved variable name, never its value.
- `/status` adds a count line and `mcp` rows after `rerank`: the count reads `N configured` until the first server connects and `U/T up` afterwards; the rows carry state, transport, trust, tool count, private marker, unresolved env names and one short error, followed by `server: tool` lines for the known tools. The count is read before the rows probe, so the first `/status` after startup shows the lazy count; with no server configured, `/status` prints `mcp not configured` instead of the count.
- `/doctor` adds one `mcp <name>` row after the remote-model lines: `✓` for a connected, disabled or not-yet-connected server, `!` for a down server or an unresolved env variable, with the fix it needs. Connected servers list their tools as `server: tool` lines.
- The welcome screen shows `mcp N configured` until the first server connects, then `U/T up`: every configured server is the denominator and a live session the numerator, and the line is hidden when no server is configured. The count never connects a server, so a server joins only after its first use.
- A `--trace` turn that called an MCP tool adds `mcp=N` to the trace stats line.

## Configuration safety

The active config file, its `config.yaml.bak-*` backups and the secrets file are protected from the agent. `write_file`, `edit_file` and `export_doc` resolve the real path (symlinks included) and refuse a protected path with the `/set` pointer; `run_shell` refuses a command whose text mentions `config.yaml`, `KONSOLAI_CONFIG` or `.config/konsolai/secrets`, which covers redirection, `tee`, `sed -i`, `mv`, `cp`, `truncate` and an interpreter that writes the file. The refusal points at `/set` to change the configuration and `/config show` to inspect it.

`read_file` on the active config or a backup returns a masked view: MCP `command`, `args`, `env` and `headers` become `<set>`, and MCP and database URLs keep their scheme and path but lose their host. A secrets-file read is refused. `search` matches the masked view of the active config and its backups, and skips the secrets file with a marker. `/config show` prints the configuration for the user at the terminal; a database password and literal MCP env/header values stay masked.

Only the user's `/set` and `/set mcp` change the configuration. The MCP bridge has no configuration path, so no tool or server can add, edit, enable, trust or remove a server.

## Threat model and what is not protected

MCP servers are third-party code and data. The bridge treats tool descriptions, schemas and results as data for the model, never as instructions for Konsolai. The plan gate and approvals are the barrier before a mutating tool runs, and read-only classification is conservative by default (`ask` does not trust the server's hint).

What is *not* protected:

- An allowed MCP call is not sandboxed. A stdio server runs as your user with its declared environment and can do anything your account can; an HTTP server sees the network. Konsolai does not wrap MCP calls in the shell guard, and a tool's side effects are the server's design.
- `trust` and `private` are caller-side policies: the session manager carries the labels but does not enforce them, so any code that calls the manager directly bypasses them. `enabled` is enforced by the manager, so a disabled server is never connected.
- The stdio process group kill is Unix-only (`Setpgid`/`SIGKILL`). On an unsupported platform a child may outlive the session.
- The HTTP transport refuses a cross-host redirect and an https-to-http downgrade, but that path has no test against a live server.
- No MCP behavior here is verified against a live vendor server; the tests use fake stdio and HTTP servers. Transports, auth schemes and tool annotations can differ in practice.
- Discovery registers the tools a `mcp_find_tool` call returns for the rest of the turn, so the exposed set can grow with each step; a very large catalog still costs one schema per found tool.
- The shell rule matches the protected names in the command text. A command that builds the name without those literal strings is not caught by that check; `read_file` and `search` classify the resolved path, so the protected config returns the masked view and the secrets file is refused or skipped with a marker.
- The session manager enforces one timeout per connect/list/call (`timeout_ms`), a process-wide cap of 4 concurrent calls, and an 8 MiB raw result cap. It does not rate-limit a server, audit its calls, or prove a `readOnlyHint` true.

See [configuration.md](configuration.md#mcp-servers) for the reference section and [commands.md](commands.md#mcp-servers) for the slash commands.
