# Team model — today and as it scales

How the team works today, what role-kinds exist, and how the model generalizes as the team grows.

## Contents
1. Today's team (concrete)
2. Role-kinds (abstract)
3. Working model (idea → plan → approval)
4. Shared-folder policy
5. Escalation triggers
6. Growth path (3 → 10 → larger)
7. Adding agents to the team

## 1. Today's team (concrete)

Source: `TEAM.md` at the repo root. As of 2026-05-22:

- **Mike** — owner; final decision-maker; can assign work, priorities, approval scope.
- **Sarah** — structure, execution, documentation, coordination, follow-through. Strong fit for task breakdowns, operating notes, decisions, checklists, making work concrete.
- **John** — ideas, design sense, positioning, messaging, shaping rough thoughts into clearer direction. Strong fit for concept development, framing, narrative, creative refinement.

This is three people in three distinct **role-kinds**. Names will change as the team grows; the role-kinds will not.

## 2. Role-kinds (abstract)

The current team encodes three durable role-kinds:

| Role-kind | Today's holder | What they do |
|---|---|---|
| **Explorer** | John | Generates ideas, frames concepts, drafts positioning, surfaces problems |
| **Structurer** | Sarah | Turns ideas into plans, breakdowns, decisions, runbooks; ensures follow-through |
| **Approver** | Mike | Makes binding decisions; controls scope; owns external-facing commitments |

A new team member fills one of these role-kinds (or a sub-specialty within one). AI agents added to the team fill one or more role-kinds — see §7.

The role-kinds are NOT job titles. The same person can shift kind by context. The framing exists so artifacts (and Skills like this one) can describe responsibilities without being brittle to people changes.

## 3. Working model (idea → plan → approval)

The standard flow:

1. **John (Explorer) starts something.** Creates `idea-YYYY-MM-DD-<short>.md` with goal, current idea, open questions, what kind of help is wanted.
2. **Sarah (Structurer) picks it up.** Adds structure: summary, decisions, assumptions, next steps, risks. Suggested filenames: `plan-YYYY-MM-DD-<short>.md`, `decision-YYYY-MM-DD-<short>.md`, `tasks-YYYY-MM-DD-<short>.md`.
3. **Mike (Approver) approves** where approval is needed (see §5).

This flow is not bureaucratic — it's a default. Simple changes skip steps; complex changes flow through fully. Status lines (`Status: draft|proposed|approved|blocked|done`) make the current state of any artifact visible at a glance.

## 4. Shared-folder policy

The repo root is the team's working surface. From `TEAM.md`:

- Files in the shared folder are approved for free internal team use.
- Shared-folder content may be read and written by team members for collaboration.
- Do not assume this permission extends outside the folder.
- **No destructive edits unless Mike explicitly asks.**
- **Prefer additive updates** when possible.

For an agent operating in this folder: never delete a top-level artifact without explicit authorization. Add new files or new sections instead.

Defaults expected on artifacts:
- Clear file names.
- Markdown preferred.
- One topic per file.
- Date in filename when useful.
- Write for handoff, not just for yourself.

## 5. Escalation triggers

Escalate to Mike when:
- External action is needed (commits to outsiders, public posts, paid services).
- Permission scope is unclear.
- A decision affects security, access, money, or public messaging.
- There is disagreement on direction.

Don't escalate for: routine code changes, doc updates, tests, refactors that preserve invariants.

## 6. Growth path (3 → 10 → larger)

### At 3 (today)
One person per role-kind. Direct communication. Mike approves everything that touches external state.

### At ~5–8
Sub-specialties appear within role-kinds:
- **Explorer** → Concept, Positioning, Research
- **Structurer** → Engineering execution, Operations, Documentation, Coordination
- **Approver** → Mike approves direction + external commitments; engineering approval may delegate to a tech lead within the Structurer cluster.

Working model addition: durable artifacts (plans, decisions) become source-of-truth for cross-role alignment. Verbal agreements decay; written ones don't.

### At larger sizes
- Role-kinds become teams.
- Approval becomes layered (engineering approves engineering, product approves product, Mike approves cross-cutting and external).
- The shared-folder policy needs friction added: write paths, review channels, retention.
- AI agents fill specialized roles within teams; humans manage and supervise.

The product itself (sharedwatch) is part of the scaling story — it is the substrate that lets many parallel agents see what each other are doing without each one having to ask.

## 7. Adding agents to the team

When the team includes AI agents as durable participants (not just one-off tools):

- **Each agent has a stable `actor` identity** in the `payload_json` v1 schema (see `sharedwatch-client-future`).
- **Each agent fills a known role-kind.** Examples: `claude-explorer-1`, `claude-structurer-1`, `claude-reviewer-1`.
- **Agents respect the shared-folder policy.** Specifically: no destructive edits without explicit Mike authorization; prefer additive updates.
- **Agents document their work in the file conventions.** An agent that drafts an idea writes `idea-YYYY-MM-DD-<short>.md` with `Status: draft` just like John would.
- **Agents identify their session** so a human can trace which thread of agent activity produced a given artifact.
- **Agents respect the escalation triggers.** When unsure about scope, they ask (file a question, ping a human) rather than guessing.

Agents do NOT:
- Bypass approval for external commitments.
- Delete top-level artifacts.
- Make decisions in the Approver role (only Mike or his designate does).
- Treat each other's `actor` identity as authentication (it's a label, not credentials).

## A note on this Skill's framing

This skill describes today's team and how it generalizes. The next person (or agent) onboarding to this repo will read it and pattern-match their own role-kind. The framing has to last past Mike/Sarah/John specifically, so when you update this file, prefer talking about *role-kinds* and add concrete-team-today as a side note. The role-kind framing is forward-compatible; specific names are not.
