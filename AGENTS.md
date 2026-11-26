# AGENTS.md

## What Konsolai is

Konsolai is a local coding agent. No request, with stdin and stdout both TTYs, starts the REPL. Every other case is one-shot and skips the welcome.
Intents are answer (stream, no tools), lookup (read-only tools), act (all tools), and build (plan gate, thinking, temperature 0.3).
Chat models are OpenAI-compatible. Memory is optional Postgres and stays off when `database.url` is empty.

## How work is organized

The architect writes lettered tasks. The IDE agent implements. The user tests and merges to prod.
Commits go to `dev` only. Never commit to `prod`. One commit per file. MOVE exception: a split of one file into siblings may be one commit so every commit builds.
Message: `[TYPE|SIZE] Title` in imperative mood, title max 72 characters. TYPE is ADD, UPDATE, FIX, DELETE, MOVE, or REFACTOR. SIZE is MINOR (under 20 lines), MEDIUM (20–100), or MAJOR (over 100, or a public API or behavior change). Keep the body short. Use `git commit -F`.
After each phase: `gofmt -l .`, `go vet ./...`, `go test ./... -count=1` (with the test DB when store is involved), `go build -o ~/.local/bin/konsolai ./cmd/konsolai`.
Test DB: `scripts/test-db.sh up|down`. Never touch the developer's own database container; integration tests use `scripts/test-db.sh`. Compose uses its own volume and published port and never the developer's database.
UI changes are verified on a real PTY (`/dev/ptmx`, `creack/pty`), not a pipe.
Code: strict types; no `interface{}` or `any` unless unavoidable; wrap errors as `pkg: context: %w`; no debug logging; no new packages unless the architect specifies them; no new dependencies unless approved.
Approved in `go.mod`: jackc/pgx/v5, alecthomas/chroma/v2, yuin/goldmark, reeflective/readline, rivo/uniseg, go.yaml.in/yaml/v3, golang.org/x/term, golang.org/x/sys, and creack/pty (shellmode and the PTY tests). Go 1.27.1. Module `github.com/SniksaX/Konsolai`.

## Reference setup (example)

Chat is any OpenAI-compatible server. The built-in model is named `default` at `http://localhost:8000/v1` with an empty id. `thinking_kwargs` sends `chat_template_kwargs.enable_thinking` only when that model's flag is true. Reasoning may arrive in `delta.reasoning`.
Fast is optional. An empty `roles.fast` uses the main model and stays quiet. An answer stream retries on main if the fast call fails.
Embed and rerank are optional URLs on `roles.embed` and `roles.rerank`. When `rag.enabled` is false, Konsolai does not ingest and does not offer the recall tool.
Never touch the developer's own database container. Integration tests use `scripts/test-db.sh`. Compose uses its own volume and published port and never the developer's database.
Config path is `--config`, then `KONSOLAI_CONFIG`, then `$XDG_CONFIG_HOME/konsolai/config.yaml` or `~/.config/konsolai/config.yaml`. Writes resolve symlinks and land in the real file, mode 0600.
A new setting is ONE entry in `internal/config/keys.go`. Every configuration change goes through `/set`.
Prefer `config.yaml`. Env vars are for containers and CI. Overrides are the `Env` column of that table, plus `KONSOLAI_PROFILE` and `KONSOLAI_CONFIG`. `--profile` beats `KONSOLAI_PROFILE` beats `default_profile` beats built-in `default`. `--config` beats `KONSOLAI_CONFIG` beats the XDG path.

## Architecture

Leaves (no `konsolai/internal` imports):

- `ui/theme`: palette roles, ANSI, cursor color, `InputEcho`.
- `config`: YAML profiles, `Builtin`, env overrides, `Validate`.
- `llm`: OpenAI-compatible chat, tools, streaming, token ratio.
- `safety`: path and command guard, `MatchPlan`.
- `redact`: mask secrets before persist and display.
- `repo`: root, file listing, project detect.
- `sysctx`: environment snapshot for the prompt.
- `diff`: Myers unified diffs.
- `docrender`: markdown, mermaid, HTML, PDF.
- `embed`: `POST /embed` and rerank.
- `clip`: wl-copy, xclip, xsel, OSC 52.
- `mcp`: lazy MCP session manager, stdio process groups, guarded HTTP, env and header expansion.

