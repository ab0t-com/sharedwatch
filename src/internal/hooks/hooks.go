// Package hooks implements the `--on-digest <command>` subprocess hook
// surface for sharedwatch (SW-AGENT-29 / v0.1.0).
//
// A hook is a single shell command (passed through `sh -c`) that the
// consumer fires AFTER each digest INSERT commits. The hook receives
// the digest JSON on stdin, runs async with a hard timeout, and never
// blocks the consumer. Failures are observable via meta-events written
// to the journal (`hook.completed` / `hook.failed`) — Phase 2 of the
// tasklist.
//
// This Phase-1 file implements the subprocess execution surface only:
// real `sh -c` execution, ctx-cancel timeout, stdin payload pipe, exit
// code + reason classification. Sidecar file capture and meta-event
// emission land in Phase 2.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
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

// Result is what RunHook returns. Callers (Phase 2 will be the
// emit-meta-event path) use this to build the `hook.completed` /
// `hook.failed` journal event.
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
	// Stdout captured from the subprocess. Phase 2 will mirror this to
	// a sidecar file; for now the caller can read it directly.
	Stdout []byte
	// Stderr captured from the subprocess. Same Phase 2 plan.
	Stderr []byte
	// SpawnErr is non-nil only when Reason == ReasonSpawnError. Carries
	// the underlying os/exec error so callers can log diagnostics.
	SpawnErr error
}

// RunHook executes the shell command via `sh -c "<command>"`, piping
// payloadJSON to its stdin, and waits up to timeout for completion.
//
// Contract:
//   - Empty command → returns Result{Reason: ReasonEmpty} immediately.
//   - Caller passes ctx for shutdown signalling; ctx cancellation OR
//     timeout expiry kills the subprocess (SIGKILL on POSIX).
//   - Stdout/Stderr captured in memory (bounded by the subprocess's own
//     output; Phase 2 adds file-based capture for unbounded streams).
//   - Never panics, never returns an error — all outcomes encoded in
//     Result.Reason. Callers can branch on Reason rather than err nil-checks.
func RunHook(ctx context.Context, payloadJSON, command string, timeout time.Duration) Result {
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
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
		Command:  command,
		Duration: dur,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
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

// Ensure the io import isn't unused — Phase 2 will need it for the
// sidecar pipe wiring. Removing the blank ref here would force a
// re-add then, so keep a no-op reference.
var _ = io.Discard
var _ = fmt.Sprint
