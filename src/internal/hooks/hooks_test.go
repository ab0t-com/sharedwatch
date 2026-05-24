package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
)

// fakeEmitter records every InsertEvent call so tests can inspect
// what the hook subsystem wrote into the journal without standing up
// a real SQLite store.
type fakeEmitter struct {
	events []events.Event
	failOn bool
}

func (f *fakeEmitter) InsertEvent(ctx context.Context, e events.Event) error {
	if f.failOn {
		return context.Canceled // arbitrary non-nil
	}
	f.events = append(f.events, e)
	return nil
}

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

// TestRunHookParentCtxCancel — when the parent ctx is cancelled (e.g.
// SIGTERM during shutdown), the subprocess is killed AND classified
// as ReasonCancelled (distinct from ReasonTimeout). Audit A4 caught
// the prior version mis-classifying this as ReasonNonzeroExit.
func TestRunHookParentCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res := RunHook(ctx, `{}`, "sleep 5", 10*time.Second)
	elapsed := time.Since(start)
	if res.Reason != ReasonCancelled {
		t.Fatalf("expected ReasonCancelled, got %s (exit=%d)", res.Reason, res.ExitCode)
	}
	if res.ExitCode != -1 {
		t.Fatalf("expected ExitCode -1 on ctx cancel, got %d", res.ExitCode)
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

// --- Phase 2 — sidecar capture + meta-event emission ---

// TestRunHookWithSidecarCreatesFiles — when sidecarDir + digestID are
// supplied, the function MUST tee stdout/stderr to files named
// <digestID>.out and <digestID>.err under the dir.
func TestRunHookWithSidecarCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	digestID := "dig_abc123"
	res := RunHookWithSidecar(context.Background(), `{"id":"dig_abc123"}`,
		"echo hello && echo oops 1>&2", 5*time.Second, dir, digestID)

	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s (stderr=%q)", res.Reason, string(res.Stderr))
	}
	if res.StdoutPath != filepath.Join(dir, digestID+".out") {
		t.Fatalf("unexpected StdoutPath: %q", res.StdoutPath)
	}
	if res.StderrPath != filepath.Join(dir, digestID+".err") {
		t.Fatalf("unexpected StderrPath: %q", res.StderrPath)
	}
	// File contents must match what we captured in memory.
	out, err := os.ReadFile(res.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(res.Stdout) {
		t.Fatalf("stdout file vs memory mismatch:\n  file: %q\n  mem:  %q", string(out), string(res.Stdout))
	}
	errBytes, err := os.ReadFile(res.StderrPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(errBytes) != string(res.Stderr) {
		t.Fatalf("stderr file vs memory mismatch")
	}
	// Sizes must match too.
	if res.StdoutBytes != int64(len(res.Stdout)) {
		t.Fatalf("StdoutBytes mismatch: field=%d, len=%d", res.StdoutBytes, len(res.Stdout))
	}
	if res.StderrBytes != int64(len(res.Stderr)) {
		t.Fatalf("StderrBytes mismatch")
	}
}

// TestRunHookWithSidecarEmptyDirSkipsFiles — when sidecarDir is empty
// (the production "user didn't set --on-digest" path), no files are
// written and the paths remain empty.
func TestRunHookWithSidecarEmptyDirSkipsFiles(t *testing.T) {
	res := RunHookWithSidecar(context.Background(), `{}`, "echo x", 5*time.Second, "", "dig_x")
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}
	if res.StdoutPath != "" || res.StderrPath != "" {
		t.Fatalf("expected empty sidecar paths, got %q / %q", res.StdoutPath, res.StderrPath)
	}
	// But in-memory capture still works.
	if !strings.Contains(string(res.Stdout), "x") {
		t.Fatalf("expected stdout 'x', got %q", string(res.Stdout))
	}
}

// TestRunHookWithSidecarNonExistentDirCreates — sidecar dir is auto-
// created if missing (MkdirAll). The consumer call site can pass a
// path that doesn't yet exist on first run.
func TestRunHookWithSidecarNonExistentDirCreates(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "hooks")
	res := RunHookWithSidecar(context.Background(), `{}`, "echo created", 5*time.Second, dir, "dig_new")
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected sidecar dir to be created: %v", err)
	}
}

// TestRunHookWithSidecarLargeOutput — file capture handles output
// larger than a typical pipe buffer (64KB+). Verifies the io.MultiWriter
// streaming path doesn't deadlock on big outputs.
func TestRunHookWithSidecarLargeOutput(t *testing.T) {
	dir := t.TempDir()
	// Generate ~256KB of stdout.
	res := RunHookWithSidecar(context.Background(), `{}`,
		"yes hello | head -c 262144", 10*time.Second, dir, "dig_big")
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}
	if res.StdoutBytes < 250000 {
		t.Fatalf("expected ~256KB of stdout, got %d bytes", res.StdoutBytes)
	}
	stat, err := os.Stat(res.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != res.StdoutBytes {
		t.Fatalf("file size (%d) != StdoutBytes (%d)", stat.Size(), res.StdoutBytes)
	}
}

