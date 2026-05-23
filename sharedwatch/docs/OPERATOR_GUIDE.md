# OPERATOR GUIDE

## What this is
sharedwatch turns file changes in `/home/node/.openclaw/shared` into durable queue events and reviewable digests.

## Mental model
- watcher detects or reconstructs changes
- queue stores them durably
- consumer turns them into digests
- John reads digests on purpose, not by interruption

## Core commands
- `sharedwatch run`
- `sharedwatch status`
- `sharedwatch mode active --ttl 30m`
- `sharedwatch mode passive`
- `sharedwatch consume`
- `sharedwatch digest list`
- `sharedwatch digest show <id>`
- `sharedwatch digest archive <id>`
- `sharedwatch reconcile now`

## Modes
### Passive
- default state
- digest review aims for calm 5-10 minute cadence depending on recent activity

### Active
- temporary collaboration burst mode
- faster review cadence
- TTL-based
- auto-extends when active collaboration continues through digest consumption

## What status should tell you
- current mode
- pending event count
- failed event count
- digest count
- last event time
- last consumer run
- last reconcile run

## Expected v1 tradeoffs
- polling snapshots rather than kernel-native fs events
- rename detection is heuristic, not guaranteed
- lightweight config parsing
- minimal but useful operational surface

## Build policy
- approved Go module domains are recorded in `docs/SUPPLY_CHAIN_POLICY.md`
- build environment defaults are sourced from `.env.build`
- current allowlisted domains: `proxy.golang.org`, `sum.golang.org`

## Failure posture
- duplicates are better than silent misses
- reconciliation exists to recover drift over time
- queue durability matters more than elegant real-time behavior
