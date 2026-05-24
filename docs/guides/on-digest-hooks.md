# `--on-digest` hooks — user guide

**Available in:** v0.1.0+ (SW-AGENT-29). Older daemons silently ignore the flag.
**Status:** stable.

This guide is for **operators and integrators** — anyone who wants `sharedwatch` to react to digests by running a shell command (notify Slack, fan out to S3, kick off a build, write to a log). It does NOT cover the broader sharedwatch surface — see [`src/README.md`](../../src/README.md) for that, or `sharedwatch help` for the full CLI.

---

## 1. What hooks do, in one paragraph

When the consumer rolls a batch of file events into a **digest** (either on the active/passive cadence inside `sharedwatch run`, or on a one-shot `sharedwatch consume`), the daemon can fire a shell command **immediately after the digest commits**. The command receives the digest as JSON on its stdin. The hook runs asynchronously — the daemon never waits for it — and is bounded by a hard timeout. Every run produces a `hook.completed` or `hook.failed` event in the journal so you can audit hook activity from inside the same `events list` tool you already use.

The mental model: **hooks turn digest creation into a coordination point.** They do not turn every file event into a notification — that would re-introduce the push-shaped latency this product is built to escape.

---

## 2. Getting started in 30 seconds

```bash
# 1. Tell the daemon what to run when a digest is created.
sharedwatch run --on-digest 'notify-send sharedwatch "$(jq -r .summary)"'

# 2. Do some work in the watched folder. Wait for a consume cycle.

# 3. Confirm the hook fired:
sharedwatch events list --type hook.completed --type hook.failed --json --limit 5

# Sidecar stdout/stderr are at:
ls $XDG_DATA_HOME/sharedwatch/hooks/
```

That's the whole API. The rest of this doc is reference and recipes.

---

## 3. Configuration surface

Three layers, standard sharedwatch resolution chain — **flag > env > config-file > default**:

| Layer | `on_digest` | `on_digest_timeout` |
|---|---|---|
| Flag | `--on-digest <shell-cmd>` | `--on-digest-timeout <duration>` |
| Env | `SHAREDWATCH_ON_DIGEST=<shell-cmd>` | `SHAREDWATCH_ON_DIGEST_TIMEOUT=<duration>` |
| Config (YAML) | `on_digest: <shell-cmd>` | `on_digest_timeout: <duration>` |
| Default | (empty — no hook fires) | `30s` |

`<duration>` accepts Go's standard syntax: `1s`, `500ms`, `2m`, `1h30m`, etc.

Both flags are bound on the **root** command, so they apply to both:

```bash
sharedwatch run --on-digest '...'         # persistent daemon
sharedwatch consume --on-digest '...'     # one-shot
```

Verify what the daemon actually picked up:

```bash
sharedwatch config show --json | jq '.effective | {on_digest, on_digest_timeout}'
```

---

## 4. What the hook receives

### 4.1 Stdin

A single JSON object, byte-for-byte identical to `digest show <id> --json`:

```json
{
  "format_version": 1,
  "id": "dgs_1779615732557177048",
  "created_at": "2026-05-24T09:42:12.557177048Z",
  "window_start": "2026-05-24T09:42:00.123Z",
  "window_end":   "2026-05-24T09:42:12.456Z",
  "mode": "passive",
  "event_count": 10,
  "summary": "Shared drive digest at 2026-05-24T09:42:12Z\n- file.modified ...",
  "status": "pending",
  "watch_root": "code"
}
```

`format_version: 1` is the envelope contract — bump means a breaking shape change, so guard parsers with it.

