package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"sharedwatch/internal/config"
	"sharedwatch/internal/consumer"
	"sharedwatch/internal/db"
	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
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
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	return NewWithLogger(ctx, cfg, slog.Default())
}

func NewWithLogger(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	if cfg.WatchPath != "" {
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
			Store: store, WatchPath: cfg.WatchPath, Recursive: cfg.Recursive,
			CoalesceWindow: cfg.CoalesceWindow,
			IgnorePatterns: cfg.IgnorePatterns, IncludePatterns: cfg.IncludePatterns,
			HashEnabled: cfg.HashEnabled, HashMaxSize: cfg.HashMaxSize,
			ProducerID: cfg.ProducerID,
		},
		Consumer: consumer.Service{Store: store},
		Reconcile: reconcile.Service{
			Store: store, WatchPath: cfg.WatchPath, Recursive: cfg.Recursive,
			Window:         cfg.CoalesceWindow,
			IgnorePatterns: cfg.IgnorePatterns, IncludePatterns: cfg.IncludePatterns,
			HashEnabled: cfg.HashEnabled, HashMaxSize: cfg.HashMaxSize,
			RetentionDays: cfg.RetentionDays,
			ProducerID:    cfg.ProducerID,
		},
		Logger: logger,
	}, nil
}

func (a *App) Close() error { return a.Store.Close() }

type StatusSnapshot struct {
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
	e, err := a.Watcher.EmitSyntheticWithPayload(ctx, relPath, events.TypeModified, events.SourceTest, payloadJSON)
	if err != nil {
		return events.Event{}, err
	}
	rt, _ := a.Store.GetRuntime(ctx)
	rt.LastEventAt = time.Now().UTC()
	_ = a.Store.UpsertRuntime(ctx, rt)
	return e, nil
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
	}
	return d, ok, nil
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
