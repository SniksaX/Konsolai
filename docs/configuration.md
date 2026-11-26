# Configuration

Konsolai reads one YAML file. `/config init` writes it, `/set` changes it, and [config.example.yaml](../config.example.yaml) is the generated reference. Factory values come from the binary.

## Config file

The active file is `--config`, then `KONSOLAI_CONFIG`, then `$XDG_CONFIG_HOME/konsolai/config.yaml` or `~/.config/konsolai/config.yaml`. A checkout may point that path at `config.yaml` in the repo (mode `0600`, gitignored). `/config path` prints the configured path and the path after symlinks are resolved. Writes go to the real file.

`/config init` writes the defaults, mode `0600`, and refuses to replace an existing file without `--force`. `/config init --from-current` writes the effective config. `/config diff` lists keys that differ from the defaults. `/config show` masks a password in `database.url`. `/config validate` checks the file.

`--profile NAME` selects the profile. If that flag is empty, `KONSOLAI_PROFILE` is used, then `default_profile`, then the built-in name `default`.

Every change goes through `/set`. A new setting is one entry in `internal/config/keys.go`.

```sh
konsolai /set
konsolai /set theme glacier
konsolai /set steps 20
konsolai /set steps default
```

`/set` with no arguments lists every setting, grouped by section. On a TTY inside the REPL it is a menu (↑/↓, Enter, Esc, `?`). `/set <name>` shows the value, its source, the default, the allowed values, and the doc. `/set <name> <value>` checks the value, writes the active config with comments and order kept, and prints `applied now`, `from the next turn`, or `after restart`. `/set <name> default` and `/set default <name>` remove that key. `/set default all` asks first, with a single key: y writes the factory document and n leaves the file. The prompt says how many keys differ from the factory values and that models, roles, and endpoints will be reset. `--yes` skips the prompt. Before the rewrite, the real file (symlinks resolved) is copied to `config.yaml.bak-<YYYYmmdd-HHMMSS>` beside it, mode `0600`, and only the last five of those backups are kept. `--session` keeps the change in this REPL and does not write the file. A full key path works (`/set agent.max_steps.act 20`). A unique suffix works. An ambiguous name lists the candidates. Tab completes names, then arguments, then allowed values.

Secrets are refused. A database password belongs in `~/.pgpass` or `KONSOLAI_DATABASE_URL`, not in the file. If the shell already sets the key's environment variable, the file is still written and `/set` warns that the variable overrides it.

## Environment overrides

Prefer `config.yaml`. Environment variables are for containers and CI.

| Variable | Sets |
| --- | --- |
| `KONSOLAI_DATABASE_URL` | `database.url` |
| `KONSOLAI_BUILD_THINKING` | `agent.build_thinking` |
| `KONSOLAI_ENDPOINT` | main model endpoint |
| `KONSOLAI_MODEL` | main model id |
| `KONSOLAI_FAST_ENDPOINT` | fast model endpoint |
| `KONSOLAI_FAST_MODEL` | fast model id |
| `KONSOLAI_EMBED_ENDPOINT` | `roles.embed` |
| `KONSOLAI_RERANK_ENDPOINT` | `roles.rerank` |
| `KONSOLAI_PROFILE` | profile name, when `--profile` is empty |
| `KONSOLAI_CONFIG` | config file, when `--config` is empty |

Compose-only variables are not config keys. `/set` does not write them.

| Variable | Default | Use |
| --- | --- | --- |
| `KONSOLAI_UID` | `1000` | container user id; `scripts/docker.sh` sets `id -u` |
| `KONSOLAI_GID` | `1000` | container group id; `scripts/docker.sh` sets `id -g` |
| `KONSOLAI_DB_PASSWORD` | `konsolai` | local Compose database password |
| `KONSOLAI_DB_PORT` | `55434` | published host port |
| `KONSOLAI_TARGET` | `base` | image stage, `base` or `full` |
| `KONSOLAI_WORKDIR` | `.` | host directory mounted at `/work` |
| `KONSOLAI_CONFIG_FILE` | `./config.yaml` | config file mounted at `/config/config.yaml` |
| `KONSOLAI_EMBED_MODEL` | `BAAI/bge-m3` | embedding service model id |
| `KONSOLAI_EMBED_PORT` | `8080` | published embedding port |
| `KONSOLAI_IMAGE` | `konsolai:dev` | image tag `scripts/docker.sh run` uses |
| `KONSOLAI_VERSION` | `docker` | version stamped into the image |
| `KONSOLAI_DB_VOLUME` | `konsolai-compose-db` | Compose database volume |
| `KONSOLAI_EMBED_VOLUME` | `konsolai-compose-embed` | Compose embedding volume |

