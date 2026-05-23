# Contributing

Thanks for taking the time. This is a small project with a deliberately narrow scope; contributions that hold to that scope are the easiest to merge.

## Ground rules
- **Queue-first stays queue-first.** Any change must preserve the "watcher writes only to the queue; consumer pulls digests" contract. Direct notification paths from watcher to consumer are out of scope.
- **Pull over push.** No mechanism that interrupts the consumer mid-task. Active mode is allowed; auto-triggered consumer wake-ups outside the configured cadence are not.
- **Durable by default.** Any state worth surviving a restart goes through the SQLite layer, not in-memory state.
- **One folder, one process.** No multi-host, no clustering, no networked queue.
- **Polling is fine.** fsnotify is welcome as an additive option behind a flag; not a replacement.

## Before you open a PR
Run locally:
```bash
make ci      # gofmt -l, go vet, go test ./...
```
And ideally:
```bash
make smoke   # builds the binary and exercises the README quickstart
```

If you change behavior visible to users (commands, flags, defaults, JSON schema), update:
- `README.md` if it's in the public Quickstart or command table
- `CHANGELOG.md` under `[Unreleased]`
- `docs/SCHEMA_CONTRACTS.md` if the DB schema changes
- `docs/APPLICATION_FLOW.md` or `docs/STATE_MODEL.md` if a state transition changes

## Style
- Go 1.22+. `gofmt -w .` before committing.
- Errors are returned, wrapped with `fmt.Errorf("context: %w", err)`. No `panic` in non-test code outside of `main`.
- New code paths get at least one unit test. Integration tests can use a `t.TempDir()` DB.
- No new dependencies without a clear reason. `modernc.org/sqlite` is the only required external import.
- Default to writing no comments. When a comment IS warranted, document *why*, not *what*.

## License
By contributing you agree your changes are licensed under the project's MIT license (see `LICENSE`).
