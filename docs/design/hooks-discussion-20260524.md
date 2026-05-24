# sharedwatch — hooks system discussion

**Date:** 2026-05-24
**Status:** discussion document (not a decision, not a ticket — yet)
**Author:** claude (this session)
**Companion:** SW-AGENT-17 (smart hints) — shipped v0.0.4; this is the *next* design-shaped question raised in the same conversation.

This document exists to settle the question **"does sharedwatch need a hooks system, and if so, what shape?"** before any code is written. The product's core promise — calm, durable, pull-based — interacts non-trivially with the common notion of "hooks", so the design decision deserves an explicit doc, not a one-line ticket.

---

## 1. The question

A user asked whether sharedwatch has anything like the hook systems found in their other tools (git, GitHub webhooks, systemd path units, the various filesystem-watch tools). The honest answer is **no, not really — only a slog.Warn line on lease violations**. The question becomes: should it?

Two sub-questions worth separating:

- **Does the product need a hook system to be useful?** No — the journal *is* the integration point. Anyone who wants to react to events can `events list --cursor-name <me>` and pipe to a script. That pattern is documented and works today.
- **Would a narrow, philosophy-respecting hook system add disproportionate value?** Probably yes — for the specific case of "run this when a digest is created", which is a calm, pull-triggered moment that doesn't violate any of the core promises.

---

## 2. What we have today

| Mechanism | What it does | What it doesn't do |
|---|---|---|
| `events list --cursor-name <me>` | Stateless tailing — agent reads new events on its own cadence | No callback; pulls are user-initiated |
| `intent declare` | Cooperative declaration of upcoming work | Advisory only — peers may ignore |
| `lease acquire` | Stronger cooperative claim on a path-glob | Watcher logs WARN on violation; doesn't block |
| `slog.Warn` on lease violation | Structured log line with `event_actor`, `lease_actor`, `lease_path_glob`, etc. | Logs only; no callable hook |
| `digest list` / `digest show` | Pull-based digest review | No notification when a new digest lands |
| `consume` | Manual one-shot consume + digest creation | No subscriber gets told a digest just happened |

There is no facility to:

- Run a subprocess when an event is emitted.
- Run a subprocess when a digest is created.
- POST to a webhook on any condition.
- Get a callable notification on lease violations.
- Stream events into another process via a long-running subscribe call.

---

## 3. What "hooks" usually means — and why most of those are bad fits here

Traditional hook patterns and how they sit against this product:

| Pattern | Example | Fit for sharedwatch | Reason |
|---|---|---|---|
| **Synchronous pre/post hooks** | git pre-commit | ❌ Bad fit | Watcher must not block on a hook; doing so re-introduces the push-based latency we explicitly rejected |
| **Webhook POST** | GitHub webhooks | ❌ Bad fit | Violates "no network until you ask". If a user wants this, `--on-digest curl ...` lets them do it themselves |
| **Long-poll subscribers** | Kafka consumer groups | ❌ Bad fit | Coordination overhead, distributed-system semantics, exactly the complexity we're avoiding |
| **systemd path units / inotify scripts** | `/etc/systemd/system/foo.path` | ❌ Bad fit | Defeats sharedwatch's purpose — if you want raw inotify, use inotifywait |
| **Async fire-and-forget subprocess** | Various CI pipelines' "post-build" steps | ⚠ Maybe | Acceptable if opt-in, error-isolated, never retried, output captured |
| **Event-stream tail-to-subprocess** | `tail -F log \| script` | ✓ Good fit | This is basically `events list --cursor-name`. A first-class `events subscribe` would just be a UX improvement over the existing pattern |
| **On-digest hook** | "When a digest is created, run X" | ✓ Best fit | Digest creation IS a pull-triggered moment. The consumer chose to consume. Running a script in response respects every core promise |

**The pattern matters more than the name.** A "hook" that fires on every filesystem event would be push-shaped and break the calm contract. A "hook" that fires when a consumer voluntarily produces a digest is pull-shaped and respects it.

---

## 4. The narrow shape that fits: `--on-digest <command>`

