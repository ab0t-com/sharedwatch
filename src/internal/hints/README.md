# `internal/hints` — modular, profile-driven next-step suggestions

This package powers the `Next:` block in human output and the `next: [...]`
array in JSON envelopes across every applicable `sharedwatch` command. It
is intentionally small and portable: the rest of the binary depends on it,
but it depends on nothing in the rest of the binary.

Filed under [SW-AGENT-17](../../../tickets/ticket-smart-hints-profiles-20260524_040256.md).
Shipped in v0.0.4.

---

## Why this package exists separately

A first-time agent loading our CLI has no way to know what command to run
*next* without reading every man page. The classic answer is hypermedia /
HATEOAS: each response carries the URI(s) of the next reasonable action.
This package is that, for a CLI: each command's output can attach a small
ordered list of "what to run next" — derived from the command's actual
state, not from a static lookup table.

Two design forces shape the package boundary:

1. **Reuse across our projects.** We have several CLIs with the same need.
   The package is designed to be lifted into a standalone Go module — see
   §"Extracting to its own module" below.
2. **Profile-driven verbosity.** Different consumers want different amounts
   of guidance. A human at a terminal wants 1–3 hints with reasons; an AI
   agent reading JSON wants the maximum useful set; some piped workflows
   want exactly one; some operators want none. One pipeline, four registers.

---

## Package layout — what belongs in each file

Keep the package to these five files. Don't grow it horizontally; if a
sixth file is tempting, the right move is usually a new sub-helper inside
one of the existing files.

| File | Purpose | Owners may grow this file when |
|---|---|---|
| [`hints.go`](hints.go) | Public types (`Hint`, `HintSet`, `Profile`, `Context`, `Provider`, `RootRef`, `DigestRef`) + the engine (`Register`, `For`, `Clear`). The package's API surface. | Adding a new field to `Context` (additive only). |
| [`profiles.go`](profiles.go) | The four built-in profiles + `Profile.Limit()` + `Profile.IsValid()` + `ResolveProfile()`. | Adding a new profile (rare — needs cross-project consensus first; see §"Adding a profile"). |
| [`providers.go`](providers.go) | One `func providerX(Context) []Hint` per command, all registered in `init()`. | Adding hints for a new command. |
| [`render.go`](render.go) | `RenderText(w io.Writer, set HintSet)` — the `Next:` block formatter. | Adding a new output format (e.g. coloured terminals would land here behind a flag, **not** in providers). |
| [`hints_test.go`](hints_test.go) | All unit tests. | Every change above adds at least one test here. |
| `README.md` | This file. | Document new conventions / new rules as they're agreed. |

**No other files.** No subpackages. No `util.go`. If a helper is shared
across providers, put it at the bottom of `providers.go`.

---

## Architectural rules (load-bearing — don't break)

These are not preferences. They protect the package's portability and the
output contract.

1. **Zero imports from `internal/app`, `internal/db`, or any `cmd/...`
   package.** The whole point of the package is that it consumes only a
   `Context` value passed in. Verify with:
   ```bash
   go list -deps ./internal/hints | grep -E 'internal/(app|db|cmd)' && \
     echo BROKEN || echo CLEAN
   ```
   The command must print `CLEAN`.

2. **`Context` is additive only.** New fields land with zero-value defaults
   so existing providers keep compiling. Never rename, never re-type,
   never delete. If a field's semantic meaning has to change, add a new
   field and deprecate the old one in a future major.

3. **`Hint` JSON shape is frozen.** `name` (string), `command` (string),
   `reason` (string, omitempty). Consumers may key behaviour off `name`,
   so name strings are part of the contract — once a name ships in a
   release, it cannot be renamed without a major-version bump.

4. **Providers must be pure functions of `Context`.** No I/O, no global
   reads, no clock reads, no env lookups. State comes in via `Context`;
   nothing else. This keeps providers trivially testable and predictable.

5. **Providers do not truncate.** Return everything that's usefully
   suggestable. `For()` applies `Profile.Limit()` centrally so the cap is
   uniform across providers.

6. **Hint name format: `snake_case` with a stable semantic root.**
   Examples: `consume_pending`, `drill_root_auth`, `archive_digest_dgs_001`.
   Use the format `<verb>_<noun>[_<scope>]`. Names should describe the
   action, not the rendering.

