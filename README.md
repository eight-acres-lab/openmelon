# OpenMelon

**Content-creation agent for the terminal — like Claude Code, but built for posts.**

OpenMelon is a content-production agent and runtime by [Point Eight AI](https://pointeight.ai). You can use it three ways:

1. **Standalone CLI** — `openmelon -p "write a Singapore food street post"` — talks to the model, picks the right [Skill-Plus](https://github.com/eight-acres-lab/skillplus) package, runs it, prints (or publishes) the result.
2. **Sub-agent via Skill** — drop a Skill file in your existing agent (Claude Code, Cursor, Codex…) so the host LLM knows when to delegate creation work to `openmelon -p`. Plain `bash` invocation, no daemon.
3. **Embedded Go library** — V-Box's own backend imports OpenMelon as the agent engine for content analysis and distribution. (The embedding contract is the `pkg/openmelon` Go package.)

OpenMelon is opinionated: it is built for content creation, not as a general-purpose agent framework.

## Status

**0.2-dev — agent loop is live**, plus the legacy 0.1 workflow engine still works. Today: `openmelon -p "<intent>"` compiles a Skill-Plus package, sends it to your chosen LLM (Anthropic / OpenAI / OpenRouter), parses the structured response, generates an image via OpenAI's image API, writes artifacts + provenance JSONL, and optionally publishes to V-Box via `vbox-cli`. LLM output streams to stderr token-by-token. What's not in yet: REPL mode, multi-scene picker, V-Box backend embedding. See [`ROADMAP.md`](ROADMAP.md).

## Try the food-exploration example today

You need [`skillplus`](https://github.com/eight-acres-lab/skillplus) installed (or its source tree adjacent to this one):

```bash
# from this repo's root
go run ./cmd/openmelon \
  --project examples/food-exploration/project.json \
  --compiler ../skillplus
```

That produces a JSON artifact under `artifacts/` plus a provenance JSONL line.

## Agent mode (0.2-dev)

```bash
go build -o openmelon ./cmd/openmelon

# one-shot — picks an LLM provider automatically based on which
# *_API_KEY you have set
openmelon -p "Singapore 牛车水夜市的食物街快闪贴" \
  --skill skillplus:food-street-realism

# with everything wired through to V-Box
openmelon -p "..." --skill skillplus:food-street-realism --publish vbox
```

### Model providers

The structuring step (intent + skill → structured generation prompt) and the image generation step are each pluggable. By default, OpenMelon picks based on which API keys it finds in your environment.

**One-key paths** — pick whichever you have:

```bash
# OpenAI only — LLM via gpt-5, image via gpt-image-1
export OPENAI_API_KEY=sk-...
openmelon -p "..."           # --llm defaults to "auto" → openai

# Anthropic only — LLM via claude-sonnet-4-6, image still via OpenAI
# (Claude doesn't generate images; pass --image=false to skip image gen)
export ANTHROPIC_API_KEY=sk-ant-...
openmelon -p "..." --image=false

# Both — Anthropic wins for the LLM step (best at structured JSON);
# OpenAI handles image gen
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
openmelon -p "..."
```

**OpenAI-compatible relays / proxies** — set `OPENAI_BASE_URL` (host only, no `/v1`):

```bash
# LiteLLM, Helicone, ChatGPT-Plus relay services, vLLM, LM Studio,
# any OpenAI-compatible endpoint
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=https://your-relay.example.com
openmelon -p "..."           # both LLM and image hit your relay
```

The same flag exists per-step for finer control: `--llm-base-url` and `--image-base-url`. OpenRouter is a first-party LLM provider (`--llm openrouter` / `OPENROUTER_API_KEY`) for cross-vendor routing.

### Coming in 0.3

- `openmelon` (no args) — interactive REPL with bubbletea TUI
- TUI scene picker (the food-street-realism schema produces multiple `scene_interpretation` candidates; today the agent runs one)
- `openmelon serve` — HTTP API for V-Box backend embedding
- `openmelon batch` — process multiple intents from a file in one run

(Sub-agent integration uses plain Skill files — see `examples/integrations/`. We considered MCP but for a fire-and-forget content-generation tool, Skill-driven CLI invocation is simpler with no real loss in capability.)

## Architecture (today)

```
project.json                              ←── declarative workflow input
    │
    ▼
internal/project           internal/workflow
   load + validate    →    iterate stages
                                │
                                ▼
                       internal/skillplus      ←── shells out to `skillplus` compiler
                                │
                                ▼
                       internal/generation     ←── pluggable provider (today: command exec)
                                │
                                ▼
                       internal/artifacts      ←── write artifact
                       internal/provenance     ←── append JSONL provenance line
```

In 0.2, the agent loop sits in front of `project.json` (you don't have to write one) and the `generation` provider grows real model clients.

## Repository layout

```text
├── cmd/openmelon/        # CLI entrypoint (today: workflow runner; 0.2: agent loop)
├── internal/
│   ├── project/          # project.json loader + validation
│   ├── workflow/         # workflow / stage execution engine
│   ├── skillplus/        # subprocess adapter to the skillplus compiler
│   ├── generation/       # generation provider interface (CommandProvider today)
│   ├── artifacts/        # artifact write
│   └── provenance/       # provenance JSONL append
├── pkg/
│   ├── contracts/        # public Go types — the embedding contract
│   └── openmelon/        # public Go API for embedded use
├── config/               # example configs
├── examples/             # food-exploration end-to-end example
└── docs/                 # design notes
```

Modules that exist in the spec but are **deferred to 0.4**: `memory`, `labeling`, `review`, `roles`, `planner`. They were previously empty skeleton files; we deleted them rather than ship hollow placeholders. The 0.2 agent loop will use the simplest possible JSONL-on-disk substitute until those come back as real implementations.

## Where this fits in the e8s ecosystem

| Repo | Role |
|---|---|
| **[vbox-cli](https://github.com/eight-acres-lab/vbox-cli)** | V-Box terminal client — OpenMelon calls this as a builtin tool to publish |
| **[openmelon](https://github.com/eight-acres-lab/openmelon)** (this) | Content-creation agent — orchestrates skill compile + generation + publish |
| **[skillplus](https://github.com/eight-acres-lab/skillplus)** | Compilable skill packages — OpenMelon's "skills" come from here |

End-to-end story: OpenMelon receives a creation intent → picks a skillplus package → compiles it → runs the resulting stages with a model client → publishes the result via vbox-cli. Each piece is independently usable.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`GOVERNANCE.md`](GOVERNANCE.md). RFC process for protocol/contract changes in [`RFC.md`](RFC.md). Code of Conduct in [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md). Security disclosures via [GitHub security advisories](https://github.com/eight-acres-lab/openmelon/security/advisories/new).

## License

[Apache 2.0](LICENSE).
