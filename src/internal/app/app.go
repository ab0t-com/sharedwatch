package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sharedwatch/internal/config"
	"sharedwatch/internal/consumer"
	"sharedwatch/internal/db"
	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
	"sharedwatch/internal/hints"
	"sharedwatch/internal/hooks"
	"sharedwatch/internal/mode"
	"sharedwatch/internal/reconcile"
	"sharedwatch/internal/watcher"
)

type App struct {
	Cfg       config.Config
	Store     db.Adapter
	Watcher   watcher.Service
	Consumer  consumer.Service
	Reconcile reconcile.Service
	Logger    *slog.Logger

	// hooksWG tracks in-flight async --on-digest subprocesses so a
	// graceful shutdown can wait for them (bounded by WaitForHooks's
	// timeout) before exit. Internal — callers use FireHookAsync to
	// register work and WaitForHooks to drain. SW-AGENT-29 Phase 4.
	hooksWG sync.WaitGroup
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	return NewWithLogger(ctx, cfg, slog.Default())
}

func NewWithLogger(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	// Auto-mkdir every configured root. In multi-root mode each root gets
	// its own directory created; legacy single-root falls back to WatchPath.
	if len(cfg.WatchRoots) > 0 {
		for _, r := range cfg.WatchRoots {
			if r.Path == "" {
				continue
			}
			if err := os.MkdirAll(r.Path, 0o755); err != nil {
				return nil, fmt.Errorf("create watch root %s (%s): %w", r.Label, r.Path, err)
			}
		}
	} else if cfg.WatchPath != "" {
		if err := os.MkdirAll(cfg.WatchPath, 0o755); err != nil {
			return nil, fmt.Errorf("create watch path %s: %w", cfg.WatchPath, err)
		}
	}
	store, err := db.OpenAdapter(ctx, cfg.StorageType, cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		Cfg:   cfg,
		Store: store,
		Watcher: watcher.Service{
			Store: store, WatchPath: cfg.WatchPath, Roots: cfg.WatchRoots, Recursive: cfg.Recursive,
			CoalesceWindow: cfg.CoalesceWindow,
			IgnorePatterns: cfg.IgnorePatterns, IncludePatterns: cfg.IncludePatterns,
			HashEnabled: cfg.HashEnabled, HashMaxSize: cfg.HashMaxSize,
			ProducerID:  cfg.ProducerID,
			PayloadJSON: cfg.PayloadJSON,
		},
		Consumer: consumer.Service{Store: store},
		Reconcile: reconcile.Service{
			Store: store, WatchPath: cfg.WatchPath, Roots: cfg.WatchRoots, Recursive: cfg.Recursive,
			Window:         cfg.CoalesceWindow,
			IgnorePatterns: cfg.IgnorePatterns, IncludePatterns: cfg.IncludePatterns,
			HashEnabled: cfg.HashEnabled, HashMaxSize: cfg.HashMaxSize,
			RetentionDays: cfg.RetentionDays,
			ProducerID:    cfg.ProducerID,
			PayloadJSON:   cfg.PayloadJSON,
			ActorTTL:      cfg.ActorTTL,
		},
		Logger: logger,
	}, nil
}

func (a *App) Close() error { return a.Store.Close() }

