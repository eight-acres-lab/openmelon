# Creative Continuity Architecture

This document defines the product and technical direction for the next
core iteration of OpenMelon.

OpenMelon should not be a prompt wrapper around LLMs or image models. It
should be an AI-native creator runtime: a workspace where the model leads
creative production, and the system preserves the long-term context that
lets the model keep working across days, episodes, assets, decisions, and
audience feedback.

The goal is not to generate one good asset. The goal is to let a creator
build and operate a durable creative system.

## Problem

Creators do not only need isolated outputs. They need continuity.

Examples:

- a talking-head creator wants the same room, desk, lens, lighting, and
  account voice across hundreds of videos;
- an education creator wants a recurring visual series with stable style,
  characters, scenes, pacing, and lesson structure;
- a brand account wants a mascot, visual language, campaign memory, and
  audience feedback to shape future content;
- an IP creator wants the model to remember the world, the canon, the
  assets, and what has already happened.

Current tools fail in two common ways.

Prompt wrappers generate from the current message and lose project
history. They are easy to replace when the base model improves.

Traditional creative tools manage assets, layers, templates, and
workflows, but the model is only an embedded assistant. The human still
owns most of the orchestration.

OpenMelon's direction is different: the model should own the production
process, while OpenMelon owns durable state, retrieval, confirmation,
provenance, and reusable context.

## Product Thesis

The defensible product is not a better prompt. It is a long-running
creative context that models can operate.

The core asset is the **creative space**: a model-readable production
environment for a series, account, campaign, character universe, or
recurring content format.

The model becomes more capable over time, but it still needs a reliable
external project context:

- what this creator is trying to build;
- what has already been decided;
- what must stay stable;
- what can change per episode;
- which assets are canonical;
- what prior outputs performed well or poorly;
- what the next content should be.

If OpenMelon keeps that context clean, inspectable, and reusable, model
upgrades improve the product instead of replacing it.

## Design Principles

1. **Model-led, not workflow-led.**
   The system gives the model tools and context. The model decides
   whether to clarify, draft, produce, revise, record, or continue.

2. **Durable context over transient prompts.**
   Every useful decision, asset, output, and feedback item should become
   part of the project state when appropriate.

3. **Canon is explicit.**
   A confirmed long-term rule is different from an episode-specific
   preference. The system must preserve that distinction.

4. **Assets are model memory.**
   Images, backgrounds, PSDs, masks, prompts, shot specs, and rejected
   drafts are not just files. They are reusable context units that the
   model can inspect and compose.

5. **Human confirmation at commitment points.**
   The user should not micromanage every step, but the system should ask
   before turning a new preference into long-term canon or replacing a
   canonical asset.

6. **Feedback changes strategy.**
   Audience metrics and user reactions should update future planning,
   pacing, style, and topic selection.

7. **Provenance is required.**
   Outputs must record which space, canon, assets, prompts, model, and
   feedback shaped them. Future models should be able to continue work
   without replaying every session.

## Core Objects

OpenMelon should introduce first-class creative continuity objects. Early
versions can store them as files under `.openmelon/spaces/`.

| Object | Purpose |
|---|---|
| `space` | A long-running creative context, such as a tennis anime lesson series. |
| `assumptions` | Model-generated setup guesses and open questions. Low authority until confirmed. |
| `canon` | Confirmed long-term rules: voice, visual style, structure, constraints. |
| `memory` | Useful project knowledge that is not necessarily a hard rule. |
| `asset` | Reusable context unit: image, background, character, prop, mask, PSD, prompt, spec. |
| `episode` | One production unit: topic, brief, outline, shots, outputs, feedback. |
| `decision` | A user-confirmed choice, with scope and reason. |
| `feedback` | User or audience signal that should affect future work. |
| `plan` | Backlog, arc, topic map, and production schedule. |

### Space

`space.json` should be the structured entry point.

```json
{
  "id": "tennis-anime-lessons",
  "name": "Tennis Anime Lessons",
  "platform": "short-video",
  "audience": "beginner tennis players",
  "status": "active",
  "description": "An anime-style illustrated series teaching ordinary people tennis basics.",
  "created_at": "2026-05-11T00:00:00Z",
  "updated_at": "2026-05-11T00:00:00Z"
}
```

### Canon

Canon is the stable contract the model should obey unless the user
explicitly changes it.

New spaces should not start with filled canon. They should start as
`draft` spaces with `assumptions.md`, then become `active` only after the
user confirms the core direction. The activation itself should be
recorded as a `decision`.

`canon.md` should be optimized for model reading:

```markdown
# Canon

## Voice
- Friendly, direct, lightly humorous.
- Avoid professional coaching jargon unless explained.

## Visual Style
- Clean anime illustration.
- Bright tennis court color palette.
- Exaggerated but readable body movement.

## Episode Structure
- Hook.
- One core concept.
- Visual demonstration.
- Common mistake.
- Closing memory cue.
```

Structured fields can be added later, but Markdown is useful because the
model can read and update it naturally.

### Decisions

`decisions.jsonl` records user-confirmed choices. Each entry should have
scope and weight.

