# Konsolai

<p align="center">
  <img src="docs/assets/konsolai-header.svg" alt="Konsolai" width="100%">
</p>

<p align="center">
  <img src="docs/assets/screens/welcome.png" alt="Konsolai welcome screen and answer" width="100%">
</p>

<p align="center">
  <img src="docs/assets/screens/build.png" alt="A build turn: plan, approval, steps, recovery" width="100%">
</p>

<p align="center">
  <img src="docs/assets/screens/commands.png" alt="Slash command palette" width="49%">
  <img src="docs/assets/screens/themes.png" alt="Theme picker" width="49%">
</p>

Local coding agent. With no request, and with both stdin and stdout on a TTY, it starts a REPL. Every other invocation is one-shot and skips the welcome.

## What it does

The REPL paints a welcome once, after startup probes. Layouts are `split`, `frame`, and `minimal`.

A request is classified into one intent. `--intent answer|lookup|act|build` skips the router. These examples are the kind of request each intent is for:

- **answer** streams a reply and calls no tools. `konsolai "what is a goroutine"`
- **lookup** uses read-only tools (list a directory, read a file, outline, search, find files, fetch a URL, recall, a read-only shell). `konsolai "where is MatchPlan defined"`
- **act** may change the machine: create, move, or delete files, run commands, git. `konsolai "make a directory named scratch"`
- **build** writes or edits project code, docs, or tests. Temperature is 0.3. `konsolai "add a health check handler"`