type StatusSnapshot struct {
	// FormatVersion is the first key emitted; lets agents validate the
	// envelope before scanning. Constant `1` until a breaking shape change
	// is needed. See sharedwatch/docs/OUTPUT_CONTRACT.md.
	FormatVersion    int        `json:"format_version"`
	Mode             string     `json:"mode"`
	Pending          int        `json:"pending"`
	Failed           int        `json:"failed"`
	Digests          int        `json:"digests"`
	UnreadDigests    int        `json:"unread_digests"`
	ArchivedDigests  int        `json:"archived_digests"`
	ActiveUntil      *time.Time `json:"active_until,omitempty"`
	ActiveExpiresIn  string     `json:"active_expires_in,omitempty"`
	LastEventAt      *time.Time `json:"last_event_at,omitempty"`
	LastConsumerRun  *time.Time `json:"last_consumer_run,omitempty"`
	LastReconcileRun *time.Time `json:"last_reconcile_run,omitempty"`
	// Actors is populated only when the caller asks for it (via `status
	// --actors`). Pointer-to-slice with omitempty: nil → omitted (default
	// status JSON shape unchanged); non-nil pointer → emitted, even if the
	// underlying slice is empty (so `--actors --json` with zero registered
	// actors still surfaces `"actors": []` rather than silently dropping
	// the key). SW-AGENT-25 (F36-D).
	Actors *[]ActorView `json:"actors,omitempty"`
	// Roots is populated only when multi-root is configured (Cfg.WatchRoots
	// non-empty). Single-root setups never emit this key — preserves the
	// pre-SW-AGENT-3 JSON contract for legacy callers.
	Roots []RootView `json:"roots,omitempty"`
	// Next carries optional next-step suggestions emitted by the hints
	// engine (SW-AGENT-17). Populated by the cmd layer when --hints is
	// not 'off'. omitempty — JSON consumers that don't know the field can
	// continue to ignore it.
	Next []hints.Hint `json:"next,omitempty"`
}

// RootView is the status-time projection of one configured watch-root.
type RootView struct {
	Label       string     `json:"label"`
	Path        string     `json:"path"`
	Pending     int        `json:"pending"`
	LastEventAt *time.Time `json:"last_event_at,omitempty"`
}

// RootsView returns one RootView per configured WatchRoot. Empty when
// single-root mode is in effect (Cfg.WatchRoots == nil). The pending count
// and last_event_at are computed via SQL aggregates against events.watch_root.
func (a *App) RootsView(ctx context.Context) ([]RootView, error) {
	if len(a.Cfg.WatchRoots) == 0 {
		return nil, nil
	}
	out := make([]RootView, 0, len(a.Cfg.WatchRoots))
	for _, r := range a.Cfg.WatchRoots {
		pending, err := a.Store.PendingCountByRoot(ctx, r.Label)
		if err != nil {
			return nil, err
		}
		last, err := a.Store.LastEventAtByRoot(ctx, r.Label)
		if err != nil {
			return nil, err
		}
		view := RootView{Label: r.Label, Path: r.Path, Pending: pending}
		if !last.IsZero() {
			t := last.UTC()
			view.LastEventAt = &t
		}
		out = append(out, view)
	}
	return out, nil
}

