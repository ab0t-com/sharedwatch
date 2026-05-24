// Package hooks implements the `--on-digest <command>` subprocess hook
// surface for sharedwatch (SW-AGENT-29 / v0.1.0).
//
// A hook is a single shell command (passed through `sh -c`) that the
// consumer fires AFTER each digest INSERT commits. The hook receives
// the digest JSON on stdin, runs async with a hard timeout, and never
// blocks the consumer. Failures are observable via meta-events written
// to the journal (`hook.completed` / `hook.failed`) — queryable via
// `events list --type hook.completed --type hook.failed`.
//
// Public surface:
//   - RunHook — in-memory subprocess execution (Phase 1).
//   - RunHookWithSidecar — same plus stdout/stderr tee'd to files
//     under <data_dir>/hooks/<digest_id>.{out,err} (Phase 2).
//   - EmitMetaEvent — writes the hook.completed / hook.failed event
//     into the journal via the EventEmitter interface (Phase 2).
//
// Phase 4 will wire these into the consumer call site so the daemon
// fires the hook async after every successful digest INSERT.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
)

// Reason narrows why a hook run produced its Result. Exactly one of
// these is set on every Result.
type Reason string

const (
	// ReasonSuccess means the hook ran to completion with exit code 0.
	ReasonSuccess Reason = "success"
	// ReasonEmpty means RunHook was called with an empty command string
	// — no subprocess was spawned. This is the no-op path the consumer
	// takes when the user didn't pass --on-digest.
	ReasonEmpty Reason = "empty"
	// ReasonTimeout means the hook subprocess was killed because it ran
	// longer than the configured timeout. ExitCode will be -1.
	ReasonTimeout Reason = "timeout"
	// ReasonNonzeroExit means the hook ran to completion but exited
	// with a non-zero status. ExitCode carries the actual exit code.
	ReasonNonzeroExit Reason = "nonzero_exit"
	// ReasonSpawnError means the subprocess could not be started at all
	// (e.g. `sh` not on PATH — pathological on POSIX, but worth surfacing).
	ReasonSpawnError Reason = "spawn_error"
)

// Result is what RunHook returns. EmitMetaEvent below uses it to
// build the `hook.completed` / `hook.failed` journal event so hook
// activity is queryable via `events list --type hook.*`.
type Result struct {
	// Command is the shell command exactly as supplied. Captured so the
	// meta-event payload can report what was run without the caller
	// having to thread the string through.
	Command string
	// Reason is the single-word classification of the outcome.
	Reason Reason
	// ExitCode is the subprocess exit code (or -1 if the process was
	// killed by signal / never started).
	ExitCode int
	// Duration is wall-clock time from spawn to completion (or timeout
	// kill). Zero for ReasonEmpty.
	Duration time.Duration
	// Stdout captured from the subprocess (in-memory). When sidecar
	// files are written this mirrors what's also on disk — kept for
	// the existing Phase-1 tests and for callers who want the bytes
	// without re-reading the file.
	Stdout []byte
	// Stderr captured from the subprocess (in-memory). Same mirroring.
	Stderr []byte
	// StdoutPath is the sidecar file the subprocess's stdout was tee'd
	// to. Empty when RunHook ran without sidecar (the Phase-1 shape).
	StdoutPath string
	// StderrPath is the sidecar file the subprocess's stderr was tee'd
	// to. Empty when RunHook ran without sidecar.
	StderrPath string
	// StdoutBytes / StderrBytes are the byte counts of the captured
	// streams. With sidecar enabled this matches the on-disk file size;
	// without sidecar it matches len(Stdout) / len(Stderr).
	StdoutBytes int64
	StderrBytes int64
	// SpawnErr is non-nil only when Reason == ReasonSpawnError. Carries
	// the underlying os/exec error so callers can log diagnostics.
	SpawnErr error
}

