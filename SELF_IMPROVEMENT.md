# SELF_IMPROVEMENT.md

How this project finds and fixes its own gaps. Standing process for the **dogfood-driven self-improvement loop** — write realistic scenarios, run them against the deployed binary, treat each finding as a forcing function for either a fix or a doc update.

Not a guide. An instruction set. When in doubt, follow the rules verbatim.

For repo-ops rules see [`GITOPS.md`](GITOPS.md). For versioning rules see [`src/CONTRIBUTING.md`](src/CONTRIBUTING.md) §Versioning. This doc covers the *quality* loop that feeds those processes.

---

## 1. The loop, in one diagram

```
   ┌─────────────────────────────────────────────────────────────────┐
   │  1. Write a diverse dogfood scenario in docs/dogfood/test_dogfood.md
   │  2. Run it end-to-end against the DEPLOYED binary
   │  3. Each unexpected output is a FINDING
   │  4. For each finding, decide:
   │       (a) matches expected           → no action (just ✓ in worklog)
   │       (b) reveals a code bug         → fix this round, record in worklog
   │       (c) reveals a doc/design drift → fix this round, record in worklog
   │       (d) reveals an unsurprising-but-non-obvious behaviour
   │                                      → DOCUMENT in agent-system-prompt
   │                                         AND/OR Skills/sharedwatch-client
   │       (e) reveals a deeper design issue
   │                                      → file a follow-up ticket, document
   │                                         the workaround in agent docs
   │  5. Ship as a normal release per GITOPS.md §10.
   │  6. Next agent loading the system prompt / Skill avoids the same trap.
   └─────────────────────────────────────────────────────────────────┘
```

That's it. Everything below is detail.

---

## 2. When to run the loop

- **Every release that touches user-facing behaviour.** Don't ship a flag, command, or env var without at least one scenario exercising it.
- **When a user reports surprise.** Surprise is data. Write a scenario reproducing it before fixing — the scenario becomes the regression.
- **When a release adds more than ~3 new things.** The compounding behaviour will surprise; scenarios catch it before users do.
- **Quarterly anyway.** Drift is silent. Even when nothing visibly changed, run a sample of the existing scenarios against the current binary to catch any unannounced regression.

## 3. What counts as a "diverse" scenario

The dogfood set should cover all the axes a real user will hit. Aim for one scenario per axis:

- **Canonical happy path** — the textbook one-paragraph workflow, end-to-end. Catches drift in the "first 60 seconds" UX.
- **Error / failure path** — what does the tool do when the user is wrong? Inconsistent error shapes are caught here.
- **Recovery / crash path** — daemon dies hard, config is corrupt, lock file is stale. Catches assumptions about graceful exit.
- **Multi-actor / multi-tenant** — when the tool's coordination story matters, exercise it with more than one actor.
- **Boundary / edge** — empty journal, huge journal, single root, many roots, fresh install, long-lived install.

When in doubt, **pick a scenario that targets the OPPOSITE of what the latest release was about**. The release authors already tested the happy path; the diverse scenario reveals what they didn't think about.

---

## 4. How to write a scenario

Same template as the existing scenarios in [`docs/dogfood/test_dogfood.md`](docs/dogfood/test_dogfood.md):

```
## N — <Title> (vX.Y.Z)

**Goal:** one sentence.

**Setup:** isolated env (XDG_DATA_HOME=/tmp/sw-scenario-N), no shared state.

**Actions + expected:** numbered steps, each with expected output.
Use real commands; do not abbreviate.

**What this exercises:** which code path, which contract.

**Pass criteria:** the binary state required for ✓.

**Recorded run:** pointer to the worklog where the actual run output lives.
```

**Hard rules** (inherited from the existing 18-scenario pass):

- No `rm -rf` anywhere. Move-to-quarantine instead.
- No writes outside the scenario's isolated dir.
- No sudo.
- Scenarios must run unattended in under a minute.

## 5. How to run a scenario (the binary, not your build)

```bash
# Build the latest tag's binary (or use the public installer if testing
# the user-visible surface).
PATH="$HOME/.local/go/bin:$PATH" bash scripts/rebuild.sh

# Execute the scenario, capturing actual stdout/stderr per step.
# Record every command verbatim in the ticket's tasklist worklog —
# not paraphrased. Future-you (or future-Claude) will need the literal
# output to compare against expected.

# If the scenario passes: one line in the worklog (✓).
# If it diverges: paste the divergent output inline + classify per §6.
```

If the scenario can't run against the deployed binary (e.g., needs a feature not yet released), it's NOT a dogfood scenario — it's a design test. File those separately under `docs/design/`.

---

## 6. Finding classification — the decision matrix

For each divergence between expected and actual:

