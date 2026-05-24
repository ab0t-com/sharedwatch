# Cursor filter-change detection — discussion (2026-05-24)

**Origin:** background dogfood agent finding F40-C from scenarios 37-40 against v0.0.9. The Skill (`Skills/sharedwatch-client/SKILL.md`) and gotchas doc both claim the binary warns when a cursor is reused with a different filter scope; empirical check shows it does not. Quote from the agent:

> F40-C: Filter-change probe is silently lossy. After reading `even/**` up to evt_ecc... (file_4), switching to `--path-glob odd/**` returned only odd/file_5, file_7, file_9 onward — odd/file_1 and odd/file_3 are skipped forever with NO warning. The "filter-change warning" gotcha referenced in the Skill is documented but NOT implemented in the binary.

**Class:** doc drift OR code bug — the answer depends on whether the warning is genuinely valuable enough to build. This doc surfaces the call.

---

## What "filter-change lossiness" means

A named cursor stores a position (the highest `created_at` / `id` pair it has seen). On the next read, the binary returns rows strictly newer than that position, filtered through whatever flags the caller passed THIS time.

If those flags differ from the prior call, events that exist in the source set but don't pass the new filter at the new position simply never appear — they were skipped during the prior call (because they didn't match the old filter) and they're now below the cursor (so they're invisible to the new filter too).

Concrete: in the dogfood scenario, the cursor advanced past `odd/file_1` and `odd/file_3` while filtering for `even/**`. Switching to `--path-glob odd/**` afterward returns only `odd/file_5+` because the cursor is already past `file_3`. The agent expects all `odd/*` events back to the start; the binary silently delivers a subset.

---

## Why the docs claim a warning

Looking at the Skill's gotchas section:

> Filter-change warning: reusing a cursor with a different filter scope silently skips events. New scope = new cursor name.

This appears in `Skills/sharedwatch-client/SKILL.md` line 214. It reads as guidance ("don't do this") and the phrasing suggests the binary either warns or silently skips — but doesn't commit which. The dogfood agent read it as "warns" because the gotcha section also mentions other things the binary actively does (coalesce, reconcile dedup). Either reading is defensible from the doc; empirically the binary does nothing.

---

## Options

### (A) Implement the warning (code change)

Store the filter-scope hash on each cursor row. On read, compare the incoming filter hash to the stored one; if they differ, emit `slog.Warn` with the previous and current filter signatures. Optionally refuse the read with `--strict-cursor` or similar.

**Pros:**
- Catches a real footgun. Agents will hit this exact issue: reuse a session cursor with new flags, lose events, debug for an hour.
- The warning is non-blocking; agents that intentionally narrow filters can ignore it.

**Cons:**
- Schema change (add `filter_signature TEXT` to cursors table) + migration.
- Defining "filter signature" requires a stable canonicalisation of all filter flags (`--type`, `--source`, `--status`, `--path-glob`, `--payload-key/value`, `--producer`, `--root`). Reordering shouldn't trigger a false warning; flag aliasing shouldn't either.
- Effort: probably 80–120 LOC + migration + tests + docs.

### (B) Fix the docs (doc change only)

Rewrite the Skill gotcha to clearly say: "the binary does NOT warn; if you change filter scope, change the cursor name too." Add the same to `agent-system-prompt-20260522.md` and the recovery reference.

**Pros:**
- Zero code risk.
- Honest about current behavior.
- Agents who internalize the docs avoid the problem upfront.

**Cons:**
- Loses the safety net for agents who don't read docs carefully enough.
- Other tools (psql `\set ON_ERROR_STOP on`, kubectl context warnings) tend to err on the side of warning; sharedwatch staying silent is the unfriendlier choice for a coordination tool.

### (C) Hybrid — document AND add `--strict-cursor` opt-in (smaller code change)

Update docs honestly (silent skip is the current behavior) AND add a `--strict-cursor` boolean that, when set, errors if the binary detects ANY non-empty filter scope change vs the prior call. Implementation is lighter than (A): no warning logic, just an error path keyed on the same signature comparison.

**Pros:**
- Honest docs.
- Opt-in safety net for callers who want it.
- Smaller code change than (A).

**Cons:**
- Still requires the schema change for `filter_signature`.
- Two-tier behavior (warn vs error vs silent) is more complex than either pure option.

---

## Recommendation

**(B) for v0.0.11 + (A) as a follow-up if/when the user wants the safety net.**

Reasoning:
- The warning is genuinely useful but not load-bearing for the current dogfood pace. We have other HIGH-severity work in flight (SW-AGENT-27 multi-root test emit, deferred F37-B digest list JSON, deferred F40-E cursor delete).
- A 5-minute doc fix unblocks the inconsistency immediately. An 80-LOC code fix needs design (schema, signature canonicalisation, test matrix) and would gate v0.0.11 unnecessarily.
- If users start hitting this in real workflows (not just dogfood), bump (A) up the queue. Until then, (B) is the right ROI.

**Concrete plan for (B):**
1. Update `Skills/sharedwatch-client/SKILL.md` line 214 to say explicitly: "the binary does NOT warn — silently skips events past the cursor that didn't match the prior filter. Use a NEW cursor name when you change filter scope."
2. Add a matching bullet in `docs/agent/agent-system-prompt-20260522.md` under "Known gotchas."
3. Add a one-line reference to `Skills/sharedwatch-client/references/gotchas.md` and `Skills/sharedwatch-client/references/recovery.md`.
4. Leave a `// TODO: SW-AGENT-28 — implement warning if/when prioritised` comment near the cursor read code so future maintainers can find it.

**If (A) gets picked up later:**
- Filing as **SW-AGENT-28 — cursor filter-change warning**.
- Required design choices before implementation: how to canonicalise the filter signature; whether to warn-only or also gate behind `--strict-cursor`; whether to migrate existing cursors (probably yes — set `filter_signature = NULL` and skip the check on first read post-migration).

---

## Decision needed from user

This doc presents three paths. Without explicit direction, the recommended path is (B) — ship the doc fix now alongside SW-AGENT-27, file (A) as deferred SW-AGENT-28. User can override.