// RunHook executes the shell command via `sh -c "<command>"`, piping
// payloadJSON to its stdin, and waits up to timeout for completion.
// In-memory capture only — see RunHookWithSidecar to also write the
// stdout/stderr streams to files under a per-digest sidecar dir.
//
// Contract:
//   - Empty command → returns Result{Reason: ReasonEmpty} immediately.
//   - Caller passes ctx for shutdown signalling; ctx cancellation OR
//     timeout expiry kills the subprocess (SIGKILL on POSIX).
//   - Stdout/Stderr captured in memory (bounded by the subprocess's own
//     output; RunHookWithSidecar's file capture is bounded by disk).
//   - Never panics, never returns an error — all outcomes encoded in
//     Result.Reason. Callers can branch on Reason rather than err nil-checks.
func RunHook(ctx context.Context, payloadJSON, command string, timeout time.Duration) Result {
	return runHookInner(ctx, payloadJSON, command, timeout, "", "")
}

// RunHookWithSidecar is the sidecar-aware variant. In addition to the
// in-memory capture, stdout/stderr are tee'd to
// <sidecarDir>/<digestID>.{out,err} so the full streams survive past
// the hook process and are inspectable from the shell. When sidecarDir
// or digestID is empty the function degrades to in-memory-only
// (equivalent to RunHook).
//
// SW-AGENT-29 Phase 2. The consumer call site (Phase 4) supplies
// sidecarDir = filepath.Join(cfg.DataDir, "hooks") and digestID from
// the freshly-committed digest's ID.
func RunHookWithSidecar(ctx context.Context, payloadJSON, command string, timeout time.Duration, sidecarDir, digestID string) Result {
	return runHookInner(ctx, payloadJSON, command, timeout, sidecarDir, digestID)
}

func runHookInner(ctx context.Context, payloadJSON, command string, timeout time.Duration, sidecarDir, digestID string) Result {
	command = strings.TrimSpace(command)
	if command == "" {
		return Result{Reason: ReasonEmpty}
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// Bound the subprocess lifetime by deriving a ctx that cancels on
	// timeout. We rely on exec.CommandContext to SIGKILL the child when
	// either the parent ctx or the timeout fires.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Stdin = strings.NewReader(payloadJSON)

	// Always keep an in-memory mirror so the existing Phase-1 tests +
	// callers that don't want file I/O still get bytes back. When the
	// sidecar dir is supplied we tee the streams to files via
	// io.MultiWriter — the files become the durable record, the memory
	// buffer the fast-path view.
	var stdout, stderr bytes.Buffer
	var stdoutFile, stderrFile *os.File
	var stdoutPath, stderrPath string

	if sidecarDir != "" && digestID != "" {
		if err := os.MkdirAll(sidecarDir, 0o755); err == nil {
			stdoutPath = filepath.Join(sidecarDir, digestID+".out")
			stderrPath = filepath.Join(sidecarDir, digestID+".err")
			// Open with truncate semantics — re-runs with the same digest
			// id (re-emission, recovery) overwrite cleanly.
			if f, e := os.Create(stdoutPath); e == nil {
				stdoutFile = f
				defer stdoutFile.Close()
			} else {
				stdoutPath = ""
			}
			if f, e := os.Create(stderrPath); e == nil {
				stderrFile = f
				defer stderrFile.Close()
			} else {
				stderrPath = ""
			}
		}
		// If MkdirAll failed (disk full, EACCES, etc.) we fall back to
		// in-memory only. The Reason still reflects the subprocess
		// outcome — sidecar I/O failures are degraded silently rather
		// than masking the real hook result.
	}

	if stdoutFile != nil {
		cmd.Stdout = io.MultiWriter(&stdout, stdoutFile)
	} else {
		cmd.Stdout = &stdout
	}
	if stderrFile != nil {
		cmd.Stderr = io.MultiWriter(&stderr, stderrFile)
	} else {
		cmd.Stderr = &stderr
	}

	// WaitDelay bounds the post-cancel cleanup. Without this, `sh -c
	// "sleep 5"` orphans a `sleep` grandchild that inherits the stdout
	// pipe; ctx cancel kills `sh` but Wait blocks on the pipe until
	// `sleep` exits naturally. With WaitDelay, Wait returns after the
	// delay and the orphan is left to the OS reaper. 500ms is plenty
	// for normal cleanup; longer wastes the timeout budget when a
	// child legitimately ignores SIGKILL (rare; usually a uninterruptible
	// kernel syscall, which we can't help).
	cmd.WaitDelay = 500 * time.Millisecond

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	res := Result{
		Command:     command,
		Duration:    dur,
		Stdout:      stdout.Bytes(),
		Stderr:      stderr.Bytes(),
		StdoutPath:  stdoutPath,
		StderrPath:  stderrPath,
		StdoutBytes: int64(stdout.Len()),
		StderrBytes: int64(stderr.Len()),
	}

	// Classify outcome. The Go exec package surfaces three meaningful
	// shapes: nil err (success), *exec.ExitError (ran but non-zero or
	// killed), other err (spawn failed).
	if err == nil {
		res.ExitCode = 0
		res.Reason = ReasonSuccess
		return res
	}

	// Timeout / ctx-cancel: the child was killed. errors.Is catches both
	// the deadline and the parent-ctx cancel cases.
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.ExitCode = -1
		res.Reason = ReasonTimeout
		return res
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Subprocess ran but exited non-zero (or was killed by signal).
		// On signal kill, ExitCode is -1 — distinguish from ctx-cancel
		// above by falling through to nonzero_exit here.
		res.ExitCode = exitErr.ExitCode()
		res.Reason = ReasonNonzeroExit
		return res
	}

	// Anything else: the subprocess couldn't be spawned (sh not on PATH,
	// permission denied, etc.). Pathological but worth surfacing
	// cleanly.
	res.ExitCode = -1
	res.Reason = ReasonSpawnError
	res.SpawnErr = err
	return res
}

