---
name: annotate-review
description: Send a Markdown packet or live site URL to the LAN-hosted Annotate Review Room, wait for a human decision, and feed structured annotations back into the active agent. Use when the user asks to open a visual review, get approval before continuing, review something on the cluster, review a live site, or route feedback back to Claude, Codex, Gemini, Goose, or another CLI agent.
---

# Annotate Review

Use `reviewctl` to open a document or live site in the LAN review room and wait for the human's comments. This is one provider-neutral interaction: the same flow returns feedback to Claude, Codex, Gemini, Goose, or any other invoking agent.

## Open a Markdown review

1. Put the exact review packet in a Markdown file. Include decisions and verification steps that need human judgment; do not dump unrelated logs.
2. Confirm the CLI is available with `command -v reviewctl`. If it is missing, stop and report that this skill needs the `reviewctl` binary from the Annotate repository.
3. Start the single open-and-wait command with a short initial yield:

```bash
reviewctl review --title "Short review title" path/to/review.md
```

## Open a live site review

Use this when the human needs to review the actual rendered SPA instead of a document. The target URL must be allowlisted by the Annotate service; production initially allows only `http://10.0.0.225:3000`.

```bash
reviewctl site --title "AnyRent live review" http://10.0.0.225:3000
```

The CLI opens or prints a per-session proxy URL like `http://<session>.10.0.0.207.nip.io/`. The real site is rendered through Annotate, `annotate.js` is injected into that page, and the reviewer can pin comments across multiple SPA routes before pressing **Send comments to agent** or **Approve and continue**.

## Browser handoff

The CLI opens the system browser when one is attached. In Litewindow it prints the room or site-review URL immediately; give that URL to the user as a clickable link, then keep the same process running. Do not make the user handle a session ID or run another command.

The cockpit-safe endpoint is `http://10.0.0.207`; LAN browsers can use `http://annotate.lan`. Override it only with the user's known endpoint:

```bash
REVIEW_SERVER_URL=http://host reviewctl review path/to/review.md
REVIEW_SERVER_URL=http://host reviewctl site http://10.0.0.225:3000
```

The human pins comments, optionally adds an overall note, and presses **Send comments to agent**. They can instead press **Approve and continue** when no changes are needed.

## Continue from the result

The original `reviewctl review` process returns the result directly:

- `APPROVED`, exit `0`: continue the already-authorized work.
- `FEEDBACK RECEIVED`, exit `3`: this is a normal review result, not a command failure. Read the note and every comment from stdout, apply them, and open another room only if the user asked for another pass.
- An operational error, exit `1` or `2`: report it. Create a new room only if the original document is still current.

Approval does not expand task scope or authorize a destructive action that was not already requested.

## Low-level recovery only

If the open-and-wait process was lost, `reviewctl status <id>` and `reviewctl wait <id>` exist for debugging. Do not expose this plumbing in the normal human flow.

```bash
reviewctl review --no-open path/to/review.md
```

Use `--no-open` only in automation that cannot launch a browser; the URL is still printed.
