# CLAUDE.md — openmelon

This file provides guidance to Claude Code (claude.ai/code) when working in this repository.

## What this is

OpenMelon is a **content-creation agent for the terminal** — think "Claude Code, but built for posts". Three usage modes:

1. **Standalone CLI** — `openmelon -p "<intent>"` (working in 0.2-dev today).
2. **Sub-agent via Skill** — registered as a Skill in another agent (Claude Code / Cursor / Codex). The host LLM calls `bash openmelon -p "..."`. No MCP daemon — Skill format is a plain markdown file with frontmatter. (Examples land in 0.3.)
3. **Embedded Go library** — V-Box's backend imports `pkg/openmelon` for content analysis and distribution.

It is intentionally domain-specific: workflows, artifacts, provenance, and Skill-Plus integration are first-class. It is **not** a general-purpose agent framework.

> Repo URL: https://github.com/eight-acres-lab/openmelon
> Go module: `github.com/eight-acres-lab/openmelon`
> See `../../CLAUDE.md` (workspace root) for the full V-Box / Berry / Point Eight context. The V-Box server is in the proprietary workspace (`backend/`, `bcp/`, `berry/`); openmelon is the **public agent**, not the platform.

## Status (today)

The repo is at **0.2-dev** — see [`ROADMAP.md`](ROADMAP.md). What works today:
- One-shot agent loop (`openmelon -p "..."`) with streaming LLM output
- Multi-vendor LLM clients (Anthropic, OpenAI, OpenRouter) with --llm=auto
- OpenAI image generation, with OPENAI_BASE_URL relay support
- Optional `--publish vbox` shells to vbox-cli to upload + post
- Legacy 0.1 workflow runner via `--project project.json`

Do not assume any of the following exist yet:
- Multi-turn conversation / REPL (planned for 0.3)
- TUI scene picker (planned for 0.3)
- HTTP serve mode (planned for 0.3)
- `internal/memory`, `internal/labeling`, `internal/review`, `internal/roles`, `internal/planner` (deleted as hollow stubs; return in 0.4 as real implementations once the agent loop has shaped what their interfaces should be)
- MCP server — considered and explicitly skipped. Skill-based integration is simpler and equivalent for this tool's shape.

## Layout

```
openmelon/
├── README.md, LICENSE (Apache 2.0), CONTRIBUTING.md, SECURITY.md, ...
├── go.mod                     # github.com/eight-acres-lab/openmelon, go 1.22
├── Makefile
├── cmd/openmelon/             # CLI entry (today: workflow runner)
├── internal/
│   ├── project/               # project.json loader + validation
│   ├── workflow/              # workflow / stage execution engine
│   ├── skillplus/             # subprocess adapter to skillplus compiler
│   ├── generation/            # generation provider interface
│   ├── artifacts/             # artifact write
│   └── provenance/            # JSONL provenance
├── pkg/
│   ├── contracts/             # public Go types
│   └── openmelon/             # public Go API for embedding
├── config/                    # example configs
├── examples/
│   └── food-exploration/      # end-to-end working example
└── docs/                      # design docs
```

When 0.3 lands, expect new dirs: `internal/repl/` (bubbletea TUI), `skills/` (Skill files for Claude Code), `examples/integrations/{claude-code,cursor,codex}/`.

## Commands

```bash
go build ./...                                # full build
go test ./...                                 # all tests (~12s)
go run ./cmd/openmelon \                      # run the food-exploration example
  --project examples/food-exploration/project.json \
  --compiler ../skillplus
```

## Conventions

- **Module path**: `github.com/eight-acres-lab/openmelon`. Never reintroduce the old `github.com/Jackyffight/openmelon` path — it was the personal-account remnant from before the org migration.
- **`internal/` is private** to this module per Go convention. Anything other consumers need goes in `pkg/`.
- **Generation providers**: implement `generation.Provider`. Today only `CommandProvider` exists; new providers (e.g. `AnthropicProvider`, `OpenAIProvider`) will live under `internal/clients/` once 0.2 lands.
- **Provenance is mandatory.** Every artifact gets a JSONL line recording skill source hash, compiled output hash, model profile, vars, and timing. Don't bypass `internal/provenance` even for one-off stages.
- **Subprocess to skillplus**: openmelon shells out to the `skillplus` Python compiler via `internal/skillplus`. The contract is JSON-in / JSON-out. Don't try to reimplement compilation in Go — the spec lives in [skillplus](https://github.com/eight-acres-lab/skillplus).

## Adding a new workflow stage

1. Add the stage's contract to `pkg/contracts/`.
2. Implement the stage handler under `internal/workflow/`.
3. If the stage needs a new generation provider, add it under `internal/generation/` (or wait for 0.2 / `internal/clients/`).
4. Add a test under `internal/workflow/` using the existing test fixtures.
5. Update an example under `examples/` to exercise it end-to-end.

## What this repo is **not**

- Not a general-purpose agent framework. The agent loop in 0.2 will be opinionated for content creation (skill compilation, artifact writing, review queues).
- Not a Skill-Plus competitor or alternative. Skill-Plus is the package format; OpenMelon is one consumer.
- Not the V-Box backend. The backend lives in the proprietary workspace; this repo is meant to be embeddable *into* it (and into other content-creation tools).

## Versioning

- 0.1 — current; workflow engine baseline.
- 0.2 — agent loop + multi-vendor model clients + standalone CLI (one-shot + REPL).
- 0.3 — REPL + Skill-based sub-agent integration + HTTP serve mode.
- 0.4 — memory / labeling / review / planner as real modules.
- 0.5 — multimodal (audio + video).
- 1.0 — stable Go API + CLI surface.
