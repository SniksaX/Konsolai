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
Test DB: `scripts/test-db.sh up|down`. Never touch the developer's own database container; integration tests use `scripts/test-db.sh`.
UI changes are verified on a real PTY (`/dev/ptmx`, `creack/pty`), not a pipe.
Code: strict types; no `interface{}` or `any` unless unavoidable; wrap errors as `pkg: context: %w`; no debug logging; no new packages unless the architect specifies them; no new dependencies unless approved.
Approved in `go.mod`: jackc/pgx/v5, alecthomas/chroma/v2, yuin/goldmark, reeflective/readline, rivo/uniseg, go.yaml.in/yaml/v3, golang.org/x/term, golang.org/x/sys, and test-only creack/pty. Go 1.27.1. Module `github.com/SniksaX/Konsolai`.

## Reference setup (example)

Chat is any OpenAI-compatible server. The built-in model is named `default` at `http://localhost:8000/v1` with an empty id. `thinking_kwargs` sends `chat_template_kwargs.enable_thinking` only when that model's flag is true. Reasoning may arrive in `delta.reasoning`.
Fast is optional. An empty `roles.fast` uses the main model and stays quiet. An answer stream retries on main if the fast call fails.
Embed and rerank are optional URLs on `roles.embed` and `roles.rerank`. When `rag.enabled` is false, Konsolai does not ingest and does not offer the recall tool.
Never touch the developer's own database container. Integration tests use `scripts/test-db.sh`.
Config path is `--config`, then `KONSOLAI_CONFIG`, then `$XDG_CONFIG_HOME/konsolai/config.yaml` or `~/.config/konsolai/config.yaml`. Writes resolve symlinks and land in the real file, mode 0600.
A new setting is ONE entry in `internal/config/keys.go`. Every configuration change goes through `/set`.
Env overrides are the `Env` column of that table, plus `KONSOLAI_PROFILE` and `KONSOLAI_CONFIG`. `--profile` beats `KONSOLAI_PROFILE` beats `default_profile` beats built-in `default`. `--config` beats `KONSOLAI_CONFIG` beats the XDG path.

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

- `ui`: TTY prompt and markdown; color only through theme.
- `banner`: static splash; `Startup` writes the wordmark once.
- `store`: Postgres via pgxpool, raw SQL, `turn_stats`.
- `intent`: four-way router, action floor, language detect.
- `tools`: shell, files, search, plan, recall, `export_doc`, finish.
- `agent`: tool loop and plan gate; never imports `memory` or `store`.
- `memory`: store adapter; the first store failure disables it.
- `rag`: chunk, index, embed, search.
- `welcome`: gather and paint; imports `store` for `WelcomeRow` and `ModelRate`.
- `turn`: `Runner.RunTurn`.
- `commands`: slash registry; never imports `repl`.
- `shellmode`: in-REPL shell.
- `repl`: TTY loop, readline, slash menu, welcome paint.
- `cmd/konsolai`: entrypoint.

`tools`, `agent`, and `ui` never import pgx. `ui` must not import `store`.

The REPL starts only when there is no request and both stdin and stdout are TTYs. Otherwise the run is one-shot and draws no welcome. A pipe with no request is the request. A request plus a pipe attaches stdin as context.
Interactive startup runs `banner.Startup` while `welcome.Gather` probes, then clears that splash and paints the welcome once.
Welcome height is read from the underlying TTY: `termFile` unwraps `nlWriter`.
`turn.Runner.RunTurn` classifies intent, then answer streams. Lookup, act, and build run the agent loop.
The plan gate is always on for build. Act requires a plan only at high effort.

## Config

A new key is ONE entry in `internal/config/keys.go`. That entry supplies the default, validation, docs, sample file, env override, and `/config` output.
Accessors are the only `<= 0` means default logic (`nonzero` and the ratio accessors).
`TestConfigKeysComplete` walks every leaf of `Config` by reflection and fails if a field has no table entry, or a table entry has no field.

## Design decisions that must not regress

- `intent.ActionFloor` plus `RaiseTo` only raise the intent (answer < lookup < act < build). How and why questions are exempt. Escalation runs once.
- Thinking follows effort. Build and act use `agent.build_thinking` (default `plan_step`: think after an approved plan, or after a shell failure or two consecutive failures). Answer thinks only at high effort. Lookup thinks after two consecutive failures.
- The plan gate is always on for build and only at high effort for act. Mutating shell lines must pass `safety.MatchPlan`. Identical rejected tool calls stop at `agent.identical_rejects` (default 2).
- Privileged commands are refused (`sudo`, `doas`, `su`, `pkexec`). The plan tells the user to run them, via `finish`.
- `ResolveRead` allows any user-readable path except the sensitive denylist. Writes stay inside the project root.
- `redact.String` and `redact.JSON` run before a message or action is persisted.
- Prompt size starts as characters times `tokensPerChar`, then that ratio is updated from real `usage.prompt_tokens`. Elision starts above `elide_trigger` (0.60) and stops at `elide_target` (0.40).
- `llm.Normalize` runs before each chat request: merge consecutive user messages, drop empty assistants, drop orphan tool results.
- The fast client is preferred when reachable. If an answer stream fails on it, the turn retries on main. Fast is skipped when the prompt passes `context_guard_ratio`.
- Reply language comes from the user message (`intent.Detect` / `ResolveLanguage`, en and fr), then the previous user line, then a router hint that is in the profile list.
- `export_doc` accepts only `html` and `pdf`. Shell PDF and HTML printers are refused. A docs plan that asks for PDF or HTML must name `export_doc`.
- `memory.Guard` disables itself on the first store error and reports one line. A `turn_stats` insert failure never fails the turn.
- Prompts tell the model to use `search`, `find_files`, and `outline` before `recall` on code.
- Color is theme roles. Submitted lines use `theme.InputEcho`. Raw ANSI stays in `internal/ui/theme` and `_test.go`.
- The slash menu is Konsolai's `slashMenu`. A bare `/name` returns no readline completions. The placeholder is only `SetInlineSuggestion`.