// EventEmitter is the subset of the storage interface the hook
// subsystem needs to write its meta-events into the journal. Defined
// here (not in db/) so the dependency direction stays clean — hooks
// imports events for the Type/Source constants, but does NOT import
// db. The consumer call site (Phase 4) passes its *db.Store which
// satisfies this interface.
type EventEmitter interface {
	InsertEvent(ctx context.Context, e events.Event) error
}

// metaPayload is the JSON shape carried on the `hook.completed` /
// `hook.failed` meta-events. Mirrors the design doc §3.3. Sidecar
// stdout/stderr full payloads are NOT inlined — only sizes + paths —
// to keep the journal lean (Q5 default).
type metaPayload struct {
	SchemaVersion int    `json:"schema_version"`
	HookCommand   string `json:"hook_command"`
	DigestID      string `json:"digest_id"`
	ExitCode      int    `json:"exit_code"`
	DurationMS    int64  `json:"duration_ms"`
	StdoutBytes   int64  `json:"stdout_bytes"`
	StderrBytes   int64  `json:"stderr_bytes"`
	StdoutPath    string `json:"stdout_path,omitempty"`
	StderrPath    string `json:"stderr_path,omitempty"`
	Reason        Reason `json:"reason"`
}

// EmitMetaEvent writes the `hook.completed` / `hook.failed` meta-event
// into the journal so hook activity is queryable through the existing
// events surface. Type is `hook.completed` iff Reason == ReasonSuccess;
// any other outcome (timeout, nonzero, spawn error, even empty) maps
// to `hook.failed` — agents can branch on the type without parsing
// the payload's Reason field.
//
// Empty-reason results (ReasonEmpty) are NOT emitted: nothing
// happened, so the journal stays quiet. Phase 4's consumer call site
// is expected to skip RunHook entirely when --on-digest is unset, so
// in practice EmitMetaEvent never sees ReasonEmpty — the guard is
// defensive.
//
// Returns the inserted event's ID, or an error if the JSON marshal
// or the InsertEvent call failed. Callers typically log-and-continue
// on error rather than fail the whole consume — hook failures must
// never block digest creation.
func EmitMetaEvent(ctx context.Context, store EventEmitter, res Result, digest digest.Digest) (string, error) {
	if res.Reason == ReasonEmpty {
		return "", nil
	}

	payload := metaPayload{
		SchemaVersion: 1,
		HookCommand:   res.Command,
		DigestID:      digest.ID,
		ExitCode:      res.ExitCode,
		DurationMS:    res.Duration.Milliseconds(),
		StdoutBytes:   res.StdoutBytes,
		StderrBytes:   res.StderrBytes,
		StdoutPath:    res.StdoutPath,
		StderrPath:    res.StderrPath,
		Reason:        res.Reason,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal hook meta payload: %w", err)
	}

	evtType := events.TypeHookCompleted
	if res.Reason != ReasonSuccess {
		evtType = events.TypeHookFailed
	}

	evt := events.Event{
		ID:   newEventID(),
		Type: evtType,
		// rel_path carries the digest id so existing path-based filters
		// (`events list --path-glob 'dig_*'`) can target hook events
		// without a separate filter. Path is left empty — sidecar paths
		// live in the payload.
		RelPath:     digest.ID,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceHook,
		Status:      events.StatusProcessed, // hook meta-events are terminal — no consumer touches them
		PayloadJSON: string(payloadJSON),
		WatchRoot:   digest.WatchRoot,
	}
	if err := store.InsertEvent(ctx, evt); err != nil {
		return "", fmt.Errorf("insert hook meta event: %w", err)
	}
	return evt.ID, nil
}

