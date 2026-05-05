<div align="center">
  <img src="assets/logo.png" alt="OpenMelon" width="160" />
  <h1>OpenMelon</h1>
  <p>A content-creation agent that lives in your terminal.</p>
</div>

```bash
npm i -g @e8s/openmelon @e8s/skillplus
cd ~/work/ai-talks                       # any directory you want as a project
openmelon                                # first run: trust → API key → init project → REPL
```

The TUI then takes over the screen. Type a request, watch the model stream a reply, see each tool call render with its result, type the next request. Multi-turn, no `-p` flag, no manual model id, no env-var dance.

```
openmelon · ai-talks · openrouter:openai/gpt-5.5 · img:openrouter:openai/gpt-5.4-image-2

session 20260506-101203-a1b2c3d4
Type a request and press ↵. /help for commands. Esc cancels a turn; Ctrl+C twice to quit.

> 老王在夜市烤羊肉串，霓虹灯反光

  ⏺ list_characters({"query":"老王"})
    ⎿ [{"slug":"lao-wang","name":"老王",...}]
  ⏺ get_character({"slug":"lao-wang"})
    ⎿ {"image_paths":["/.../portrait.png"],...}

正在生成
  ⏺ generate_image({"prompt":"...","reference_images":["/.../portrait.png"]})
    ⎿ {"path":".../draft-1.png","sha256":"..."}

完成。
⠋ Streaming response · 0:34 · 1.4k in / 312 out · esc to cancel
```

## Why

Most content-creation workflows are death by a thousand chats: stitch a prompt, paste it into a chat UI, paste the result into another tool, lose track of which character looked like what. OpenMelon turns that into one persistent terminal: each project keeps your characters / reference scenes / generated artifacts on disk, and a tool-using agent (your LLM of choice + an image model) drafts inside that context.

It's content's `claude-code` — same tool-loop architecture, same TUI vocabulary (slash commands, palette, approval modal), but pointed at image / multimodal output instead of code.

## Install

```bash
npm install -g @e8s/openmelon @e8s/skillplus
```

The npm package is a Node shim that downloads the matching Go binary from GitHub Releases on install (verified against `SHASUMS256.txt`). To build from source:

```bash
go install github.com/eight-acres-lab/openmelon/cmd/openmelon@latest
```

For `--publish vbox`:

```bash
npm install -g @e8s/vbox-cli
```

## First run

`openmelon` walks you through a Codex-style onboarding the first time:

1. **Trust** — confirm the current directory. Trusted paths persist in `~/.openmelon/config.json:trusted_dirs`; subdirectories auto-trust.
2. **API key** — pick provider (OpenRouter / OpenAI / Anthropic), paste key (masked input). Stored at `~/.openmelon/credentials.json`, mode 0600. Detected env vars (`OPENROUTER_API_KEY` etc.) are offered for re-use.
3. **LLM model** — pick from a curated top-10 (or "Custom…").
4. **Image model** — pick from a curated top-5 (or "Custom…", or skip).
5. **Project init** — if cwd has no project, prompt for id / name / description and write `<workdir>/.openmelon/project.json` + `.gitignore`.

After that every step is skipped silently and `openmelon` drops straight into the TUI.

Re-run any wizard with `openmelon setup` (re-pick provider / key / models). Per-project key overrides via `openmelon project set-key`.

## TUI commands

Inside the TUI, type `/` to bring up the slash command palette:

| Command | What |
|---|---|
| `/skill` | Pick a skillplus package to apply to your next message |
| `/model` | Switch the LLM model for this session (persists to `project.json`) |
| `/model-image` | Switch the image-gen model |
| `/settings` | Bash permission mode (strict / auto-judge / trusted) |
| `/clear` | Forget the conversation history |
| `/history` | Print the message log so far |
| `/save <path>` | Write the conversation to a file (jsonl) |
| `/session` | Show the session directory |
| `/help` | Show this list |
| `/exit` | Exit |

Keys: `↵` submit · `⇧↵` newline · `Esc` cancel turn / dismiss palette · `Ctrl+C` ×2 quit · `↑/↓` scroll · `mouse wheel` scroll.

## CLI subcommands

