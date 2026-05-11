# Character Consistency

Character consistency is one implementation area inside the broader
[Creative Continuity Architecture](creative-continuity.md).

Within that larger system, character consistency keeps the same person,
mascot, product figure, or recurring visual subject recognizable across
generated artifacts.

OpenMelon already has the first layer of this system:

- `character add/list/show/rm` stores reusable character records under
  `.openmelon/characters/<slug>/`.
- `get_character` returns character metadata and absolute portrait paths.
- `generate_image` accepts `reference_images`, which OpenRouter image
  models can receive as multimodal input.
- The project system prompt tells the agent to search for known
  characters before generation and pass portraits as references.

This document covers only the character-specific layer. The overall
product architecture, including spaces, canon, episodes, assets,
feedback, retrieval, ranking, and long-term planning, is defined in
`docs/creative-continuity.md`.

## Goals

- Reuse registered characters automatically when the user refers to them
  by name, alias, role, or tag.
- Preserve stable identity traits across images: face shape, age range,
  hair, body type, clothing anchors, accessories, and art direction.
- Keep flexible traits editable per request: pose, emotion, lighting,
  location, camera, outfit variants, and scene action.
- Record which character references influenced each generated artifact.
- Support both interactive TUI and headless `openmelon -p` runs through
  the same tool loop.

## Non-Goals

- A face-recognition database or biometric identity system.
- A vector search service in the first implementation. The current grep
  search is enough for small project libraries.
- A separate image-editing workflow. Until `edit_image` exists, retries
  should call `generate_image` again with prior outputs or portraits as
  references.
- Guaranteed vendor parity. Reference-image support depends on the image
  provider and model.

## Current Model

The current registry item shape is deliberately generic:

```text
.openmelon/characters/<slug>/
  character.json
  .search
  portrait-001.png
  portrait-002.png
```

`character.json` stores scalar metadata in `extra`; `.search` stores the
one-line description plus tags; image files are discovered from disk.

This is enough for a lightweight character identity profile if we define
conventions for `extra` keys:

| Key | Meaning |
|---|---|
| `aliases` | Comma-separated names the agent should match. |
| `role` | Narrative role, e.g. `host`, `vendor`, `mascot`. |
| `identity_traits` | Stable visual traits that should not drift. |
| `style_traits` | Stable style anchors, e.g. illustration style or realism level. |
| `avoid_traits` | Traits the agent should avoid introducing. |
| `default_reference` | Preferred image basename when multiple portraits exist. |

The first implementation can keep these as strings in `extra` to avoid a
schema migration. A later schema can promote them to typed fields once the
workflow has settled.

## Runtime Flow

When the user asks for content that may include a known character, the
agent should follow this sequence:

1. Search the project: use `search` with names, aliases, roles, and tags
   from the user request.
2. Fetch exact records: use `get_character` for likely matches.
3. Build the generation prompt with two layers:
   - Stable identity block from the character description and metadata.
   - Scene-specific block from the user's request and selected skill.
4. Pass selected portrait paths to `generate_image.reference_images`.
5. Save the final artifact with enough provenance to recover which
   character slug and reference images were used.

The model should not rely only on prompt text when a registered portrait
exists. The portrait is the strongest available anchor.

## Prompt Contract

Generation prompts should separate identity from scene instructions:

```text
Character identity anchor:
- Character: <name> (<slug>)
- Stable traits: <identity_traits or description>
- Must preserve: face shape, age range, hair, body type, signature accessories.
- Avoid: <avoid_traits>

Scene:
- <user intent and skill output>

Reference images:
- Use the attached portraits as identity anchors, not as exact pose or
  background constraints unless the user asks for that.
```

This contract gives image models a clearer hierarchy: identity is stable;
the scene is variable.

## Artifact Provenance

Generated artifacts should eventually record:

- `characters`: character slugs used in the generation.
- `reference_images`: absolute or project-relative paths used as inputs.
- `identity_prompt`: the stable identity block sent to the image model.
- `scene_prompt`: the scene-specific block.
- `provider` and `model`.
- `session_id` and source tool call.

Today `generate_image` returns path, label, sha256, size, and prompt. The
next implementation should extend session metadata or artifact metadata
without changing the user-facing CLI first.

## Implementation Plan

### Phase 1: Documented Conventions

- Document character `extra` keys and prompt contract.
- Add examples for `character add --update` usage.
- Tighten the project system prompt so the agent explicitly searches by
  alias/tag and distinguishes identity traits from scene changes.

### Phase 2: Selection Helper

- Add a read-only helper that resolves candidate characters from a user
  request using names, aliases, tags, and roles.
- Keep it deterministic and grep-based.
- Return selected portrait paths plus stable identity text.

Possible tool:

```text
resolve_characters({"query":"Lao Wang at the night market"})
```

### Phase 3: Generation Metadata

- Extend generated session records with `characters` and
  `reference_images`.
- Include the same metadata when `save_artifact` promotes an image.
- Make artifact provenance inspectable without replaying the transcript.

### Phase 4: Consistency Review

- Add an optional review step after generation.
- The reviewer compares the requested character profile, reference image
  names, and generated result metadata.
- First version can be text-only and rubric-based. A later version can
  use a vision model when configured.

Review output should be simple:

```json
{
  "consistent": true,
  "issues": [],
  "retry_prompt": ""
}
```

### Phase 5: Guided Retries

- If review fails, call `generate_image` again with the same references
  plus a focused correction prompt.
- Keep retry count small and visible in the session transcript.
- Preserve failed drafts in the session directory for inspection.

## CLI Examples

Register a character:

```bash
openmelon character add lao-wang \
  --name "Lao Wang" \
  --description "Mid-50s street food vendor with a quiet smile, round face, short salt-and-pepper hair, navy apron." \
  --portrait ./refs/lao-wang-front.png \
  --tag character \
  --tag vendor \
  --tag night-market
```

Update the searchable identity description without replacing the
portrait:

```bash
openmelon character add lao-wang --update \
  --description "Mid-50s street food vendor with a quiet smile, round face, short salt-and-pepper hair, navy apron." \
  --tag character \
  --tag vendor \
  --tag night-market
```

Today the CLI does not expose `extra` editing flags, so richer identity
metadata requires editing `character.json` manually. A follow-up can add
typed flags such as `--alias`, `--identity-trait`, and `--avoid-trait`.

## Open Questions

- Should `extra` remain a string map, or should `character.json` gain
  typed character fields before this feature grows?
- Should `default_reference` select one image, or should the agent pass
  all portraits up to a model-specific limit?
- Should consistency review be automatic for every image, or opt-in per
  project setting to control cost?
- Should character identity metadata live in `.search` for discoverability
  or only in `character.json` for structured use?