## Friendly names

The `name` column is accepted by `/set` in place of a full key path. The `values` column summarises the accepted forms, and the `keys` column lists what the name writes.

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
| clear | full \| compact \| none | ui.clear_header |
| pinned_header | on \| off | ui.pinned_header |
| emoji | on \| off | ui.strip_emoji |
| highlight | on \| off | ui.code_highlight |
| cursor | on \| off | ui.cursor_color |
| steps | \<n\> \| lookup\|act\|build \<n\> | agent.max_steps.lookup, agent.max_steps.act, agent.max_steps.build |
| budget | \<intent\> \<n\> | agent.explore_budget.* |
| think | on\|off\|auto \| \<intent\> on\|off\|plan_step\|until_plan \| plan_think always\|on_reject | agent.think_mode, agent.think_answer, agent.think_lookup, agent.think_act, agent.build_thinking, agent.plan_think |
| temperature | \<x\> [intent] | llm.temperature.answer, llm.temperature.lookup, llm.temperature.act, llm.temperature.build |
| top_p | \<x\> | llm.top_p |
| tool_choice | named \| required \| auto | llm.tool_choice_mode |
| reasoning | default \| none \| minimal \| low \| medium \| high | llm.reasoning_effort |
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
| mcp | add stdio \<name\> \<command\> [args...] \| add http \<name\> \<url\> \| remove \| enable \| disable \| trust \<name\> ask\|read_only\|auto \| allow \| deny \<name\> \<glob\> \| header \| env \<name\> \<KEY\> \<value\> | mcp.servers.*, mcp.max_inline_tools |

## Models

Named models live under `models`. `roles.main` selects the chat model. `roles.fast` is a name or empty. An empty fast role uses the main model and says nothing. `roles.embed` and `roles.rerank` are URLs or empty. `chat_template_kwargs.enable_thinking` is sent only when that model's `thinking_kwargs` is true. A role change applies on the next turn.

The built-in model is `default` at `http://localhost:8000/v1`, with an empty id and thinking off. Leave the id empty only when the endpoint serves exactly one model; otherwise set the id, or Konsolai stops with the served list. When the fast role is set and reachable, the router uses it. A greeting, or an answer at effort `none` whose full prompt is under `router.answer_main_above_tokens` (default `200`), streams on fast unless the prompt is past `fast.context_guard_ratio` of that model's window. Every other answer streams on main, and if the fast stream fails the turn retries on main.

```sh
konsolai /set model
konsolai /set model main default
konsolai /set model fast off
konsolai /set model add local http://localhost:8000/v1
konsolai /set model id local deepseek-flash
konsolai /set model discover
konsolai /set embed off
konsolai /set rerank off
```

`/set model id <name> <id>` changes one model's server id; like the endpoint command it writes through the YAML node writer, so comments survive. `/set model` prints name, roles, host, id, context, and status. The current main and fast rows are marked. `/set model discover` lists ids on each endpoint that are not configured yet, each with the `/set model add` line. Embed and rerank URLs are optional. The client appends `/embed` and `/rerank`. With `rag.enabled` false there is no ingestion, no recall tool, and no warning. `/status` prints `not configured` for a role that is off.

## Cloud models

A model is remote when its endpoint host resolves outside loopback and the private address ranges. Remote models are supported: `/set model` and `/status` mark a remote endpoint, the welcome models box marks a remote row, and the footer marks the model that served the last call when that model is remote. `/doctor` prints one line per remote model with its host, whether a key is present (never the value), and a warning for plain HTTP. The privacy keys below gate all of it.

