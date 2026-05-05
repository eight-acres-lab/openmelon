# CLAUDE.md — openmelon

Guidance for Claude Code (claude.ai/code) when working in this repo.

## What this is

`openmelon` is a content-creation agent CLI. Three usage modes:

1. **Interactive TUI** — `openmelon` (no args, in a project) drops into a bubbletea REPL with slash commands, palette, model + skill pickers, bash approval modal, session resume.
2. **Headless one-shot** — `openmelon -p "<intent>"`. Runs the same tool stack, just without the TUI. Used by sub-agent integrations and scripts.
3. **Embedded Go library** — `pkg/openmelon` for V-Box backend.

> Repo: https://github.com/eight-acres-lab/openmelon
> Module: `github.com/eight-acres-lab/openmelon`

## Layout

```
cmd/openmelon/
  main.go               subcommand dispatch + legacy flag-based one-shot
  cmd_init.go           openmelon init
  cmd_project.go        project list|use|show|set-key|unset-key|keys
  cmd_registry.go       character / reference / material add|list|show|rm
  cmd_search.go         openmelon search
  cmd_repl.go           runRepl: builds runtime + dispatches TUI vs bufio
  cmd_resume.go         openmelon resume [<id>]
  cmd_setup.go          openmelon setup (re-run auth wizard)
  agent_runtime.go      headless `-p` path inside a project
  publish.go            --publish vbox helper

internal/
  userconfig/           ~/.openmelon/{config,credentials,projects}.json
                        + IsTrusted / ResolveAPIKey (project → global → env)
  projectx/             <workdir>/.openmelon/project.json + Settings + .gitignore
  registry/             characters / references / materials on-disk store
                        (.search files, source-of-truth for description + tags)
  search/               tag + grep, no vectors
  llm/                  pluggable Client (Complete/Stream) + ToolCaller (Chat)
                        + StreamingToolCaller (StreamChat) + Usage tracking
  imagegen/             pluggable Generator with ReferenceImages support
                        + retry on transient TLS / 5xx + DisableKeepAlives
  tools/                tool registry + builtin tools (list_characters,
                        get_character, search, compile_skill, generate_image,
                        save_artifact, bash, finish, ...)
                        + bash judge LLM + per-session allowlist
  runtime/              tool-using agent loop driven by llm.ToolCaller
                        + Tracer interface + History support for multi-turn
  session/              per-run messages.jsonl + meta.json + Recent / LoadHistory
  onboard/              first-run wizards (trust → auth → project init) as
                        ONE alt-screen bubbletea program with state machine
                        + providers.go (public Provider / Preset for /model)
  tui/                  bubbletea TUI: model.go (state machine), tui.go (entry),
                        tracer.go (runtime → tea.Msg bridge), style.go, keys.go,
                        messages.go (per-event tea.Msg types)
  repl/                 bufio fallback REPL for non-tty contexts (CI, pipes)
  skillplus/            subprocess wrapper to the `skillplus` CLI + ListSkills
  agent/                legacy 0.2 one-shot agent (used outside a project)
  artifacts/            legacy artifact write helper
  provenance/           legacy provenance JSONL helper
  project/              legacy 0.1 project.json loader
  workflow/             legacy 0.1 declarative workflow runner
  generation/           legacy 0.1 shell generation provider
  version/

pkg/
  contracts/            public Go types
  openmelon/            public Go API for embedding (Version constant lives here)

npm/                    @e8s/openmelon Node distribution (downloads the binary)
examples/
  food-exploration/     legacy 0.1 declarative example
  integrations/         Skill files for Claude Code, Cursor
assets/                 logo + provenance
docs/                   design notes, testing recipe
scripts/release.sh      tag + build × 4 platforms + GH release + npm publish
```

## Commands

```bash
go build -ldflags "-X github.com/eight-acres-lab/openmelon/internal/version.Version=$(git describe --tags --always)" -o ./openmelon ./cmd/openmelon
go test ./...

# Local install for testing:
go install -ldflags "-X github.com/eight-acres-lab/openmelon/internal/version.Version=v0.x-dev" ./cmd/openmelon
```

## Architecture conventions

- **Module path**: `github.com/eight-acres-lab/openmelon`. Do not reintroduce `github.com/Jackyffight/openmelon`.

