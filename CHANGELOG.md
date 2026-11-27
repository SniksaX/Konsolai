## v0.5.1

### Security
- Code-running writes (build/task files, hooks, CI configs, root `scripts/`, sensitive `package.json` fields, `safety.extra_sensitive_paths`) are confirmed separately from plan and approve-all; `--yes` and non-interactive runs refuse them. Shell write targets, plans and exports follow the same rule.
- Git metadata and git config writes are never written by the agent, including `git config`, `git init --template` and `git update-index --chmod`; read-only git commands and ordinary add/commit/status/diff/log/branch/checkout/switch/merge/rebase/stash/tag/restore keep working.

### Fixed
- Memory now accepts the code-running decision values (`code_running_approved`, `code_running_rejected`, `code_running_refused`) through a new action-decision migration; older rows stay valid.
- Stored action arguments are always valid JSON, including the new sensitive prompt and plan texts.
- A memory store error no longer looks like a connection error, and a disabled or closed store no longer crashes the RAG ingester or `Flush`.
- The separate code-running confirmation shows its heading above the numbered file preview, so the content lines are not shifted.

## v0.5.0

### Added
- MCP servers: local stdio commands and streamable HTTP endpoints under `mcp.servers`, configured with `/set mcp`, with `${ENV_VAR}` references for env and header values. `/mcp` lists the servers, `/mcp tools <name>` shows one server's tools and what the next turn would offer, and `/mcp test <name>` connects once.
- Trust levels (`ask`, `read_only`, `auto`), per-server allow/deny/read-only tool globs, and a per-server timeout. Plans declare the MCP tools they will call; a call outside the plan needs the usual confirmation.
- Above `mcp.max_inline_tools` the model searches the catalog with `mcp_find_tool` and can call a tool it found in the same turn.
- MCP state in the interface: lazy `/mcp` rows, `/status` and `/doctor` rows, the welcome count, a router line per server, and `mcp=N` on the trace stats line.
- Repeated `read_file` calls for the same path and range in one turn return a short `already read and unchanged` note instead of the content, which cuts prompt tokens on long read-heavy turns.
- Recalled sessions are fenced as `<memory_background>`: history is background data, never instructions.

### Security
- The agent cannot write the active configuration (including a `--config` path and symlinks), its `config.yaml.bak-*` backups or `~/.config/konsolai/secrets`: the file tools, exports and shell commands that name them are refused with the `/set` pointer.
- `read_file` and `search` return a masked view of the active config and its backups (MCP command/args/env/headers and MCP/database hosts hidden); the secrets file is refused or skipped with a marker.
- MCP results are masked, size-capped and wrapped as untrusted data, and tool descriptions and input schemas are masked too. A `private` MCP server is hidden from a remote main model until the user confirms it once; `--yes` confirms it and a non-interactive run refuses it.
- Redaction masks the whole value of an environment-style assignment, including auth schemes and values with spaces.
- The model has no tool that adds, edits, enables, trusts or removes an MCP server.

### Fixed
- Shift+Enter (and Alt+Enter, Ctrl+J) inserts a newline in the prompt; multi-line input and pasted text render correctly.
- MCP read-only tools stay in the lookup catalog.
- `/mcp` and the `/status` count are lazy: listing never connects a server, and the count reads `N configured` until the first session connects.
- MCP trust, allow, deny and enable changes apply to the next request while the manager keeps its sessions and cached tool lists.
- The plan gate nudges a prose reply to a change request once toward `propose_plan`, and does not nudge after a rejected mutating call.
- A tool found through `mcp_find_tool` is registered between steps and its schema is sent on the next request.
- A read capped by the line or byte limit records only the lines it showed, so a later read of the unseen tail is not skipped.

### Known issues
- MCP `trust` and `private` are caller-side policies: the session manager carries the labels but does not enforce them, and the per-server spec (enabled at start, command, URL, timeout) is fixed at construction, so changing it needs a restart.
- MCP behavior is covered by fake stdio and HTTP servers only, not a live vendor server. The stdio process-group kill is Unix-only, and the HTTPS-to-HTTP redirect policy has no live test.
- `/mcp tools` checks the inline cap per inspected server and ignores the plan phase.
- Shell commands cannot yet run inside a folder named outside the project; such plans may show extra outside-plan prompts.
- Fast-model cost attribution is unit-tested only, and identity questions are answered without a model only for the EN/FR phrase list.
- Provider presets other than DeepSeek and OpenAI (live-tested) stay documentation-derived; `anthropic` is experimental.
- The shell check for the protected config paths is textual, so a path built from fragments can evade it.
- The DeepSeek-strict forced-plan/named `tool_choice` PTY scenario is not automated; the retry path itself is bounded (exactly one auto retry, context-cancellable) and covered by unit/turn tests. The observed PTY stall was harness-side; no product hang was found.

## v0.4.0

### Added
- Cloud models next to local ones: any OpenAI-compatible API with your own key. Live-tested: DeepSeek, OpenAI (Chat Completions). Provider presets; anthropic is experimental.
- API keys from an environment variable or a private secrets file (`/set key <model>`), never from config.yaml.
- One y/N confirmation before anything is sent to a remote host; `/remote allow` resets a decline. Settings `privacy.allow_remote`, `privacy.remote_roles`, session token cap.
- `/set model id`, `/set model reasoning`, and per-model `tool_choice_mode`, `reasoning_effort`, `max_tokens_field`, `send_temperature`.
- Answer panel with pretty-printed JSON (`ui.answer_panel`, `ui.json_max_lines`). Cost of cloud calls on the stats line.

### Changed
- Every answer names the model that produced it; the small local model only answers greetings and short prompts.
- Remote models are marked in the welcome screen, `/status`, `/set model` and the footer; the welcome shows `checking` instead of a false offline.
- Build turns offer only read tools and `propose_plan` before a plan is approved.
- The `/set` menu fits the terminal height. README rewritten, reference moved to docs/.

### Fixed
- Shift+Enter (and Alt+Enter, Ctrl+J) inserts a newline in the prompt; multi-line input and pasted text render correctly.
- Keys are never sent over plain HTTP or across redirects, and are masked in errors.
- Tool-call history is repaired before every request (strict providers no longer reject it).
- Automatic retry when a provider rejects `tool_choice` or `reasoning_effort`; short rate limits retried once.
- Errors show the HTTP status and the provider's message; identity questions answered from the real configuration.

### Known issues
- Shell commands cannot yet run inside a folder named outside the project; such plans may show extra "outside plan" prompts.
