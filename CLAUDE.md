# CLAUDE.md — openmelon

Guidance for Claude Code (claude.ai/code) when working in this repo.

## What this is

`openmelon` is a content-creation agent CLI. Three usage modes:

1. **Standalone CLI** — `openmelon -p "<intent>"`
2. **Sub-agent via Skill** — invoked by Claude Code / Cursor / etc. via plain `bash openmelon -p ...`
3. **Embedded Go library** — `pkg/openmelon` for V-Box backend

> Repo: https://github.com/eight-acres-lab/openmelon
> Module: `github.com/eight-acres-lab/openmelon`

## Layout

```
cmd/openmelon/        # CLI entry, subcommand dispatch, publish helper
                      # cmd_init / cmd_project / cmd_registry / cmd_search,
                      # agent_runtime.go (in-project agent), main.go (legacy)
internal/
  userconfig/         # ~/.openmelon/{config.json,projects.json}
  projectx/           # <workdir>/.openmelon/project.json
  registry/           # characters/references/materials on-disk store
  search/             # tag + grep, no vectors
  llm/                # pluggable Client (Complete/Stream) + ToolCaller (Chat)
  imagegen/           # pluggable Generator with ReferenceImages support
  tools/              # tool registry + builtin tool implementations
  runtime/            # tool-using agent loop driven by llm.ToolCaller
  session/            # per-run messages.jsonl + summary.json
  skillplus/          # subprocess wrapper to the `skillplus` console script
  agent/              # legacy 0.2 one-shot agent (used outside a project)
  artifacts/          # legacy artifact write helper
  provenance/         # legacy provenance JSONL helper
  project/            # legacy 0.1 project.json loader
  workflow/           # legacy 0.1 workflow runner
  generation/         # legacy 0.1 shell generation provider
  version/
pkg/
  contracts/          # public Go types
  openmelon/          # public Go API for embedding
npm/                  # @e8s/openmelon Node distribution (downloads the binary)
examples/
  food-exploration/   # legacy 0.1 declarative example
  integrations/       # Skill files for Claude Code, Cursor
assets/               # logo + provenance for the logo
docs/                 # design notes, testing recipe
```

## Commands

```bash
go build -ldflags "-X github.com/eight-acres-lab/openmelon/internal/version.Version=$(git describe --tags --always)" -o ./openmelon ./cmd/openmelon
go test ./...
```

## Conventions

- **Module path**: `github.com/eight-acres-lab/openmelon`. Do not reintroduce `github.com/Jackyffight/openmelon`.
- **Minimal runtime deps in core code.** `internal/llm`, `internal/imagegen`, `internal/registry`, `internal/projectx`, `internal/userconfig`, `internal/runtime`, `internal/tools` stay pure stdlib + net/http + encoding/json. No vendor SDKs. No YAML / CLI-parser deps. The `.search` format is a tiny line-oriented parser inside `registry`.
- **TUI deps are allowed in `internal/tui` only.** The Charm stack (bubbletea, lipgloss, bubbles) is the canonical Go TUI framework and impossible to replicate in stdlib. Confine it to `internal/tui` so the rest of the codebase stays light.
- **No vendor model defaults.** Code returns `ErrModelRequired` when no model id is passed; the user must specify `--llm-model` / `--image-model` (or set `defaults.*_model` in project.json).
- **Subprocess to skillplus.** Don't reimplement skill compilation in Go. Contract is JSON-in / JSON-out via `internal/skillplus`.
- **Streaming is opt-in via `Agent.StreamTo`.** Tests use `Complete` for determinism; `cmd/openmelon` sets `StreamTo = os.Stderr` in legacy agent mode. The runtime loop uses `Chat` (no per-token streaming yet).
- **Slug rules are uniform.** `projectx.ValidateID` and `registry.ValidateSlug` both require kebab-case `[a-z][a-z0-9-]*`, len 2–64. Material slugs are `m-<hex>` so the hash satisfies the rule.
- **Provenance is mandatory in legacy mode.** The runtime path persists everything via session messages.jsonl + summary.json instead.

## Adding an LLM provider

Implement `llm.Client` (`Complete` + `Stream` + `Provider` + `Model`), register in `llm.New`. Reuse `internal/llm/sse.go` for SSE parsing.

## Adding an image provider

Implement `imagegen.Generator` (`Generate` + `Provider` + `Model`), register in `imagegen.New`. The two existing implementations show the two wire shapes (REST images endpoint vs chat-completions with `modalities`).

## Versioning

`internal/version/version.go` defaults to `"dev"`. Release builds override via `-ldflags`. The release script (`scripts/release.sh`) reads `git describe --tags`.
