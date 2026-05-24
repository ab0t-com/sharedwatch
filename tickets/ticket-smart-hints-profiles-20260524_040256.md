# TICKET: Smart hints / next-step suggestions — modular, profile-driven, reusable

**ID:** SW-AGENT-17
**Filed:** 2026-05-24 04:02 UTC
**Filed by:** claude (this session)
**Status:** in progress — claimed by claude (this session)
**Target:** sharedwatch v0.0.4
**Estimated:** ~half a day (the package is small; the per-command wiring is mechanical)
**Branch:** `main` (small additive feature; no feature branch needed pre-1.0)

---

## 1. Motivation

A first-time agent loading the `sharedwatch-client` skill against a deployed binary has to *know* the CLI surface in order to chain commands meaningfully. Today, only `sharedwatch overview --format json` emits a `drill` map of suggested follow-ups (see `src/internal/app/overview.go`). Every other command — `status`, `roots`, `events list`, `events stats`, `digest list`, `digest show`, `init`, `version` — returns data with no hint about what to run next.

Pattern requested by the user: **smart helpers** common across their other projects, so that any command's output can answer "ok, given what I just saw, what would I run next?" without the consumer needing to memorise the full CLI.

Two design constraints from the user:

1. **Modular.** A system we can adapt over time. Adding hints for a new command, or tightening hints for an existing one, must not require touching every call site — only the central registry.
2. **Profile-driven.** Different consumers want different verbosity. A human running `status` interactively wants 1–3 contextual hints. An AI agent slurping JSON wants the maximum useful set, in a machine-readable shape, with reasons. Some workflows want hints completely off.

---

## 2. Scope

### In scope

- New package `src/internal/hints/` with:
  - `Profile` enum (`default`, `agent`, `terse`, `off`).
  - `Hint` (`name`, `command`, `reason`) and `HintSet` (`profile`, `hints[]`) types.
  - `Context` struct carrying the state snapshot providers consult (pending, failed, roots, top actor, cursor name, etc.).
  - An `Engine` that holds per-command `Provider`s and resolves `For(command, ctx) HintSet`.
  - A `Register(command, provider)` extension point so adding a new command's hints is *one* call from `init()`.
- A `--hints <profile>` global flag + `SHAREDWATCH_HINTS` env var, with sensible defaults (text → `default`; JSON → `agent`).
- Per-command providers for: `status`, `roots`, `digest list`, `digest show`, `events stats`, `events list` (cursor mode), `init`, `version`.
- Output rendering:
  - **JSON:** new `"next": [{name, command, reason}]` array on the envelope (additive, `omitempty`; `format_version` unchanged).
  - **Text:** a trailing `Next:` block, indented, ≤4 lines in `default` profile.
- Replace the existing ad-hoc `drill` map in `overview --format json` with the same `hints.HintSet` mechanism, so there is exactly one place to extend (drift-prevention).
- Unit tests for the engine + every provider.
- Docs: CHANGELOG `[Unreleased]` block, README section, man page entry, Skills update.

### Out of scope

- A hint-suggestion *quality* model. Hints are deterministic functions of state. No fuzzing, no learning, no ranking against past usage.
- Multi-language hint copy. English only; the `reason` field is short and English.
- Coloured terminal output. Plain text; let downstream pipe to whatever.
- Persisting the user's hint history.

---

## 3. Design — locked decisions

### 3.1 Package shape

```
src/internal/hints/
├── hints.go       — Hint, HintSet, Context, Profile, Engine, Register
├── profiles.go    — built-in profiles + Profile.Limit()/Profile.Verbose()
├── providers.go   — built-in per-command providers (one func per command)
├── render.go      — RenderText(w, set) for the "Next:" block
└── hints_test.go  — unit tests
```

The package has **zero imports from `internal/app` or `internal/db`** — it consumes only the `Context` struct passed in. This keeps the design portable; lifting it into a standalone module later is a `git mv` away.

### 3.2 Types

