# Output envelopes — exact shapes per `--format`

What `events list` and `sql` actually emit on stdout, byte-precise enough that an agent can parse it without guessing. Use this to validate JSONL streaming, expect cursor sentinels, and avoid surprises around mixed text/data lines.

## Contents
1. The four formats at a glance
2. `text` — humans only
3. `json` — single object envelope
4. `jsonl` — one row per line + cursor sentinel
5. `csv` — RFC-4180 plus a commented cursor trailer
6. Field projection (`--fields`)
7. Cursor presence rules
8. `schema --format json` envelope (separate shape)

## 1. The four formats at a glance

| Format | Shape | Cursor delivery | Parse-friendly? |
|---|---|---|---|
| `text` | Tab-aligned columns | Trailer line `# next_cursor=<tok>` | No — for humans |
| `json` | Single JSON object | `next_cursor` key (when applicable) | Yes |
| `jsonl` | One JSON object per line | Sentinel object on the last line: `{"next_cursor":"..."}` | Yes — best for streaming |
| `csv` | RFC-4180 with header | Trailing commented row `# next_cursor,<tok>` | Yes (skip lines starting with `#`) |

## 2. `text` — humans only

```
id           type           rel_path        created_at
evt_a1b2     file.modified  auth/login.go   2026-05-22T14:32:11Z
evt_c3d4     file.created   auth/oauth.go   2026-05-22T14:33:00Z
# next_cursor=eyJjcmVhdGVkX2F0X25hbm8iOjE3MTYz...
```

- No header by default; `--header` adds one (when present).
- Tab-separated columns, NOT space-aligned in machine-parseable form.
- Cursor trailer appears as a single line starting with `#`, when a cursor is active.
- **Do not parse text format programmatically.** Use `json` or `jsonl`.

## 3. `json` — single object envelope

```json
{
  "columns": ["id", "type", "rel_path", "created_at"],
  "rows": [
    {"id": "evt_a1b2", "type": "file.modified", "rel_path": "auth/login.go", "created_at": "2026-05-22T14:32:11Z"},
    {"id": "evt_c3d4", "type": "file.created",  "rel_path": "auth/oauth.go", "created_at": "2026-05-22T14:33:00Z"}
  ],
  "next_cursor": "eyJjcmVhdGVkX2F0X25hbm8iOjE3MTYz..."
}
```

- `columns` lists the columns in the order they appear in each row object.
- `rows` is always present (empty array if no matches).
- `next_cursor` is present **only when a cursor is active** (caller passed `--since-cursor` or `--cursor-name`) AND there are more rows beyond what was returned.
- Use for small, batch-consume scenarios. For high-volume streaming, prefer `jsonl`.

## 4. `jsonl` — one row per line + cursor sentinel

```jsonl
{"id":"evt_a1b2","type":"file.modified","rel_path":"auth/login.go","created_at":"2026-05-22T14:32:11Z"}
{"id":"evt_c3d4","type":"file.created","rel_path":"auth/oauth.go","created_at":"2026-05-22T14:33:00Z"}
{"next_cursor":"eyJjcmVhdGVkX2F0X25hbm8iOjE3MTYz..."}
```

- One JSON object per line; no surrounding array.
- Each row's keys are exactly the projected `--fields` (or all default columns).
- **The last line is a sentinel object `{"next_cursor": "..."}`** when a cursor is active AND more rows exist. The sentinel has a `next_cursor` key and no row fields, so a consumer parsing each line as a `RowOrCursor` union can distinguish.
- When no more rows exist, no sentinel is emitted.
- **Recommended format for agents.** Cheap to stream, cheap to parse, distinguishable cursor.

Reading pattern:
```bash
sharedwatch events list --cursor-name me --limit 50 --format jsonl \
  | while IFS= read -r line; do
      if jq -e '.next_cursor' >/dev/null <<<"$line"; then
        # last line — capture cursor if you want, but it's already advanced
        continue
      fi
      # process row
      echo "$line" | jq -r .rel_path
    done
```

## 5. `csv` — RFC-4180 plus a commented cursor trailer

```csv
id,type,rel_path,created_at
evt_a1b2,file.modified,auth/login.go,2026-05-22T14:32:11Z
evt_c3d4,file.created,auth/oauth.go,2026-05-22T14:33:00Z
# next_cursor,eyJjcmVhdGVkX2F0X25hbm8iOjE3MTYz...
```

- Header row always included.
- Values containing commas, quotes, or newlines are quoted per RFC-4180.
- Cursor appears as a trailing line that starts with `#` — strict CSV parsers should be configured to skip comment lines, or split on `\n` and filter `^#` yourself.
- `payload_json` column values are escaped as a single CSV cell (the JSON is opaque to the CSV layer).

## 6. Field projection (`--fields`)

`--fields id,type,rel_path` restricts every format's column list:

- `text` — only the named columns printed.
- `json` — `columns` array reflects the projection; `rows` only carry projected keys.
- `jsonl` — only projected keys per line.
- `csv` — header row reflects projection.

**Valid column names** (CLI-side, see `references/commands.md` §5):
`id, type, rel_path, old_path, source, status, retry_count, created_at, file_size, mtime, content_hash, coalesced_into, producer_id, payload_json`

Unknown field names are silently ignored (no row column appears for them). This is deliberate: agents can pass forward-looking projections (e.g., `actor`, `session`) without errors against today's binary.

## 7. Cursor presence rules

The `next_cursor` field/line is included **only when**:
- the caller passed `--since-cursor` or `--cursor-name`, AND
- there are more rows beyond the returned page (i.e., the page hit the `--limit`).

If neither condition holds, no cursor is emitted. This matters for JSONL consumers: don't assume every JSONL response ends with a sentinel — check.

When using `--cursor-name`, the cursor advances **server-side** on read; the `next_cursor` value returned to the client is informational (you don't need to send it back). Use `--no-advance` to peek without committing.

## 8. `schema --format json` envelope (separate shape)

`sharedwatch schema --format json` returns a different shape — an array of table descriptors, NOT the `{columns, rows, next_cursor}` envelope:

```json
[
  {
    "name": "events",
    "sql": "CREATE TABLE events (id TEXT PRIMARY KEY, type TEXT NOT NULL, ...)",
    "columns": [
      {"name": "id",          "type": "TEXT",    "not_null": true, "primary_key": true},
      {"name": "type",        "type": "TEXT",    "not_null": true, "primary_key": false},
      {"name": "rel_path",    "type": "TEXT",    "not_null": true, "primary_key": false},
      {"name": "retry_count", "type": "INTEGER", "not_null": true, "primary_key": false, "default": "0"}
    ]
  },
  {"name": "digests", "sql": "...", "columns": [...]}
]
```

- Top-level is an array of tables.
- Each table has `name`, `sql` (full DDL), `columns` (array of column metadata).
- Each column has `name`, `type`, `not_null`, `primary_key`, and optionally `default`.
- No cursor — schema is fully returned in one call.

`sharedwatch schema --format text` (default) is just the raw DDL concatenated with `;` separators — not a structured contract.
