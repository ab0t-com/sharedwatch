# Self-Improvement Loop — portable agent prompt

**What this is.** A copy-paste-able workflow for any project where an agent (or a careful human) should dogfood a tool's recent changes and turn each finding into either a code fix, a doc update, or a follow-up ticket. Derived from a workflow that produced 3 real bug fixes + 1 doc-drift fix + 2 documented behaviours from 4 scenarios in one release of `sharedwatch`. Lifted here so it can be applied to other CLIs, daemons, or library APIs.

**When to use it.**
- Every release that touches user-facing behaviour.
- When a user reports surprise.
- Quarterly anyway — drift is silent.

**How to use it.**
1. Replace `<TOOL>`, `<TICKET-ID>`, and `<RELEASE-VERSION>` placeholders with your project's values.
2. Paste the **AGENT OPERATING INSTRUCTIONS** block below into your agent's system prompt (or hand it to a human contributor as a checklist).
3. Make sure the project has the four required sinks listed under "Required artifacts" — if not, set them up first; the loop has no value without somewhere for findings to land.

---

## Required artifacts (set these up once per project)

The loop assumes four things exist. If your project doesn't have them, the loop will produce findings that fall on the floor.

| Artifact | What it does | Naming convention used in the worked example |
|---|---|---|
| **Scenarios doc** | A living markdown file with numbered, runnable, isolated end-to-end scenarios. Each one: setup → actions → expected output → pass criteria. | `docs/dogfood/test_dogfood.md` |
| **Ticket + tasklist pair** | Per-release ticket with locked scope; tasklist with append-only worklog where findings are recorded. | `tickets/ticket-<short>-<datetime>.md` + `tickets/tasklist_<datetime>.md` |
| **Agent-facing knowledge sink** | A "known gotchas" section in your project's system prompt or AI-agent skill where non-obvious behaviours land so the next agent doesn't repeat the mistake. | `docs/agent/agent-system-prompt-*.md` |
| **CHANGELOG with structured headings** | Standard sections (`Added`, `Changed`, `Fixed`, etc.) so code fixes from findings get a permanent record. | `src/CHANGELOG.md` |

---

## The loop, in one diagram

```
   ┌─────────────────────────────────────────────────────────────────┐
   │  1. Write 2-4 DIVERSE scenarios in the scenarios doc.
   │     Diverse = canonical + error + crash + multi-actor + boundary.
   │  2. Run each scenario END-TO-END against the DEPLOYED binary
   │     (not your local build — the deployed binary catches release-
   │     machinery bugs too).
   │  3. Each unexpected output is a FINDING. Record actual output
   │     verbatim in the ticket's tasklist worklog.
   │  4. Classify each finding (see matrix below):
   │       (a) match           → ✓ in worklog
   │       (b) code bug        → fix this round, CHANGELOG entry
   │       (c) doc drift       → fix this round, CHANGELOG entry
   │       (d) non-obvious     → document in agent knowledge sink
   │       (e) deeper design   → file a follow-up ticket; document
   │                              the workaround in agent sink
   │  5. Ship as a normal release. Next agent loading the system
   │     prompt avoids the same trap.
   └─────────────────────────────────────────────────────────────────┘
```

---

## Classification matrix

| Class | Symptom | Action |
|---|---|---|
| **Match** | output equals expected | ✓ in worklog, no other action |
| **Code bug** | tool does the wrong thing | **fix this round**, record bug + fix in worklog; CHANGELOG `Fixed —` entry |
| **Doc / spec drift** | docs say X but tool does Y, and Y is intended | **fix the docs** to match the tool; CHANGELOG `Fixed —` under "doc drift" |
| **Non-obvious behaviour** | tool does what's intended, but a typical user wouldn't predict it | **document as a "Known gotcha"** in agent system prompt; no code change |
| **Deeper design issue** | tool's behaviour is wrong by design; fixing needs real thought | **file a follow-up ticket** with the scenario as the repro; document workaround in agent sink so users aren't stuck while ticket is open |

Be conservative about "match" and aggressive about "non-obvious". A behaviour that surprised *you* will surprise the next user.

---

## AGENT OPERATING INSTRUCTIONS (paste into your agent's system prompt)

