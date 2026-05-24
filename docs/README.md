# `docs/` — manifest

The team's free-form documentation: design discussions, engineering and product reports, original specs, agent-facing docs, and dogfood scenarios + findings. Code-internal docs (schema contracts, application flow, operator guide) live with the Go module under [`../src/docs/`](../src/docs/) instead.

This file is the index — every doc under `docs/` is listed below with a one-line description and the date it was authored. Where docs supersede or are companion to each other, that's called out.

---

## Top-level

| File | What's in it |
|---|---|
| [`TEAM.md`](TEAM.md) | The team contract. Roles (Mike / Sarah / John), shared-folder policy, file-naming conventions, escalation triggers. Anyone joining the team reads this first. |

---

## [`design/`](design/) — design discussions and evaluations

Read-only thinking artifacts. Some become tickets; some stay as the *why* behind a shipped feature. All grounded in the code at the time of writing.

| File | Date | What's in it | Status |
|---|---|---|---|
| [`design/multi-agent-discussion-20260522.md`](design/multi-agent-discussion-20260522.md) | 2026-05-22 | The gap analysis that motivated the v0.8 ship — what data we collect, what's missing for the multi-AI-agent-on-a-shared-folder use case, what to build next. | Discussion → led to v0.8 |
| [`design/disclosure-attribution-discussion-20260522.md`](design/disclosure-attribution-discussion-20260522.md) | 2026-05-22 | Progressive disclosure ladder (L1–L6) for ambient-aware consumers + attribution layers (cooperative writer flags → watcher-inferred). | Discussion → shipped in v0.8 |
| [`design/multifolder-design-20260522.md`](design/multifolder-design-20260522.md) | 2026-05-22 | The design-quality layer on top of the multi-folder ticket — storage, access, defaults, control surface. Companion to [`./design-questions-20260523.md`](design/design-questions-20260523.md) and [`./disclosure-attribution-discussion-20260522.md`](design/disclosure-attribution-discussion-20260522.md). | Shipped in v0.8 |
| [`design/design-questions-20260523.md`](design/design-questions-20260523.md) | 2026-05-23 | Living Q&A log. Q1: is this a git replacement? Q2: what's the point of knowing a file changed without knowing what? Q3: should we run `git add . && git commit` on every change? | Living |
| [`design/content-storage-evaluation-20260523.md`](design/content-storage-evaluation-20260523.md) | 2026-05-23 | 440-line evaluation of three content-storage options (diff-only vs blob-store vs hybrid) and three implementation paths (git-format-compat vs go-git lib vs sharedwatch-owned). Drives the team decision before SW-AGENT-16 implementation. | Awaiting team decision |
| [`design/future-features-20260523.md`](design/future-features-20260523.md) | 2026-05-23 | 15 predicted next-features ranked by usage × need × design-fit, framed as complaints an agent will make in six months. | Predictive |
| [`design/hooks-discussion-20260524.md`](design/hooks-discussion-20260524.md) | 2026-05-24 | Discussion: does sharedwatch need a hooks system? What shape fits the calm/pull architecture? Recommendation: build `--on-digest <command>` only, when a real user asks for it. | Discussion → on-demand |
| [`design/defaults-audit-20260524.md`](design/defaults-audit-20260524.md) | 2026-05-24 | Audit of every CLI flag against "does the agent repeat this every time?". Phase 1 (the two known gaps) shipped in v0.0.5; Phase 2 (env-var-driven agent identity) + Phase 3 (cursor auto-default) deferred for review. | Action list |

---

## [`reports/`](reports/) — engineering, product, and ship reports

Time-anchored snapshots: what the system looks like, what just landed, what's next. Useful as historical records and onboarding context.

| File | Date | What's in it |
|---|---|---|
| [`reports/engineering-report-20260520.md`](reports/engineering-report-20260520.md) | 2026-05-20 | Structural map of the codebase, bug list, rubric scoring (27/60 baseline). The structural reference behind the v0.7-era tickets. |
| [`reports/pmm-report-20260520.md`](reports/pmm-report-20260520.md) | 2026-05-20 | Positioning, users, launch checklist. The product/marketing companion to the engineering report. References [`../specs/shared-drive-watcher-spec.md`](specs/shared-drive-watcher-spec.md) for v1 success criteria. |
| [`reports/branch-report-feature-future-20260523.md`](reports/branch-report-feature-future-20260523.md) | 2026-05-23 | Ship report for v0.8.0 on `feature/future`: what landed, why, acceptance criteria met, follow-ups deferred. The executive summary. |

---

## [`specs/`](specs/) — original product specs

Frozen-in-time artifacts. The product spec the team rallied around.

