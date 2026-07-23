---
name: annotate-review
description: Send a Markdown plan, proposal, runbook, or code-review summary to the LAN-hosted Annotate Review Room, wait for a human decision, and feed structured annotations back into the active agent. Use when the user asks to open a visual review, get approval before continuing, review something on the cluster, or route feedback back to Claude, Codex, Gemini, Goose, or another CLI agent.
---

# Annotate Review

Use `reviewctl` to put a document in the cluster review room. The human can pin comments directly to the rendered Markdown, request changes, or approve it. Treat the returned decision as the source of truth for the next step.

## Submit

1. Put the exact review packet in a Markdown file. Include decisions and verification steps that need human judgment; do not dump unrelated logs.
2. Confirm the CLI is available with `command -v reviewctl`. If it is missing, stop and report that this skill needs the `reviewctl` binary from the Annotate repository.
3. Submit it:

```bash
reviewctl submit --format json --title "Short review title" path/to/review.md
```

The cockpit-safe default server is `http://10.0.0.207`; LAN browsers can also use `http://annotate.lan`. Override it only with the user's known endpoint:

```bash
REVIEW_SERVER_URL=http://host reviewctl submit --format json path/to/review.md
```

4. Give the user the returned `url` and retain the returned session `id`.

## Receive the decision

For a quick check:

```bash
reviewctl status --format json SESSION_ID
```

To wait in a shell that supports a long-running command:

```bash
reviewctl wait --format json --timeout 30m SESSION_ID
```

Keep the user updated while waiting. A pending review is not approval.

- `approved`: continue the already-authorized plan.
- `changes_requested`: exit code `3`; read stdout despite the non-zero exit, apply the `summary` and every `feedback` item, then submit a new review if the user requested another approval pass.
- Missing or expired session: report it and create a new room only if the original document is still current.

Approval does not expand task scope or authorize a destructive action that was not already requested.

## Agent and hook output

`--format json` is provider-neutral and is the preferred skill interface. The output contains `status`, `summary`, and the full Annotate comment objects in `feedback`.

For a Claude Code `PreToolUse` hook, emit only the hook payload on stdout:

```bash
reviewctl wait --format claude-hook SESSION_ID
```

Approval maps to `permissionDecision: allow`; requested changes map to `deny` with the review summary and annotations in `permissionDecisionReason`. Operational messages and the room URL go to stderr when using `reviewctl review`.

For a single submit-and-wait workflow:

```bash
reviewctl review --format json --title "Release plan" path/to/plan.md
```
