# Agent Runtime Architecture

OpenMelon's runtime is a creative-production agent harness. It borrows
the useful parts of coding agents, but its core contract is creator
workflow continuity: durable spaces, confirmed canon, reusable assets,
episodes, feedback, and model-operated production state.

## Session State

Each run creates `.openmelon/sessions/<id>/`.

- `meta.json` records schema version, project id, workspace root,
  provider/model, intent, and resume lineage.
- `messages.jsonl` records the model conversation and tool results.
- `prompt_history.jsonl` records raw user prompts, including pending
  context typed while the agent is running.
- `events.jsonl` records lifecycle events for model requests, model
  responses, tool calls, tool results, and continuity writes.
- `compactions.jsonl` is reserved for future context compression records.

The message log remains compatible with older sessions. New metadata is
additive so resume can keep working while the schema evolves.

## Lifecycle Hooks

`internal/hooks` defines an in-process lifecycle interface. Multiple
managers can be combined with `ChainManagers`, so session audit,
runtime policies, and future plugin hooks can coexist. Hooks can observe
or gate these events:

- before a model request
- after a model response
- before and after a tool call
- before and after continuity writes

Hooks may deny/cancel an operation, append user-visible feedback for the
next model request, rewrite tool arguments, or rewrite continuity write
payloads. Session hook recording is always installed for interactive
runs, giving each session a durable audit trail without changing the
model-visible message protocol.

## Permission Policy

`internal/policy` is the side-effect gate. It currently covers:

- `bash.execute`
- `continuity.write`

Bash keeps the existing strict/auto/trusted behavior. Continuity writes
are allowed by default, but they now pass through policy and hooks so
future canon promotion, asset mutation, and memory write rules have a
single enforcement point.

Asset weighting is part of the continuity policy surface. The
`update_asset_weight` tool lets the model promote, demote, or archive
assets after user or audience feedback, while keeping the write visible
to hooks and session events.

## Context Selection

Continuity context is selected through `BuildSelectedContextPacket`.
The model can pass a retrieval query and limits for decisions, feedback,
episodes, and assets. The returned packet includes selection metadata:
the active budget, why the context was selected, and which sections were
truncated.

This creates a stable seam for future compaction and retrieval work:
the model does not need every stored item, only the highest-authority and
most relevant context for the current creative intent.

## Memory Promotion

`record_memory_item` stores provisional observations, patterns,
preferences, risks, or open questions. These are intentionally lower
authority than decisions and canon.

`promote_memory_item` converts a provisional item into a confirmed
decision only after explicit user confirmation. This protects long-term
creative memory from model guesses while still letting the model collect
useful weak signals during production.

## Workflow Planning And Compaction

`plan_creator_workflow` gives the model a first step before writing
state. It classifies a request as:

- `new_space`: no matching creative space exists, create a draft and ask
  for confirmation.
- `confirm_space`: a matching draft exists, load context and confirm or
  correct it.
- `continue_space`: an active space matches, load selected context and
  produce.

`record_compaction` stores a reusable summary for a space. Humans can
also run `openmelon space compact <space-id> --draft` to inspect a draft
summary before recording one with `--summary`.

Session events can be inspected with `openmelon session events <id>`.
In the TUI, `/events`, `/space <id>`, and `/compact <id>` provide quick
runtime inspection.

## Markdown Rendering

TUI Markdown rendering is behind a `MarkdownRenderer` interface. The
current implementation is dependency-free and handles the subset the
agent commonly emits: headings, lists, quotes, links, inline code, code
fences, horizontal rules, and simple table fallback.

Both streaming output and finalized assistant messages use the same
renderer so the transcript does not change shape when streaming ends.

## Creator Workflow Parity

`internal/parity` contains product-level regression tests. These tests do
not chase Claude Code feature parity. They lock OpenMelon's own creator
workflow contract:

- new durable topics start as draft spaces
- assumptions do not become canon without confirmation
- active spaces can create durable episodes
- feedback appears in later context packets
- existing spaces are retrieved for continuation requests
- assets are ranked for reuse
- assets can be demoted or promoted after feedback
- selected context respects budget and carries selection reasons
- provisional memory does not become durable guidance until promoted
- workflow planner chooses new/draft/continue modes
- compaction drafts summarize canon, decisions, feedback, assets, and
  recent episodes
- pending input reaches the next model call

This suite should grow with every important creative workflow behavior.
