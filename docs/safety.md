# Safety model

The plan gate is on for every build turn, and for an act turn only when effort is high. Until `propose_plan` is approved, a mutating tool is rejected with `call propose_plan first`. Before the plan is approved, a build turn (and a high-effort act) offers only read-only tools and `propose_plan`; a change request answered in prose is nudged once to call `propose_plan`. You confirm the plan at the `y` / `n` / `a` / `q` prompt.

After approval, a file write must be one of the plan's files. A shell line must pass `safety.MatchPlan`: a read-only command, a command declared on the plan, a file operation on a planned file, or a git command in the same family as the plan. A match runs with no second prompt.

Anything outside the plan is confirmed again. `a` (approve the rest) does not cover that second prompt, except an outside-plan shell command after `a`, which runs and is marked outside the plan. `--yes`, and `/yes on` in the REPL, approve without a prompt. An outside-plan action under `--yes` still prints a warning.

When the plan gate is off, each mutating action is confirmed the same way.

`read_file` may open any user-readable path except the sensitive denylist: `~/.ssh`, `~/.gnupg`, `~/.mozilla`, `~/.local/share/keyrings`, browser profiles under `~/.config` (`BraveSoftware`, `google-chrome`, `chromium`), `~/.config/konsolai`, `.netrc`, `.pgpass`, names starting with `id_`, extensions `.key`, `.pem`, `.p12`, `.pfx`, `.kdbx`, and `/proc/<pid>/environ`. `safety.extra_read_deny` adds prefixes.

Writes (`write_file`, `edit_file`, `export_doc`) stay inside the project root. An absolute or `~` path named in the request is added as an extra root, and so is an absolute or `~` path from an approved plan, including its parent directories under the home directory. Those extras are refused under `~/.ssh`, `~/.gnupg`, `~/.config`, and `~/.local/share/keyrings`.

`sudo`, `doas`, `su`, and `pkexec` are refused. The model is told to list them for you under `finish`, as `Run these yourself:`. The command denylist also refuses recursive `rm` / `chmod` / `chown` of `/` or the home directory, `mkfs`, `wipefs`, `dd` onto a device, redirection onto a disk device, a curl or wget pipe into a shell, a fork bomb, and `shutdown` / `reboot` / `poweroff` / `halt`. `safety.extra_command_deny` adds substrings.

`redact.String` and `redact.JSON` run before a message or action is stored. They mask private keys, AWS and GitHub tokens, Slack tokens, `sk-` keys, JWTs, bearer tokens, URL passwords, and assignment lines that look like secrets. Reads of sensitive paths (`.env`, key and certificate files) mask values and keep the first four characters.

`read_file` and `search` treat the active configuration (including a `--config` path), its `config.yaml.bak-*` backups and `~/.config/konsolai/secrets` as protected: reads and matches return the masked view, with MCP `command`/`args`/`env`/`headers` and MCP/database hostnames hidden; the secrets file is refused or skipped with a marker. Writes and shell commands that name any of these paths are refused, because the configuration is changed only through `/set` and viewed with `/config show`.