| Class | Symptom | Action |
|---|---|---|
| **Match** | output equals expected | ✓ in worklog, no other action |
| **Code bug** | binary does the wrong thing | **fix this round**, record both the bug and the fix in worklog; CHANGELOG `Fixed —` entry |
| **Doc/spec drift** | docs say X but binary does Y, and Y is intended | **fix the docs this round** to match the binary; CHANGELOG `Fixed —` entry under "doc drift" |
| **Non-obvious behaviour** | binary does what's intended, but a typical agent wouldn't predict it | **document as a "Known gotcha"** in `docs/agent/agent-system-prompt-20260522.md` AND/OR `Skills/sharedwatch-client/SKILL.md`; no code change |
| **Deeper design issue** | binary's behaviour is wrong by design — fixing requires real thought | **file a follow-up ticket** with the dogfood scenario as the repro; document the workaround in agent docs so the next agent doesn't get stuck while the ticket is open |

Be conservative about "match" and aggressive about "non-obvious behaviour". A behaviour that surprised *you* will surprise the next agent. Document it.

---

## 7. Where findings land

The point of the loop is that the *next reader* of the agent-facing docs doesn't have to re-derive what this pass learned. Three canonical sinks:

| Finding shape | Sink |
|---|---|
| Fix landed in this release | `src/CHANGELOG.md` under `Fixed —`; tasklist worklog table |
| Non-obvious behaviour to remember | `docs/agent/agent-system-prompt-20260522.md` → "Known gotchas (surfaced by dogfood)" section near the bottom |
| Operational gotcha for the user-facing Skill | `Skills/sharedwatch-client/SKILL.md` → agent-startup ritual block, append a numbered comment |
| Deeper design issue → ticket | `tickets/ticket-<short>-<datestamp>.md` linked from the worklog finding |

Never let a finding stay in the worklog only. The worklog is a process artifact; the canonical docs are what the next agent reads.

---

## 8. Worked example — SW-AGENT-20 (v0.0.7 release)

Filed: [`tickets/ticket-fatalJSON-expansion-dogfood-self-improvement-20260524_053824.md`](tickets/ticket-fatalJSON-expansion-dogfood-self-improvement-20260524_053824.md).
Tasklist with full worklog: [`tickets/tasklist_20260524_053824.md`](tickets/tasklist_20260524_053824.md).

- **Wrote 4 scenarios** (canonical-with-env-vars / stale-lock-recovery / error-parity / lease-warning).
- **Ran each end-to-end** against the v0.0.7 binary on `/tmp/sw-scenario-21..24`.
- **5 findings**:
  - F1 (code bug): `SHAREDWATCH_ACTOR` env var didn't reach `test emit`'s payload → fixed.
  - F2 (deeper design): `--data-dir` alone doesn't re-derive watch_path/db_path → workaround documented, follow-up ticket open.
  - F3 (code bug): `stop` missed `os.ErrProcessDone` → fixed.
  - F4 (doc drift): docs said `lease acquire`, binary expects `lease grant` → docs fixed.
  - F5 (non-obvious behaviour): lease warning fires only on watcher events, not test-emit → documented in agent-system-prompt + Skill gotchas.
- **3 inline code fixes + 1 doc-drift fix + 2 doc updates** in one release.

The cost was about 30 minutes of execution time. The value was three real bugs that would have been hit by the next agent and two pieces of folklore that would have caused repeated debug loops.

---

## 9. Anti-patterns to avoid

- **Writing a scenario without running it.** A scenario that hasn't been executed is design, not dogfood. Always run.
- **Burying findings in a long tasklist.** Use the worklog *table* format from the SW-AGENT-20 close-out. One row per finding. Skim-able.
- **"Just fix it" without recording.** Even one-line bug fixes get a CHANGELOG entry and a worklog row. The agent-facing doc update only happens when the link from "what the binary does" to "what the agent should expect" is non-obvious — but record always.
- **Documenting the bug in only one place.** Bug fixed in code → CHANGELOG. Non-obvious behaviour → agent system prompt. Workaround for an open issue → Skill. Each sink has a different audience; pick the right one.
- **Letting scenarios bit-rot.** When a release changes a command, walk the dogfood doc and update any scenario that quotes that command. The scenarios are the regression spec; let them lie and they lose value fast.
- **Running scenarios against the local build, not the deployed binary.** Local build = what you're shipping in this release; deployed binary = what users actually have. Both have value, but the deployed binary catches release-machinery bugs (install, manifest, version stamping) that the local build hides.

---

## 10. References

- [`docs/dogfood/test_dogfood.md`](docs/dogfood/test_dogfood.md) — the scenario set.
- [`tickets/`](tickets/) — every release has a ticket + tasklist; tasklist worklogs are where findings land first.
- [`docs/agent/agent-system-prompt-20260522.md`](docs/agent/agent-system-prompt-20260522.md) — "Known gotchas" section is the agent-facing sink.
- [`Skills/sharedwatch-client/SKILL.md`](Skills/sharedwatch-client/SKILL.md) — the alt agent-facing sink, focused on operational ritual.
- [`GITOPS.md`](GITOPS.md) — the release rules this loop ships through.
- [`src/CONTRIBUTING.md`](src/CONTRIBUTING.md) §Versioning — version-bump conventions for the release that lands findings.
- [`docs/design/`](docs/design/) — discussion docs for findings that need design thought before fixing.
