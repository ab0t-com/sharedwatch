# PMM REPORT

## Problem
Shared-folder collaboration is useful, but raw file events are noisy and interruption-heavy if consumed directly.

## User
Primary user: John.
Secondary operator: Sarah.
Approver/owner: Mike.

## Value proposition
sharedwatch turns shared-drive activity into calm, reviewable digests without losing durability or responsiveness when live collaboration is needed.

## Strengths of this design
- decouples detection from review
- durable state
- simple operating model
- human-readable docs
- active/passive dual-speed behavior

## Risks / gaps
- filesystem semantics differ by platform
- coalescing logic can get subtle
- realtime mode needs careful TTL handling
- SQLite locking must be handled cleanly

## Next priorities
1. Resolve SQLite module trust/fetch issue so the build is fully verifiable in runtime.
2. Add stronger digest UX and richer status/health output.
3. Make retention and config loading fully first-class rather than minimal file parsing.
4. Improve rename detection confidence and edge-case handling.
5. Add status metrics polish.
