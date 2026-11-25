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

### API-key models and request quirks

A model entry can name the environment variable that holds its bearer key: `api_key_env`. The key is read at call time; when the variable is unset the client falls back to `~/.config/konsolai/secrets` (mode `0600`, directory `0700`, never in the repository or in `config.yaml`). `/set key <model>` reads a key with hidden input and writes that file. `/set` refuses keys as plain arguments.

A model also accepts `max_tokens_field` (`max_tokens` or `max_completion_tokens`), `send_temperature` (default true; some providers reject the field), `context` (required for a remote provider), and optional `price_in` / `price_out` in USD per million tokens.

```sh
konsolai /set model add gpt --provider openai
konsolai /set key gpt
konsolai /set model main gpt
```

The presets `openai`, `openrouter`, `groq`, `together`, `mistral`, `deepseek`, `gemini`, and `anthropic` fill the endpoint, context and quirks. They come from each provider's OpenAI-compatible documentation and were not exercised against live accounts here. The `anthropic` preset is experimental: its OpenAI-compatible endpoint accepts an auth header that has not been verified.

A model whose host resolves to a public address is marked `remote` in `/set model`. Embedding endpoints must be loopback or private; a remote embed service is rejected. The `privacy.allow_remote`, `privacy.remote_roles`, and `privacy.session_token_cap` keys are defined; the remote-use prompt, role enforcement, cost line and token cap are not wired yet.