**Single-root daemons OMIT `watch_root`.** When `sharedwatch` is configured with a single watch path (no `--root <label>=<path>` setup), the `watch_root` key is left out of the payload entirely (Go's `omitempty`). Multi-root setups always include it. Write your jq recipes defensively: `jq -r '.watch_root // "(default)"'`.

### 4.2 Environment

The hook process inherits the daemon's environment unchanged. **No `SW_*` variables are auto-injected** — extract everything you need from the stdin JSON via `jq` or your scripting language of choice.

### 4.3 Working directory

The hook runs in the daemon's CWD (wherever `sharedwatch run` was launched from). If you need a specific directory, `cd` inside the hook command:

```bash
--on-digest 'cd /var/log && jq -c . >> sharedwatch.jsonl'
```

### 4.4 Shell

`sh -c "<your command>"` — POSIX shell semantics. Pipes, redirects, env-var expansion, glob, `&&` / `||` all work. The command is a single string; use single quotes on the CLI to prevent your outer shell from interpreting it.

---

## 5. Recipes

### 5.1 Desktop notification

```bash
sharedwatch run --on-digest 'notify-send sharedwatch "$(jq -r .summary)"'
```

### 5.2 Slack channel post

```bash
export SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
sharedwatch run --on-digest '
  jq -c "{text: \"sharedwatch digest: \" + .summary}" |
  curl -s -X POST -H "Content-Type: application/json" \
    --data-binary @- "$SLACK_WEBHOOK_URL"'
```

### 5.3 Append to a long-term audit log

```bash
sharedwatch run --on-digest 'jq -c . >> /var/log/sharedwatch/audit.jsonl'
```

### 5.4 Archive every digest to S3

```bash
sharedwatch run --on-digest '
  aws s3 cp - "s3://my-bucket/sharedwatch/$(date -u +%Y/%m/%d)/$(jq -r .id).json"'
```

### 5.5 Trigger a downstream pipeline

```bash
sharedwatch run --on-digest 'make ingest DIGEST_ID=$(jq -r .id)'
```

### 5.6 Multiple actions via a wrapper script

When the one-liner gets too dense, point `--on-digest` at a script you maintain:

```bash
# /usr/local/bin/sw-hook.sh
#!/bin/sh
payload=$(cat)
echo "$payload" | jq -c . >> /var/log/sharedwatch/audit.jsonl
echo "$payload" | jq -r .summary | head -c 1000 | curl -s -X POST ... # notify
# add more steps as needed
```

```bash
sharedwatch run --on-digest '/usr/local/bin/sw-hook.sh'
```

### 5.7 PagerDuty event on high-traffic digests

```bash
sharedwatch run --on-digest '
  payload=$(cat)
  count=$(echo "$payload" | jq .event_count)
  if [ "$count" -gt 100 ]; then
    echo "$payload" | jq -c "{
      routing_key: \"YOUR_KEY\",
      event_action: \"trigger\",
      payload: {
        summary: (\"high sharedwatch activity: \" + (.event_count | tostring) + \" events\"),
        source: \"sharedwatch\",
        severity: \"info\"
      }
    }" | curl -s -X POST -H "Content-Type: application/json" \
        --data-binary @- https://events.pagerduty.com/v2/enqueue
  fi'
```

---

## 6. Observability — auditing hook runs

Every hook run produces **exactly one meta-event** in the journal:

```bash
# Most-recent 10 hook runs:
sharedwatch events list \
  --type hook.completed --type hook.failed \
  --json --limit 10
```

Meta-event payload shape (carried in `payload_json`):

```json
{
  "schema_version": 1,
  "hook_command":   "notify-send sharedwatch ...",
  "digest_id":      "dgs_1779615732557177048",
  "exit_code":      0,
  "duration_ms":    124,
  "stdout_bytes":   0,
  "stderr_bytes":   0,
  "stdout_path":    "/home/user/.local/share/sharedwatch/hooks/dgs_1779615732557177048.out",
  "stderr_path":    "/home/user/.local/share/sharedwatch/hooks/dgs_1779615732557177048.err",
  "reason":         "success"
}
```

### 6.1 `reason` values

| `reason` | Meaning | `exit_code` |
|---|---|---|
| `success` | Hook ran to completion with exit 0. Event type = `hook.completed`. | `0` |
| `timeout` | Subprocess killed by `--on-digest-timeout`. Event type = `hook.failed`. | `-1` |
| `cancelled` | Subprocess killed by SIGTERM/SIGINT during daemon shutdown. Event type = `hook.failed`. | `-1` |
| `nonzero_exit` | Subprocess ran to completion but exited non-zero. Event type = `hook.failed`. | (real exit code, e.g. `1`, `42`) |
| `spawn_error` | `sh` itself failed to start (extremely rare — only if `sh` isn't on PATH). Event type = `hook.failed`. | `-1` |
| `empty` | Internal — never emitted to journal. Means no hook was configured. | (N/A) |

### 6.2 Sidecar files

Hook stdout/stderr is captured to `<data_dir>/hooks/<digest_id>.{out,err}`. These files survive past the hook process so you can inspect them later from the shell:

```bash
cat $XDG_DATA_HOME/sharedwatch/hooks/dgs_1779615732557177048.out
cat $XDG_DATA_HOME/sharedwatch/hooks/dgs_1779615732557177048.err
```

The meta-event's `stdout_path` and `stderr_path` give you the exact path so you don't have to know the data dir layout.

### 6.3 Lifecycle and retention

Sidecar files are **automatically pruned** during the reconcile cycle once the corresponding digest is removed (per the existing `retention_days` setting — default 30). If you write your own files into `<data_dir>/hooks/` for any reason (operator notes, manual experiments), sharedwatch will **not** delete them — only `.out` and `.err` files whose digest no longer exists in the DB get cleaned up.

---

## 7. Behavioural guarantees + caveats

These are commitments the daemon makes that integrators can rely on:

### 7.1 Pull-shape preserved

- The hook fires **once per digest**, never per file event. A burst of file activity that collapses into a single digest produces a single hook run, not one per file.
- The consumer **never blocks** on the hook. The digest INSERT commits, then the hook is spawned in a goroutine, then `consume` returns. A slow hook does not backpressure the watcher.

### 7.2 Atomicity

- The hook fires **after** the digest INSERT commits. The script will never see a half-built digest. From inside the hook, `sharedwatch digest show <id>` will succeed (the row exists).

### 7.3 Failure isolation

- A failed hook **never blocks** future digests. The next digest will trigger its own hook run, regardless of what happened to the prior one.
- A failed hook **never retries**. If you want retries, implement them in your script (e.g. `curl --retry`). The hook surface stays simple by design.
- A failed hook produces a `hook.failed` meta-event so you can detect it via the same `events list` tooling you already use.

### 7.4 Bounded resource usage

- Subprocess wall-clock is bounded by `--on-digest-timeout` (default 30s). On timeout, the child is SIGKILL'd and a `hook.failed` event with `reason: timeout` lands in the journal.
- Subprocess output is captured to **disk** (sidecar files), not memory. A hook that emits 100 MB of stdout will write 100 MB of file, not eat 100 MB of daemon RAM.
- Concurrent hook runs are tracked by a daemon-internal `WaitGroup`. Graceful shutdown (`sharedwatch stop`) waits up to 5s for in-flight hooks to drain before releasing the DB lock; anything still running gets the orphan-process treatment.

### 7.5 What we explicitly DON'T do

- **No per-event hooks.** If a use case requires reacting to every event, write a cursor-tail script that polls `events list --cursor-name <you>` — the journal is the API. A per-event hook flag would re-introduce push-shape latency and fork-bomb risk.
- **No native webhook POST flag.** `--on-digest 'curl -X POST ...'` gives you custom headers, retries, TLS pinning, and proxy support that a built-in flag would have to re-implement. We don't.
- **No plugin loading / Go-plugin system.** Massive overkill for a single-host daemon.
- **No synchronous hooks.** The hook never blocks the consumer; you cannot use a hook to veto digest creation.
- **No retry logic.** Failures are visible via meta-events; retry is the caller's policy.

---

## 8. Gotchas + troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Hook never seems to fire | No digest has been created yet (the watcher hasn't observed any events, or events haven't been consumed). Hooks fire on **digest**, not on file change. | Check `sharedwatch status` — `pending > 0` means events are waiting for consume. Run `sharedwatch consume` to force one. |
| `events list --type hook.completed` is empty | Hook wasn't configured at the time the digest was created, OR the daemon was running a binary older than v0.1.0. | Confirm with `sharedwatch config show --json \| jq '.effective.on_digest'`. Check `sharedwatch --version`. |
| Hook fires but produces no visible side effect | The shell command failed silently. | Check `cat <data_dir>/hooks/<digest_id>.err`. Or query: `events list --type hook.failed --json --limit 1`. |
| Quoting hell | The shell command is one string, but you're trying to embed shell metachars. | Wrap in single quotes; use double quotes inside; or write a wrapper script (§5.6). |
| Hook seems slow | Hook timeout is generous (30s default) and SIGKILL waits another 500ms for the pipe to drain. So a "hanging" hook can take up to ~30.5s. | Lower `--on-digest-timeout` if your hook should never run that long. |
| Sidecar dir fills up | Hooks producing large stdout/stderr (e.g. `curl -v`); sidecar files retain until digest is pruned (default 30 days). | Lower `retention_days`, OR redirect noisy commands to `/dev/null` inside the hook: `--on-digest 'curl ... 2>/dev/null'`. |
| `--on-digest '/path/to/missing-binary'` shows `reason: nonzero_exit` not `spawn_error` | `sh` itself spawned successfully; it's just `sh` that reported "command not found" with exit 127. `spawn_error` only fires when `sh` itself can't be exec'd (pathological). | Use exit code 127 as the "missing binary" signal: most shells emit it for command-not-found. |
| Hook fires twice for what looks like one event | Look at the `digest_id` in each meta-event payload. If they differ, two consumes produced two digests (one per active-mode tick). | If undesired, batch events for longer via `--coalesce-window` or stay in passive mode. |
| Daemon shutdown takes ~5s after `sharedwatch stop` | A hook was in flight; the daemon waited up to 5s for it before exit. | Working as designed. To exit faster, use `sharedwatch stop --force` (SIGKILL — orphaned hooks). |

---

## 9. Security notes

The hook command runs with the **same privileges as the daemon**. The same trust boundary applies as for setting `$PATH` or `/etc/cron.d/*`:

- Don't pass untrusted strings into the hook command. Treat it like any other shell-eval site.
- If the hook needs lower privileges, wrap it in `sudo -u <user>` or `setpriv`.
- If the hook needs network access in a restricted environment, set up the daemon's environment accordingly — sharedwatch does no network access itself.
- The hook's stderr is captured to a world-readable sidecar file by default (mode 0644 directory, 0644 files). If hooks emit secrets, redirect stderr inside the hook to a more restrictive path: `--on-digest 'do-thing 2>/dev/null'` and rely on the structured payload for audit.

---

## 10. Reference

| Topic | Where |
|---|---|
| Design rationale | `docs/design/hooks-discussion-20260524.md` |
| Implementation tasklist | `tickets/tasklist_20260524_091730.md` |
| Source: subprocess runner | `src/internal/hooks/hooks.go` (`RunHook` / `RunHookWithSidecar` / `EmitMetaEvent`) |
| Source: consumer call site | `src/internal/app/app.go` (`FireHookAsync` / `WaitForHooks`) |
| CHANGELOG entry | `src/CHANGELOG.md` under "SW-AGENT-29" |
| Agent-facing summary | `docs/agent/agent-system-prompt-20260522.md` gotchas section |
| Skill quick-ref | `Skills/sharedwatch-client/SKILL.md` pocket decision tree |