- `ui`: TTY prompt and markdown; color only through theme.
- `banner`: static splash; `Startup` writes the wordmark once.
- `store`: Postgres via pgxpool, raw SQL, `turn_stats`.
- `intent`: four-way router, action floor, language detect, one safe line per MCP server.
- `tools`: shell, files, search, plan, recall, web fetch, remember, outline, `export_doc`, finish, the MCP bridge.
- `agent`: tool loop and plan gate, MCP tools in plans and per-call stats; never imports `memory` or `store`.
- `memory`: store adapter; the first store failure disables it.
- `rag`: chunk, index, embed, search.
- `welcome`: gather and paint; imports `store` for `WelcomeRow` and `ModelRate`.
- `turn`: `Runner.RunTurn`, the MCP runtime and its private-server gate.
- `commands`: slash registry; never imports `repl`.
- `shellmode`: in-REPL shell.
- `repl`: TTY loop, readline, slash menu, welcome paint.
- `cmd/konsolai`: entrypoint; owns the MCP manager lifecycle and the `/status` adapter.

`tools`, `agent`, and `ui` never import pgx. `ui` must not import `store`.

The REPL starts only when there is no request and both stdin and stdout are TTYs. Otherwise the run is one-shot and draws no welcome. A pipe with no request is the request. A request plus a pipe attaches stdin as context.
Interactive startup runs `banner.Startup` while `welcome.Gather` probes, then clears that splash and paints the welcome once.
Welcome height is read from the underlying TTY: `termFile` unwraps `nlWriter`.
`turn.Runner.RunTurn` classifies intent, then answer streams. Lookup, act, and build run the agent loop.
The plan gate is on for build and for act at high effort by default; `agent.plan.*` can change that.

## Config

Configuration lives in `config.yaml`. `config.example.yaml` is generated documentation. The repo has no other config files. `/set default` and `/set default all` write `InitYAML()` from the key table.
A new key is ONE entry in `internal/config/keys.go`. That entry supplies the default, validation, docs, the example file, the env override, and `/config` output.
Accessors are the only `<= 0` means default logic (`nonzero` and the ratio accessors).
`TestConfigKeysComplete` walks every leaf of `Config` by reflection and fails if a field has no table entry, or a table entry has no field.

## Design decisions that must not regress

- `intent.ActionFloor` plus `RaiseTo` only raise the intent (answer < lookup < act < build). How and why questions are exempt. Escalation runs once.
- Thinking follows effort. Build and act use `agent.build_thinking` (default `plan_step`: think after an approved plan, or after a shell failure or two consecutive failures). Answer thinks only at high effort. Lookup thinks after two consecutive failures.
- The plan gate is on for build and at high effort for act by default (`agent.plan.*` can change it). Mutating shell lines must pass `safety.MatchPlan`. Identical rejected tool calls stop at `agent.identical_rejects` (default 2).
- Exit codes: 0 finished, 1 error, 2 usage, 3 aborted at a prompt, 4 max steps, 5 rejected (identical rejected calls reached the limit: `agent.ErrRejected`, one stderr line, session `failed`), 130 interrupted. `turn.SessionStatus` maps 0 to `finished`, 3 and 130 to `aborted`, the rest to `failed`. A stuck turn never exits 0.
- Privileged commands are refused (`sudo`, `doas`, `su`, `pkexec`). The plan tells the user to run them, via `finish`.
- `ResolveRead` allows any user-readable path except the sensitive denylist. Writes stay inside the project root.
- An approved plan grants the planned file and its immediate parent as extra write roots; nothing above the parent is granted.
- Write roots named by the request are shown at the forced confirmation before a write outside the project root.
- `redact.String` and `redact.JSON` run before a message or action is persisted.
- The shell child gets no `KONSOLAI_*` or secret-shaped environment, and captured shell output is redacted before the model sees it. A read-only shell path is checked against the sensitive denylist, globs included.
- `run_shell` is not a sandbox: substitutions can reach anything the user can. Literal redirects inside an interpreter command are checked against the write roots; the plan gate and the confirm prompt are the write barrier.
- Prompt size starts as characters times `tokensPerChar`, then that ratio is updated from real `usage.prompt_tokens`. Elision starts above `elide_trigger` (0.60) and stops at `elide_target` (0.40).
- `llm.Normalize` runs before each chat request: merge consecutive user messages, drop empty assistants, drop orphan tool results.
- The fast client is the first router when reachable. It answers only greetings and effort-`none` answers whose full prompt is under `router.answer_main_above_tokens` (default 200), and it is skipped when the prompt passes `context_guard_ratio`; every other answer streams on main. If the fast stream fails, the turn retries on main.
- Reply language comes from the user message (`intent.Detect` / `ResolveLanguage`, en and fr), then the previous user line, then a router hint that is in the profile list.
- `export_doc` accepts only `html` and `pdf`. Shell PDF and HTML printers are refused. A docs plan that asks for PDF or HTML must name `export_doc`.
- Tests that need external binaries skip when they are missing; CI installs them.
- `memory.Guard` disables itself on the first store error and reports one line. A `turn_stats` insert failure never fails the turn.
- The first embedding of a process is compared with `memory_chunks`. A mismatch disables RAG for that process, prints one line, and does not insert. Turns still finish. `/set rag reset-dim` drops and recreates `memory_chunks` and `memory_files` at the current model dimension after the single-key confirm (`--yes` skips). `/status` shows `embed dims n (index m)` when both are known.
- Prompts tell the model to use `search`, `find_files`, and `outline` before `recall` on code.
- Color is theme roles. Submitted lines use `theme.InputEcho`. Raw ANSI stays in `internal/ui/theme` and `_test.go`.
- The slash menu is Konsolai's `slashMenu`. A bare `/name` returns no readline completions. The placeholder is only `SetInlineSuggestion`.

