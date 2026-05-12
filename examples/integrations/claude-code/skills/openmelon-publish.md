---
name: publish-vbox-content
description: Publish a previously generated openmelon artifact to V-Box (upload image + create post). Use ONLY after the user has reviewed a generated artifact and confirmed they want it live on V-Box.
---

# Publish a V-Box post via vbox-cli

You have access to `vbox-cli`, the V-Box terminal client. It uploads media and creates posts via the BCP API. This skill assumes an OpenMelon artifact already exists and the user has approved it.

## When to use this skill

Trigger this when, **after** the user has reviewed an existing OpenMelon artifact, they say something like:

- "publish that to V-Box"
- "post it"
- "ship it"
- "send to my V-Box account"

Do **not** trigger this on the same turn as content creation — always let the user see the image first.

## Prerequisites check

Before invoking, verify:

1. `vbox-cli` is on PATH (`command -v vbox-cli`). If not, tell the user to `npm i -g @e8s/vbox-cli` (or `npm link` from the repo).
2. `VBOX_API_KEY` env var is set. If not, tell the user to mint a key in the V-Box app and `export VBOX_API_KEY=bcp_sk_...`.
3. There's a recent artifact under `outputs/artifacts/**/*.png` or `outputs/sessions/**/*.png`. If multiple, ask the user which one.

## How to invoke

Two-step: upload, then post. Capture the upload's `fid` and feed it into post.

```bash
# 1. Upload
fid=$(vbox-cli upload --file "<path-to-png>" --category image | jq -r .fid)

# 2. Post (text + media attachment)
vbox-cli post \
  --text "<short caption — derive from the user's intent or ask them>" \
  --media-fid "$fid"
```

Alternative (when you've just generated an artifact in the same conversation): re-run the full openmelon flow with `--publish vbox`. That uploads + posts in one call. Only use this shortcut if the user explicitly says "create and publish" together.

## Caption guidance

- Keep it short (1-2 lines). The image carries most of the signal.
- Use the same language as the user's original intent.
- If the user didn't suggest a caption, propose one based on the intent and ask before sending.
- Do NOT use marketing-style language ("amazing!", "must-try!"). The food-street-realism skill is intentionally non-commercial — match the same voice.

## What you'll see

V-Box posts go through a Review Queue first; the response status will be one of:

- `queued_for_review` — **expected** for the gated `post` action. Tell the user to approve in the V-Box app. This is **not** a failure.
- `accepted` — published immediately (only happens for users with auto-approve enabled).
- `rejected` — show the user the error code, don't retry.
- `rate_limited` — wait the indicated duration before retry.

## Failure modes

- **`vbox-cli` not on PATH** — install instructions above.
- **`VBOX_API_KEY` not set** — tell the user to set it; never log the key.
- **upload returns no fid** — the worker rejected the file (size, content type, or moderation). Surface the error to the user; don't retry blindly.
- **post returns rejected** — moderation flagged the text or image. Surface the error code; don't retry without user input.

## Don't

- Don't post without showing the user the artifact first.
- Don't guess captions for non-trivial topics — ask if you're unsure.
- Don't run upload + post in a loop trying to "make it work" — content moderation rejections are intentional, not transient.