7. **One source of truth for "what to do next".** If a command has any
   next-step suggestion, it goes here — not as a `fmt.Println("hint: ...")`
   buried in a handler. The whole point of the package is uniform shape.

---

## How to add hints for a new command

Worked example: imagine we add a `sharedwatch backup` command.

### 1. Decide whether new fields are needed in `Context`

Look at the existing `Context` (in `hints.go`). If the state your provider
needs is already there, skip to step 2. If not, add the field at the
*bottom* of the struct (preserves field ordering in struct-literal
construction at the call sites), with a comment naming the consumer
command:

```go
// For `backup` — the destination path the command just used.
BackupDestination string
```

### 2. Write the provider

Add to `providers.go`:

```go
// providerBackup suggests the verify step after a backup completes.
func providerBackup(ctx Context) []Hint {
    if ctx.BackupDestination == "" {
        return nil
    }
    return []Hint{{
        Name:    "verify_backup",
        Command: fmt.Sprintf("sharedwatch backup verify %s", ctx.BackupDestination),
        Reason:  "confirm the backup archive is readable end-to-end",
    }}
}
```

Rules:
- Return `nil` (not `[]Hint{}`) when no hints apply.
- Reasons are short, English, half-sentence, lowercase first letter, no
  trailing period.
- Commands are fully-qualified — start with `sharedwatch ` so an operator
  can copy-paste anywhere.
- Never include profile checks; the engine handles that.

### 3. Register the provider

In the `init()` block at the top of `providers.go`:

```go
Register("backup", providerBackup)
```

For multi-word commands use space-separated names (`Register("backup verify", ...)`).

### 4. Wire the handler

In `cmd/sharedwatch/`:

```go
hints.RenderText(os.Stdout, hints.For("backup", hints.Context{
    Profile:           resolveHintsProfile(false),
    BackupDestination: destPath,
}))
```

For JSON envelopes, attach a `Next []hints.Hint json:"next,omitempty"`
field to the envelope struct and populate from `hints.For(...).Hints`.

### 5. Add tests

In `hints_test.go`, at minimum:

```go
func TestProvider_Backup_Present(t *testing.T) {
    hs := For("backup", Context{Profile: ProfileDefault, BackupDestination: "/tmp/x"})
    if len(hs.Hints) != 1 || hs.Hints[0].Name != "verify_backup" {
        t.Errorf("want single verify_backup, got %+v", hs.Hints)
    }
}

func TestProvider_Backup_Empty(t *testing.T) {
    hs := For("backup", Context{Profile: ProfileDefault})
    if len(hs.Hints) != 0 {
        t.Errorf("want no hints without a destination, got %+v", hs.Hints)
    }
}
```

### 6. Update docs

- One bullet in `src/CHANGELOG.md` `[Unreleased]` under "Added — hints".
- If the command itself is new, that lives in its own changelog entry; the
  hints addition is a separate bullet.

---

## How to add a new profile

Rare. Profiles are a cross-project naming convention; adding one needs
agreement, not just code. The mechanical steps:

1. Add the const + describe its semantics in `profiles.go`:

   ```go
   ProfileVerbose Profile = "verbose" // 12, with reasons + provenance
   ```

2. Add the limit in `Profile.Limit()` and the validation case in
   `Profile.IsValid()`.

3. Document it in the table in this README (§"Profile cheat sheet" below)
   and in `man/sharedwatch.1` under `--hints`.

4. Add a `TestProfile_Verbose_Limit` case to `hints_test.go`.

5. Cut at least a minor version bump — profile names are public API.

---

## Profile cheat sheet

| Profile | `Limit()` | Reasons in output | Default for | Use case |
|---|---|---|---|---|
| `default` | 4 | yes | text output (humans) | Interactive shell, terminal multiplexer pane |
| `agent` | 8 | yes | JSON output (machines) | AI agents reading the journal programmatically |
| `terse` | 1 | no (stripped by `For()`) | (never auto) | Piping through to a script that wants exactly one suggestion |
| `off` | 0 | n/a | (never auto) | When hints are noisy (CI captures, diff reviews) |

Resolution order in `ResolveProfile()`:

1. `--hints <profile>` flag value (highest).
2. `SHAREDWATCH_HINTS` env var.
3. If output is JSON, auto-promote to `agent`.
4. Default: `default`.

