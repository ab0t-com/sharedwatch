package hooks

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunHookEmptyCommand — empty string short-circuits, no subprocess.
func TestRunHookEmptyCommand(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "", 5*time.Second)
	if res.Reason != ReasonEmpty {
		t.Fatalf("expected ReasonEmpty, got %s", res.Reason)
	}
	if res.Duration != 0 {
		t.Fatalf("expected zero duration for empty cmd, got %v", res.Duration)
	}
}

// TestRunHookEmptyAfterTrim — whitespace-only command also short-circuits.
// Catches the common "user pasted with trailing newline" case.
func TestRunHookEmptyAfterTrim(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "   \n  \t  ", 5*time.Second)
	if res.Reason != ReasonEmpty {
		t.Fatalf("expected ReasonEmpty for whitespace-only cmd, got %s", res.Reason)
	}
}

// TestRunHookSuccess — sh -c "true" → exit 0.
func TestRunHookSuccess(t *testing.T) {
	res := RunHook(context.Background(), `{"id":"dig_x"}`, "true", 5*time.Second)
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected ReasonSuccess, got %s (stderr=%q)", res.Reason, string(res.Stderr))
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", res.ExitCode)
	}
	if res.Command != "true" {
		t.Fatalf("expected Command=true, got %q", res.Command)
	}
}

// TestRunHookNonzeroExit — sh -c "false" → exit 1, ReasonNonzeroExit.
func TestRunHookNonzeroExit(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "false", 5*time.Second)
	if res.Reason != ReasonNonzeroExit {
		t.Fatalf("expected ReasonNonzeroExit, got %s", res.Reason)
	}
	if res.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", res.ExitCode)
	}
}

// TestRunHookExitCodePropagation — explicit exit 42 propagates.
func TestRunHookExitCodePropagation(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "exit 42", 5*time.Second)
	if res.ExitCode != 42 {
		t.Fatalf("expected exit 42, got %d", res.ExitCode)
	}
	if res.Reason != ReasonNonzeroExit {
		t.Fatalf("expected ReasonNonzeroExit, got %s", res.Reason)
	}
}

// TestRunHookTimeout — sleep longer than timeout → child killed, ReasonTimeout.
// Uses a short timeout so the test is fast; sleep is long enough to be
// unambiguously interrupted.
func TestRunHookTimeout(t *testing.T) {
	start := time.Now()
	res := RunHook(context.Background(), `{}`, "sleep 5", 100*time.Millisecond)
	elapsed := time.Since(start)
	if res.Reason != ReasonTimeout {
		t.Fatalf("expected ReasonTimeout, got %s (exit=%d)", res.Reason, res.ExitCode)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v (expected < 2s)", elapsed)
	}
	if res.ExitCode != -1 {
		t.Fatalf("expected ExitCode -1 on timeout, got %d", res.ExitCode)
	}
}

// TestRunHookStdinReceivesPayload — the payload JSON must reach the
// subprocess's stdin. Use `cat` to echo it back to stdout, then assert.
func TestRunHookStdinReceivesPayload(t *testing.T) {
	payload := `{"id":"dig_abc","summary":"test"}`
	res := RunHook(context.Background(), payload, "cat", 5*time.Second)
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}
	if string(res.Stdout) != payload {
		t.Fatalf("stdin/stdout round-trip mismatch:\n  sent:    %q\n  recv:    %q", payload, string(res.Stdout))
	}
}

// TestRunHookStdoutCaptured — subprocess stdout is captured byte-for-byte.
func TestRunHookStdoutCaptured(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "echo hello world", 5*time.Second)
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}
	got := strings.TrimRight(string(res.Stdout), "\n")
	if got != "hello world" {
		t.Fatalf("expected stdout=%q, got %q", "hello world", got)
	}
}

// TestRunHookStderrCaptured — stderr is captured separately from stdout.
func TestRunHookStderrCaptured(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "echo to-err 1>&2 && echo to-out", 5*time.Second)
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}
	if !strings.Contains(string(res.Stderr), "to-err") {
		t.Fatalf("expected stderr to contain 'to-err', got %q", string(res.Stderr))
	}
	if !strings.Contains(string(res.Stdout), "to-out") {
		t.Fatalf("expected stdout to contain 'to-out', got %q", string(res.Stdout))
	}
	if strings.Contains(string(res.Stdout), "to-err") {
		t.Fatalf("stderr leaked into stdout: %q", string(res.Stdout))
	}
}

// TestRunHookParentCtxCancel — when the parent ctx is cancelled, the
// subprocess is killed. The user-visible guarantee is that we don't
// hang past the parent cancel.
func TestRunHookParentCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res := RunHook(ctx, `{}`, "sleep 5", 10*time.Second)
	elapsed := time.Since(start)
	if res.Reason == ReasonSuccess {
		t.Fatalf("expected non-success (ctx cancel killed child), got success")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("parent ctx cancel took too long: %v", elapsed)
	}
}

// TestRunHookDefaultTimeout — zero / negative timeout defaults to 30s.
// We use `true` (instant) so the default doesn't slow the test.
func TestRunHookDefaultTimeout(t *testing.T) {
	res := RunHook(context.Background(), `{}`, "true", 0)
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success with default timeout, got %s", res.Reason)
	}
}