// TestEmitMetaEventSuccess — a successful hook emits hook.completed
// with the right payload shape.
func TestEmitMetaEventSuccess(t *testing.T) {
	em := &fakeEmitter{}
	res := Result{
		Command:     "echo ok",
		Reason:      ReasonSuccess,
		ExitCode:    0,
		Duration:    123 * time.Millisecond,
		StdoutBytes: 3,
		StderrBytes: 0,
		StdoutPath:  "/var/.../hooks/dig_x.out",
	}
	d := digest.Digest{ID: "dig_x", WatchRoot: "code"}

	id, err := EmitMetaEvent(context.Background(), em, res, d)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || !strings.HasPrefix(id, "evt_") {
		t.Fatalf("expected evt_ id, got %q", id)
	}
	if len(em.events) != 1 {
		t.Fatalf("expected 1 inserted event, got %d", len(em.events))
	}
	got := em.events[0]
	if got.Type != events.TypeHookCompleted {
		t.Fatalf("expected type hook.completed, got %s", got.Type)
	}
	if got.Source != events.SourceHook {
		t.Fatalf("expected source hook, got %s", got.Source)
	}
	if got.Status != events.StatusProcessed {
		t.Fatalf("expected status processed (terminal), got %s", got.Status)
	}
	if got.RelPath != "dig_x" {
		t.Fatalf("expected rel_path=dig_x, got %q", got.RelPath)
	}
	if got.WatchRoot != "code" {
		t.Fatalf("expected watch_root=code, got %q", got.WatchRoot)
	}
	// Payload shape check.
	var p metaPayload
	if err := json.Unmarshal([]byte(got.PayloadJSON), &p); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if p.SchemaVersion != 1 {
		t.Fatalf("expected schema_version=1, got %d", p.SchemaVersion)
	}
	if p.HookCommand != "echo ok" {
		t.Fatalf("expected hook_command=%q, got %q", "echo ok", p.HookCommand)
	}
	if p.DigestID != "dig_x" {
		t.Fatalf("expected digest_id=dig_x, got %q", p.DigestID)
	}
	if p.ExitCode != 0 {
		t.Fatalf("expected exit_code=0, got %d", p.ExitCode)
	}
	if p.DurationMS != 123 {
		t.Fatalf("expected duration_ms=123, got %d", p.DurationMS)
	}
	if p.Reason != ReasonSuccess {
		t.Fatalf("expected reason=success, got %s", p.Reason)
	}
}

// TestEmitMetaEventFailure — any non-success reason emits hook.failed.
// Use a timeout outcome to exercise the typical failure shape.
func TestEmitMetaEventFailure(t *testing.T) {
	em := &fakeEmitter{}
	res := Result{
		Command:  "sleep 99",
		Reason:   ReasonTimeout,
		ExitCode: -1,
		Duration: 1 * time.Second,
	}
	d := digest.Digest{ID: "dig_t"}
	_, err := EmitMetaEvent(context.Background(), em, res, d)
	if err != nil {
		t.Fatal(err)
	}
	if em.events[0].Type != events.TypeHookFailed {
		t.Fatalf("expected hook.failed, got %s", em.events[0].Type)
	}
	var p metaPayload
	_ = json.Unmarshal([]byte(em.events[0].PayloadJSON), &p)
	if p.Reason != ReasonTimeout {
		t.Fatalf("expected reason=timeout, got %s", p.Reason)
	}
}

// TestEmitMetaEventCancelled — ReasonCancelled (parent ctx cancel
// during shutdown) also maps to hook.failed. Distinct from timeout
// in the payload's reason field. Audit A4 regression guard.
func TestEmitMetaEventCancelled(t *testing.T) {
	em := &fakeEmitter{}
	res := Result{
		Command:  "sleep 99",
		Reason:   ReasonCancelled,
		ExitCode: -1,
		Duration: 50 * time.Millisecond,
	}
	d := digest.Digest{ID: "dig_c"}
	_, err := EmitMetaEvent(context.Background(), em, res, d)
	if err != nil {
		t.Fatal(err)
	}
	if em.events[0].Type != events.TypeHookFailed {
		t.Fatalf("expected hook.failed for cancelled, got %s", em.events[0].Type)
	}
	var p metaPayload
	_ = json.Unmarshal([]byte(em.events[0].PayloadJSON), &p)
	if p.Reason != ReasonCancelled {
		t.Fatalf("expected reason=cancelled, got %s", p.Reason)
	}
}

