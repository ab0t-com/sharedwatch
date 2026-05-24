# sharedwatch

> Calm, durable, pull-based activity feed for a local shared folder.
> Capture instantly. Consume calmly.

A single Go binary that watches a folder on disk, captures every change into a SQLite-backed queue, and lets you review the activity on your own cadence — every 5–10 minutes by default, near-real-time on demand. The watcher never interrupts you; you pull a digest when you're ready.

## Why this exists
Shared folders are useful precisely because activity in them carries signal. But consumed raw, that signal arrives at random times, comes in bursts, and is lossy across restarts. sharedwatch turns the firehose into a digest you read on purpose.

Designed for:
- a single host where a human (or an assistant agent) wants ambient awareness of a folder without being interrupted,
- low-to-moderate write volume,
- restart-safe operation with a durable on-disk queue.

Not designed for: multi-host file shares, push notifications, content sync, or high-throughput logging.

## Quickstart (60 seconds)
```bash
# 1. Build (Go 1.22+)
git clone <this repo> && cd sharedwatch
go build -o sharedwatch ./cmd/sharedwatch

# 2. Initialize defaults under $XDG_DATA_HOME/sharedwatch (or ~/.local/share/sharedwatch)
./sharedwatch init
# -> watch_path=/.../sharedwatch/watch
#    db_path=/.../sharedwatch/queue.db
#    data_dir=/.../sharedwatch

# 3. Start the watcher (Ctrl-C to stop)
./sharedwatch run &

# 4. In another shell, drop a file into the watched folder
echo "hello" > ~/.local/share/sharedwatch/watch/hello.md

# 5. See the digest
./sharedwatch digest list
./sharedwatch digest show <id>
```

Want a different folder? Override with `--watch-path` or a config file:
```bash
./sharedwatch --watch-path ./my-folder --db ./my-folder.db run
```

### Multiple folders (multi-root)
A single daemon can watch any number of folders, each tagged with a stable label that appears in `events.watch_root`:
```bash
./sharedwatch \
  --root auth=/workspace/projects/auth \
  --root billing=/workspace/projects/billing \
  --db ./shared.db run

# Query one root
./sharedwatch --db ./shared.db events list --root auth

# Across-roots status
./sharedwatch --db ./shared.db --root auth=/workspace/projects/auth --root billing=/workspace/projects/billing status --json | jq .roots
```
Single-folder is still the default; if you don't pass any `--root` flags (or `watch_roots:` in config), the behaviour is exactly as before — no new columns appear in `events list` output and no `roots` array in `status --json`.

## Commands
```
sharedwatch [global flags] <command> [command flags] [args]

  init                      create the data dir + DB; print resolved paths
  run                       start watcher + consumer + reconcile loop
  status [--json]           print current state (mode, queue depth, last runs)
  roots [--json]            list the folders being watched (single + multi-root)
  mode active [--ttl 30m]   enable active (fast-cadence) mode with TTL
  mode passive              force passive mode
  consume                   process pending events once and create a digest
  digest list [--limit N]   list recent digests (newest first)
  digest show <id>          print one digest in full (marks it read)
  digest archive <id>       mark a digest archived
  reconcile now             run reconcile pass immediately
  test emit [relpath]       inject a synthetic event for end-to-end testing
                            (--payload <json> OR attribution flags below)
  update [--apply]          check for / install a newer release (safe: dry-run by default;
                            --apply downloads + SHA-256 verifies + atomic-swaps the binary)
  config show [--json]      print effective config + env vars + searched config files
  stop [--timeout 10s]      send SIGTERM to the running daemon (--force to SIGKILL)
  version                   print version and exit
  help                      print this help
```

## Environment variables

Set these once at session start (e.g. in your shell rc) so the agent doesn't repeat them on every command. **Resolution order: `flag > env > config.yaml > built-in default`**.