// ActorView is the status-time projection of an actor registry row. Stale is
// derived against the running config's ActorTTL.
type ActorView struct {
	ActorID       string    `json:"actor_id"`
	Label         string    `json:"label,omitempty"`
	ActorKind     string    `json:"actor_kind,omitempty"`
	Focus         string    `json:"focus,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Stale         bool      `json:"stale"`
}

// ActorsView returns the current actors registry projected for status output,
// with Stale computed against Cfg.ActorTTL.
func (a *App) ActorsView(ctx context.Context) ([]ActorView, error) {
	rows, err := a.Store.ListActors(ctx)
	if err != nil {
		return nil, err
	}
	ttl := a.Cfg.ActorTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	threshold := time.Now().UTC().Add(-ttl)
	out := make([]ActorView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ActorView{
			ActorID:       r.ActorID,
			Label:         r.Label,
			ActorKind:     r.ActorKind,
			Focus:         r.Focus,
			LastHeartbeat: r.LastHeartbeat,
			Stale:         r.LastHeartbeat.Before(threshold),
		})
	}
	return out, nil
}

func (a *App) StatusSnapshot(ctx context.Context) (StatusSnapshot, error) {
	rt, err := a.Store.GetRuntime(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	pending, err := a.Store.PendingCount(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	health, _ := a.Store.Health(ctx)
	now := time.Now().UTC()
	snap := StatusSnapshot{
		FormatVersion:    1,
		Mode:             rt.EffectiveMode(now),
		Pending:          pending,
		Failed:           health.FailedEvents,
		Digests:          health.TotalDigests,
		UnreadDigests:    health.UnreadDigests,
		ArchivedDigests:  health.ArchivedDigests,
		ActiveUntil:      nonZeroTime(rt.ActiveUntil),
		LastEventAt:      nonZeroTime(rt.LastEventAt),
		LastConsumerRun:  nonZeroTime(rt.LastConsumerRun),
		LastReconcileRun: nonZeroTime(rt.LastReconcileRun),
	}
	if snap.Mode == mode.Active && !rt.ActiveUntil.IsZero() {
		if d := rt.ActiveUntil.Sub(now); d > 0 {
			snap.ActiveExpiresIn = d.Round(time.Second).String()
		}
	}
	// Multi-root: populate Roots automatically so `status --json` shows
	// per-root pending counts. Single-root invocations skip this (RootsView
	// returns nil when WatchRoots is empty).
	if roots, err := a.RootsView(ctx); err == nil && len(roots) > 0 {
		snap.Roots = roots
	}
	return snap, nil
}

func (a *App) Status(ctx context.Context) (string, error) {
	snap, err := a.StatusSnapshot(ctx)
	if err != nil {
		return "", err
	}
	expires := snap.ActiveExpiresIn
	if expires == "" {
		expires = "-"
	}
	return fmt.Sprintf("mode=%s pending=%d failed=%d digests=%d unread_digests=%d archived_digests=%d active_until=%s active_expires_in=%s last_event=%s last_consumer=%s last_reconcile=%s",
		snap.Mode, snap.Pending, snap.Failed, snap.Digests, snap.UnreadDigests, snap.ArchivedDigests,
		showTimePtr(snap.ActiveUntil), expires, showTimePtr(snap.LastEventAt), showTimePtr(snap.LastConsumerRun), showTimePtr(snap.LastReconcileRun)), nil
}

func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func showTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func (a *App) SetActive(ctx context.Context, ttl time.Duration) error {
	rt, err := a.Store.GetRuntime(ctx)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		ttl = a.Cfg.ActiveTTL
	}
	rt.Mode = mode.Active
	rt.ActiveUntil = time.Now().UTC().Add(ttl)
	return a.Store.UpsertRuntime(ctx, rt)
}

func (a *App) SetPassive(ctx context.Context) error {
	rt, err := a.Store.GetRuntime(ctx)
	if err != nil {
		return err
	}
	rt.Mode = mode.Passive
	rt.ActiveUntil = time.Time{}
	return a.Store.UpsertRuntime(ctx, rt)
}

func (a *App) TestEmit(ctx context.Context, relPath string) (events.Event, error) {
	return a.TestEmitWithPayload(ctx, relPath, "")
}

func (a *App) TestEmitWithPayload(ctx context.Context, relPath, payloadJSON string) (events.Event, error) {
	e, _, err := a.TestEmitWithPayloadResult(ctx, relPath, payloadJSON)
	return e, err
}

// TestEmitWithPayloadResult is the result-returning variant. coalescedIntoID
// is the prior event's id if the emit coalesced into an existing pending
// event in the (rel_path, watch_root, actor) coalesce window; empty if a
// fresh row was inserted. The CLI `test emit` handler uses this to print
// "coalesced into <prior_id>" instead of returning a phantom id.
func (a *App) TestEmitWithPayloadResult(ctx context.Context, relPath, payloadJSON string) (events.Event, string, error) {
	return a.TestEmitWithPayloadResultToRoot(ctx, relPath, "", payloadJSON)
}

// TestEmitWithPayloadResultToRoot is the root-aware variant. When rootLabel
// is empty, falls back to the legacy "use roots[0]" behaviour. When non-empty,
// routes to the named configured root or errors with available labels.
// SW-AGENT-27.
func (a *App) TestEmitWithPayloadResultToRoot(ctx context.Context, relPath, rootLabel, payloadJSON string) (events.Event, string, error) {
	e, coalescedInto, err := a.Watcher.EmitSyntheticToRoot(ctx, relPath, rootLabel, events.TypeModified, events.SourceTest, payloadJSON)
	if err != nil {
		return events.Event{}, "", err
	}
	rt, _ := a.Store.GetRuntime(ctx)
	rt.LastEventAt = time.Now().UTC()
	_ = a.Store.UpsertRuntime(ctx, rt)
	return e, coalescedInto, nil
}

func (a *App) ListDigests(ctx context.Context, limit int) ([]digest.Digest, error) {
	return a.Store.ListDigests(ctx, limit)
}

func (a *App) GetDigest(ctx context.Context, id string) (digest.Digest, error) {
	return a.Store.GetDigest(ctx, id)
}

func (a *App) ConsumeNow(ctx context.Context) (digest.Digest, bool, error) {
	rt, err := a.Store.GetRuntime(ctx)
	if err != nil {
		return digest.Digest{}, false, err
	}
	m := rt.EffectiveMode(time.Now().UTC())
	d, ok, err := a.Consumer.ConsumePending(ctx, m, a.Cfg.MaxBatchSize)
	if err != nil {
		return digest.Digest{}, false, err
	}
	if ok {
		rt.LastConsumerRun = time.Now().UTC()
		if m == mode.Active {
			rt.ActiveUntil = time.Now().UTC().Add(a.Cfg.ActiveTTL)
		}
		_ = a.Store.UpsertRuntime(ctx, rt)
		// SW-AGENT-29 Phase 4: fire the --on-digest hook async. The
		// digest INSERT has already committed by this point, so the
		// hook always sees a complete row. FireHookAsync returns
		// immediately; the goroutine writes the meta-event when the
		// subprocess finishes (or times out).
		a.FireHookAsync(ctx, d)
	}
	return d, ok, nil
}

// FireHookAsync spawns the configured --on-digest hook for d, if any.
// Returns immediately — the subprocess runs in a goroutine bounded by
// cfg.OnDigestTimeout. WaitForHooks drains in-flight runs at shutdown.
// SW-AGENT-29 Phase 4.
func (a *App) FireHookAsync(ctx context.Context, d digest.Digest) {
	if a.Cfg.OnDigest == "" {
		return
	}
	// Marshal the digest into the JSON shape the hook receives on stdin.
	// Mirrors `digest show --json` so consumers can use the same jq
	// recipes either way (see tasklist §3.2).
	payload, err := json.Marshal(struct {
		FormatVersion int       `json:"format_version"`
		ID            string    `json:"id"`
		CreatedAt     time.Time `json:"created_at"`
		WindowStart   time.Time `json:"window_start"`
		WindowEnd     time.Time `json:"window_end"`
		Mode          string    `json:"mode"`
		EventCount    int       `json:"event_count"`
		Summary       string    `json:"summary"`
		Status        string    `json:"status"`
		WatchRoot     string    `json:"watch_root,omitempty"`
	}{
		FormatVersion: 1,
		ID:            d.ID,
		CreatedAt:     d.CreatedAt,
		WindowStart:   d.WindowStart,
		WindowEnd:     d.WindowEnd,
		Mode:          d.Mode,
		EventCount:    d.EventCount,
		Summary:       d.Summary,
		Status:        string(d.Status),
		WatchRoot:     d.WatchRoot,
	})
	if err != nil {
		// Pathological — Digest is all simple types. Log and skip
		// rather than crash the consumer.
		a.Logger.Error("on-digest hook: marshal failed", "err", err, "digest_id", d.ID)
		return
	}

	a.hooksWG.Add(1)
	go func() {
		defer a.hooksWG.Done()

		// Use a fresh context (not the caller's ctx) so a Consumer-
		// triggered context cancel doesn't kill an in-flight hook that
		// the WaitGroup is meant to drain. The hook's own timeout
		// bounds it; WaitForHooks at shutdown is the outer bound.
		hookCtx := context.Background()
		sidecarDir := filepath.Join(a.Cfg.DataDir, "hooks")
		res := hooks.RunHookWithSidecar(hookCtx, string(payload), a.Cfg.OnDigest, a.Cfg.OnDigestTimeout, sidecarDir, d.ID)

		if _, err := hooks.EmitMetaEvent(hookCtx, a.Store, res, d); err != nil {
			a.Logger.Error("on-digest hook: meta-event insert failed", "err", err, "digest_id", d.ID, "reason", res.Reason)
		}
		switch res.Reason {
		case hooks.ReasonSuccess:
			a.Logger.Info("on-digest hook completed", "digest_id", d.ID, "duration_ms", res.Duration.Milliseconds())
		case hooks.ReasonTimeout:
			a.Logger.Warn("on-digest hook timed out", "digest_id", d.ID, "timeout", a.Cfg.OnDigestTimeout.String())
		case hooks.ReasonNonzeroExit:
			a.Logger.Warn("on-digest hook exited non-zero", "digest_id", d.ID, "exit_code", res.ExitCode, "duration_ms", res.Duration.Milliseconds(), "stderr_bytes", res.StderrBytes)
		case hooks.ReasonSpawnError:
			a.Logger.Error("on-digest hook failed to spawn", "digest_id", d.ID, "err", res.SpawnErr)
		}
	}()
}

// WaitForHooks blocks until every in-flight --on-digest hook has
// completed OR the supplied timeout elapses, whichever comes first.
// The graceful-shutdown path in main calls this before Close(). Hooks
// that ignore SIGKILL (rare) won't be waited for past the timeout —
// see hooks.RunHook's WaitDelay for the per-hook bound.
func (a *App) WaitForHooks(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		a.hooksWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		a.Logger.Warn("graceful shutdown: --on-digest hooks did not drain in time", "timeout", timeout.String())
	}
}

func (a *App) Run(ctx context.Context) error {
	lock, err := acquireRunLock(a.Cfg.DBPath)
	if err != nil {
		return err
	}
	defer lock.Release()

	watchTicker := time.NewTicker(2 * time.Second)
	defer watchTicker.Stop()
	consumerTicker := time.NewTicker(a.Cfg.ActiveInterval)
	defer consumerTicker.Stop()
	reconcileTicker := time.NewTicker(a.Cfg.ReconcileInterval)
	defer reconcileTicker.Stop()

	a.Logger.Info("sharedwatch starting",
		"watch_path", a.Cfg.WatchPath,
		"db_path", a.Cfg.DBPath,
		"passive_interval", a.Cfg.PassiveInterval.String(),
		"active_interval", a.Cfg.ActiveInterval.String(),
		"reconcile_interval", a.Cfg.ReconcileInterval.String(),
	)

	if _, err := a.Watcher.ScanAndQueue(ctx, watcher.SnapshotSourceWatcher); err != nil {
		return fmt.Errorf("initial watch scan: %w", err)
	}
	if _, err := a.Reconcile.RunNow(ctx); err != nil {
		return fmt.Errorf("initial reconcile: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			a.Logger.Info("sharedwatch stopping", "reason", ctx.Err().Error())
			return ctx.Err()
		case <-watchTicker.C:
			count, err := a.Watcher.ScanAndQueue(ctx, watcher.SnapshotSourceWatcher)
			if err != nil {
				a.Logger.Error("watch scan failed", "err", err)
				continue
			}
			if count > 0 {
				a.Logger.Debug("watch scan produced events", "count", count)
				rt, _ := a.Store.GetRuntime(ctx)
				rt.LastEventAt = time.Now().UTC()
				_ = a.Store.UpsertRuntime(ctx, rt)
			}
		case <-consumerTicker.C:
			if a.shouldConsume(ctx) {
				if d, ok, err := a.ConsumeNow(ctx); err != nil {
					a.Logger.Error("consume failed", "err", err)
				} else if ok {
					a.Logger.Info("digest created", "id", d.ID, "events", d.EventCount, "mode", d.Mode)
				}
			}
		case <-reconcileTicker.C:
			n, err := a.Reconcile.RunNow(ctx)
			if err != nil {
				a.Logger.Error("reconcile failed", "err", err)
			} else if n > 0 {
				a.Logger.Info("reconcile recovered events", "count", n)
			}
		}
	}
}

func (a *App) shouldConsume(ctx context.Context) bool {
	rt, err := a.Store.GetRuntime(ctx)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	if rt.EffectiveMode(now) == mode.Active {
		return true
	}
	interval := a.Cfg.PassiveInterval
	if !rt.LastEventAt.IsZero() && now.Sub(rt.LastEventAt) <= 30*time.Minute {
		interval = 5 * time.Minute
	}
	if rt.LastConsumerRun.IsZero() {
		return true
	}
	return now.Sub(rt.LastConsumerRun) >= interval
}
