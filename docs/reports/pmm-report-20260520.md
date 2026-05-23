# sharedwatch — PMM Report

Date: 2026-05-20 (UTC)
Source ingested: `sharedwatch/` (full source + docs)
Author of this report: independent re-read; treats the project's self-claims as input, not ground truth.
Audience: PMM evaluating whether to open this up, how to position it, and what to commit to externally.

---

## 1. Elevator
sharedwatch is a calm collaboration bus for a single shared folder: it captures file changes durably the moment they happen, and lets a human (or an assistant acting for them) review them on their own schedule — every 5–10 minutes by default, near-real-time on demand — instead of being interrupted in the middle of other work.

## 2. Problem statement (the JTBD)
A shared folder between two collaborators (e.g. John + Sarah, or a user + an agent) is useful precisely because activity in it carries signal. But consumed raw, that signal:
- arrives at random times,
- arrives in bursts (editors save 5× per second),
- preempts whatever the consumer is doing,
- and is lossy across restarts (no durable history).

The "job to be done": **let me stay aware of what changed in our shared folder without it derailing me, and without me missing anything important when I check back later.**

## 3. Users & roles
| Role | Who | What they need from sharedwatch |
|---|---|---|
| Primary consumer | John (the reviewer) | Pull-based digests on his own cadence; ability to "go fast" during active collaboration |
| Operator | Sarah | A boring, restart-safe service; readable docs; one-line status |
| Approver / owner | Mike | Confidence the design holds the line ("queue-first, not interrupt-first"); explicit policy decisions recorded |

