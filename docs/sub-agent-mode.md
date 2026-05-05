# Sub-agent mode

OpenMelon runs as a sub-agent inside any host that can shell out to a CLI:

```bash
openmelon -p "<intent>"
```

Headless `-p` shares the interactive TUI's tool stack:

- Same project context (`<workdir>/.openmelon/project.json` plus characters, references, materials).
- Same builtin tools (`list_characters`, `get_character`, `search`, `compile_skill`, `generate_image`, `save_artifact`, `bash`, `finish`).
- Same credential resolution (project → global → env).
- Same bash permission policy (`project.json:settings.bash_permission_mode`).

There's no UI for approval, so the bash tool is unavailable in `strict` mode. To run shell from `-p`, set the project's mode to `trusted` or `auto` first via the TUI's `/settings`.

## What the host receives

Stderr is the activity log. With `--json`, stdout is a single JSON line summarising the run: skill id + version, intent, generation prompt, image path, sha256, timestamps. The full transcript and generated artifacts live under `<project>/.openmelon/sessions/<id>/`.

## Integrations

Skill files for Claude Code and Cursor are in [`examples/integrations/`](../examples/integrations/). They shell to `openmelon -p "$intent"`.