// TestEmitMetaEventEmptySkips — ReasonEmpty means RunHook was a no-op.
// No meta-event should be written.
func TestEmitMetaEventEmptySkips(t *testing.T) {
	em := &fakeEmitter{}
	res := Result{Reason: ReasonEmpty}
	id, err := EmitMetaEvent(context.Background(), em, res, digest.Digest{ID: "dig_e"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("expected empty id for empty reason, got %q", id)
	}
	if len(em.events) != 0 {
		t.Fatalf("expected no event inserted, got %d", len(em.events))
	}
}

// TestEmitMetaEventStoreErrorBubbles — when the store insert fails,
// EmitMetaEvent surfaces the error so callers can log it. (Production
// call site in Phase 4 will log-and-continue; the hook outcome itself
// must never block digest creation.)
func TestEmitMetaEventStoreErrorBubbles(t *testing.T) {
	em := &fakeEmitter{failOn: true}
	res := Result{Command: "true", Reason: ReasonSuccess}
	_, err := EmitMetaEvent(context.Background(), em, res, digest.Digest{ID: "dig_z"})
	if err == nil {
		t.Fatal("expected error when store insert fails")
	}
}

// --- Phase 5 — sidecar retention pruning ---

// TestPruneOrphanedSidecarsRemovesOrphans — the canonical Phase 5
// case: some digests are still tracked, some have been pruned, the
// orphaned sidecar files get removed and tracked ones survive.
func TestPruneOrphanedSidecarsRemovesOrphans(t *testing.T) {
	dir := t.TempDir()
	// Create three sidecar pairs.
	for _, id := range []string{"dig_keep", "dig_drop", "dig_also_drop"} {
		os.WriteFile(filepath.Join(dir, id+".out"), []byte("stdout"), 0o644)
		os.WriteFile(filepath.Join(dir, id+".err"), []byte("stderr"), 0o644)
	}
	// Also drop an unrelated file — should be left alone.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("operator notes"), 0o644)

	tracked := map[string]bool{"dig_keep": true}
	deleted, err := PruneOrphanedSidecars(dir, func(id string) bool {
		return tracked[id]
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2 orphan ids × 2 files each = 4 deletions.
	if deleted != 4 {
		t.Fatalf("expected 4 deletions, got %d", deleted)
	}
	// dig_keep files survive.
	if _, err := os.Stat(filepath.Join(dir, "dig_keep.out")); err != nil {
		t.Fatalf("dig_keep.out was wrongly deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "dig_keep.err")); err != nil {
		t.Fatalf("dig_keep.err was wrongly deleted")
	}
	// dig_drop files are gone.
	if _, err := os.Stat(filepath.Join(dir, "dig_drop.out")); !os.IsNotExist(err) {
		t.Fatalf("dig_drop.out should be deleted")
	}
	// Unrelated file is preserved.
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatalf("notes.txt was wrongly deleted (operator-owned files must stay)")
	}
}

// TestPruneOrphanedSidecarsEmptyDir — empty sidecar dir is a no-op,
// no error.
func TestPruneOrphanedSidecarsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	n, err := PruneOrphanedSidecars(dir, func(id string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 deletions on empty dir, got %d", n)
	}
}

// TestPruneOrphanedSidecarsMissingDir — non-existent sidecar dir is
// a no-op (this is the "no hooks have ever fired" startup state),
// not an error.
func TestPruneOrphanedSidecarsMissingDir(t *testing.T) {
	n, err := PruneOrphanedSidecars(filepath.Join(t.TempDir(), "does-not-exist"), func(id string) bool { return true })
	if err != nil {
		t.Fatalf("expected no error for missing dir, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 deletions for missing dir, got %d", n)
	}
}

// TestPruneOrphanedSidecarsEmptyPathNoop — empty sidecarDir param
// (operator passed in zero-value DataDir, e.g. in tests) is a no-op.
func TestPruneOrphanedSidecarsEmptyPathNoop(t *testing.T) {
	n, err := PruneOrphanedSidecars("", func(id string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

// TestRunHookEndToEndWithSidecarAndMetaEvent — the canonical Phase 2
// happy path: real subprocess → sidecar files → meta-event in the
// (fake) journal. Sizes in the meta-event payload must match the
// actual on-disk sidecar file sizes.
func TestRunHookEndToEndWithSidecarAndMetaEvent(t *testing.T) {
	dir := t.TempDir()
	digestID := "dig_e2e"
	d := digest.Digest{ID: digestID, WatchRoot: "code"}

	res := RunHookWithSidecar(context.Background(),
		`{"id":"`+digestID+`","summary":"e2e"}`,
		"echo OK && echo problem 1>&2",
		5*time.Second, dir, digestID)
	if res.Reason != ReasonSuccess {
		t.Fatalf("expected success, got %s", res.Reason)
	}

	em := &fakeEmitter{}
	_, err := EmitMetaEvent(context.Background(), em, res, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(em.events) != 1 {
		t.Fatalf("expected 1 meta-event, got %d", len(em.events))
	}

	var p metaPayload
	_ = json.Unmarshal([]byte(em.events[0].PayloadJSON), &p)

	// On-disk file sizes must equal what the meta-event advertises.
	outStat, _ := os.Stat(res.StdoutPath)
	errStat, _ := os.Stat(res.StderrPath)
	if p.StdoutBytes != outStat.Size() {
		t.Fatalf("meta StdoutBytes=%d but file is %d", p.StdoutBytes, outStat.Size())
	}
	if p.StderrBytes != errStat.Size() {
		t.Fatalf("meta StderrBytes=%d but file is %d", p.StderrBytes, errStat.Size())
	}
	if p.StdoutPath != res.StdoutPath {
		t.Fatalf("meta StdoutPath=%q != res.StdoutPath=%q", p.StdoutPath, res.StdoutPath)
	}
}
