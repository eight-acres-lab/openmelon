# Roadmap

## 0.3 (current)

- **Projects.** `openmelon init`, per-project state under `<workdir>/.openmelon/`, global registry under `~/.openmelon/`.
- **Character + reference libraries.** `openmelon character add|list|show|rm`, `openmelon reference add|list|show|rm`, hash-addressed `material add|list`.
- **Search.** `openmelon search`, tag + substring grep across all libraries. Operators: `tag:`, `kind:`, `-negative`, quoted phrases.
- **Tool-driven runtime.** `openmelon -p` inside a project switches to a tool-using agent loop (`list_characters`, `get_character`, `list_references`, `get_reference`, `search`, `read_file`, `compile_skill`, `generate_image`, `save_artifact`, `finish`).
- **Reference-image inputs.** `imagegen.GenerateOptions.ReferenceImages` wired through OpenRouter chat-completions content array — anchor images keep characters / scenes consistent across runs.
- **Sessions.** Every run records system + user + assistant + tool messages into `<project>/.openmelon/sessions/<id>/messages.jsonl`; final summary + artifacts go to `summary.json`.
- Legacy `--project` workflow mode and one-shot `-p ... --skill ...` outside a project still work.

## 0.4

- Vision auto-describe on `character add` / `reference add` (single LLM call writes `.search` so the user doesn't have to).
- `openmelon` (no args) → interactive REPL, bubbletea TUI like Claude Code (streamed text + tool-call rendering + per-call approval).
- Long-term project memory: `openmelon memory add|recall` for free-form notes the agent loads on startup.
- Anthropic tool-use parity (Chat / ToolCaller).

## 0.5

- More image providers (Stability, Replicate).
- More built-in tools: `edit_image` (round-trip refs), `crop`, `caption`.
- `openmelon serve` — HTTP API surface for embedding into V-Box backend.

## 1.0

- Public Go API frozen for embedded use.
- CLI flags + provenance schema frozen.
- LTS policy + deprecation timeline.
