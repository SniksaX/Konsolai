# Commands

```
konsolai [--yes] [--profile NAME] [--config FILE] [--intent answer|lookup|act|build] [--trace] [--plain] [--version] [new] ["<request>"]
konsolai /<command> [args]
```

The request is one argument. `new` closes resumable sessions so the next request starts fresh. `--plain` turns styled output off. `--trace` prints per-call timing and tokens.

## REPL

`/` opens the slash menu from the registry. A bare `/name` is not a readline completion. The grey placeholder is an inline suggestion; Enter does not submit it.

`!ls` runs that line in `bash` in the working directory. `/cmd` toggles shell mode (`exit` or `/cmd` leaves it). Commands from that mode are attached as context on the next model request.

`@path` attaches a file (32 KiB) or a directory outline. `\@path` stays literal. A missing path is left as text. An email address is not an attachment.

A paste of at least `ui.paste_chip_lines` lines (default 11) or `ui.paste_chip_chars` characters (default 1000) is shown as `[pasted #N · L lines]` and expanded before the turn.

`/theme` with no argument opens a picker on a TTY. Esc leaves it with the current theme. Enter saves the highlighted palette, erases the picker, and leaves the screen in place. `/theme glacier` is `/set theme glacier`: it writes the palette and prints `applied now`. Names: `amber`, `glacier`, `nebula`, `jade`, `rose`, `mono`.

These commands exist only in the REPL: `exit`, `clear`, `cd`, `cmd`, `yes`, `trace`, `think`, `model`, `compact`.

`/clear` redraws the welcome. `ui.clear_header` is `full` (the welcome), `compact` (one line), or `none`. `/cmd clear`, Ctrl-L, and `clear` or `reset` in shell mode do the same. `/cmd forget` drops the shell-mode context. `ui.pinned_header` keeps the header fixed in a scroll region. Lines scrolled inside that region may not enter the terminal's scrollback.

## One-shot

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
@@ -1 +1 @@
-old line
+new line
EOF
```

No request, stdin is a TTY, and stdout is not: the request is read from `/dev/tty`, same 64 KiB cap. Both TTYs and no request is the REPL, above.

Exit codes of a one-shot run:

| Code | Meaning |
| --- | --- |
| 0 | The turn finished |
| 1 | Error: the model was unreachable, returned no answer, or the conversation no longer fits its context |
| 2 | Usage error |
| 3 | Aborted: you quit at an approval prompt |
| 4 | Max steps: the step budget ran out before the task finished |
| 5 | Rejected: the model repeated the same rejected tool call and the turn was stopped; the task was not completed |
| 130 | Interrupted (Ctrl-C) |

A session is recorded as `finished` on 0, `aborted` on 3 and 130, and `failed` on every other code.

## Intents

| Intent | Tools | Plan |
| --- | --- | --- |
| answer | none; the reply is streamed | no |
| lookup | read-only, including a read-only shell | no; a mutating attempt escalates to act once |
| act | all | only when effort is high |
| build | all | always; temperature 0.3 |

## Memory

Memory is off when `database.url` is empty. The first store error disables it for the process and prints one line.

- `/history` lists sessions of the current project first, then every other project, newest first. `/history --here` stays in the current project. The same numbers are `/resume`, `/show`, `/diff`, and the welcome RECENT panel. `/resume <n> "<request>"` continues session n.
- `/undo` restores files changed in recent sessions.
- `/remember "<fact>"` stores a user fact. `/remember --project "<fact>"` stores a project fact. `/facts` lists them. `/forget user|project <id>` deletes one.
- `/recall "<query>"` searches the index and does not call the model. `/index` builds that index. `/index status` and `/index clear` report or drop it.

From the shell the same commands are `konsolai /history` and so on. The bare words `history`, `undo`, `facts`, `prune`, `remember`, and `forget` dispatch the same code and print `note: use /<name>`.

## Export

`export_doc` accepts `html` and `pdf`. The model writes Markdown, then exports it. Mermaid fences render in headless Chromium; the HTML result inlines the SVGs and contains no script. PDF uses the same Chromium. Shell PDF and HTML printers are refused. A docs plan that asks for PDF or HTML has to name `export_doc`.

## Slash commands

The `name` cell is `Command.DisplayNames`. The `args` cell lists `Arg.Name`; optional args are in brackets. The `description` cell is `Command.Summary`. Order is `commands.Default()`. `TestDocsSlashTable` fails when this table drifts.

| name | args | description |
| --- | --- | --- |
| /help | | Show this list of commands |
| /copy | what [n] | Copy the last answer, pair, code block or command |
| /history | [n] [here] | List recent sessions |
| /show | n | Reprint a session's prompts and answers |
| /resume | n request | Continue session n with a new request |
| /new | | Close resumable sessions so the next request starts fresh |
| /exit, /quit | | Leave the REPL |
| /clear | | Clear the screen |
| /cd | dir | Change the working directory |
| /cmd | [clear] [forget] | Toggle shell mode |
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
