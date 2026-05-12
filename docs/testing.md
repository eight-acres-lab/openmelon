# End-to-end testing

Three integration paths, pick whichever applies:

| Path | Requires |
|---|---|
| **A**. Interactive TUI | API key for OpenRouter / OpenAI / Anthropic |
| **B**. Headless `-p` + V-Box publish | A's keys + `VBOX_API_KEY` + `vbox-cli` linked locally |
| **C**. Claude Code Skill | A's keys + Claude Code installed |

## Setup

```bash
npm install -g @e8s/openmelon @e8s/skillplus
```

Or build from source (clone first):

```bash
cd path/to/openmelon
go install -ldflags "-X github.com/eight-acres-lab/openmelon/internal/version.Version=$(git describe --tags --always)" ./cmd/openmelon
cd path/to/skillplus && npm link
```

For Path B, also `cd path/to/vbox-cli && npm link`.

Run `openmelon` once in any directory to walk the first-run wizard (trust → API key → LLM model → image model → project init). Credentials land in `~/.openmelon/credentials.json` (mode 0600).

## Path A — Interactive TUI

```bash
cd path/to/your/project
openmelon
```

Inside the TUI:

```
> /skill
[picker → choose food-street-realism]

> Grab a bowl of beef noodles after work, write an authentic restaurant-visit post
```

Expected output in the viewport:

```
  ⏺ compile_skill({"skill":"food-street-realism","locale":"zh-CN"})
    ⎿ {"package":{"id":"food-street-realism",...},"compiled_prompt":"...",...}
  ⏺ generate_image({"prompt":"A handheld phone snapshot inside...",...})
    ⎿ {"path":".../draft-1.png","sha256":"abc123..."}

⠋ Streaming response · 0:24 · 1.4k in / 312 out · esc to cancel
```

Verify the artifact:

```bash
ls -lh outputs/sessions/*/draft-1.png
open outputs/sessions/*/draft-1.png
```

Or ask the agent to inspect — first switch to `auto` mode so read-only bash runs without prompting:

```
> /settings
[switch to "Auto-judge"]
> Open the image and tell me if it looks like a real phone photo
```

Other commands worth exercising: `/model`, `/model-image`, `/clear`, `/save out.jsonl`, `/history`, `/exit`.

After exit, the shell prints a resume command:

```bash
openmelon resume                            # list recent + pick id
openmelon resume 20260506-101203-a1b2c3d4   # load directly
```

The new TUI renders prior turns and continues with full context.

## Path B — Headless `-p` + V-Box publish

`-p` runs the same tool stack without the TUI. Useful for scripts.

```bash
cd path/to/your/project
openmelon -p "Grab a bowl of beef noodles after work, write an authentic restaurant-visit post"
```

Activity logs go to stderr. To publish to V-Box:

```bash
openmelon -p "..." --publish vbox
```

Expected:

```
[openmelon] uploaded → fid=fid_xxx
[openmelon] published. vbox-cli response: {"status":"queued_for_review","content_id":"post_xxx"}
```

`queued_for_review` is expected — the V-Box Review Queue gates publish. Approve it in the V-Box app to go live.

The bash tool needs `/settings → trusted` (or `auto`) when used from `-p` — there's no UI for the approval modal. Set the mode interactively first:

```bash
openmelon                                                   # /settings → Trusted, /exit
openmelon -p "Inspect generated images and report sizes"
```

## Path C — Claude Code Skill

Install the Skills:

```bash
mkdir -p ~/.claude/skills
cp path/to/openmelon/examples/integrations/claude-code/skills/openmelon-create-content.md ~/.claude/skills/
cp path/to/openmelon/examples/integrations/claude-code/skills/openmelon-publish.md       ~/.claude/skills/
```

Claude Code inherits PATH from its launching shell. After `npm install -g @e8s/openmelon`, `which openmelon` should resolve. If you `go install`-ed, ensure `$GOPATH/bin` is on PATH.

Inside Claude Code:

> Use openmelon to create a realistic post about eating beef noodles at an old-neighborhood shop downstairs.

Claude Code matches the Skill, runs `openmelon -p "..."`, and reports the artifact path. Then:

> Now publish that image to my V-Box account.

Claude Code runs `vbox-cli upload ...` then `vbox-cli post --media-fid ...`.

## Smoke checks (no API keys)

```bash
openmelon help                              # CLI loads
mkdir -p /tmp/smoke && cd /tmp/smoke && openmelon   # first-run wizard appears (Esc to abort)
openmelon resume                            # session list works
openmelon search "tag:character"            # empty result on a fresh project
openmelon -p "test"                         # friendly "run openmelon setup" error
```

## Known limitations

- Anthropic provider doesn't yet implement `ToolCaller.Chat`. Picking Anthropic in the wizard works for the legacy `-p --skill` path but the TUI's tool-driven runtime needs OpenAI or OpenRouter. (Closes in 0.4.)
- `compile_skill` shells to `skillplus`; the binary must be on PATH (`npm i -g @e8s/skillplus`).
- No dedicated `edit_image` tool yet — refine by asking the agent to call `generate_image` again with the prior generation as a reference. (0.4.)