```json
{
  "id": "dec_001",
  "scope": "space",
  "target": "visual_style",
  "decision": "Use clean anime illustration with playful motion lines.",
  "reason": "User chose this over semi-real and chibi options.",
  "weight": 1.0,
  "status": "active",
  "created_at": "2026-05-11T00:00:00Z"
}
```

### Feedback

Feedback is not raw storage. It should be normalized into actionable
signals.

```json
{
  "id": "fb_001",
  "episode_id": "2026-05-12-serving-basics",
  "source": "user",
  "signal": "pace_too_fast",
  "evidence": "Comments say the episode moved too quickly.",
  "recommendation": "Use fewer concepts per episode and add a recap panel.",
  "weight_delta": {
    "episode_structure.slower_pacing": 0.2
  },
  "created_at": "2026-05-13T00:00:00Z"
}
```

## Runtime Loop

Every user request should go through a continuity-aware loop.

1. **Classify intent**
   - New space
   - Continue existing space
   - Modify canon
   - Produce an episode
   - Review prior output
   - Record feedback
   - Plan future content

2. **Retrieve context**
   - Project summary
   - Candidate spaces
   - Active canon
   - Relevant decisions
   - Recent episodes
   - Asset candidates
   - Feedback and performance signals
   - Open plan or backlog

3. **Build a context packet**
   - Stable rules first
   - Current task second
   - Relevant assets and prior outputs third
   - Feedback and strategy fourth
   - Tool instructions last

4. **Let the model choose the next action**
   - Ask clarifying questions
   - Draft options
   - Propose canon changes
   - Produce episode assets
   - Reuse existing assets
   - Record decisions
   - Update plan

5. **Commit state**
   - Save new outputs
   - Record provenance
   - Add or update assets
   - Add decisions only after confirmation
   - Add feedback and derived strategy
   - Update the plan

## Context Packet

The context packet is the main interface between OpenMelon's persistent
state and the model's current reasoning.

It should be compact, ordered, and explicit about authority.

Recommended order:

```text
1. System role and operating method
2. Project summary
3. Active space summary
4. Canon: must-follow long-term rules
5. Current user request
6. Relevant recent episodes
7. Relevant assets and references
8. Feedback-derived strategy
9. Open plan or backlog
10. Available tools
11. Expected response contract
```

Authority matters:

- `canon` has the highest authority below the system prompt.
- `decision` entries explain why canon exists and how strong it is.
- `assumptions` are provisional. They help the model ask better
  questions, but must not be treated as long-term rules until the user
  confirms them.
- `feedback` can suggest changes but should not silently override canon.
- recent episodes provide continuity but may be superseded.
- user messages can modify canon only after explicit confirmation.

## Retrieval and Ranking

The first implementation can use deterministic file-backed retrieval.
Vector search is optional later.

Retrieval should combine:

- slug and title match;
- tag match;
- substring search over summaries and `.search` files;
- recency;
- active status;
- explicit links between episodes, assets, and decisions;
- weight from feedback and user confirmation.

A simple scoring model is enough initially:

```text
score =
  exact_slug_match * 10
  + tag_match * 5
  + substring_match * 2
  + active_bonus * 3
  + recency_bonus
  + feedback_weight
  + explicit_link_bonus * 6
```

### Promote and Demote

OpenMelon needs explicit promotion and demotion mechanics.

Promote when:

- the user confirms an option as the future direction;
- an asset is reused successfully across episodes;
- feedback indicates a format works;
- the model proposes a rule and the user accepts it.

Demote when:

- the user rejects an output;
- an asset causes inconsistency;
- feedback shows a repeated weakness;
- canon changes make an older decision obsolete.

Do not delete historical records by default. Mark them as inactive,
superseded, or low-weight so future models can understand the evolution.

## Asset Reuse

Assets should be stored with metadata that tells the model how to reuse
them.

```json
{
  "id": "asset_court_background_v1",
  "kind": "background",
  "space_id": "tennis-anime-lessons",
  "status": "canonical",
  "description": "Bright outdoor tennis court background used for lesson panels.",
  "reuse_policy": "Use as default court scene unless the episode requires a different location.",
  "files": [
    "image.png",
    "prompt.txt"
  ],
  "tags": ["court", "background", "canonical"],
  "weight": 1.0
}
```

The model should see:

- what the asset is;
- when to use it;
- when not to use it;
- whether it is canonical, experimental, rejected, or archived;
- which prior episodes used it;
- whether feedback supports or weakens it.

For image generation, reusable assets can include reference images,
background-only scenes, character portraits, props, masks, prompt blocks,
and shot specs. OpenMelon should not require heavy image editing to make
these valuable.

## Episode Production

An episode is the unit of long-term content production.

The model should produce or update:

- `brief.md`: what this episode is trying to do;
- `outline.md`: script or narrative structure;
- `shots.json`: visual plan, panels, scenes, assets, and generation specs;
- `assets.json`: assets created or reused;
- `outputs.json`: final artifacts and provenance;
- `feedback.json`: later performance or user reactions.

Example `shots.json`:

```json
[
  {
    "id": "shot_001",
    "purpose": "Hook: why serving matters",
    "scene": "canonical tennis court",
    "characters": ["coach", "beginner-student"],
    "assets": ["asset_court_background_v1"],
    "visual_prompt": "Clean anime panel of the coach pointing at the service box...",
    "continuity_constraints": [
      "use canonical coach design",
      "use default court background",
      "keep playful motion lines"
    ]
  }
]
```

This lets future runs understand what was produced without rereading the
entire chat transcript.

## Long-Term Planning

A creative space should maintain a plan.

The plan is not a fixed calendar. It is a living content map:

- arcs;
- topic backlog;
- completed episodes;
- skipped topics;
- high-performing patterns;
- weak patterns to avoid;
- next suggested episode;
- unresolved creative questions.

When the user says "continue the streak", the model should retrieve this
plan, choose a suitable next topic, and produce without asking the user
to restate the series context.

When feedback arrives, the plan should change. For example:

- if pacing is too fast, future topics become smaller;
- if a character performs well, the character gets more appearances;
- if a visual style underperforms, it is demoted or revised;
- if a topic cluster performs well, the backlog expands around it.

## Model Response Contracts

OpenMelon should ask the model for explicit structured decisions, not
only prose.

For early versions, the model can return a human-readable summary plus a
machine-readable action block:

```json
{
  "mode": "continue_space",
  "space_id": "tennis-anime-lessons",
  "needs_confirmation": true,
  "question": "Should the more humorous tone become a permanent rule?",
  "proposed_updates": [
    {
      "type": "canon_change",
      "target": "voice",
      "text": "Use more light humor in lesson examples."
    }
  ],
  "next_actions": [
    "draft_episode_outline",
    "reuse_canonical_assets"
  ]
}
```

Expected feedback from the model:

- what it thinks the request is;
- which space it selected or whether a new one is needed;
- what context it used;
- what assets it plans to reuse;
- what should be confirmed by the user;
- what it will write back to project state.

This makes the agent inspectable and prevents silent drift.

## Tool Direction

The model should operate through small tools that persist state.

Candidate tools:

```text
list_spaces
get_space
create_space
activate_space
search_continuity
get_context_packet
update_canon
record_decision
record_feedback
create_episode
update_episode
list_episodes
resolve_assets
register_asset
link_asset
promote_asset
demote_asset
update_plan
finish
```

Tools should not force a rigid workflow. They should make the model's
chosen workflow durable and auditable.

`create_space` creates a draft space and writes provisional
`assumptions.md`. It must not write long-term canon. `activate_space`
requires an explicit user-confirmed decision and moves the space into
`active` status. Durable episodes should only be created after
activation.

## Disk Layout

Initial layout:

```text
.openmelon/
  spaces/
    tennis-anime-lessons/
      space.json
      assumptions.md
      canon.md
      memory.md
      decisions.jsonl
      feedback.jsonl
      plan.md
      episodes/
        2026-05-11-basic-tennis/
          episode.json
          brief.md
          outline.md
          shots.json
          assets.json
          outputs.json
      assets/
        court-background-v1/
          asset.json
          image.png
          prompt.txt
        coach-character-v1/
          asset.json
          portrait.png
          prompt.txt
```

This can coexist with today's project layout:

```text
.openmelon/
  characters/
  references/
  materials/
  sessions/
  artifacts/
```

Existing registry items can be linked from space assets instead of moved.

## Pain Points Solved

OpenMelon should solve these problems:

- **Context reset**: creators should not explain the same series every day.
- **Visual drift**: style, scenes, characters, and assets should remain
  stable unless intentionally changed.
- **Workflow drift**: each episode should follow the confirmed production
  method for that space.
- **Prompt fragility**: reusable context should be stored as project
  state, not hidden in one long prompt.
- **Model replaceability**: the value lives in the evolving creative
  space, not in a thin call to one model.
- **Unclear feedback loop**: user and audience feedback should become
  future strategy.
- **Lost provenance**: future runs should know why an asset exists and
  how it was made.

## First Implementation Boundary

The first implementation focuses on process control and durable
state. It should not start with heavy image processing.

Built first:

- file-backed spaces;
- canon and memory files;
- decision and feedback logs;
- episode records;
- asset manifests;
- continuity retrieval;
- context packet builder;
- tools to create spaces, record decisions and feedback, create episodes,
  register assets, and fetch context packets.

Still to build:

- model response contract;
- promote/demote tools;
- plan update tools;
- richer context-packet summarization.

Defer:

- vector database;
- face recognition;
- custom image diffing;
- heavy PSD editing;
- timeline editors;
- full automation without confirmation.

The minimum viable product is a model-led creative space that can be
created, confirmed, reused, updated, and continued across multiple days.

## Success Criteria

The architecture is working when:

- a first-time user can create a new series through model-led questions;
- the system records the confirmed style, structure, characters, scenes,
  and assets;
- the next run can continue the series from a short user instruction;
- the model can explain what context it used and what it reused;
- user feedback changes future production;
- old outputs remain inspectable through episode and asset provenance;
- a stronger future model can pick up the same space and produce better
  work without requiring the user to rebuild context.