- **Dep policy** (per package):
  - `internal/llm`, `internal/imagegen`, `internal/registry`, `internal/projectx`, `internal/userconfig`, `internal/runtime`, `internal/tools`, `internal/session`, `internal/search`: pure stdlib + net/http + encoding/json. No vendor SDKs. No YAML / CLI-parser deps.
  - `internal/tui` and `internal/onboard`: Charm stack allowed (bubbletea, lipgloss, bubbles, textinput, textarea, viewport, spinner, key). These are the canonical Go TUI framework and impossible to replicate in stdlib. Confine to these two packages so the runtime stays light.
  - `cmd/openmelon`: imports anything; orchestrates.

- **Slug rules are uniform.** `projectx.ValidateID` and `registry.ValidateSlug` both require kebab-case `[a-z][a-z0-9-]*`, len 2–64. Material slugs are `m-<hex>` so the hash satisfies the rule.

- **No vendor model defaults baked into source.** Code returns `ErrModelRequired` when no model id is passed. Users get curated preset lists via the auth wizard / `/model` selector (see `internal/onboard/auth.go:providerOptions`); choices are persisted to `project.json:defaults` and `~/.openmelon/config.json:defaults`.

- **Subprocess to skillplus.** Don't reimplement skill compilation in Go. Contract is JSON-in / JSON-out via `internal/skillplus`. `ListSkills` shells `skillplus list --json`.

- **Tool dispatch is synchronous from a worker goroutine.** The bash tool's approval flow uses a reply channel + tea.Msg to bridge into the bubbletea event loop. See `tools/bash.go` + `tui/messages.go:approvalRequestMsg`.

- **The bash tool is gated by 4 tiers**: trusted-mode bypass → per-session allowlist → judge LLM (AUTO/ASK/BLOCK) → user modal. Mode is `project.json:settings.bash_permission_mode` (strict/auto/trusted). Headless `-p` only has the first two tiers (no UI to show modal); strict mode + no allowlist = bash is unavailable, by design.

- **API key resolution order**: project credentials.json → global credentials.json → env var (e.g. `OPENROUTER_API_KEY`). Both TUI and headless `-p` go through `userconfig.ResolveAPIKey(workdir, provider)`.

- **Sessions are append-only.** A new session dir is created per `openmelon` launch (or per `openmelon resume`); the prior dir is never modified. `meta.json` records `resumed_from` for traceability.

- **Streaming**: `llm.StreamingToolCaller.StreamChat` parses SSE, fires `OnText` for each text delta, accumulates tool-call deltas (vendors split function.arguments across many chunks) into a single ToolCall list at the end. `stream_options.include_usage=true` makes the final chunk carry the Usage block.

- **TUI rendering**: viewport content is bottom-anchored when transcript is shorter than the viewport (pad with leading newlines). The textarea auto-grows from 1 line up to 10 as the user types newlines. Active state replaces the input area entirely (running spinner, /settings, /model, /skill, approval modal).

## Adding an LLM provider

1. Implement `llm.Client` (Complete + Stream + Provider + Model). For tool-use support, also implement `ToolCaller.Chat` and ideally `StreamingToolCaller.StreamChat`. Reuse `internal/llm/sse.go` for SSE parsing.
2. Register the constructor in `llm.New` (factory.go).
3. Add a row to `internal/onboard/auth.go:providerOptions` so the auth wizard / `/model` selector know about it.

## Adding an image provider

1. Implement `imagegen.Generator` (Generate + Provider + Model). Honor `GenerateOptions.ReferenceImages`. Use `freshTransport()` and `transientHTTPDo` for the HTTP client (see `internal/imagegen/retry.go`).
2. Register in `imagegen.New` (factory.go).
3. Add to the relevant providerOptions row's `imagePresets`.

## Adding a slash command

1. Append to `slashCommands` in `internal/tui/model.go`.
2. Add a `case "/<name>"` branch in `handleSlash`.
3. If it needs its own state, add a `state*` constant + `update*` + `render*` + an `overlayRows` entry in `recomputeLayout`.

## Versioning

`internal/version/version.go` defaults to `"dev"`. `pkg/openmelon/openmelon.go` exposes the `Version` constant for embedded use. Release builds override via `-ldflags` (see `scripts/release.sh`).
