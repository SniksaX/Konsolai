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
- Keys are never sent over plain HTTP or across redirects, and are masked in errors.
- Tool-call history is repaired before every request (strict providers no longer reject it).
- Automatic retry when a provider rejects `tool_choice` or `reasoning_effort`; short rate limits retried once.
- Errors show the HTTP status and the provider's message; identity questions answered from the real configuration.

### Known issues
- Shell commands cannot yet run inside a folder named outside the project; such plans may show extra "outside plan" prompts.
