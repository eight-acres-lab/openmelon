# Roadmap

## 0.3 (current)

Interactive TUI release. The `-p` one-shot becomes a full creator workflow.

**Project model**
- `openmelon init`. Per-project state under `<workdir>/.openmelon/` (project.json, characters/, references/, materials/, sessions/, artifacts/). Global registry at `~/.openmelon/`.
- Per-cwd trust list. Subdirectories of trusted dirs auto-trust.
- Per-project + global credentials.json (mode 0600). Resolution order: project → global → env. Auto-generated `.gitignore` excludes credentials and sessions.

**Content libraries**
- `character`, `reference`, `material` add/list/show/rm subcommands.
- `openmelon search` — tag + substring grep across all libraries. Operators: `tag:`, `kind:`, `-negative`, `"quoted phrases"`.

**Runtime**
- Tool-using agent loop driven by `llm.ToolCaller`.
- Builtin tools: list_characters, get_character, list_references, get_reference, search, read_file, compile_skill, generate_image (with reference images), save_artifact, bash, finish.
- Streaming via `llm.StreamingToolCaller`: text deltas live, tool-call deltas reassembled at end of turn, per-turn token usage.
- Multi-turn continuation via `RunInput.History`.

**TUI** (`internal/tui`, bubbletea)
- Single-line input that auto-grows up to 10 lines.
- Slash command palette: Tab autocompletes, Enter executes highlighted.
- Top header: project + active LLM + active image model.
- Spinner row: activity (`Calling search`), elapsed time, running token count.
- Tool calls rendered as `⏺ name(args)` + `⎿ result`.
- `/model` and `/model-image` selectors with curated presets + Custom row. Hot-swaps the live runtime, persists to project.json.
- `/skill` picker — lists `skillplus list --json`.
- `/settings` panel — bash permission mode.
- Mouse wheel + PgUp/PgDn scroll. Ctrl+C ×2 quit. Esc cancels in-flight turn.

**Bash tool**
- 4-tier permission gate: trusted bypass → per-session allowlist → judge LLM (AUTO/ASK/BLOCK) → user modal.
- Approval modal: Yes / Yes-always-for-`<binary>` / No.
- Judge LLM reuses the main agent LLM with a focused classifier prompt.

**Onboarding**
- First-run wizard: trust → provider → API key (masked) → LLM model → image model → project init. Each step skipped when its precondition is met. `openmelon setup` re-runs the auth wizard.

**Sessions and resume**
- Every TUI launch records the conversation and any generated images under `sessions/<ts>-<rnd>/`.
- `openmelon resume [<id>]` lists or loads. The continuation runs in a new session dir; `meta.json` records `resumed_from`.

**Headless `-p` consistency**
- Same credential pipeline, same tool stack, same bash policy as the TUI. Bash requires trusted or auto mode in headless (no modal).

**Reliability**
- Image-gen HTTP uses `DisableKeepAlives` + 3-attempt retry on transient errors (TLS, EOF, connection reset, 5xx).

## 0.4

- Vision auto-describe on `character add` / `reference add` (one LLM call writes the `.search` file).
- Anthropic ToolCaller parity.
- Full untruncated transcript view (`/transcript` or Ctrl+O).
- `edit_image` tool — refine a prior generation as a reference.
- `openmelon serve` — HTTP API for embedding.
- In-TUI skill catalog: `/skill add` / `/skill remove`.

## 0.5

- More image providers (Stability, Replicate, Black Forest Labs direct).
- Markdown rendering in the viewport.
- `@file` mention completion.
- Per-tool token cost tracking + per-session budget.

## 1.0

- Frozen public Go API for embedded use.
- Frozen CLI flags + `project.json` schema.
- LTS policy + deprecation timeline.