The presets `openai`, `openrouter`, `groq`, `together`, `mistral`, `deepseek`, `gemini`, and `anthropic` fill the endpoint, context and request quirks for an API-key provider. They come from each provider's OpenAI-compatible documentation and were not exercised against live accounts here. The `anthropic` preset is experimental: its OpenAI-compatible endpoint accepts an auth header that has not been verified.

### Probes

Every model endpoint check sends the key the turn would send: the welcome, `/status`, `/doctor`, `/set model` and `/set model discover` all resolve `api_key_env` or the secrets file for their model, and a local model with no key sends no `Authorization` header. A probe reports only what the endpoint answered:

| probe result | shown as |
| --- | --- |
| 2xx | `ok` with the latency; the welcome and `/set model` show `slow` past `ui.model_slow_ms` |
| 401 or 403, or a key that could not be loaded | `auth failed` |
| the probe deadline | `slow` |
| any other non-2xx | `http N` |
| a dial, DNS or refused connection | `unreachable` (`down` in `/set model`; the welcome shows the port as unreachable) |

A failed probe never means the host is down: only a failed chat call can say the server cannot be reached, and a probe failure says the turn made no chat call. `llm.remote_probe_timeout_ms` (default `3000`) is the budget for one remote probe in `/status`, `/doctor`, the startup probe and the turn clients; local endpoints keep their short built-in budgets. The welcome probes a remote model with its short gather budget (`ui.probe_timeout_ms`, `ui.probe_deadline_ms`) instead, so a remote row can read `slow` on the welcome while `/status` reads `ok`.

### Privacy

Remote use is allowed by default and gated by three keys:

| key | default | meaning |
| --- | --- | --- |
| `privacy.allow_remote` | `true` | allow prompts to be sent to a remote model |
| `privacy.remote_roles` | `main`, `fast` | roles that may point at a remote model; empty means no restriction |
| `privacy.session_token_cap` | `0` | stop remote calls after this many tokens; `0` = off |

The first remote call in a process asks once on the terminal:

```
this sends your prompts and file contents to api.openai.com [y/N]:
```

In the REPL the question opens in the normal editor, so history, arrows and paste work; Ctrl+C and Ctrl+D count as no. A one-shot run reads the answer from stdin, and a non-interactive run without `--yes` refuses the remote model before sending anything; `--yes` skips the question. Only `y` or `yes` agrees, and the answer is remembered for the rest of the process: an agreed remote model is not asked again, and a decline refuses every remote model until the process exits. The cap adds the token usage each remote call reports; once the total passes it, the next remote call stops with a `privacy.session_token_cap` error.

What leaves the machine is the prompts and the file contents a turn attaches, plus the model's replies. `config.yaml` stays local, and the key goes only into the `Authorization` header for that host. A key over plain HTTP to a non-local host is refused; use HTTPS.

### Keys and request quirks

A model entry can name the environment variable that holds its bearer key: `api_key_env`. The key is read at call time; when the variable is unset the client falls back to `~/.config/konsolai/secrets` (mode `0600`, directory `0700`, never in the repository or in `config.yaml`). `/set key <model>` reads a key with hidden input and writes that file. `/set` refuses keys as plain arguments.

A model also accepts `max_tokens_field` (`max_tokens` or `max_completion_tokens`), `send_temperature` (default true; some providers reject the field), `context` (the window; `0` means the server reports it), and optional `price_in` / `price_out` in USD per million tokens.

An empty model id resolves only when the endpoint advertises exactly one model. A configured id must appear in the served list. Otherwise Konsolai stops before the chat call with an error that lists the served ids, and `/status` shows `(server serves: ...)` or `(not served; server serves: ...)` instead of making up an id.

The `deepseek` preset sends no temperature (`send_temperature` false) because the reasoner may reject it, and prints that note. DeepSeek's `reasoning_content` (or `reasoning`) is trace-only: it never enters the answer, and its length counts in the `think` tokens of the trace and stats lines when the server does not report `reasoning_tokens`.

```sh
konsolai /set model add gpt --provider openai
konsolai /set key gpt
konsolai /set model main gpt
```

