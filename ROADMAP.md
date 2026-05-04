# Roadmap

OpenMelon is being built around three deliverables:
- a **standalone agent CLI** (like Claude Code, but for content creation),
- a **Skill-based sub-agent surface** so other agents (Claude Code, Cursor, Codex) can delegate to it via plain `bash openmelon -p ...` invocation,
- an **embeddable Go library** so V-Box's backend can use it as its content-analysis and distribution engine.

Versions below frame those deliverables in shipping order.

## 0.1 (current) — Workflow engine baseline

What works:

- `project.json` loader with validation (`internal/project`).
- Workflow + stage execution engine (`internal/workflow`).
- Skill-Plus compiler subprocess adapter (`internal/skillplus`).
- Pluggable generation provider with a `CommandProvider` for shell-based generation (`internal/generation`).
- Artifact write + JSONL provenance append.
- Working end-to-end example: `examples/food-exploration/`.

Module path is `github.com/eight-acres-lab/openmelon`. Tests pass on Go 1.22+. No release tag yet.

## 0.2 — Agent loop + builtin tools + standalone CLI

The major shape change. After 0.2, you don't write `project.json` — you talk to the agent.

- `internal/agent/` — multi-turn agent loop with streaming output and tool calling.
- Pluggable model client interface (`internal/clients/`) with first-party implementations:
  - `anthropic` (Claude family)
  - `openai` (GPT family)
  - `google` (Gemini family)
  - `openrouter` (multi-vendor proxy)
  Selection is config-driven; OpenMelon doesn't hard-code a vendor.
- `internal/tools/` — builtin tool catalog the agent can call:
  - `vbox.post`, `vbox.reply`, `vbox.upload` — shell out to `vbox-cli`
  - `skillplus.compile` — shell out to `skillplus`
  - `image.generate`, `web.fetch`, `fs.read`, `fs.write`
- `cmd/openmelon` subcommands:
  - `openmelon -p "<intent>"` — one-shot, like `claude -p`
  - `openmelon` (no args) — interactive REPL
- A bare-minimum `internal/memory` (JSONL on disk) so the agent can carry session state without re-introducing the deleted skeleton modules.
- First release: `v0.2.0` tag → `go install github.com/eight-acres-lab/openmelon/cmd/openmelon@v0.2.0`.

## 0.3 — REPL + Skill-based sub-agent integration

Make OpenMelon comfortable to use directly AND comfortable to delegate to.

- Interactive REPL: `openmelon` (no args) launches a bubbletea TUI with text input, streaming output, and an in-session skill switcher.
- TUI scene picker: when a skill produces multiple `scene_interpretation` candidates, let the user pick before the image-generation step (matches the original food-street-realism workflow).
- `skills/` directory with Claude Code-compatible Skill files that wrap `openmelon -p`. One-line install in Claude Code: `cp openmelon/skills/*.md ~/.claude/skills/`.
- `examples/integrations/{claude-code,cursor,codex}/` end-to-end setup walkthroughs — all Skill-based, no MCP daemon.
- `cmd/openmelon serve` — HTTP API mode for V-Box backend embedding (in addition to direct Go import).

**Why no MCP server?** We considered it. For long-lived stateful tools (databases, issue trackers, telemetry feeds) MCP earns its complexity. For a fire-and-forget content-generation CLI, a Skill file plus plain `bash openmelon -p "..."` is functionally equivalent and an order of magnitude simpler to maintain. Revisitable if a concrete need shows up (e.g., live progress streaming back into the host conversation).

## 0.4 — Memory, labeling, review, planner come back as real modules

The skeleton modules deleted in 0.1 return as actual implementations once the 0.2/0.3 agent loop has informed what the real interfaces should be.

- `internal/memory` — long-term project memory (vector + structured).
- `internal/labeling` — artifact labeling pipeline.
- `internal/review` — human-in-the-loop and automatic review.
- `internal/planner` — multi-step plan synthesis for complex briefs.
- `internal/roles` — persona / character / creator role enforcement across stages.

These are deferred deliberately: writing them before the agent loop locks in interfaces produces hollow code (which is exactly what 0.1 had to delete).

## 0.5 — Multimodal production

- Audio: TTS workflows, podcast scripts, interview transcripts.
- Video: shot lists, storyboards, edit decision lists.
- Cross-modal artifact linking + per-modality review.

## 1.0 — Stable

- Public Go API surface frozen for embedded use.
- Skill format + HTTP API surface frozen.
- CLI flags, config schema, and provenance schema frozen.
- Long-term support policy and deprecation timeline.

## Out of scope

- General-purpose agent framework. OpenMelon is for content creation; if you need a generic agent loop, use Claude Code / OpenAI Agents / etc.
- Hosted SaaS. OpenMelon is self-hosted by design — it lives next to your model credentials.
- A specific model vendor lock-in. Vendor selection is always user-configured.
