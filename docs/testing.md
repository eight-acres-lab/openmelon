# Testing OpenMelon end-to-end

Recipe for verifying the openmelon ↔ skillplus ↔ vbox-cli chain locally before any of the three projects are released.

This doc walks through three integration paths — pick whichever you have credentials for. Each path verifies a different audience:

| Path | Audience | Requires |
|---|---|---|
| **A**. Direct CLI | terminal users, scripts | one of `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` (LLM) + `OPENAI_API_KEY` (image) |
| **B**. Direct CLI + V-Box publish | the full content-production loop | A's keys + `VBOX_API_KEY` + `vbox-cli` linked locally |
| **C**. Claude Code Skill | host-agent integration | A's keys + Claude Code installed |

If you only have an OpenAI key, that's enough — OpenMelon will use GPT for the LLM step and `gpt-image-1` for the image step from the same key.

---

## Setup (once)

### 1. Build openmelon

```bash
cd /Users/zhi/dev/e8s/openmelon
go build -o /tmp/openmelon ./cmd/openmelon
# or, for permanent install:
go install github.com/eight-acres-lab/openmelon/cmd/openmelon
```

### 2. Install skillplus

```bash
# editable install into the workspace venv
/Users/zhi/dev/.venv/bin/pip install -e /Users/zhi/dev/e8s/skillplus

# verify the console script works
PATH=/Users/zhi/dev/.venv/bin:$PATH skillplus --help | head -3
```

### 3. (Path B only) Link vbox-cli locally

```bash
cd /Users/zhi/dev/e8s/vbox-cli
npm link        # exposes `vbox-cli` on PATH using the local checkout

# verify
vbox-cli --version    # should print 0.3.1
```

### 4. Set environment

```bash
# Pick whichever you have. Both work; both is best.
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...

# Path B only
export VBOX_API_KEY=bcp_sk_...

# Optional: route everything through an OpenAI-compatible relay
# (LiteLLM, Helicone, ChatGPT-Plus relay, etc.)
# export OPENAI_BASE_URL=https://your-relay.example.com
```

---

## Path A — direct CLI one-shot

### A.1. The food-noodles demo (the canonical reference)

```bash
PATH=/Users/zhi/dev/.venv/bin:$PATH /tmp/openmelon \
  -p "下班后在老小区楼下吃一碗牛肉面，想发一条真实的探店帖" \
  --skill skillplus:food-street-realism
```

Expected sequence (token-streamed to stderr):

```
[openmelon ...] skill=skillplus:food-street-realism llm=anthropic/claude-sonnet-4-6 image=openai/gpt-image-1
[openmelon] intent: 下班后在老小区楼下吃一碗牛肉面...
{"scene_interpretation":{"camera_position":"...","table_objects":[...],...},"generation_prompt":"...","negative_rules":[...],"failure_modes":[...]}
[openmelon] skill compiled: food-street-realism@0.1.0
[openmelon] generation prompt: A handheld phone snapshot inside a small old-neighborhood beef noodle shop, overhead fluorescent lighting, slight motion blur on the steam rising from a bowl of beef noodles...
[openmelon] image: .openmelon/artifacts/food-street-realism-20260504-203045.png (sha256=abc123def456)
[openmelon] provenance: .openmelon/artifacts/provenance.jsonl
[openmelon] duration: 24.3s
```

### A.2. Verify the artifact

```bash
ls -lh .openmelon/artifacts/
open .openmelon/artifacts/food-street-realism-*.png    # macOS

# inspect the provenance line
tail -1 .openmelon/artifacts/provenance.jsonl | jq
```

### A.3. Variations to try

```bash
# Different intent
/tmp/openmelon -p "周末在大排档吃烧烤的快闪贴" --skill skillplus:food-street-realism

# OpenAI-only (skip Anthropic):
unset ANTHROPIC_API_KEY
/tmp/openmelon -p "..." --skill skillplus:food-street-realism

# Structuring only — preview the prompt before paying for image gen:
/tmp/openmelon -p "..." --skill skillplus:food-street-realism --image=false --json

# Force a different LLM model:
/tmp/openmelon -p "..." --llm-model claude-opus-4-7

# Force a different image model:
/tmp/openmelon -p "..." --image-model dall-e-3
```

### A.4. Things to look for

✅ Streaming JSON appears word-by-word in stderr (not silent for 30s)
✅ Image opens and looks like a real phone snapshot, not a magazine shot
✅ Provenance line includes `skill`, `llm`, `image`, `intent`, `duration_ms`, image `sha256`
✅ Same intent + same skill produces *similar but not identical* output (temperature 0.2 is fairly tight)

❌ If the image looks staged / commercial: the model isn't following the negative rules. Re-run; if persistent, the model_profile prompt overlay needs tuning (file an issue).
❌ If the JSON parsing fails: model didn't follow the schema; check stderr for the raw response.

---

## Path B — direct CLI + V-Box publish

After Path A produces an image you like:

### B.1. One-command create + publish

```bash
PATH=/Users/zhi/dev/.venv/bin:$PATH /tmp/openmelon \
  -p "下班后在老小区楼下吃一碗牛肉面，想发一条真实的探店帖" \
  --skill skillplus:food-street-realism \
  --publish vbox
```

Expected additional lines after the create flow:

