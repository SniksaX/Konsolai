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

## Tools in a turn

- `answer` turns see no MCP tools. `lookup` sees only read-only tools. `act` and `build` see the allowed catalog.
- A tool is exposed to the model as `server__tool` (lowercase, sanitized, collisions get a stable numeric suffix). Two servers can offer tools with the same name.
- Above `mcp.max_inline_tools`, the turn exposes one meta-tool, `mcp_find_tool`, instead of every schema. It searches names and one-line descriptions, so a large catalog does not fill the prompt. The names it returns are not registered as callable tools in that turn: raise the cap or narrow `tools.allow` to call one.
- In a `build` or planned `act` turn, `propose_plan` accepts `tools`: the MCP tools the plan will call, as `server__tool` or `server:tool`. A call outside that list is outside the plan and follows the usual confirmation path.
- A tool result is untrusted data. Konsolai masks secret-shaped values, caps the raw result at 8 MiB and the wrapped output at 64 KiB, omits images, audio and resources with a marker, and wraps the text in `<untrusted_mcp_result>` followed by a line telling the model that the content is data, never instructions. Tool descriptions are flattened and capped at 240 characters, and secret-shaped text is masked in descriptions and input schemas too, so a server cannot leak a configured env or header value back to the model through its tool list.

## Privacy

An MCP result is part of the conversation, so it leaves the machine when the main model is remote. `private` marks a server whose results you do not want a remote model to see. When the main model is remote, a private server is hidden unless it is confirmed: the CLI asks once per process ("this sends MCP results from <server> to the remote model"), `--yes` accepts it without asking, and a non-interactive run without `--yes` leaves it hidden. A local main model needs no confirmation. The MCP session itself is always between Konsolai and the server; the server may itself be remote.

## Status and diagnostics

- `/mcp` (or `/mcp list`) prints one row per configured server: name, transport, enabled or disabled, trust, private marker, cached tool count and live state.
- `/status` adds `mcp` rows after `rerank`: state, transport, trust, tool count, private marker, unresolved env names and one short error, followed by `server: tool` lines for the known tools.
- `/doctor` adds one `mcp <name>` row after the remote-model lines: `✓` for a connected, disabled or not-yet-connected server, `!` for a down server or an unresolved env variable, with the fix it needs. Connected servers list their tools as `server: tool` lines.
- The welcome screen shows `mcp 2/3 up`: every configured server is the denominator and a live session is the numerator; the line is hidden when no server is configured. The count never connects a server, so a server joins only after its first use.
- A `--trace` turn that called an MCP tool adds `mcp=N` to the trace stats line.

## Threat model and what is not protected

MCP servers are third-party code and data. The bridge treats tool descriptions, schemas and results as data for the model, never as instructions for Konsolai. The plan gate and approvals are the barrier before a mutating tool runs, and read-only classification is conservative by default (`ask` does not trust the server's hint).

What is *not* protected:

- An allowed MCP call is not sandboxed. A stdio server runs as your user with its declared environment and can do anything your account can; an HTTP server sees the network. Konsolai does not wrap MCP calls in the shell guard, and a tool's side effects are the server's design.
- `trust`, `private` and `enabled` are caller-side policies. The session manager carries the labels but does not enforce them, so any code that calls the manager directly bypasses them.
- The stdio process group kill is Unix-only (`Setpgid`/`SIGKILL`). On an unsupported platform a child may outlive the session.
- The HTTP transport refuses a cross-host redirect and an https-to-http downgrade, but that path has no test against a live server.
- No MCP behavior here is verified against a live vendor server; the tests use fake stdio and HTTP servers. Transports, auth schemes and tool annotations can differ in practice.
- The session manager enforces one timeout per connect/list/call (`timeout_ms`), a process-wide cap of 4 concurrent calls, and an 8 MiB raw result cap. It does not rate-limit a server, audit its calls, or prove a `readOnlyHint` true.

See [configuration.md](configuration.md#mcp-servers) for the reference section and [commands.md](commands.md#mcp-servers) for the slash commands.