| Var | Maps to | Notes |
|---|---|---|
| `SHAREDWATCH_ACTOR` | `--actor` / `payload_json.actor` | Stable writer id. The single biggest UX win for AI agents. |
| `SHAREDWATCH_ACTOR_KIND` | `--actor-kind` | `human` / `ai_agent` / `automation` |
| `SHAREDWATCH_SESSION` | `--session` | Per-shell session id, e.g. `sess-2026-05-24-abc` |
| `SHAREDWATCH_TASK` | `--task` | Short work label |
| `SHAREDWATCH_ADDRESSEE` | `--addressee` | Who the change is FOR (rare) |
| `SHAREDWATCH_FORMAT` | `--format` default | `text` / `json` / `jsonl` / `csv` |
| `SHAREDWATCH_ROOT` | `--root` filter default | For agents scoped to one root |
| `SHAREDWATCH_CURSOR_NAME` | `--cursor-name` default | Activates cursor mode without typing the flag |
| `SHAREDWATCH_HINTS` | `--hints` profile default | `default` / `agent` / `terse` / `off` |
| `XDG_DATA_HOME` | data dir base | Defaults to `~/.local/share` |
| `XDG_CONFIG_HOME` | config search root | Defaults to `~/.config` |

Run `sharedwatch config show` any time to see which env vars and config files are actually being read.

## Global flags
| Flag | Default | Notes |
|---|---|---|
| `--config <path>` | `config.yaml` | Optional; silently ignored if missing |
| `--watch-path <dir>` | `$XDG_DATA_HOME/sharedwatch/watch` | Overrides config and default |
| `--db <file>` | `$XDG_DATA_HOME/sharedwatch/queue.db` | Overrides config and default |
| `--data-dir <dir>` | `$XDG_DATA_HOME/sharedwatch` | Reported by `init`; informational |
| `--ignore <pat>` | (empty) | Repeatable, comma-aware; appended to default ignore patterns |
| `--include <pat>` | (empty) | Repeatable, comma-aware; include-only filter applied before ignores |
| `--hash on\|off` | `off` | Enable per-file SHA-256 content hashing during snapshot building |
| `--producer <id>` | `<host>:<pid>` | Stamps `events.producer_id` on emitted events |
| `--log-format text\|json` | `text` | `json` for log aggregators |
| `--log-level debug\|info\|warn\|error` | `info` | |
| `--hints default\|agent\|terse\|off` | `default` (text) / `agent` (json) | Next-step suggestions profile. Env: `SHAREDWATCH_HINTS`. JSON auto-promotes to `agent` unless overridden. |
| `--version` | — | Same as the `version` subcommand |

## Attribution flags (payload_json v1)
Available on both the root flagset (apply to every event of this invocation, including watcher-detected events during `run`) and on `test emit` (override per-event). Mutually exclusive with `--payload <raw-json>` on `test emit`.

| Flag | payload_json key | Notes |
|---|---|---|
| `--actor <id>` | `actor` | Stable id of the writer (required to populate any of the others) |
| `--actor-kind <k>` | `actor_kind` | `human` / `ai_agent` / `automation` |
| `--session <id>` | `session` | Logical-run identifier; convention `sess-YYYY-MM-DD-<short>` |
| `--task <label>` | `task` | Short human-meaningful work label |
| `--intent <text>` | `intent` | One-sentence reason (quote multi-word values) |
| `--addressee <id>` | `addressee` | Who the change is FOR (peer agent or human) |
| `--ref <event-id>` | `ref_event_id` | Causal predecessor event id |
| `--tag <t>` | `tags[]` | Repeatable; comma-aware |

Examples:
```bash
# Attribute every event the daemon emits during this lifetime
sharedwatch --actor claude-coordinator-1 --session sess-2026-05-23-abc --task refactor-auth run

# Attribute one synthetic event
sharedwatch test emit specs/widget.md \
  --actor claude-spec --task new-widget-spec \
  --addressee claude-code --tag spec --tag widget
```

## For agents
If you're an agent (or anything scripting against sharedwatch), the `digest list / show` flow is for humans. You want the underlying event journal directly:

```bash
# Stream every event in the journal as JSONL — ideal for pipes
sharedwatch events list --limit 0 --order asc --format jsonl

# Only what's new since last time (server tracks the cursor for you)
sharedwatch events list --cursor-name my-agent --format jsonl

# Project just the fields you care about, filter by type and path
sharedwatch events list \
  --type file.modified --type file.created \
  --path-glob 'auth/**' \
  --fields id,type,rel_path,created_at \
  --format jsonl

# Raw SQL when you already know what you want (read-only by default)
sharedwatch sql "SELECT type, COUNT(*) FROM events GROUP BY type" --format json

# Discover the schema once, then write SQL with confidence
sharedwatch schema --format json
```

**Cursor semantics:** when `--cursor-name` or `--since-cursor` is set, results iterate ASC over `(created_at, id)` and the cursor advances to the last returned row. Reading without a cursor preserves your `--order` preference and does NOT advance any state.

**The `sql` command is a read-only escape hatch.** Multi-statement input and anything that's not `SELECT`/`WITH`/`EXPLAIN`/`PRAGMA table_info` is refused unless you pass `--write`. This is a tripwire, not a security boundary — the DB file is right there on disk.

## How it works
```
filesystem ──▶ watcher (polling diff)
                 └─▶ coalesce ──▶ SQLite queue (pending events)
                                       │
                                       ▼ pull
                                 consumer ──▶ digest table
                                                  │
                                                  ▼ pull
                                          you / your agent

separately, every reconcile_interval:
   snapshot ──▶ diff against last reconcile snapshot ──▶ recovery events
```

Three independent moving parts, all writing through one SQLite DB:
- **Watcher** scans the folder on a tick, diffs against the last snapshot, coalesces noisy modify-bursts, and writes pending events.
- **Consumer** reads pending events on schedule (10 min passive, 5 sec active), produces a digest row, marks events processed.
- **Reconciler** runs every 30 min as a heartbeat — re-diffs the folder to catch anything the watcher missed and enqueues recovery events.

Two operating modes:
- **passive** (default): consumer runs every 10 min, or every 5 min if there's been recent activity.
- **active**: consumer runs every 5 sec. Set with `sharedwatch mode active --ttl 30m`. Each successful consume during active mode extends the TTL.

## Configuration
Defaults work without any config file. To override, drop a `config.yaml` next to the binary or pass `--config <path>`:
```yaml
watch_path: ./my-watched-folder
db_path: ./my-watched-folder.db
coalesce_window: 5s
passive_interval: 10m
active_interval: 5s
active_ttl: 30m
reconcile_interval: 30m
max_batch_size: 100
ignore_patterns: .git,.DS_Store,*.tmp,*.swp
retention_days: 30
```
Precedence: built-in defaults < config file < global flags.

## What's stored
SQLite tables (DDL in `internal/db/db.go`):
- `events` — every detected change. Statuses: `pending → processing → processed | failed | suppressed`.
- `digests` — batched reviewable summaries. Statuses: `pending → read → archived`.
- `runtime_state` — current mode, last-run timestamps, snapshot hash.
- `snapshots` — folder snapshots used by the diff path; bounded to the latest 5 per source.

Data lives where `--data-dir` points (default `$XDG_DATA_HOME/sharedwatch`). Nothing under the watched folder itself.

## Operational notes
- **One `run` per data dir.** A file lock (`<data_dir>/sharedwatch.lock`) prevents two `run` processes from racing against the same DB.
- **Polling, not fsnotify.** A deliberate v1 simplification — predictable, portable, no kernel-specific wiring.
- **Rename detection is heuristic.** Pairs delete+create with identical size+mtime.
- **Duplicates beat misses.** The reconcile pass is the safety net for any change the watcher missed.
- **Linux is the supported platform for v1.** macOS and Windows likely work (polling), but are not exercised in CI.

## Project status
The implementation passes its own test suite and end-to-end dogfooding scenarios (emit / consume / digest / reconcile / mode transitions / restart safety). See `CHANGELOG.md` for what's landed and `docs/JOHN_HANDOFF.md` for design intent.

Not yet:
- fsnotify integration,
- content-diff previews,
- multi-host / network queue,
- push notifications.

## License
MIT. See `LICENSE`.