### Design sketch

```
sharedwatch consume --on-digest /path/to/script.sh
sharedwatch run --on-digest /path/to/script.sh         # applies to every consume the daemon does
sharedwatch run --on-digest "curl -X POST https://my-webhook ..."   # the webhook case, your shell
```

Semantics:

- Hook is a single shell command (passed through `sh -c`, like cron).
- Fired *after* the digest INSERT commits — no risk of the script seeing a half-built digest.
- The digest JSON is sent to the hook's **stdin**.
- Hook is run **async** (fire-and-forget); the consumer never waits for it.
- Hook gets a **30-second timeout** (configurable via `--on-digest-timeout`).
- Hook **stdout + stderr** captured to `<data_dir>/hooks/<digest_id>.{out,err}` (sidecar log).
- Hook **exit code** logged at WARN if non-zero, INFO if zero; never retried.
- A meta-event `hook.completed` (or `hook.failed`) is written to the **journal** itself — closing the loop, making hooks first-class citizens visible through the same pull surface.

### What this gets us

- **Slack/email/PagerDuty integration** via the shell — `--on-digest "slack-notify ..."`. Three lines of bash, zero new sharedwatch code per integration.
- **Pipeline triggering** — `--on-digest "make ingest"` to kick off downstream work when meaningful activity lands.
- **External archiving** — `--on-digest "aws s3 cp /dev/stdin s3://bucket/$(date +%s).json"`.
- **Audit fan-out** — `--on-digest "tee -a /var/log/sharedwatch-audit.jsonl"`.

### What this deliberately does NOT do