## Tests that define behavior

- `internal/welcome/testdata/*.golden` (180) via `TestRenderGoldens`. `TestFrameBelow120MatchesSplit`. `TestWideSplitKeepsRecentBesideSession`.
- PTY: `internal/repl/readline_pty_test.go` (placeholder not accepted, Ctrl-L, slash menu), `internal/repl/theme_pty_test.go` (Esc leaves the theme, Enter applies it in place), `cmd/konsolai/e2e_slash_pty_test.go`.
- `turn_stats` tests need the test DB (`internal/store/stats_test.go`).
- Plan: `internal/tools/plan_test.go` (command-only plans, `export_doc` for PDF). `internal/agent/agent_test.go` (`TestIdenticalPlanRejectStops`, `TestShellStillRequiresPlan`). Exit codes: `TestRunTurnRepeatedRejectExit`, `TestRunTurnMaxStepsExit`, `TestRunTurnInterruptExit`, `TestSessionStatusCodes` in `internal/turn/turn_test.go`.
- Config: `TestConfigKeysComplete` in `internal/config/keys_test.go`.
- MCP: `internal/mcp` (manager lifecycle, transports, results, env/header rules, crash and timeout), `internal/tools/mcp_test.go` (bridge, trust, plan tools, find meta-tool), `internal/turn/mcp_*_test.go` (runtime, private gate, router lines), `internal/agent/mcp_*_test.go` (call count, plan, display), `internal/commands/*_mcp_test.go` (list, set, inspect, status, doctor), `internal/repl/mcp_welcome_test.go`, the welcome MCP count tests, and the leak surfaces (`mcp_leak_test.go` in `internal/mcp`, `internal/tools`, `internal/turn`, `internal/commands` and `internal/config`).

## Current status

Work lands on `dev`. Config keys are the single table in `internal/config/keys.go`. Flat mirrors are removed. Defaults target any OpenAI-compatible server.