These role assignments are stated in `docs/PMM_REPORT.md` and `docs/JOHN_HANDOFF.md` and are consistent through the code (e.g. `Source = "watcher" | "reconciler" | "test"` matches the spec's source enum).

## 4. Value proposition (positioning)
**Headline:** *Calm awareness of a shared drive. Capture instantly. Consume calmly.*

**Three claims it can credibly make:**
1. **Durability over elegance.** Every change is written to SQLite before any human reviews it; restarts don't lose state. (Verified in code: `runtime_state` table is upserted on every meaningful transition; events persist with explicit lifecycle.)
2. **Pull, not push.** The system never injects events into the consumer's runtime; the consumer reads digests on purpose. (Verified: there is literally no outbound notification path in the code — the only delivery is the `digest list/show` CLI.)
3. **Two-speed.** Default is calm 5–10 min review; active mode is on-demand with TTL and auto-extends when collaboration continues. (Verified: `mode.Runtime.EffectiveMode` honours `ActiveUntil`; `app.ConsumeNow` extends TTL on each successful consume while active.)

**What it does NOT claim, and shouldn't:**
- It is not a sync tool (does not move files).
- It is not multi-host.
- It is not a chat or notification system.
- It is not a content-diff engine — it sees create/modify/delete/rename, not the bytes.

## 5. Use situations (when to reach for this)

| Situation | Fit | Why |
|---|---|---|
| Two collaborators sharing one local folder, one wants ambient awareness | **Strong** | Exactly the design center |
| A human + a long-running agent that mutates files in a shared workspace | **Strong** | Agent can produce; human checks via `digest list` on their cadence |
| CI pipeline producing artifacts a reviewer skims later | **Moderate** | Works; lacks per-artifact metadata |
| Many writers, network share, multiple hosts | **Poor** | Single-host SQLite; no leader election |
| High-volume logging / millions of events | **Poor** | Polling + per-event row + snapshot-per-tick |
| Push notifications into chat | **Out of scope** | Spec explicitly excludes; would violate queue-first principle |

## 6. Funnel / activation surface (PMM lens)

How a new user actually gets to "I see value":

```
hear about it → clone repo → ./install.sh → ./.bin/sharedwatch run
                                                  │
                                                  ├── sharedwatch status      (proves it's alive)
                                                  ├── (edit a file in /shared)
                                                  ├── sharedwatch consume     (creates a digest)
                                                  └── sharedwatch digest list (sees the digest)
```

**Time to first digest, in best case:** < 60 seconds.

**Real-world activation friction (today, as ingested):**
- `./install.sh` fails on hosts where the Go module proxy can't be reached (Sarah hit `tls: failed to verify certificate` in `AUDIT-2026-03-18.md`).
- The default `WatchPath` is hardcoded to `/home/node/.openclaw/shared`. A new user must either create that path or edit source — there is no `--watch-path` flag and `config.yaml` is not actually loaded (engineering report B2).
- The build will not currently complete due to an Adapter-interface mismatch (engineering report B1).

So the **PMM-visible activation reality is: time to first digest is currently ∞** for an external user who follows the README. That has to be fixed before this is positioned externally.

## 7. Competitive / category framing
This is *not* in the file-sync category (rsync, Syncthing, Dropbox). It is in a niche that doesn't have a strong category name yet — closest analogues:

| Adjacent thing | How sharedwatch differs |
|---|---|
| `inotifywait` / `fswatch` | Those are raw event firehoses; sharedwatch is a durable, debounced, batched, pull-based digest layer on top of "watch a folder" |
| Logrotate + `tail -f` | Logging is push and stream-oriented; sharedwatch is pull and event-oriented |
| Activity feed in Google Drive / Dropbox web UI | Same conceptual layer, but local-first, single-host, scriptable, no auth |
| Filesystem-event SDKs (Watchman, chokidar) | Those are libraries; sharedwatch is a service with a durable queue and a human-friendly review surface |

**Positioning sentence for a launch page:**
*"A small, durable, pull-based activity feed for a local shared folder."*

## 8. Pricing / model implications (if any)
Single binary, single-host, SQLite, MIT-style permissive — there is no SaaS or paid surface implied by the design. If opened up, the natural model is:
- OSS core (this repo).
- Optional later: a hosted dashboard that reads a remote SQLite over SSH, or pushes digests to Slack/email. **Both should be additive layers, not changes to the core**, to preserve the "queue-first" guarantee.

## 9. Strengths (PMM-visible)
- **Story is sharp and defensible.** "Capture instantly, consume calmly" is a one-line story a developer immediately understands.
- **Design principles are written down and enforced by the code shape.** Queue-first isn't a slogan in this repo; it's the only path the bytes can take.
- **Docs are reader-friendly.** `JOHN_HANDOFF.md` is literally a product-intent doc; `OPERATOR_GUIDE.md` is a runbook; `PMM_REPORT.md` already exists as a project artifact. Open-sourcing this gets free narrative content.
- **Honest self-assessment.** `AUDIT-2026-03-18.md` is unusually candid for a project handoff — that builds external trust if published as-is.
- **Schema is documented and stable.** `docs/SCHEMA_CONTRACTS.md` matches what the DB actually creates.

## 10. Risks (PMM-visible)
- **Truth-in-marketing risk.** `README.md` says "implemented in source, pending runtime verification." That is technically true but currently understates two real bugs (engineering report B1 will not compile; B2 config file is unused). If we publish externally without fixing these, the first user will catch them — and the calm, careful narrative will look performative.
- **Build supply chain.** `POLICY-2026-03-18.md` allowlists `proxy.golang.org` + `sum.golang.org`. That is correct and auditable. But the original blocker (TLS verification fetching `modernc.org/sqlite`) is not yet resolved. We cannot ship "it builds" until we prove it builds on a clean host.
- **No LICENSE in the repo.** Cannot open-source legally without one. Likely choice: Apache-2.0 (patent grant, common in infra OSS) or MIT (minimum surface). PMM should align with Mike before publishing.
- **Naming.** `sharedwatch` is unclaimed and descriptive but also generic. Worth a five-minute trademark / GitHub-org search.
- **`/home/node/.openclaw/shared` is OpenClaw-specific in code and docs.** External users will not have that path. A pre-release pass should genericize defaults to something like `~/.local/share/sharedwatch/watch`.
- **No telemetry plan.** If we want to learn how this is used post-launch, decide explicitly: ship with no telemetry (the privacy-first story matches the calm narrative), or design opt-in metrics. Default recommendation: **no telemetry**, advertise that.
- **Active mode TTL is a UX cliff.** When TTL expires mid-collaboration, the consumer cadence silently slows from seconds to ten minutes. Worth a CLI warning or a status field that surfaces "active mode expires in 3m". Today, `status` shows `active_until` but doesn't compute "expires in".

## 11. Spec-fit, restated for PMM

The spec ([`../specs/shared-drive-watcher-spec.md`](../specs/shared-drive-watcher-spec.md)) gives a checklist of v1 success criteria. Tracked literally:

| Spec line | Status in code |
|---|---|
| "a new or changed file gets recorded reliably" | Implemented (polling diff) |
| "events land in a durable queue" | Implemented (SQLite events table) |
| "John can review a digest without being interrupted mid-task" | Implemented (pull-only CLI) |
| "active collaboration mode allows much faster review" | Implemented + auto-extends |
| "a heartbeat/reconciliation pass catches missed changes eventually" | Implemented (30m reconcile) |
| "the system is simple enough for Sarah to maintain comfortably in Go" | True from the source shape |

So **at the spec/PMM-claim level, the product is feature-complete for v1.** The gap is operational: build, config, observability, OSS hygiene. That is a meaningfully different message than "the product isn't done", and PMM should not conflate the two.

## 12. Pre-launch open questions for PMM/Mike
1. **What is the launch surface?** GitHub repo only? README + a blog post? Hacker News post? Internal-only first?
2. **What LICENSE?** Apache-2.0 vs MIT.
3. **What is the public default for `WatchPath`?** OpenClaw-specific paths must be removed.
4. **Telemetry posture — confirm: none.**
5. **Do we promise binary releases (GoReleaser → tagged GitHub releases) or source-only?** Source-only is simpler; binaries are a real-world UX win.
6. **Do we open-source the policy / audit docs too** (`POLICY-2026-03-18.md`, `AUDIT-2026-03-18.md`, `tasklist-2026-03-18.md`)? Recommendation: yes, with light editing — the honesty is the differentiator.
7. **What is the supported platform set?** Linux only is the truthful answer for v1 given polling-snapshot semantics.

## 13. Recommendation
**Conditional go.** The design and narrative are open-source-grade. The artifact is not — yet — but the gap is small (3–5 engineer-days per the engineering report) and well-scoped.

Open this up only after:
1. The build compiles end-to-end on a clean Linux host with `./install.sh`.
2. `config.yaml` is honored (or removed from the repo).
3. A LICENSE is in the tree.
4. Defaults are de-OpenClaw'd.
5. A 60-second "Quickstart" section is on top of the README.

If those five are done, the PMM-visible story holds together. If we ship without them, the first user will write a louder, less flattering version of the engineering report.

## 14. Suggested external-facing messaging (drafts)

**One-liner:** *Calm, durable, pull-based activity feed for a local shared folder.*

**Tagline option A:** *Capture instantly. Consume calmly.*
**Tagline option B:** *A shared folder that doesn't interrupt you.*
**Tagline option C:** *The quiet half of file watching.*

**Three bullets for a README hero:**
- Every change in your shared folder, captured the moment it happens and stored durably in SQLite.
- Review on your cadence — 5–10 minute digests by default, near-real-time on demand.
- One Go binary. One folder. No daemons to babysit. No notifications to mute.

**What to leave out of launch copy:**
- "Production-ready." It isn't, and saying so undermines the calm narrative.
- Comparisons to Dropbox / Syncthing. Different category; comparison invites the wrong mental model.
- Anything about LLMs / agents in the hero. The use case fits but mentioning it narrows the addressable audience.