For non-interactive use:

```bash
openmelon init [<id>]                      Set up cwd as an openmelon project
openmelon project list | use <id> | show   Manage / inspect projects
openmelon project set-key | unset-key      Per-project API key overrides
openmelon project keys                     Show key sources (project / global / none)
openmelon character add <slug> ...         Project character library
openmelon character list | show | rm
openmelon reference add <slug> ...         Project reference-image library
openmelon reference list | show | rm
openmelon material add <path>              Hash-addressed material pool
openmelon search "<query>"                 tag:foo · kind:character · -negative · "phrase"
openmelon setup                            Re-run the auth wizard
openmelon resume [<id>]                    List or load a prior session
openmelon -p "<intent>"                    Headless one-shot (still tool-driven)
```

## Bash tool + permission modes

The agent has a `bash` tool (read files, inspect images, check sizes, etc.). Every call is gated by a four-tier policy:

```
Trusted mode      Run anything, no prompt. Like Claude Code's --dangerously-skip-permissions.
                  Use for throwaway projects only.
Auto-judge mode   Judge LLM auto-runs read-only inspection (file, ls, identify, du, grep, …).
                  Writes prompt; destructive commands (rm -rf, sudo, curl POST off-host) blocked.
Strict (default)  Every bash needs your approval. Judge LLM only auto-blocks destructive ones.
```

The approval modal has three options: `Yes` / `Yes always allow <binary> this session` / `No` — the second one populates a per-session allowlist so e.g. `file *.png` doesn't keep nagging.

Switch modes via `/settings`. Persists to `<project>/.openmelon/project.json:settings.bash_permission_mode`.

## Sessions and resume

Every TUI run records full transcript + tool calls + generated images under `<project>/.openmelon/sessions/<ts>-<rnd>/`. After exit, the shell prints:

```
session saved at /path/to/.openmelon/sessions/20260506-101203-a1b2c3d4
to resume:    openmelon resume 20260506-101203-a1b2c3d4
```

`openmelon resume` (no args) lists the most recent sessions. `openmelon resume <id>` loads its history into a fresh TUI: prior turns render into the transcript, the model sees them as context, and a new session dir is opened to record the continuation (with `resumed_from` recorded in meta.json).

## How it works

Inside a project, openmelon runs a tool-using agent loop. The model sees your project (name, persona, house rules) and a tool box; it decides what to call:

```
list_characters / get_character    pull people from your registry
list_references / get_reference    pull scenes, lighting, composition refs
search                             tag + grep across the project's libraries
read_file                          any file under the project workdir
compile_skill                      compile a skillplus package on demand
generate_image (refs[])            run the image model with optional anchors
save_artifact                      promote a session image to a final
bash (gated)                       inspect files, check outputs, etc.
finish                             end the loop with a summary + artifacts
```

Search is intentionally **not vector**. Every character / reference has a one-line description plus 1–10 kebab-case tags in a `.search` file; queries are tag matches plus substring grep. Per-project corpora are small enough that this is faster and cheaper than embeddings.

Headless `openmelon -p "..."` runs the same tool stack, just without the TUI — useful for scripting or sub-agent integrations. Bash tool requires `/settings` → trusted mode in headless (no approval UI to ask).

## Layout

```
~/.openmelon/
  config.json                      defaults + trusted_dirs
  credentials.json                 mode 0600, per-provider api keys
  projects.json                    project id → workdir registry

<project>/.openmelon/
  project.json                     name, persona, defaults, settings
  credentials.json                 mode 0600, per-project key overrides
  .gitignore                       excludes credentials.json + sessions/
  characters/<slug>/               character.json + .search + portraits
  references/<slug>/               reference.json + .search + image
  materials/<sha-prefix>/          hash-addressed raw inputs
  sessions/<ts>-<rnd>/             messages.jsonl, meta.json, generated images
  artifacts/<slug>/<ts>/           final outputs promoted via save_artifact
```

## Sub-agent integration

`openmelon -p "..."` is invokable from any agent CLI that can run a shell command. Drop-in Skill files for Claude Code and Cursor are in [`examples/integrations/`](examples/integrations/).

## License

[Apache 2.0](LICENSE).
