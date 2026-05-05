---
name: create-vbox-content
description: Generate a realistic V-Box-style post (image + structured output) from a one-sentence intent using OpenMelon. Use when the user wants to create social content, food-exploration posts, lifestyle photos, or anything described as "a real-feeling post about X". Do NOT use for code generation, debugging, or general Q&A.
---

# Create V-Box content via OpenMelon

`openmelon -p` runs a tool-using agent that pulls characters and references from the project's `.openmelon/` directory, optionally compiles a [skillplus](https://github.com/eight-acres-lab/skillplus) package, and generates an image.

## Invoke

```bash
openmelon -p "<the user's intent verbatim>"
```

If the user explicitly names a skill, append `--skill <bare-slug>` (no `skillplus:` prefix).

## Optional flags

| Flag | Use |
|---|---|
| `--llm-model <id>` | Override the LLM model |
| `--image-model <id>` | Override the image model |
| `--image=false` | Skip image generation (useful for previewing the prompt) |
| `--json` | Print a structured summary on stdout |

Provider, default models, and bash permission mode come from `project.json` and `~/.openmelon/config.json`. Don't pass flags the user didn't ask for.

## Reporting back

After success, tell the user the image path under `.openmelon/sessions/<id>/` and the resume id (`openmelon resume <id>`). If they want to publish, suggest the `publish-vbox-content` skill.

## Common failures

- **No API key** — error says "run `openmelon setup`".
- **`skillplus` not found** — `npm i -g @e8s/skillplus`.
- **Bash unavailable** — project's bash mode is `strict`. Have the user run `openmelon` interactively, set `/settings` to Auto-judge or Trusted, then retry.
- **Content-policy block** — retry with a less ambiguous intent.

## Don't

- Don't add `--publish vbox` here. Publishing is a separate skill so the user can review first.
- Don't summarise the activity log; report the session dir and image path.
