# OpenMelon — V-Box content creation tool

When the user asks for a realistic social-media post, food-exploration content, "real-feeling" image, or anything described in terms of "a post about X for V-Box / Xiaohongshu", delegate to the `openmelon` CLI in the integrated terminal.

## Invocation

Single command. Pass the user's intent verbatim:

```bash
openmelon -p "<the user's intent>" \
  --skill skillplus:food-street-realism
```

Available skills today:
- `skillplus:food-street-realism` — street-food / shop-visit / "phone snapshot" posts.

## Required env

One of `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` for the LLM step, plus `OPENAI_API_KEY` for image generation. If only OpenAI is set, OpenMelon uses GPT + gpt-image-1 from one key.

## Output

The command streams the LLM's structured JSON to stderr while it generates, then writes:
- `outputs/artifacts/<skill>-<timestamp>.png` — the image
- `outputs/artifacts/provenance.jsonl` — append-only record of the run

Show the user the image path; quote 1-2 sentences of the `generation_prompt` so they see what the skill produced. Don't dump the full streamed JSON.

## When NOT to use

- Code generation
- Generic Q&A
- Raw image generation without a "skill-as-filter" wrapper (the user can call OpenAI directly for that)
- Publishing — for that, run a separate `vbox-cli upload` then `vbox-cli post --media-fid`. Always let the user review the image first.

## Failure modes

- "no API key" — tell the user to set `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`.
- "`skillplus` not found" — `pip install skillplus`.
- Image gen timeout — `gpt-image-1` can take 30-90s; default timeout is 5 minutes.
- Content-policy block — re-run with a less ambiguous intent; don't force.