Build always requires an approved plan before a write. Act requires one only at high effort. Other mutating actions stop for a confirm (`y` / `n` / `a` / `q`). `--yes` approves without asking. Details are in [Safety model](#safety-model).

A lookup that tries to mutate is raised to act once.

## Requirements

- Go 1.27.1 (`go 1.27.1` in `go.mod`).
- An OpenAI-compatible chat endpoint, such as vLLM, llama.cpp, or Ollama. The client posts to `<endpoint>/chat/completions`. A typical base is `http://localhost:8000/v1`. An empty model name uses the first model returned by `<endpoint>/models`.

Optional:

- Postgres with the `vector` extension (pgvector). Memory and RAG stay off when `database.url` is empty. Opening the store runs `CREATE EXTENSION vector`.
- A TEI-compatible embedding server and, separately, a rerank server. The client `POST`s `/embed` with `inputs`, `normalize`, and `truncate`, and `POST`s `/rerank` with `query` and `texts`. A failed rerank keeps the fused search hits.
- PDF and Mermaid export. `export_doc` renders Markdown with goldmark. A `mermaid` fence is sent to the `mmdc` binary from `@mermaid-js/mermaid-cli` (`npm i -g @mermaid-js/mermaid-cli`). PDF printing runs headless Chrome: `KONSOLAI_CHROME` if set, otherwise the first of `chromium`, `chromium-browser`, `google-chrome-stable`, `google-chrome`, `brave`, and `brave-browser`. A Puppeteer-cached Chrome is preferred over Brave. HTML with no Mermaid fence needs neither binary.
- Clipboard for `/copy`. With `WAYLAND_DISPLAY` set, `wl-copy`. With `DISPLAY` set, `xclip`, then `xsel`. Otherwise OSC 52 on the TTY (100 KiB cap).

## Install

```sh
go install github.com/SniksaX/Konsolai/cmd/konsolai@latest
```

From a checkout of this repo, `scripts/build.sh` sets the version from `git describe --tags --always --dirty` and runs:

```sh
go build -trimpath -ldflags "-s -w -X main.version=${ver}" -o ~/.local/bin/konsolai ./cmd/konsolai
```

Put `~/.local/bin` on `PATH`. `konsolai --version` and `/status` print that version.

A binary in the current directory, with `version` left as `dev`:

```sh
go build -o konsolai ./cmd/konsolai
```

Stamp the version yourself with `-ldflags "-X main.version=..."`.

## Configuration

Every change goes through `/set`. A new setting is one entry in `internal/config/keys.go`.

```sh
konsolai /set
konsolai /set theme glacier
konsolai /set steps 20
konsolai /set steps default
```

`/set` with no arguments lists every setting, grouped by section. On a TTY inside the REPL it is a menu (↑/↓, Enter, Esc, `?`). `/set <name>` shows the value, its source, the default, the allowed values, and the doc. `/set <name> <value>` checks the value, writes the active config with comments and order kept, and prints `applied now`, `from the next turn`, or `after restart`. `/set <name> default` and `/set default <name>` remove that key. `/set default all` rewrites the active file from the factory document. `--session` keeps the change in this REPL and does not write the file. A full key path works (`/set agent.max_steps.act 20`). A unique suffix works. An ambiguous name lists the candidates. Tab completes names, then arguments, then allowed values.

Secrets are refused. A database password belongs in `~/.pgpass` or `KONSOLAI_DATABASE_URL`, not in the file. If the shell already sets the key's environment variable, the file is still written and `/set` warns that the variable overrides it.

`/theme`, `/think`, `/yes`, `/trace`, and `/models` remain for one release. Each prints `now: /set ...`. `/think`, `/yes`, and `/trace` stay session-only.

The active file is `--config`, then `KONSOLAI_CONFIG`, then `$XDG_CONFIG_HOME/konsolai/config.yaml` or `~/.config/konsolai/config.yaml`. A checkout may point that path at `config.yaml` in the repo (mode `0600`, gitignored). `/config path` prints the configured path and the path after symlinks are resolved. Writes go to the real file. [config.example.yaml](config.example.yaml) and [config.default.yaml](config.default.yaml) are the commented factory defaults, generated from the key table. `/set default all` writes that document. [.env.example](.env.example) lists the environment overrides.

`/config init` writes the defaults, mode `0600`, and refuses to replace an existing file without `--force`. `/config init --from-current` writes the effective config. `/config diff` lists keys that differ from the defaults. `/config show` masks a password in `database.url`. `/config validate` checks the file.

`--profile NAME` selects the profile. If that flag is empty, `KONSOLAI_PROFILE` is used, then `default_profile`, then the built-in name `default`.

| Variable | Field |
| --- | --- |
| `KONSOLAI_DATABASE_URL` | `database.url` |
| `KONSOLAI_ENDPOINT` | main model endpoint |
| `KONSOLAI_MODEL` | main model id |
| `KONSOLAI_FAST_ENDPOINT` | fast model endpoint |
| `KONSOLAI_FAST_MODEL` | fast model id |
| `KONSOLAI_BUILD_THINKING` | `agent.build_thinking` |
| `KONSOLAI_EMBED_ENDPOINT` | `roles.embed` |
| `KONSOLAI_RERANK_ENDPOINT` | `roles.rerank` |
| `KONSOLAI_PROFILE` | profile name, when `--profile` is empty |
| `KONSOLAI_CONFIG` | config file, when `--config` is empty |

Friendly names:

| name | values | keys |
| --- | --- | --- |
| model | main \<name\> \| fast \<name\>\|off \| add \<name\> \<endpoint\> [id] \| remove \<name\> \| discover | models, roles.main, roles.fast |
| embed | \<url\>\|off | roles.embed |
| rerank | \<url\>\|off | roles.rerank |
| database | \<url\>\|off | database.url |
| theme | amber \| glacier \| nebula \| jade \| rose \| mono | ui.theme |
| appearance | auto \| dark \| light | ui.appearance |
| welcome | split \| frame \| minimal | ui.welcome |
| banner | on \| off | ui.banner |
| emoji | on \| off | ui.strip_emoji |
| highlight | on \| off | ui.code_highlight |
| cursor | on \| off | ui.cursor_color |
| steps | \<n\> \| lookup\|act\|build \<n\> | agent.max_steps.lookup, agent.max_steps.act, agent.max_steps.build |
| budget | \<intent\> \<n\> | agent.explore_budget.* |
| think | on\|off\|auto \| \<intent\> on\|off\|plan_step\|until_plan | agent.think_mode, agent.think_answer, agent.think_lookup, agent.think_act, agent.build_thinking |
| temperature | \<x\> [intent] | llm.temperature.answer, llm.temperature.lookup, llm.temperature.act, llm.temperature.build |
| top_p | \<x\> | llm.top_p |
| approve | ask \| in_plan \| all_safe | agent.approve |
| plan | act_low\|act_high\|build on\|off | agent.plan.act_low, agent.plan.act_high, agent.plan.build |
| trace | on \| off | ui.trace |
| stats | on \| off | ui.stats |
| badge | on \| off | ui.badge |
| language | auto \| en \| fr | language.mode |
| languages | \<list\> | language.languages |
| web | on\|off \| results \<n\> | tools.web.enabled, tools.web.results |
| memory | on \| off | memory.enabled |
| rag | on \| off | rag.enabled |
| idle | \<minutes\> | memory.session_idle_minutes |
| retention | \<days\> | memory.retention_days |
| elide | \<trigger\> \<target\> | context.elide_trigger, context.elide_target |
| ctx | warn\|hot \<pct\> | ui.ctx_warn_pct, ui.ctx_hot_pct |
| timeout | shell \<sec\> \| model \<sec\> | tools.shell.default_timeout_sec, llm.idle_timeout_seconds |

## Models

Named models live under `models`. `roles.main` selects the chat model. `roles.fast` is a name or empty. An empty fast role uses the main model and says nothing. `roles.embed` and `roles.rerank` are URLs or empty. `chat_template_kwargs.enable_thinking` is sent only when that model's `thinking_kwargs` is true. A role change applies on the next turn.

The built-in model is `default` at `http://localhost:8000/v1`, with an empty id and thinking off. Leave the id empty to use the first model from `<endpoint>/models`. When the fast role is set and reachable, the router uses it. An answer at effort `none` uses it too, unless the prompt is past `fast.context_guard_ratio` of that model's window. If that stream fails, the turn retries on main.

```sh
konsolai /set model
konsolai /set model main default
konsolai /set model fast off
konsolai /set model add local http://localhost:8000/v1
konsolai /set model discover
konsolai /set embed off
konsolai /set rerank off
```

`/set model` prints name, roles, host, id, context, and status. The current main and fast rows are marked. `/set model discover` lists ids on each endpoint that are not configured yet, each with the `/set model add` line. Embed and rerank URLs are optional. The client appends `/embed` and `/rerank`. With `rag.enabled` false there is no ingestion, no recall tool, and no warning. `/status` prints `not configured` for a role that is off.

## Usage

```
konsolai [--yes] [--profile NAME] [--config FILE] [--intent answer|lookup|act|build] [--trace] [--plain] [--version] [new] ["<request>"]
konsolai /<command> [args]
```

The request is one argument. `new` closes resumable sessions so the next request starts fresh. `--plain` turns styled output off. `--trace` prints per-call timing and tokens.

### REPL

`/` opens the slash menu from the registry. A bare `/name` is not a readline completion. The grey placeholder is an inline suggestion; Enter does not submit it.

`!ls` runs that line in `bash` in the working directory. `/cmd` toggles shell mode (`exit` or `/cmd` leaves it). Commands from that mode are attached as context on the next model request.

`@path` attaches a file (32 KiB) or a directory outline. `\@path` stays literal. A missing path is left as text. An email address is not an attachment.

A paste of at least `ui.paste_chip_lines` lines (default 11) or `ui.paste_chip_chars` characters (default 1000) is shown as `[pasted #N · L lines]` and expanded before the turn.

`/theme` with no argument opens a picker on a TTY. Esc leaves it with the current theme. Enter saves the highlighted palette, erases the picker, and leaves the screen in place. `/theme glacier` is `/set theme glacier`: it writes the palette and prints `applied now`. Names: `amber`, `glacier`, `nebula`, `jade`, `rose`, `mono`.

These commands exist only in the REPL: `exit`, `clear`, `cd`, `cmd`, `yes`, `trace`, `think`, `model`, `compact`.

### One-shot

No request, and stdin is a pipe: stdin is the request, capped at 64 KiB. Past that, the tail is dropped and the text ends with `[stdin truncated]`.

```sh
konsolai <<'EOF'
where is the plan gate set?
EOF
```

A request, and stdin is a pipe: stdin is context, capped at 32 KiB, appended after a blank line inside `<stdin>` tags.

```sh
konsolai "review this diff" <<'EOF'
diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
EOF
```

No request, stdin is a TTY, and stdout is not: the request is read from `/dev/tty`, same 64 KiB cap. Both TTYs and no request is the REPL, above.

### Intents

| Intent | Tools | Plan |
| --- | --- | --- |
| answer | none; the reply is streamed | no |
| lookup | read-only, including a read-only shell | no; a mutating attempt escalates to act once |
| act | all | only when effort is high |
| build | all | always; temperature 0.3 |

### Memory

Memory is off when `database.url` is empty. The first store error disables it for the process and prints one line.

- `/history` lists recent sessions. `/resume <n> "<request>"` continues session n.
- `/undo` restores files changed in recent sessions.
- `/remember "<fact>"` stores a user fact. `/remember --project "<fact>"` stores a project fact. `/facts` lists them. `/forget user|project <id>` deletes one.
- `/recall "<query>"` searches the index and does not call the model. `/index` builds that index. `/index status` and `/index clear` report or drop it.

From the shell the same commands are `konsolai /history` and so on. The bare words `history`, `undo`, `facts`, `prune`, `remember`, and `forget` dispatch the same code and print `note: use /<name>`.

### Export

`export_doc` accepts `html` and `pdf`. The model writes Markdown, then exports it. Mermaid fences go through `mmdc`. PDF goes through headless Chrome, as in [Requirements](#requirements). Shell PDF and HTML printers are refused. A docs plan that asks for PDF or HTML has to name `export_doc`.

## Safety model

The plan gate is on for every build turn, and for an act turn only when effort is high. Until `propose_plan` is approved, a mutating tool is rejected with `call propose_plan first`. You confirm the plan at the `y` / `n` / `a` / `q` prompt.

After approval, a file write must be one of the plan's files. A shell line must pass `safety.MatchPlan`: a read-only command, a command declared on the plan, a file operation on a planned file, or a git command in the same family as the plan. A match runs with no second prompt.

Anything outside the plan is confirmed again. `a` (approve the rest) does not cover that second prompt, except an outside-plan shell command after `a`, which runs and is marked outside the plan. `--yes`, and `/yes on` in the REPL, approve without a prompt. An outside-plan action under `--yes` still prints a warning.

When the plan gate is off, each mutating action is confirmed the same way.

`read_file` may open any user-readable path except the sensitive denylist: `~/.ssh`, `~/.gnupg`, `~/.local/share/keyrings`, browser profiles under `~/.config` (`BraveSoftware`, `google-chrome`, `chromium`), `~/.config/konsolai`, `.netrc`, `.pgpass`, names starting with `id_`, extensions `.key`, `.pem`, `.p12`, `.pfx`, `.kdbx`, and `/proc/<pid>/environ`. `safety.extra_read_deny` adds prefixes.

Writes (`write_file`, `edit_file`, `export_doc`) stay inside the project root. An absolute or `~` path named in the request is added as an extra root, and so is an absolute or `~` path from an approved plan, including its parent directories under the home directory. Those extras are refused under `~/.ssh`, `~/.gnupg`, `~/.config`, and `~/.local/share/keyrings`.

`sudo`, `doas`, `su`, and `pkexec` are refused. The model is told to list them for you under `finish`, as `Run these yourself:`. The command denylist also refuses recursive `rm` / `chmod` / `chown` of `/` or the home directory, `mkfs`, `wipefs`, `dd` onto a device, redirection onto a disk device, a curl or wget pipe into a shell, a fork bomb, and `shutdown` / `reboot` / `poweroff` / `halt`. `safety.extra_command_deny` adds substrings.

`redact.String` and `redact.JSON` run before a message or action is stored. They mask private keys, AWS and GitHub tokens, Slack tokens, `sk-` keys, JWTs, bearer tokens, URL passwords, and assignment lines that look like secrets. Reads of sensitive paths (`.env`, key and certificate files) mask values and keep the first four characters.

## Slash commands

The `name` cell is `Command.DisplayNames`. The `args` cell lists `Arg.Name`; optional args are in brackets. The `description` cell is `Command.Summary`. Order is `commands.Default()`. `TestReadmeSlashTable` fails when this table drifts.

| name | args | description |
| --- | --- | --- |
| /help | | Show this list of commands |
| /copy | what [n] | Copy the last answer, pair, code block or command |
| /history | [n] | List recent sessions |
| /show | n | Reprint a session's prompts and answers |
| /resume | n request | Continue session n with a new request |
| /new | | Close resumable sessions so the next request starts fresh |
| /exit, /quit | | Leave the REPL |
| /clear | | Clear the screen |
| /cd | dir | Change the working directory |
| /cmd | [clear] | Toggle shell mode |
| /yes | mode | Auto-approve for this REPL session |
| /trace | mode | Toggle per-call traces |
| /think | mode | Override thinking for this session |
| /model | | Show model, window and endpoint |
| /compact | | Summarize earlier messages in this session |
| /undo | | Restore files changed in recent sessions |
| /diff | [n] | Show file diffs from a session |
| /run | [k] | Run a bash/sh block from the last answer |
| /output | [n] | Reprint the full output of a recent command |
| /status | | Show version, model, database and environment |
| /config | action | Show or write the YAML config |
| /set | [name] [value] | Show or change a setting |
| /models | | Show configured models |
| /theme | [name] | List palettes or apply one |
| /index | [action] | Index the project for recall |
| /recall | query | Search indexed memory without calling the model |
| /remember | fact | Store a fact |
| /facts | | List remembered facts |
| /forget | scope id | Delete a fact |
| /prune | | Prune old sessions now |
| /stats | [window] | Show calls, tokens, and latency |
| /doctor | | Check config, endpoints, database, and local tools |
| /save | [file] | Write the last answer to a Markdown file |
| /pdf | [file] | Render the last answer to a PDF |
| /explain | | Explain the last failed shell command (set PROMPT_COMMAND="history -a") |

## Development

Leaves, which do not import `konsolai/internal`: `ui/theme`, `config`, `llm`, `safety`, `redact`, `repo`, `sysctx`, `diff`, `docrender`, `embed`, `clip`.

The TTY side is `ui`, `banner`, `repl`, `shellmode`, `commands`, and `cmd/konsolai`. A turn is `intent`, `turn`, `agent`, and `tools`. Persistence is `store` (Postgres through pgxpool), `memory` (the first store error disables it), `rag`, and `welcome`. `tools`, `agent`, and `ui` do not import pgx. `ui` does not import `store`. `agent` does not import `memory` or `store`.

Tests that define behavior:

- Welcome goldens: 150 files in `internal/welcome/testdata`, checked by `TestRenderGoldens`.
- PTY: `internal/repl/readline_pty_test.go`, `internal/repl/theme_pty_test.go`, `cmd/konsolai/e2e_slash_pty_test.go`. UI checks use a real PTY, not a pipe.
- Store tests read `KONSOLAI_TEST_DATABASE_URL`. `scripts/test-db.sh up` starts the throwaway test database and `scripts/test-db.sh down` removes it. Never touch the developer's own database container.

Commits go to `dev` only. One commit per file. A split of one file into siblings may be one commit, so that every commit builds. The message is `[TYPE|SIZE] Title` in the imperative, title at most 72 characters. TYPE is `ADD`, `UPDATE`, `FIX`, `DELETE`, `MOVE`, or `REFACTOR`. SIZE is `MINOR` (under 20 lines), `MEDIUM` (20–100), or `MAJOR` (over 100, or a public API or behavior change).

Working rules and the decisions that must not regress are in [AGENTS.md](AGENTS.md).

## License

License: TBD