- REPL welcome: `split`, `frame`, and `minimal`. Paint once after probes. Height from the wrapped TTY.
- `turn_stats`: best-effort token and timing rows. Welcome rates use `ModelRate`.
- `/theme`: picker. Esc does not change the theme or clear the screen. Enter applies the palette in place and does not redraw the welcome.
- Frame side text removed; theme applies in place. `/set default <name>` restores one key. `/set default all` asks y/n first (`--yes` skips), reports how many keys differ and that models, roles, and endpoints reset, backs up the real file as `config.yaml.bak-<timestamp>` (mode 0600, five kept), then writes the factory document from `InitYAML()`. Decline leaves the file. The symlink stays.
- Docker: `Dockerfile` targets `base` and `full` (Debian Chromium via `KONSOLAI_CHROME`). The `konsolai` service runs as the host uid (`KONSOLAI_UID` / `KONSOLAI_GID`). `compose.yaml` project `konsolai` starts only the database on `docker compose up -d`. Every variable has a default (password `konsolai`, port `55434`) and there is no env file. `KONSOLAI_DATABASE_URL` in the compose file points at host `db` and overrides the mounted file. The user's `config.yaml` keeps models and roles. Own volume and port, never the developer's database. `scripts/docker.sh` builds, runs the database, and runs the CLI. Inside the container the model server is `http://host.docker.internal:<port>/v1`. The optional embed service defaults to `BAAI/bge-m3` (1024-wide). `BAAI/bge-base-en-v1.5` is a commented 768-wide alternative.
- CI: `.github/workflows/ci.yml` on push and pull request to `dev` and `prod` runs gofmt, vet, and `go test` against pgvector, and builds the base image with no push. Tests that need external binaries skip when they are missing; CI installs poppler-utils and sets `KONSOLAI_CHROME` when Chrome is already on the runner. `.github/workflows/release.yml` runs that test job before it publishes on `v*` tags: `ghcr.io/sniksax/konsolai:<tag>`, `:latest`, and `:<tag>-full`, plus linux amd64 and arm64 binaries. The token is `GITHUB_TOKEN`.
- Placeholder: inline suggestion only. Enter does not submit it.
- Footer context meter: last prompt tokens against the window (`ctx_warn_pct`, `ctx_hot_pct`, `ctx_cells`).
- `propose_plan`: a command-only plan may omit `files` when the plan does not write or edit files.
- Config keys are one table in `internal/config/keys.go`. Named models and `roles` replace `llm`/`fast` endpoints. `/set` is the configuration command. `TestConfigKeysComplete` walks every scalar leaf.
- File splits: agent helpers, safety plan rules and segments, welcome paint helpers.
- README at the repo root. Header image `docs/assets/konsolai-header.svg`. Screenshots live in `docs/assets/screens/`: `welcome.png`, `build.png`, `commands.png`, `themes.png`, and README embeds those four. `TestDocsSlashTable` in `internal/commands` locks the slash-command table in `docs/commands.md` to `commands.Default()`.
- `/stats [today|week|all]` (default week) reads `turn_stats` and sessions. Per model: calls, tokens, average tok/s, and p50/p95 of `model_ms`. Per intent: turns, average steps (mean `turn_stats` rows per session), and average wall time (mean `model_ms`; there is no separate wall-clock column). With memory off: `stats need the database (memory is not configured)`.
- `/doctor` prints one line per check (`✓`, `!`, or `✗`) and a fix for every line that is not `✓`. Exit 1 only when a line is `✗`. Network probes use the welcome probe timeout. Embed dimensions are ok, the mismatch line, or `not configured`.
- `/save [file]` writes the last answer as Markdown. `/pdf [file]` renders it through `docrender`, the same pipeline as `export_doc`. The default name is a slug of the session title. Overwrite needs `--force`. Paths go through the Guard. `konsolai new "<request>"` is one turn; `/save` then reads that answer from the session store, or from the local last-session file when the database is off.
- `/explain` runs an answer turn at high effort for the latest failed shell-mode command and does not re-run it. With no capture, a readable `$HISTFILE` is explained after `no captured output; explaining the command only`. The help text documents `PROMPT_COMMAND="history -a"`.
- The first mutating call rejected by the plan gate forces the next request to `propose_plan` (thinking follows `plan_step`) and does not spend the explore budget. Later gate rejects are unchanged. `run_shell` still needs an accepted plan.
- `scripts/changelog.sh` groups commits after the latest `v*` tag. With no `v*` tag it uses `prod`, or else the root commit and the whole history. `CHANGELOG.md` is titled `## v0.4.0` (regenerated from the `v0.3.0` tag). The next tag is not created.
- Secret-shaped test literals are assembled at runtime in each package's `fixtures_test.go`.
- `TestNoLocalMachine` in `internal/commands` and the `machine_name` filter in `scripts/changelog.sh` deny machine and personal names only: host, user, home paths, and local ports. Public model ids are not in that list.

## Known issues and backlog