```go
// Profile controls how many hints emit and in what register.
type Profile string

const (
    ProfileDefault Profile = "default" // 1–4, contextual, for humans
    ProfileAgent   Profile = "agent"   // up to 8, fully-qualified, reasons included
    ProfileTerse   Profile = "terse"   // 1 — single most likely next move
    ProfileOff     Profile = "off"     // emit nothing
)

type Hint struct {
    Name    string `json:"name"`              // stable identifier, e.g. "consume_pending"
    Command string `json:"command"`           // full runnable command line
    Reason  string `json:"reason,omitempty"`  // why it's suggested
}

type HintSet struct {
    Profile Profile `json:"profile"`
    Hints   []Hint  `json:"hints"`
}

// Context is the state snapshot a Provider inspects. Additive; new fields
// land with omitempty/zero-value defaults so old providers keep working.
type Context struct {
    Profile       Profile
    Pending       int
    Failed        int
    Digests       int
    UnreadDigests int
    Roots         []RootRef    // {Label, Path, Pending}
    TopActor      string
    CursorName    string
    HasCursor     bool
    NewerExists   bool         // true when version < latest (for `version`)
    Latest        string
    // ... grow as needed; never remove
}

type Provider func(ctx Context) []Hint

func Register(command string, p Provider)        // wires a command -> Provider
func For(command string, ctx Context) HintSet    // resolve, trim to profile limit
```

### 3.3 Profile semantics

| Profile | Max hints | Reasons in output | Default for |
|---|---|---|---|
| `default` | 4 | yes | text output (humans) |
| `agent` | 8 | yes | json output (machines) |
| `terse` | 1 | no | when piping to a script |
| `off` | 0 | n/a | when an operator finds them noisy |

Profile is resolved per-invocation in this order:
1. `--hints <profile>` command-line flag (highest).
2. `SHAREDWATCH_HINTS` env var.
3. If `--format json` is set on a command that supports it, **promote** to `agent` (unless overridden by 1 or 2).
4. Default: `default`.

### 3.4 Per-command providers (first cut)

- **status** — `consume` if pending>0; `events retry` if failed>0; `digest list` if unread>0; per-root drills if multi-root.
- **roots** — for each root, a `events list --root <label> --since 1h --format jsonl` hint.
- **digest list** — for the newest 1–2 digests, a `digest show <id>` hint.
- **digest show** — `digest archive <id>` (the read-and-archive flow).
- **events stats** — top type → narrow query; top actor → narrow query.
- **events list** (cursor mode) — if 0 returned: `status`; if many: `events stats --root <root>`.
- **init** — `run` (with the resolved watch_path baked in).
- **version** — if newer exists: `update --apply`.
- **overview** — same provider drives the existing `drill` map (consolidated; no double maintenance).

### 3.5 Output integration

**JSON.** Add `"next": [...]` to every envelope-emitting command, `omitempty`. Existing `format_version: 1` is preserved (additive). For `overview`, the `drill` map is *removed in v0.0.4* and replaced with `next` — this is the one breaking JSON shape, called out in the CHANGELOG. (Consumers using `drill` were not yet many; we have one shipped release, v0.0.3.)

**Text.** After the main output, an empty line, then:

```
Next:
  consume pending events      sharedwatch consume
  drain failed events         sharedwatch events retry
  read newest digest          sharedwatch digest show dgs_xyz
```

Two-space indent; column-aligned via `text/tabwriter`. Skipped entirely if `profile == off` or no hints apply.

### 3.6 Configuration surface

- Global flag: `--hints default|agent|terse|off` (default empty → auto-resolved).
- Env: `SHAREDWATCH_HINTS=<profile>`.
- No per-command override (keeps the surface tight; pre-1.0).

---

## 4. Acceptance criteria

The ticket is done when **all** are true:

1. `sharedwatch status` (text) shows a `Next:` block with context-driven hints whenever any of {pending, failed, unread digests, roots configured} is non-zero. With everything zero, no `Next:` block is emitted.
2. `sharedwatch status --json` includes a `"next"` array (omitempty).
3. `sharedwatch overview --format json` no longer has the `drill` field; it has the unified `next` array. CHANGELOG calls out the breaking shape.
4. `sharedwatch roots`, `digest list`, `digest show <id>`, `events stats --root X`, `events list --cursor-name X`, `init`, `version` all gain hints per §3.4.
5. `--hints off` suppresses hints on every command (text and JSON).
6. `--hints agent` emits up to 8 hints per command in JSON; `--hints terse` emits exactly 0 or 1.
7. `SHAREDWATCH_HINTS=off sharedwatch status` matches `sharedwatch --hints off status`.
8. New package `internal/hints` has ≥8 unit tests covering: profile resolution, per-provider output for representative `Context` states, profile-limit truncation, the empty-state no-hints case, and JSON marshal stability.
9. All 11 existing test packages still green: `gofmt -l .` empty, `go vet ./...` clean, `go test ./... -count=1` passes.
10. CHANGELOG `[Unreleased]` block added with the new section + breaking-shape note for `overview`.
11. `src/README.md` command table and DEFAULT PATHS block reflect the new `--hints` flag.
12. `man/sharedwatch.1` `.TH` version bumped + new `--hints` flag documented in GLOBAL FLAGS.
13. `Skills/sharedwatch-client*/SKILL.md` mention `--hints agent` as the recommended profile for agents.
14. Release v0.0.4 cut + pushed per GITOPS.md §10 (in-repo `release/v0.0.4/`).

---

## 5. Test plan

- **Unit (`internal/hints/hints_test.go`):**
  - `TestProfile_Resolve` — flag > env > json-default > default.
  - `TestProfile_Limit` — each profile truncates to its declared cap.
  - `TestProvider_Status_Pending` — pending>0 yields `consume_pending`.
  - `TestProvider_Status_Empty` — all-zero state yields zero hints.
  - `TestProvider_Status_MultiRoot` — N roots yield ≤N per-root drills.
  - `TestProvider_Roots_PerRoot` — each root present in output.
  - `TestProvider_DigestList_NewestN` — top 1–2 digests addressed.
  - `TestProvider_Version_NewerExists` — only suggests `update --apply` if newer.
  - `TestRender_Text_Aligned` — golden output check, two-space indent.
  - `TestRender_JSON_OmitEmpty` — empty HintSet produces no `"next"` key.
- **Integration (`cmd/sharedwatch/*_test.go` if present, else table-style):**
  - `status` text mode with synthetic state → expected hints visible.
  - `status --json --hints off` → no `"next"` key.
  - `overview --format json` → `drill` absent, `next` present.

---

## 6. References

- `src/internal/app/overview.go` — the existing `drill` map that this generalises.
- `src/internal/app/app.go` `StatusSnapshot` — source of the Context fields.
- `src/cmd/sharedwatch/main.go` — every handler that needs the integration call.
- `src/cmd/sharedwatch/roots.go` — recent precedent for per-command Go file.
- `src/cmd/sharedwatch/update.go` — recent precedent for stdlib-only feature.
- `docs/agent/agent-system-prompt-20260522.md` — should be updated to recommend `--hints agent`.
- `GITOPS.md` §10 — release flow this ticket follows at the end.
- `../src/CONTRIBUTING.md` §Versioning — v0.0.4 sits within pre-1.0 minor-as-noticeable-release framing; ship as patch increment.

---

## 7. Definition of done

All 14 acceptance criteria green. Release v0.0.4 visible at `release/v0.0.4/` + `release/LATEST` updated + `git push origin refs/heads/main refs/heads/v0.0.4 refs/tags/v0.0.4` succeeded. `curl -fsSL https://raw.githubusercontent.com/ab0t-com/sharedwatch/main/scripts/install.sh | bash` installs the v0.0.4 binary cleanly; `sharedwatch --hints agent status --json` returns a `"next"` array on a fresh init.

---

## 8. Notes on reusability (per user's "common across our projects")

The package is designed to be lifted into a separate Go module later. Specifically:

- Zero imports from `internal/app` or `internal/db`. Only the standard library + the `Context` struct shared across the binary.
- The `Provider` signature is a plain function over a value type — no interfaces to satisfy, no constructors, no globals to thread.
- `Register(command, provider)` is the only mutable surface; init order is documented.
- Profiles are strings, not enum constants in a foreign package — so a downstream module can add its own profile names without forking.
- The text renderer is a small helper using `text/tabwriter` from stdlib; the JSON shape is plain `[]Hint`.

To extract into its own module:

1. `git mv src/internal/hints/ <newmod>/hints/`
2. Update the module path in the package's import lines (one find-replace).
3. The `Context` struct is the only thing that would need to be either parameterised on a generic type or split into "core" fields + "extensions" map. Both are easy.

Worth doing if a second project in the org adopts the same pattern — flag it then.
