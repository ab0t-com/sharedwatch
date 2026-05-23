# PROJECT_STATUS.md

Status: implemented in source, not runtime-verified in this environment.

## Completed in code
- polling-based watcher snapshot pipeline
- durable SQLite schema for events, digests, runtime state, snapshots
- queue insertion with short-window coalescing
- consumer claim/process flow with digest generation
- active/passive mode handling with TTL behavior
- reconciliation pass with snapshot diffing
- CLI commands for run, status, mode, digest, reconcile, test, consume
- unit/integration-oriented test files

## Environment gap
This OpenClaw runtime does not currently expose a Go toolchain:
- `go` not found
- `gofmt` not found
- elevated apt install is blocked in this session

## What to do on a host with Go
Run:
- `./install.sh`
- or `make install`

That should:
- format code
- resolve modules
- run tests
- build `.bin/sharedwatch`

## Design note
Implementation uses polling snapshots rather than kernel-specific fsnotify wiring, to keep the first complete version simpler and more portable while still satisfying the queue-first design.
