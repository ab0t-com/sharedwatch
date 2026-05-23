# `config.yaml` — keys, defaults, what is and isn't parsed

The shape of `config.yaml` is **smaller than you'd guess**. Many `Config` fields are flag-only (no YAML key). This reference is the authoritative inventory.

## Contents
1. Discovery and precedence
2. Keys the parser actually accepts
3. Defaults
4. Fields that are flag-only (NOT in config.yaml)
5. Parser behavior (quirks)
6. Example: minimal vs maximal

## 1. Discovery and precedence

```
defaults  <  config file  <  flags
```

- Default config path: `./config.yaml`. Override with `--config <path>`.
- **A missing config file is silently ignored.** It does not error.
- Flags always win over both config and defaults.

## 2. Keys the parser actually accepts

Source of truth: `internal/config/file.go`. These keys are parsed; **anything else in the YAML is silently dropped**.

```yaml
watch_path: ./watched               # string; abs or rel
data_dir: ./data                    # string; abs or rel
db_path: ./data/queue.db            # string
recursive: true                     # bool (true|false, case-insensitive)
coalesce_window: 5s                 # time.Duration: 5s, 1m30s, etc.
passive_interval: 10m
active_interval: 5s
active_ttl: 30m
reconcile_interval: 30m
max_batch_size: 100                 # int
ignore_patterns: .git,.DS_Store,*.tmp,*.swp   # CSV string
retention_days: 30                  # int
```

That is the complete set. **The `storage_type` line in the example config.yaml is not actually parsed** — it's documentation only.

## 3. Defaults

| Field | Default | Source |
|---|---|---|
| `watch_path` | `$XDG_DATA_HOME/sharedwatch/watch` (or `~/.local/share/sharedwatch/watch`) | `config.Default()` |
| `data_dir` | `$XDG_DATA_HOME/sharedwatch` | same |
| `db_path` | `$XDG_DATA_HOME/sharedwatch/queue.db` | same |
| `recursive` | `true` | |
| `coalesce_window` | `5s` | |
| `passive_interval` | `10m` | |
| `active_interval` | `5s` | |
| `active_ttl` | `30m` | |
| `reconcile_interval` | `30m` | |
| `max_batch_size` | `100` | |
| `ignore_patterns` | `.git, .DS_Store, *.tmp, *.swp` | |
| `retention_days` | `30` | |

## 4. Fields that are flag-only (NOT in config.yaml)

These exist on the `Config` struct but the YAML parser has no case for them. To override, use the global flag.

| Config field | Override how |
|---|---|
| `IncludePatterns` | `--include <pat>` (repeatable, comma-aware) |
| `HashEnabled` | `--hash on\|off` |
| `HashMaxSize` | Not exposed via CLI today; defaults to 1 MB (1<<20). Edit code to override. |
| `ProducerID` | `--producer <str>` |
| `StorageType` | Not configurable; hard-coded to `sqlite`. |

If you want one of these as a YAML key, file a ticket. It's a small change but a real API expansion.

## 5. Parser behavior (quirks)

- **No YAML library.** The parser is a line-by-line `key: value` reader — see `internal/config/file.go`. It does not handle nested structures, lists, anchors, or multi-line strings. If a key is missing, the default applies.
- **Strings are quote-trimmed.** `"foo"` and `'foo'` both become `foo`.
- **CSV strings** (`ignore_patterns`) are split on `,` and each part is trimmed.
- **Durations** use Go's `time.ParseDuration` — accepts `300ms`, `5s`, `1m30s`, `2h`, etc. Invalid durations silently fall back to the default.
- **Ints** silently fall back to default on parse failure.
- **Booleans** are case-insensitive for `true`; anything else is `false`. So `recursive: True` works, `recursive: 1` does NOT (becomes false).
- **Comments** start with `#`. Inline comments after a value are NOT stripped — keep them on their own line.

## 6. Example: minimal vs maximal

**Minimal** (everything optional):
```yaml
# (empty file is valid; defaults apply)
```

**Common** (override watch + DB paths, keep timing defaults):
```yaml
watch_path: ./my-folder
db_path: ./my-folder.db
```

**Maximal** (every parseable key):
```yaml
watch_path: ./watched
data_dir: ./data
db_path: ./data/queue.db
recursive: true
coalesce_window: 5s
passive_interval: 10m
active_interval: 5s
active_ttl: 30m
reconcile_interval: 30m
max_batch_size: 100
ignore_patterns: .git,.DS_Store,*.tmp,*.swp
retention_days: 30
```

**Maximal-plus-flags** (every parseable key AND every flag-only override):
```yaml
# config.yaml (parseable keys only)
watch_path: ./watched
passive_interval: 5m
```

```bash
# Launch with the flag-only overrides
sharedwatch \
  --config ./config.yaml \
  --include 'src/**' --include 'docs/**' \
  --hash on \
  --producer "claude-coordinator-1" \
  run
```

## Verification

Confirm what sharedwatch actually loaded:

```bash
sharedwatch init   # prints resolved paths
# watch_path=/abs/path
# db_path=/abs/path
# data_dir=/abs/path
```

If the resolved paths don't match what you expect, the config file may not be at the path you think, or a flag is winning, or a key isn't being parsed.