```
[openmelon] uploaded → fid=fid_xxx
[openmelon] published. vbox-cli response: {"status":"queued_for_review","content_id":"post_xxx"}
```

`queued_for_review` is **expected** — the post action is gated by V-Box's Review Queue. You approve it in the V-Box app to make it live.

### B.2. Two-step (manual review between)

```bash
# Create
/tmp/openmelon -p "..." --skill skillplus:food-street-realism

# Inspect the image
open .openmelon/artifacts/food-street-realism-*.png

# When happy: upload + post manually
fid=$(vbox-cli upload --file .openmelon/artifacts/food-street-realism-*.png --category image | jq -r .fid)
vbox-cli post --text "下班吃了一碗牛肉面" --media-fid "$fid"
```

### B.3. Things to look for

✅ Upload returns a `fid` starting with `fid_`
✅ Post returns status `queued_for_review` (not `rejected`)
✅ Post appears in the V-Box app's Review Queue, attached to the right image
✅ After approving in-app, the post goes live with the image visible

---

## Path C — Claude Code Skill

### C.1. Install the Skills

```bash
mkdir -p ~/.claude/skills
cp /Users/zhi/dev/e8s/openmelon/examples/integrations/claude-code/skills/openmelon-create-content.md ~/.claude/skills/
cp /Users/zhi/dev/e8s/openmelon/examples/integrations/claude-code/skills/openmelon-publish.md       ~/.claude/skills/
```

### C.2. Verify openmelon is on PATH for Claude Code

Claude Code inherits PATH from the shell that launched it. If you used `go build -o /tmp/openmelon`, either:

- copy the binary somewhere on PATH: `cp /tmp/openmelon ~/bin/openmelon`
- or use `go install`: `go install github.com/eight-acres-lab/openmelon/cmd/openmelon` (puts it in `$GOPATH/bin`)

Same for `skillplus` — make sure the venv's bin is on PATH or pip-install globally.

### C.3. Test from inside Claude Code

In a Claude Code conversation:

> Use openmelon to create a realistic post about eating beef noodles at an old-neighborhood shop downstairs.

Expected: Claude Code reads the Skill, recognizes the intent matches, runs `openmelon -p "..." --skill skillplus:food-street-realism` in its terminal, surfaces the streamed output, and reports the artifact path back into the conversation.

Then:

> Now publish that image to my V-Box account.

Expected: Claude Code reads the publish Skill, runs `vbox-cli upload ...` then `vbox-cli post --media-fid ...`, surfaces the `queued_for_review` response.

### C.4. Things to look for

✅ Claude Code surfaces "I'll use the create-vbox-content skill" or similar
✅ The streamed openmelon output appears in Claude Code's view as it generates
✅ Claude Code reports the image path; you can open it directly
✅ Publishing is a separate decision — Claude Code waits for you to approve before running the publish Skill

❌ If Claude Code doesn't recognize the intent: check `~/.claude/skills/openmelon-create-content.md` exists and the description matches the intent's wording (zh-CN intents may need a Chinese description line — open an issue if so).
❌ If Claude Code can't find the binary: PATH issue. Run `which openmelon skillplus vbox-cli` in the same shell that launched Claude Code.

---

## Smoke checks (no API keys)

If you don't have keys yet, you can still verify the code paths work:

```bash
# Build
go build -o /tmp/openmelon ./cmd/openmelon

# Help text
/tmp/openmelon

# Workflow mode (legacy 0.1) with the bundled food-exploration project
# Note: this needs the skillplus binary on PATH AND a valid relative path
# to the food-street-realism example. The bundled example uses a path
# that assumes the workspace layout — if you've moved things, the
# project.json needs updating.
PATH=/Users/zhi/dev/.venv/bin:$PATH /tmp/openmelon \
  --project examples/food-exploration/project.json \
  --compiler /Users/zhi/dev/e8s/skillplus/src

# Agent mode without keys (verifies the friendly error message)
unset ANTHROPIC_API_KEY OPENAI_API_KEY OPENROUTER_API_KEY
/tmp/openmelon -p "test" --skill skillplus:food-street-realism
# → "llm: --llm=auto could not pick a provider — set one of ..."

# Test the full chain with fake keys (compile succeeds, LLM returns 401)
ANTHROPIC_API_KEY="sk-ant-fake" PATH=/Users/zhi/dev/.venv/bin:$PATH \
  /tmp/openmelon -p "test" --skill skillplus:food-street-realism --image=false
# → reaches Anthropic, returns "HTTP 401: ... invalid x-api-key"
```

---

## Cost ballpark (with real keys)

| Step | Vendor | Approx. cost per run |
|---|---|---|
| LLM structuring (1.1k token system + ~50 token user → ~500 token JSON) | Anthropic Sonnet 4.6 | $0.005 |
| LLM structuring | OpenAI GPT-5 | $0.01-0.02 |
| Image generation (1024×1024) | OpenAI gpt-image-1 | $0.04 |
| **Per run total** | | **$0.05–$0.06** |

20 test runs ≈ $1. Fine for development.

---

## What's not yet testable (defer)

These are 0.3 / 0.4 work, no test recipe yet:

- REPL mode (`openmelon` with no args) — bubbletea TUI not built
- TUI scene picker for multi-candidate `scene_interpretation` outputs
- HTTP serve mode for V-Box backend embedding
- More skill packages (today only food-street-realism; vendor model profiles for openai/google/xai planned)
