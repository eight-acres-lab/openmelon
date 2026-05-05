<div align="center">
  <img src="assets/logo.png" alt="OpenMelon" width="160" />
  <h1>OpenMelon</h1>
  <p>A content-creation agent that runs in your terminal.</p>
</div>

```bash
cd ~/work/ai-talks
openmelon init                                                   # set up the project
openmelon character add lao-wang --portrait portraits/lw.png
openmelon reference add kitchen-night --image scenes/kitchen.png
openmelon -p "Lao Wang stir-fries beef noodles at the night kitchen"
```

→ inside a project, the agent searches your character + reference library, fetches the portraits it needs, and passes them as anchor images so the result stays visually consistent across posts. Every turn — model reply, tool call, generated image — is recorded under `.openmelon/sessions/<id>/`.

Outside a project the CLI still runs as a one-shot:

```bash
openmelon -p "下班吃一碗牛肉面" \
  --skill skillplus:food-street-realism \
  --llm openrouter --llm-model openai/gpt-5.5 \
  --image-provider openrouter --image-model google/gemini-2.5-flash-image
```

## Install

```bash
npm install -g @e8s/openmelon @e8s/skillplus
```

`@e8s/openmelon` is a Node shim that fetches the matching Go binary from GitHub Releases on install (verified against `SHASUMS256.txt`). To build from source instead:

```bash
go install github.com/eight-acres-lab/openmelon/cmd/openmelon@latest
```

For `--publish vbox`, also:

```bash
npm install -g @e8s/vbox-cli
```

## Authentication

Set whichever you have. `--llm auto` (default) picks based on what's set, preferring Anthropic.

| Variable | Purpose |
|---|---|
| `ANTHROPIC_API_KEY` | LLM via Anthropic |
| `OPENAI_API_KEY` | LLM and/or image generation via OpenAI |
| `OPENROUTER_API_KEY` | LLM and/or image generation via OpenRouter |
| `OPENAI_BASE_URL` | route OpenAI calls through a relay (LiteLLM, Helicone, etc.) |
| `VBOX_API_KEY` | required for `--publish vbox` |

## Commands

Project lifecycle:

```
openmelon init [<id>] [--name "<name>"] [--description "..."]
openmelon project list | use <id> | show
openmelon character add <slug> --portrait <path> --description "..." --tag <t>
openmelon character list | show <slug> | rm <slug>
openmelon reference add <slug> --image <path> --description "..." --tag <t>
openmelon reference list | show <slug> | rm <slug>
openmelon material add <path> [--tag <t>]
openmelon search "<query>"      # supports tag:foo, kind:character, -negative
```

Generation:

```
openmelon -p "<intent>"                  # in a project: tool-driven runtime
openmelon -p "<intent>" --skill ...      # outside a project: one-shot legacy
openmelon --project <path>               # legacy declarative workflow mode
openmelon help                           # full subcommand list
```

### Common flags

| Flag | Default | Notes |
|---|---|---|
| `-p` | — | one-shot intent. Triggers agent mode. |
| `--skill` | `skillplus:food-street-realism` | `skillplus:<name>` / `path:<dir>` / `<bare path>` |
| `--llm` | `auto` | `auto` / `anthropic` / `openai` / `openrouter` |
| `--llm-model` | — | required. e.g. `openai/gpt-5.5`, `claude-sonnet-4-6`, `x-ai/grok-4` |
| `--image` | `true` | set `--image=false` to skip image generation |
| `--image-provider` | `openai` | `openai` / `openrouter` |
| `--image-model` | — | required when `--image=true`. e.g. `gpt-image-1`, `google/gemini-2.5-flash-image` |
| `--image-size` | vendor default | e.g. `1024x1024`, `1792x1024` |
| `--locale` | `zh-CN` | passed to the skill compiler |
| `--model-profile` | `gpt-image-family` | per-skill prompt overlay |
| `--publish` | — | `vbox` to upload + post via `vbox-cli` |
| `--artifact-dir` | `.openmelon/artifacts` | where images + provenance go |
| `--json` | `false` | also print run summary as JSON to stdout |

`openmelon --help` for the full list.

## How it works

Inside a project, openmelon runs a tool-using agent loop. The model sees your project context (name, persona, house rules) and a tool box, and decides what to call:

```
list_characters / get_character    pull people from your registry
list_references / get_reference    pull scenes, lighting, composition refs
search                              tag + grep across the project's libraries
read_file                           any file under the project workdir
compile_skill                       compile a skillplus package on demand
generate_image (refs[])             run the image model with optional anchors
save_artifact                       promote a session image to a final
finish                              end the loop with a summary + artifacts
```

Search is intentionally not vector. Every character / reference has a one-line description plus 1–10 kebab-case tags in a `.search` file; queries are tag matches plus substring grep. The corpus per project is small enough that this is faster and cheaper than embeddings.

Outside a project (`openmelon -p ... --skill ...`) the legacy one-shot path runs: skillplus compiles a package → LLM produces structured JSON → image model paints from `generation_prompt` → provenance JSONL gets a line.

Layout:

```
~/.openmelon/                        global: config + project registry
<project>/.openmelon/
  project.json                       project name, persona, defaults
  characters/<slug>/                 character.json + .search + portraits
  references/<slug>/                 reference.json + .search + image
  materials/<sha-prefix>/            hash-addressed raw inputs
  sessions/<ts>-<rnd>/               messages.jsonl, generated images
  artifacts/<slug>/<ts>/             final outputs promoted via save_artifact
```

## Sub-agent integration

`openmelon` is invokable from any agent CLI that can run a shell command. Drop-in Skill files for Claude Code and Cursor are in [`examples/integrations/`](examples/integrations/).

## End-to-end testing

See [`docs/testing.md`](docs/testing.md) for the full recipe (direct CLI, `--publish vbox`, Claude Code Skill paths).

## License

[Apache 2.0](LICENSE).