- No per-event hook (would be push-shaped; users who really want it can poll the journal).
- No webhook POST natively (use `--on-digest curl ...`).
- No retry logic (failures observable through the meta-events; retry is the caller's policy).
- No structured "hook configuration file" with N hooks (one command, one chain — if you want multiple, your script can fan out).

### Failure modes considered

| What if... | Behaviour |
|---|---|
| Hook hangs forever | 30-second timeout (configurable); subprocess killed; logged as `hook.failed` with `timeout` reason |
| Hook fails (non-zero exit) | Logged; meta-event `hook.failed` written; consumer continues normally |
| Hook produces huge output | Captured to sidecar files; bounded by disk space, not memory |
| Hook tries to write to the journal during its run | Allowed — the hook is just another writer; it'll go through the normal pipeline |
| Daemon crashes mid-hook | Subprocess is orphaned (POSIX behaviour); next daemon start sees an INFO that no `hook.completed`/`hook.failed` event was recorded for the previous digest |
| Hook is misconfigured (binary not found) | First fire produces an immediate `hook.failed` meta-event; user sees it on next `events list` |

### Implementation footprint (rough)

- ~150 LOC in a new package `src/internal/hooks/`.
- Wire two new flags into `cmd/sharedwatch/main.go` (`--on-digest`, `--on-digest-timeout`).
- Wire one call point in `consumer.Consume` (after digest INSERT, before return).
- ~6 unit tests + 1 integration test.
- 2 paragraphs in CHANGELOG, README, man page.
- Estimate: ~half a day, ships in v0.1.0 (minor — it's a real new feature).

---

## 5. Alternatives considered (and why not now)

### Alt 1 — `events subscribe` long-running subcommand

A subcommand that's just a thin wrapper over the existing cursor loop, streaming new events to stdout for piping. Convenient but adds little — anyone who needs this can already write it in 4 lines of shell. Defer until UX pain is concrete.

### Alt 2 — Per-event hook `--on-event <cmd>`

Most flexible but most dangerous. A hook that fires on every detected change can backpressure the watcher (slow hook → coalesce window blown), can fan out into a fork bomb on bursts, and re-introduces the failure modes pull was meant to escape. Only build if a user demonstrates a real use case that the `--on-digest` shape doesn't cover.

### Alt 3 — On-lease-violation callback

The watcher already logs structured WARN lines when an event lands inside a lease held by a different actor. A callback would let cooperative agents react programmatically rather than relying on log scraping. Worth considering, but the `--on-digest` hook subsumes this use case if the consuming agent reads the journal regularly (the violation event is already there with the lease metadata).

### Alt 4 — Webhook POST built-in

A `--webhook https://...` flag that POSTs each digest as JSON. Tempting because users will ask for it. But it's strictly weaker than `--on-digest "curl -X POST ..."` (no retry policy, no header customisation, no TLS pinning) and creates a maintenance burden (HTTP client config, proxy support, etc.). Don't build; document the curl recipe instead.

### Alt 5 — Plugin system / Go plugin loading

Some tools (e.g. Vector, Telegraf) support compiled-in plugins. For our scope (a single-host, low-volume daemon), this is enormous over-engineering. `--on-digest` is the right level of extensibility.

---

## 6. Recommendation

**Build `--on-digest` only, when there's a user request for it. Don't build it speculatively.**

Three reasons:

1. **The journal already covers 80% of "hook" use cases.** Anyone who wants to react to events can tail the journal with a cursor and a script. The current docs already point at this pattern. We should reinforce the pattern before building around it.

2. **Building `--on-digest` is small** (~half a day) once a real use case lands — which means **building it speculatively has a higher opportunity cost than waiting**. If we build now without a concrete user, we'll get the API surface wrong somewhere subtle and have to break it later.

3. **The product's calmness is its differentiator.** Every push-shaped extension we add nudges us closer to "yet another notification bus" and away from "the calm thing that won't interrupt you". The default answer to "should we add a hook?" should be "no, the journal is the API" — exceptions ratified explicitly, in design discussions like this one.

### Action

- **Today:** file this discussion. No code.
- **When a real user asks for `--on-digest`:** convert §4 into ticket SW-AGENT-18 and build. Estimate ~half a day from spec to merged.
- **Never build (without further design discussion):** per-event hooks, native webhook POST, plugin system.

---

## 7. Open questions

These remain unresolved and should be discussed when SW-AGENT-18 is filed:

- **Q1.** Should the hook be called once per *digest*, or once per *consume run* (a single consume can produce multiple digests if multi-root + grouped)? Probably per-digest, but verify against actual consume semantics first.
- **Q2.** Where should sidecar logs live? `<data_dir>/hooks/<digest_id>.{out,err}` is the obvious answer, but it grows unbounded. Add to the existing retention pass (`retention_days`)? Add a separate `hook_retention_days`?
- **Q3.** Does the hook receive *just* the digest JSON, or the digest JSON *plus* a `next[]` array (consistent with the hints work) suggesting what the hook author might want to do next? Strong instinct: just the digest. Stay narrow.
- **Q4.** Should `--on-digest` be a single command or a comma-separated list of commands? Default: single. If multiple are needed, the user writes a shell wrapper. Keep the flag's grammar simple.
- **Q5.** Should the meta-event (`hook.completed` / `hook.failed`) carry the hook's stdout/stderr inline (truncated) or just a pointer to the sidecar file? Strong instinct: pointer + size; full payload only in the sidecar. Keeps the journal lean.

---

## 8. References

- [`SW-AGENT-17 ticket`](../../tickets/ticket-smart-hints-profiles-20260524_040256.md) — companion design (smart hints) that landed in v0.0.4.
- [`./future-features-20260523.md`](./future-features-20260523.md) §13 — publisher adapters (NATS/Redis/webhook) — ranked low fit; this doc refines why.
- [`./multi-agent-discussion-20260522.md`](./multi-agent-discussion-20260522.md) §6 — original "are we collecting enough information" framing; hooks are a natural extension of "give consumers ways to react", but pull-shaped, not push-shaped.
- [`./design-questions-20260523.md`](./design-questions-20260523.md) — running Q&A log; if a Q5 emerges from this discussion it lands there.
- [`../../GITOPS.md`](../../GITOPS.md) — release flow for whichever version SW-AGENT-18 lands in.
- [`../../src/internal/consumer/consumer.go`](../../src/internal/consumer/consumer.go) — the natural call site for `--on-digest`.