- Task X animations: not done. The banner is static (`banner.Startup` writes the wordmark once). Do not implement animations unless a task says so.
- `agent.Run` keeps its control flow; the shared flags, counters and slices live in `runState`. Do not split `Run` by changing control flow.
- Read dedupe (v0.5.0): within one turn an identical `read_file` path and range returns a short `already read and unchanged` note, not the content; a different range is deduped only when the shown lines cover it; a read capped by the 2000-line or 32 KiB limit records only the lines it showed, so a later read of the unseen tail still returns content. The guard is per turn. Three reads of a 29 KiB file measured 65,390 prompt tokens before and 38,972 after dedupe, 26,418 saved (40.4%) at 0.30 tokens/char (`TestReadDedupeTokenSaving`). Config read_file masking and the secrets refusal are unchanged.
- Prompt weight (v0.5.0): measured at the default 0.30 tokens/char with a fixed snapshot and facts sample: answer 524 tokens (prompt 410, snapshot 44, facts 60, no tools); lookup 1905 (prompt 856, tools 935 for 8 tools); act 3269 (prompt 1175, tools 1980 for 14 tools); build 3086 (prompt 992, tools 1980); act plus six MCP tool schemas 3591 (MCP 322). Lookup narrowing saves 53% of the full tool set and the plan-gated first step saves 47%; nothing else is clearly unneeded (each remaining block is read by at least one intent), so no further cut was made. Numbers are pinned by `TestPromptOverheadReport` and `TestPromptOverheadExistingCuts` in `internal/agent`.
- Placeholder color is fixed by readline: `github.com/reeflective/readline@v1.3.0/internal/display/refresh.go` lines 277 and 282 paint `color.Dim` plus Fg 242. The public API is only `SetInlineSuggestion(string)`. Do not fork the library.
- CPU embeddings can make `/index` slow when an embed service is configured.
- A configured reranker is often unreachable.
- Symbol index: not built.
- Remote models: the y/n gate decision and the token total are per process. `privacy.session_token_cap` counts only the usage a remote call reports, so a request that fails before reporting adds nothing. Remote classification resolves each endpoint host once and caches it; an unresolvable host counts as remote.
- A probe only reports what the endpoint answered: 401/403 is `auth failed`, its deadline is `slow`, another non-2xx is `http N`, and only a failed connection is `unreachable`. Only a failed chat call may say the server cannot be reached. An empty id needs exactly one served model and a configured id must be served, so a resolution failure stops the turn before any chat call.
- The welcome drops the `remote` marker when the models box has no room for it.
- The footer names the model that served the last call (resolved id and role) and hides the `remote` word in shell mode, with a mode word, and when the footer drops the model word (under 60 columns or when the row does not fit). `/set model` and `/status` always mark a remote endpoint.
- `router.answer_main_above_tokens` (default 200) compares the full answer-prompt estimate on the fast client (system context, history and question), not the question text, so a long history sends a short question to main. Greetings bypass it and `0` disables the token gate.
- The identity floor matches EN and FR phrases only; other identity or setup questions reach the model, which is told to answer from the model facts and to say "I don't know" when a fact is missing.
- `welcome.Probe` keeps a `models` branch that nothing calls; welcome model probes go through `llm.Client`, and only embed and rerank still use `welcome.Probe`.
- Remote cost needs `price_in` or `price_out` on a remote model (no price fetch) and shows on the end-of-turn `ui.stats` line and the trace `stats:` line; a local model and an unpriced remote model show none. Fast pricing is unit-tested, but no full fast answer exercises the cost line.
- The DeepSeek and OpenAI presets were exercised against live accounts; the other presets come from published docs and are not exercised here. `anthropic`'s OpenAI-compatible auth header stays unverified, and `deepseek` sends no temperature by default.
- Embeddings stay loopback or private; a remote embed service is rejected, so RAG cannot use a cloud provider.
- README embeds four screenshots. `slash-menu`, `act-plan`, and `pdf` are not captured.
- Latest tag `v0.4.0`; `dev` is past it. `version` in `cmd/konsolai` is still `"dev"` unless ldflags stamp it.
- MCP (v0.5.0): servers under `mcp.servers` (stdio child or streamable HTTP), `mcp.max_inline_tools` 15. `/set mcp` is the only writer (`add|remove|enable|disable|trust|allow|deny|header|env`) and `/mcp` lists one row per server. `internal/mcp` owns the lazy session manager: one timeout per connect/list/call, a 4-call concurrency cap, an 8 MiB raw result cap, a process-group kill for stdio, and a guarded HTTP client that sends the key and headers on every request and refuses a cross-host or https-to-http redirect. `tools.MCPBridge` exposes `server__tool`, filters allow/deny, classifies read-only from `tools.read_only` globs plus (only for `read_only`/`auto` trust) the server's `readOnlyHint`, wraps results as untrusted data, masks secret-shaped text in results, descriptions and input schemas, and above the inline cap offers `mcp_find_tool`, which registers the tools it finds so they are callable in the same turn. `turn.MCPRuntime` maps the profile, hides private servers under a remote main unless confirmed, and exposes tools per intent (answer none, lookup read-only, act/build allowed). Plans list MCP tools in `tools` (`server__tool` or `server:tool`), and a call outside the list is outside the plan; a tool found through `mcp_find_tool` follows the same plan rule. `/mcp` lists one row per server without connecting it (known state plus a cached tool count), `/mcp tools <name>` classifies each tool and shows what the next turn would offer now, and `/mcp test <name>` connects once and reports the latency and the tool count. `Stats.MCPCalls` reaches the `--trace` line as `mcp=N`; `/status` and `/doctor` add per-server rows; the welcome shows `mcp N configured` until a server connects and then `U/T up`, via the non-blocking `Known`; the intent router gets one safe line per server. The bridge has no configuration path: no tool can add, edit, enable, trust or remove a server. Docs: `docs/mcp.md`.
- MCP: trust and private are caller-side policies. `internal/mcp` carries the labels but does not enforce them, so a direct manager caller bypasses them; `enabled` is enforced by the manager (a disabled server is never connected). The stdio process-group kill is Unix-only. The HTTP redirect policy (cross-host and https-to-http refused) has no live test, and no MCP behavior is verified against a real server: the tests use fake stdio and HTTP servers. `/mcp list` never connects (known state plus a cached tool count); `/mcp tools`, `/mcp test`, `/status` and `/doctor` connect a server (up to its timeout) to show live state, so many or unreachable servers make those commands slow. The welcome never connects.
- MCP trust `auto` only trusts the server's `readOnlyHint`; a mutating MCP call still goes through the plan and approval gate, so `/set mcp trust auto` is not auto-approval.
- MCP private-server confirmation reads one y/n line from stdin (the runtime remembers it, `--yes` skips it and a non-interactive run refuses it); it is not routed through the REPL editor.
- Above `mcp.max_inline_tools` a turn exposes `mcp_find_tool`; a found `server__tool` is registered between steps and is callable in the same turn, with its schema presented on the next request. The plan gate still applies (declared in the plan is accepted, outside the plan asks again), `read_only` trust hides mutating found tools, and a name the server does not serve stays refused. Discovery grows the exposed set within the turn, so a very large catalog still costs one schema per found tool.
- MCP rules (trust, allow, deny, enabled, private, inline cap) are re-read for every request, and the manager keeps its session and cached tool list. The manager's per-server spec (whether the server was enabled at start, command, URL, timeout) is fixed at construction, so enabling a server that started disabled, or changing its command, URL or timeout, needs a restart.
- `/mcp tools <name>` checks the inline cap against the inspected server's allowed tools only, so with several servers the `offered` column can read `yes` while the real turn uses the find meta-tool. The column also ignores the plan phase: before a required plan is approved the turn offers only read-only tools and `propose_plan` (plus `mcp_find_tool` above the cap), so direct MCP tools are offered after approval.
- Config protection: `write_file`, `edit_file` and `export_doc` resolve the real path (symlinks included) and refuse the active config, its backups and the secrets file with the `/set` pointer; `read_file` masks the active config/backups and refuses secrets; `search` matches the masked view of the active config and its backups and skips the secrets file with a marker; `run_shell` refuses a command whose text mentions the protected names, covering redirection, `tee`, `sed -i`, `mv`, `cp`, `truncate` and interpreter writes. Limit: the shell check is textual, so a name assembled without the literal strings is not caught; the file tools and `search` classify the resolved path and are not affected.

## Lessons

- Verify UI in a real PTY.
- Measure with `--trace` before optimizing.
- Never trust the 3B router for safety decisions.
- Compare timings over several runs (shared server).