Unrecognised values fall through (don't fail) — a typo in env doesn't
break the tool, just gets the default register.

---

## JSON output contract

The package owns one new key in every envelope that includes its output:

```json
{
  ...,
  "next": [
    { "name": "<snake_case>", "command": "<full-command-line>", "reason": "<short>" }
  ]
}
```

Rules for whoever wires a new envelope:

- The field name is **`next`** (lowercase, no underscore). Never `hints`,
  never `drill`, never `actions`.
- Use `json:"next,omitempty"` on the struct field so an empty slice is
  omitted entirely (consumers that don't know the field continue to
  ignore it cleanly).
- `format_version` stays at its current value. The `next` field is
  additive — adding it is **not** a breaking change. *Removing* it would
  be.

The legacy `drill` map on `overview` + `events stats` was removed in
v0.0.4 in favour of `next`. Don't add a `drill` field to any new envelope.

---

## Testing conventions

- One test per provider per representative state: at minimum a
  *populated* case and an *empty* case.
- Use the engine (`For(...)`) in tests, not the providers directly — that
  way you also exercise registration, profile-limit truncation, and the
  terse-strips-reasons behaviour.
- Don't call `hints.Clear()` in any test. The built-in providers are
  registered in package `init()`; clearing breaks every subsequent test
  in the binary.
- Golden-output assertions are fine for `RenderText` (see
  `TestRender_Text_HasNextBlock`). For provider output, prefer asserting
  on `Name` + a substring of `Command` — the exact reason wording shifts.

Run the suite: `go test ./internal/hints -count=1`.

---

## Extracting to its own module (someday)

When a second project in the org adopts this package, lift it out:

1. `git mv src/internal/hints/ <newmod>/hints/` in a fresh repo at the
   org level (e.g. `github.com/ab0t-com/cli-hints`).
2. `cd <newmod>/hints && go mod init github.com/ab0t-com/cli-hints/hints`.
3. Update the package's import path in any tests that reference internal
   paths. (Currently there are none — verified.)
4. Decide on the `Context` extensibility story:
   - **Option A (simplest):** leave `Context` as a struct of well-known
     fields; downstream callers ignore fields they don't populate.
     Adding a new consumer's field is a PR upstream.
   - **Option B (cleaner long-term):** split `Context` into a "core"
     struct (Pending, Failed, Profile, Roots) plus a `Extras map[string]any`
     for downstream-specific fields. Providers retrieve their fields by
     key. Type-safety cost vs. extensibility win.
5. Update *this* repo's `src/go.mod` to depend on the extracted module,
   keep the import lines identical otherwise (re-export from
   `internal/hints` to ease migration if needed).

The longer this package stays in-repo, the more in-repo callers it has —
extraction gets slightly harder each release. Flag the moment a second
project wants it; that's the trigger.

---

## References

- [`SW-AGENT-17 ticket`](../../../tickets/ticket-smart-hints-profiles-20260524_040256.md) — full design rationale, acceptance criteria, test plan.
- [`SW-AGENT-17 tasklist`](../../../tickets/tasklist_20260524_040256.md) — what was done, in what order, by whom.
- [`src/CHANGELOG.md`](../../CHANGELOG.md) — `[Unreleased]` (now v0.0.4) "Added — smart hints" + "Changed — drill→next" entries.
- [`GITOPS.md`](../../../GITOPS.md) §10 — the release flow used to ship v0.0.4 with this package.
- [`man/sharedwatch.1`](../../../man/sharedwatch.1) — `--hints` flag + `SHAREDWATCH_HINTS` env var documented for end-users.

---

## What's expected in this directory if the package is ever empty

If you've just cloned the repo and `internal/hints/` looks empty (which
shouldn't happen — git tracks everything), the recovery is:

1. The five files listed in §"Package layout" are the canonical state.
2. Skeleton commit (v0.0.4) lives at
   [`release/v0.0.4/`](../../../release/v0.0.4/) — extract the tarball
   and copy `src/internal/hints/*` from inside.
3. Or check the package out from a tag: `git checkout v0.0.4 -- src/internal/hints/`.
4. Or, if the source is genuinely lost, the locked design in the
   [SW-AGENT-17 ticket](../../../tickets/ticket-smart-hints-profiles-20260524_040256.md)
   §3 is detailed enough to reconstruct the package end-to-end in under a
   day. Rebuild from there, then run the test suite (§"Testing
   conventions") to validate the rebuild.

If you arrived because someone deleted the package by accident: revert
the commit. Don't try to inline the functionality into handlers; the
modular shape is the value.