| File | Date | What's in it |
|---|---|---|
| [`specs/shared-drive-watcher-spec.md`](specs/shared-drive-watcher-spec.md) | (original) | The original product spec ("the John spec"). v1 success criteria, the calm-and-pull-based premise. |
| [`specs/note-to-sarah-about-watcher-spec.md`](specs/note-to-sarah-about-watcher-spec.md) | (original) | Follow-up note expanding on the spec. Read alongside the spec. |

---

## [`agent/`](agent/) — docs for AI agents using sharedwatch

These are not docs about agents — they are docs *for* agents. The system prompt is consumed verbatim by an LLM; the fit analysis is read by humans deciding what to build next for that audience.

| File | Date | What's in it |
|---|---|---|
| [`agent/agent-fit-20260520.md`](agent/agent-fit-20260520.md) | 2026-05-20 | Multi-agent fit analysis: which gaps in v0.7 block the multi-agent use case, scored. Drove the v0.8 roadmap. |
| [`agent/agent-system-prompt-20260522.md`](agent/agent-system-prompt-20260522.md) | 2026-05-22 | The system prompt that ships to AI consumers — explains the journal, the patterns (HANDOFF / AUDIT / DOGFOOD), references the dogfood scenarios as canonical examples. Updated post-v0.8 ship. |

---

## [`brand/`](brand/) — branding and marketing assets

Two parallel image-prompt sets covering the *same fifteen images* (same uses, same aspect ratios) in two distinct visual registers. Pick one for the public rollout; run both first, compare in context, decide.

| File | Date | Register | What's in it |
|---|---|---|---|
| [`brand/image-prompts-20260524.md`](brand/image-prompts-20260524.md) | 2026-05-24 | **v1 — photographic, watchmaker's-bench** | Fifteen prompts with Photoshop-layer thinking, slate-stone + aged-cream + brass palette, golden-hour photography references (Kinfolk, Stripe Press, Joel Meyerowitz). Lighthouse-at-dawn aesthetic. |
| [`brand/image-prompts-v2-cute-20260524.md`](brand/image-prompts-v2-cute-20260524.md) | 2026-05-24 | **v2 — cute cartoon, J×A corporate launch** | The same fifteen prompts restyled to a Notion-meets-Sanrio-meets-Studio-Ghibli illustration register. Pastel palette (peach, mint, lavender) with one warm accent per scene. Lighthouse-as-friendly-mascot. |

---

## [`dogfood/`](dogfood/) — runnable test scenarios + findings

| File | Date | What's in it |
|---|---|---|
| [`dogfood/test_dogfood.md`](dogfood/test_dogfood.md) | 2026-05-22 | 18 runnable end-to-end scenarios covering the canonical workflows, edge cases, and known failure modes. Each scenario specifies setup, action, expected events, and verify steps. Safety-scaffolded (no `rm -rf`, no writes outside the test dir). The regression spec the CI smoke job aspires to. |
| [`dogfood/findings-20260523.md`](dogfood/findings-20260523.md) | 2026-05-23 | Per-scenario findings from running the 18-scenario pass against the v0.8.0 binary on `feature/future`. 17 green, 1 real bug found (the `.git` ignore pattern leak) + fixed in-pass with a 10-case regression test. |

---

## Cross-reference map

The docs reference each other heavily. The high-traffic edges:

- The **design-questions** Q3 → **content-storage-evaluation** → ticket `SW-AGENT-16` (under [`../tickets/`](../tickets/)) form the live storage-design thread.
- **multi-agent-discussion** is the gap analysis; **disclosure-attribution-discussion** and **multifolder-design** are the design layers; the **branch-report** is the ship record.
- **agent-fit** → SW-AGENT roadmap → **engineering-report** §10 (interface gaps) form the agent-product thread.
- **shared-drive-watcher-spec** is the original target; the **pmm-report** §11 tracks spec-fit literally.
- **test_dogfood** is the regression spec; **dogfood-findings** is each pass's report.

## Where to put a new doc

| If your doc is about... | Put it in |
|---|---|
| A design idea or evaluation | `docs/design/` |
| A point-in-time engineering/product/ship report | `docs/reports/` |
| A formal spec or original requirements | `docs/specs/` |
| The AI-agent consumer's experience | `docs/agent/` |
| A test scenario or post-test findings | `docs/dogfood/` |
| Branding, marketing copy, image briefs | `docs/brand/` |
| Schema, internal pipeline flow, operator guide | `../src/docs/` (program-internal, not here) |
| A team contract or process | `docs/` top-level (like `TEAM.md`) |

When you add a doc, **add a row to this manifest** in the appropriate section.