With `price_in` or `price_out` set on a remote model, the end-of-turn stats line (`ui.stats`, on by default) ends with the turn cost, such as `$0.015`, and the `--trace` stats line adds the model tag and `cost=$0.015 in=2.0k out=1.0k`. A local model, or a remote model with both prices at `0`, shows no cost.

### Facts, identity and attribution

Every system context carries the model facts: the configured main and fast model names, their ids, endpoint hosts, local or remote, and the role answering the call. A question that asks which model answers (`what model are you`, `which model is this`, `quel modèle es-tu`, `who are you`) is answered by Konsolai itself from those facts, without a model call, a probe or a remote confirmation; `--trace` prints `identity: answered from the configured model facts (no model call)`. Every other identity or setup question goes to the model, which is told to answer from the facts and to say "I don't know" when a fact is missing.

The trace line, the end-of-turn stats line (`ui.stats`) and the footer name the model that served the call: the id the endpoint reports (or the configured name), the role `main` or `fast`, and `remote` for a remote endpoint, for example `model main-model (main) remote`. The footer shows that id on the left and the serving role on the right; after an answer built from the facts it says `facts` and `local`.

### Fast answers

The fast role answers only short greetings (such as `hi`, `hello`, `bonjour`, `merci`) and answer turns at effort `none` whose estimated full answer prompt is under `router.answer_main_above_tokens` (default `200`; `0` disables the token gate). The estimate counts the system context, the history and the question, not only the question, so a long history sends even a short question to main. Every other answer streams on main. The fast client is still the first router when it is reachable, and `fast.context_guard_ratio` still skips it when the answer prompt fills its window.

Embedding endpoints must be loopback or private; a remote embed service is rejected, so RAG stays local.

## MCP servers

MCP is configured under `mcp`. `mcp.max_inline_tools` (default `15`) caps how many MCP tools are offered inline; a later phase moves the rest behind a listing. Each entry of `mcp.servers` is one server:

```yaml
mcp:
  max_inline_tools: 15
  servers:
    files:
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem"]
      env:
        FS_KEY: "${FILES_KEY}"
      enabled: true
      trust: ask
      timeout_ms: 30000
      private: false
      tools:
        allow: ["read_*"]
        deny: ["write_*"]
        read_only: ["read_*"]
    docs:
      transport: http
      url: https://example.com/mcp
      headers:
        Authorization: "Bearer ${DOCS_TOKEN}"
      trust: read_only
```

| field | values |
| --- | --- |
| `transport` | `stdio` runs `command` with `args`; `http` connects to `url` |
| `command`, `args`, `env` | stdio only; `env` values may be a `${ENV_VAR}` reference |
| `url`, `headers` | http only; `https`, or plain `http` on a loopback or private host; `headers` values may be a `${ENV_VAR}` reference |
| `enabled` | default `true` |
| `trust` | `ask` (default), `read_only` or `auto` |
| `timeout_ms` | default `30000`; must be positive |
| `private` | no remote classification for this server |
| `tools.allow`, `tools.deny`, `tools.read_only` | shell-style globs over tool names |

An env or header value that looks like a stored credential (`sk-`, `ghp_`, `AKIA`, a long token, a `Bearer` literal, `password=`) is refused; use a `${ENV_VAR}` reference instead. A reference that does not resolve yet is accepted and resolved when the server starts. `mcp` entries are not keys: `/set` edits them through `/set mcp`, and `/config show` / `/config diff` mask every env and header value.

```sh
konsolai /set mcp add stdio files npx -y @modelcontextprotocol/server-filesystem
konsolai /set mcp add http docs https://example.com/mcp
konsolai /set mcp trust files read_only
konsolai /set mcp allow files "read_*"
konsolai /set mcp header docs Authorization "Bearer ${DOCS_TOKEN}"
konsolai /set mcp disable files
konsolai /set mcp remove files
konsolai /mcp
```

Adding a stdio server prints the exact command line and asks once; `n` leaves the file unchanged. `/mcp` lists one row per server with its transport, enabled state, trust, private marker, cached tool count and live status; until the runtime is connected the status reads `not connected`. `/mcp tools` and `/mcp test` are not implemented yet.