// newEventID — local "evt_" id generator. Mirrors the watcher's
// newID without leaking that helper across packages. 16 hex chars
// of crypto-random.
func newEventID() string {
	// Reuse the existing id format: "evt_" + 16 hex chars. Stable
	// for sql-level filters and human-skimmable in `events list`.
	return "evt_" + randHex(8)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := osReadRand(b); err != nil {
		// Fall back to a time-based id if /dev/urandom is unavailable
		// (pathological); collisions are astronomically unlikely at
		// our event rate.
		return fmt.Sprintf("%016x", time.Now().UnixNano())[:n*2]
	}
	const hex = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, by := range b {
		out[i*2] = hex[by>>4]
		out[i*2+1] = hex[by&0x0f]
	}
	return string(out)
}

// osReadRand is a seam for the rand source so tests can stub if
// needed; production uses crypto/rand.
var osReadRand = func(b []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.ReadFull(f, b)
}

// PruneOrphanedSidecars deletes hook sidecar files (.out / .err)
// under sidecarDir whose corresponding digest no longer exists per
// the isStillTracked predicate. Returns the number of files deleted.
//
// Called from reconcile's retention pass (Phase 5) so sidecar lifetime
// tracks digest lifetime — once `db.PruneArchivedDigests` removes a
// digest row, its sidecar files become orphans and are cleaned up
// here on the next reconcile cycle.
//
// The function is conservative: if sidecarDir doesn't exist, return
// (0, nil) — nothing to do. If a file can't be deleted (permission,
// race with another reader), log via the returned error count rather
// than abort the whole pass.
//
// SW-AGENT-29 Phase 5.
func PruneOrphanedSidecars(sidecarDir string, isStillTracked func(digestID string) bool) (int, error) {
	if sidecarDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(sidecarDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read sidecar dir: %w", err)
	}
	deleted := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Sidecar files follow <digest_id>.out / <digest_id>.err.
		// Parse the digest id by stripping the suffix.
		var digestID string
		switch {
		case strings.HasSuffix(name, ".out"):
			digestID = strings.TrimSuffix(name, ".out")
		case strings.HasSuffix(name, ".err"):
			digestID = strings.TrimSuffix(name, ".err")
		default:
			// Unrecognised file in the hooks dir — leave it alone.
			// Operator scripts (audit pipelines, manual notes) may
			// drop files here; sharedwatch only owns the .out/.err
			// it created.
			continue
		}
		if isStillTracked(digestID) {
			continue
		}
		if err := os.Remove(filepath.Join(sidecarDir, name)); err != nil {
			// Continue past per-file errors; reconcile pass shouldn't
			// abort a retention cycle for a single permission glitch.
			continue
		}
		deleted++
	}
	return deleted, nil
}