```text
You are dogfooding <TOOL> on behalf of <RELEASE-VERSION>, recorded
under ticket <TICKET-ID>. Your job is to run real scenarios, classify
every divergence between expected and actual output, and feed the
findings back into the project's living docs so the next agent doesn't
trip over the same gaps.

WORKFLOW (follow in order, do not skip steps):

1. PICK 2-4 SCENARIOS to add, covering different axes:
   - One canonical happy path (textbook workflow, end-to-end).
   - One error / bad-input path.
   - One crash / recovery / edge case (lock files, restarts, OOM,
     unreachable network).
   - One multi-actor / multi-tenant if the tool has that surface.
   Avoid duplicating axes the latest release authors already tested
   themselves. Bias toward the OPPOSITE of what just shipped.

2. WRITE each scenario in the scenarios doc using this template:

     ## N — <Title> (<RELEASE-VERSION>)
     **Goal:** one sentence.
     **Setup:** isolated env, no shared state.
     **Actions + expected:** numbered shell commands, each with
       expected output verbatim.
     **What this exercises:** which code path / contract.
     **Pass criteria:** the binary state required for ✓.
     **Recorded run:** pointer to the worklog where actual output
       lives.

3. RUN each scenario END-TO-END against the DEPLOYED binary
   (downloaded via your project's public installer, not your local
   build). Capture stdout, stderr, and exit code verbatim.

4. FOR EACH DIVERGENCE between expected and actual:
   a. Record the actual output inline in the ticket's tasklist worklog.
   b. Classify per the matrix:
        match           → ✓
        code bug        → fix this round; CHANGELOG Fixed entry
        doc drift       → fix the docs this round; CHANGELOG entry
        non-obvious     → add to agent-facing knowledge sink
        deeper design   → file follow-up ticket; doc the workaround
   c. Make the change (or file the ticket) BEFORE moving to the next
      scenario, while the context is fresh.

5. AT THE END, write a worklog summary table:
     | # | Finding | Class | Outcome |
   So a reader can scan the impact of this round without reading
   every step.

HARD RULES (no exceptions):

- No `rm -rf` anywhere in scenarios. Use move-to-quarantine instead.
- Scenarios must run unattended in under a minute each.
- Scenarios isolate via per-scenario temp dirs; never touch shared
  state.
- Every finding lands in at least one canonical sink. The tasklist
  worklog is a process artifact; the agent knowledge sink and
  CHANGELOG are the long-lived record.
- Don't skip a finding because "it's small". Documentation drift
  is cheaper to fix when found than after it confuses three users.

ANTI-PATTERNS to avoid:

- Writing a scenario without running it. A scenario that hasn't
  been executed is design, not dogfood.
- Running against your local build instead of the deployed binary.
- "Just fix it" without recording. Even one-line fixes get a
  worklog entry + CHANGELOG entry.
- Documenting a bug in only one place. Bug fix → CHANGELOG.
  Non-obvious behaviour → agent sink. Workaround for open issue →
  user-facing skill. Different audiences; pick the right sink.
- Letting scenarios bit-rot. When a release changes a command,
  walk the scenarios doc and update any scenario quoting that
  command. The scenarios are the regression spec.

EXIT CRITERIA: every finding is either (a) fixed and recorded, or
(b) ticketed and documented as a known gotcha. No finding survives
in the worklog alone.
```

---

## Worked example (one paragraph)

The team that built this loop ran it on a small CLI's v0.0.7 release. They wrote 4 scenarios (canonical-with-env / stale-lock / error-parity / multi-actor). Running them surfaced 5 findings: 2 code bugs (env vars not flowing to one code path; a `kill(2)` wrapper not matching `os.ErrProcessDone`), 1 doc drift (a command verb was wrong in 3 places), 1 non-obvious behaviour (a warning fired in a logged-only code path, not via the journal), 1 deeper design issue (an umbrella flag wasn't really umbrella-shaped). Three bugs were fixed inline. The doc drift was fixed in the same release. The non-obvious behaviour was added to the agent system prompt's "Known gotchas" section. The deeper design issue was filed as a follow-up ticket, the workaround was documented in the agent sink, and the next release (v0.0.8, half a day later) closed it. Net: 4 scenarios → 3 bugs fixed + 1 doc-drift fix + 2 documented behaviours + 1 design improvement, all in one release-and-a-half. The agent who loads the system prompt next won't repeat any of the mistakes.

---

## Adapting to your tool

This loop is shape-agnostic. It works for:

- **CLIs** — scenarios are shell command sequences with expected stdout/stderr/exit-code.
- **Daemons / services** — scenarios start the service, send requests, capture responses, inspect side-effects.
- **Libraries** — scenarios are short Go / Python / TypeScript test files; "deployed binary" becomes "the published package version".
- **HTTP APIs** — scenarios are curl commands or HTTP fixtures.

The five-step structure (write → run → classify → fix-or-document → close) doesn't change. Only the verbs in step 3 (`run` becomes "exercise") and the artifacts in step 4 (CHANGELOG can be release notes; agent system prompt can be a README "Common pitfalls" section).

---

## Setup checklist for a new project

If you're applying this loop to a project for the first time, set these up first (each is a few minutes):

- [ ] Create `docs/dogfood/test_dogfood.md` (or equivalent) with the safety rules header from the worked example.
- [ ] Decide your ticket+tasklist naming convention. Pick one and stick with it across releases.
- [ ] Add a "Known gotchas" section to your project's agent system prompt OR README's "Common pitfalls".
- [ ] Make sure your CHANGELOG has structured headings (`Added`, `Changed`, `Fixed`).
- [ ] Add one starter scenario (just the canonical happy path) so future scenarios have a format to follow.

Then paste the AGENT OPERATING INSTRUCTIONS block into your agent and start. The first run will be slow (you're learning the rhythm); subsequent runs become routine.