## Tests that define behavior

- `internal/welcome/testdata/*.golden` (150) via `TestRenderGoldens`. `TestFrameBelow120MatchesSplit`. `TestWideSplitKeepsRecentBesideSession`.
- PTY: `internal/repl/readline_pty_test.go` (placeholder not accepted, Ctrl-L, slash menu), `internal/repl/theme_pty_test.go` (Esc leaves the theme, Enter applies it in place), `cmd/konsolai/e2e_slash_pty_test.go`.
- `turn_stats` tests need the test DB (`internal/store/stats_test.go`).
- Plan: `internal/tools/plan_test.go` (command-only plans, `export_doc` for PDF). `internal/agent/agent_test.go` (`TestIdenticalPlanRejectStops`, `TestShellStillRequiresPlan`).
- Config: `TestConfigKeysComplete` in `internal/config/keys_test.go`.

## Current status

Work lands on `dev`. Config keys are the single table in `internal/config/keys.go`. Flat mirrors are removed. Defaults target any OpenAI-compatible server.

- REPL welcome: `split`, `frame`, and `minimal`. Paint once after probes. Height from the wrapped TTY.
- `turn_stats`: best-effort token and timing rows. Welcome rates use `ModelRate`.
- `/theme`: picker. Esc does not change the theme or clear the screen. Enter applies the palette in place and does not redraw the welcome.
- Frame side text removed; theme applies in place; `/set default <name|all>` restores `config.default.yaml`.
- Placeholder: inline suggestion only. Enter does not submit it.
- Footer context meter: last prompt tokens against the window (`ctx_warn_pct`, `ctx_hot_pct`, `ctx_cells`).
- `propose_plan`: a command-only plan may omit `files` when the plan does not write or edit files.
- Config keys are one table in `internal/config/keys.go`. Named models and `roles` replace `llm`/`fast` endpoints. `/set` is the configuration command. `TestConfigKeysComplete` walks every scalar leaf.
- File splits: agent helpers, safety plan rules and segments, welcome paint helpers.
- README at the repo root. Header image `docs/assets/konsolai-header.svg`. No screenshots in the tree. `TestReadmeSlashTable` locks the slash-command table to `commands.Default()`.
- `/stats [today|week|all]` (default week) reads `turn_stats` and sessions. Per model: calls, tokens, average tok/s, and p50/p95 of `model_ms`. Per intent: turns, average steps (mean `turn_stats` rows per session), and average wall time (mean `model_ms`; there is no separate wall-clock column). With memory off: `stats need the database (memory is not configured)`.
- `/doctor` prints one line per check (`✓`, `!`, or `✗`) and a fix for every line that is not `✓`. Exit 1 only when a line is `✗`. Network probes use the welcome probe timeout.
- `/save [file]` writes the last answer as Markdown. `/pdf [file]` renders it through `docrender`, the same pipeline as `export_doc`. The default name is a slug of the session title. Overwrite needs `--force`. Paths go through the Guard. `konsolai new "<request>"` is one turn; `/save` then reads that answer from the session store, or from the local last-session file when the database is off.
- `/explain` runs an answer turn at high effort for the latest failed shell-mode command and does not re-run it. With no capture, a readable `$HISTFILE` is explained after `no captured output; explaining the command only`. The help text documents `PROMPT_COMMAND="history -a"`.
- The first mutating call rejected by the plan gate forces the next request to `propose_plan` (thinking follows `plan_step`) and does not spend the explore budget. Later gate rejects are unchanged. `run_shell` still needs an accepted plan.
- `scripts/changelog.sh` groups commits after the latest `v*` tag. With no `v*` tag it uses `prod`, or else the root commit and the whole history. `CHANGELOG.md` is titled `## v0.2.0 (unreleased)`. The tag is not created.
- Secret-shaped test literals are assembled at runtime in each package's `fixtures_test.go`.

## Known issues and backlog

- Task X animations: not done. The banner is static (`banner.Startup` writes the wordmark once). Do not implement animations unless a task says so.
- `Run`'s shared locals: `confirm.go` was not created. The confirm flow, plan-gate loop, and explore-budget counter live inside `agent.Run` and share locals (`planApproved`, `planFiles`, `planCmds`, `planText`, `explore`, `prepRejectKey`, `prepRejects`, `seenFail`, `approvedAll`, `shellFail`, `consecFail`, `planRejected`, `postPlanReads`, `docsExportNoted`, `docsExportWait`, `forceMutating`, `forceExport`, `forcePropose`, `sawGateReject`). This needs a state-struct refactor. Do not split `Run` by changing control flow.
- Placeholder color is fixed by readline: `github.com/reeflective/readline@v1.3.0/internal/display/refresh.go` lines 277 and 282 paint `color.Dim` plus Fg 242. The public API is only `SetInlineSuggestion(string)`. Do not fork the library.
- CPU embeddings can make `/index` slow when an embed service is configured.
- A configured reranker is often unreachable.
- Symbol index: not built.
- README screenshots are not in the tree: `welcome.png`, `slash-menu.png`, `theme.png`, `build.png`, `act-plan.png`, `pdf.png`.
- Tag `v0.2.0`. `version` in `cmd/konsolai` is still `"dev"` unless ldflags stamp it.

## Lessons

- Verify UI in a real PTY.
- Measure with `--trace` before optimizing.
- Never trust the 3B router for safety decisions.
- Compare timings over several runs (shared server).
