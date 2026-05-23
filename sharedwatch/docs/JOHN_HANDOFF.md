# JOHN HANDOFF

## Why this exists
You asked for the shared folder to act like a calm collaboration bus rather than an interruption engine.

That is the design center here.

## Product intent
- changes in `/home/node/.openclaw/shared` should be captured reliably
- those changes should become queue events
- review should happen through digests on purpose
- active collaboration can temporarily speed that up
- reconciliation should catch drift or missed events later

## How to read the code
Start here:
- `README.md`
- `docs/OPERATOR_GUIDE.md`
- `docs/APPLICATION_FLOW.md`
- `docs/STATE_MODEL.md`
- `docs/PMM_REPORT.md`

Then the main implementation packages:
- `internal/watcher/` — snapshot/diff/rename detection
- `internal/db/` — SQLite persistence, digest state, retention, health
- `internal/consumer/` — batch consumption and digest creation
- `internal/reconcile/` — drift recovery path
- `internal/app/` — orchestration and runtime behavior

## Current product judgment
This is intentionally not overbuilt.

It aims to be:
- durable
- understandable
- calm
- extendable

Not yet:
- fancy
- distributed
- UI-heavy
- platform-complex

## Likely next refinements
- stronger digest formatting
- richer health/status visibility
- better config loading
- more precise rename detection
- retention policy polish
