package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestActorUpsertInsertsThenUpdatesLastHeartbeat(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{
		ActorID: "claude-1", ActorKind: "ai_agent", Focus: "auth/**",
	}); err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.GetActor(ctx, "claude-1")
	if err != nil || !ok {
		t.Fatalf("expected row to exist: ok=%v err=%v", ok, err)
	}
	if first.ActorKind != "ai_agent" || first.Focus != "auth/**" {
		t.Fatalf("unexpected fields: %+v", first)
	}

	time.Sleep(5 * time.Millisecond)
	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "claude-1"}); err != nil {
		t.Fatal(err)
	}
	second, _, _ := s.GetActor(ctx, "claude-1")
	if !second.LastHeartbeat.After(first.LastHeartbeat) {
		t.Fatalf("last_heartbeat should advance: first=%s second=%s", first.LastHeartbeat, second.LastHeartbeat)
	}
}

func TestActorUpsertEmptyFieldsPreservePrior(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{
		ActorID: "claude-1", ActorKind: "ai_agent", Focus: "auth/**", Label: "Coordinator",
	}); err != nil {
		t.Fatal(err)
	}
	// Thin heartbeat — only actor_id; the previous metadata MUST be preserved.
	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "claude-1"}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetActor(ctx, "claude-1")
	if got.ActorKind != "ai_agent" || got.Focus != "auth/**" || got.Label != "Coordinator" {
		t.Fatalf("thin heartbeat clobbered metadata: %+v", got)
	}
}

func TestListActorsOrderedByLastHeartbeatDesc(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "oldest"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "middle"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "newest"}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListActors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].ActorID != "newest" || list[2].ActorID != "oldest" {
		t.Fatalf("ordering wrong: %+v", list)
	}
}

func TestPruneStaleActors(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "fresh"}); err != nil {
		t.Fatal(err)
	}
	// Backdate one actor manually so it's stale relative to our test threshold.
	stale := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO actors (actor_id, last_heartbeat) VALUES ('stale', ?)`, stale); err != nil {
		t.Fatal(err)
	}

	n, err := s.PruneStaleActors(ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected to prune 1 stale actor, got %d", n)
	}
	list, _ := s.ListActors(ctx)
	if len(list) != 1 || list[0].ActorID != "fresh" {
		t.Fatalf("expected only 'fresh' to remain, got %+v", list)
	}
}

func TestActorKindChangeIsTolerated(t *testing.T) {
	// We log a warn on kind change but never reject the heartbeat. This test
	// asserts the upsert still succeeds and the new kind wins.
	ctx := context.Background()
	s := openStore(t)
	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "x", ActorKind: "ai_agent"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertActorHeartbeat(ctx, ActorRecord{ActorID: "x", ActorKind: "human"}); err != nil {
		t.Fatalf("kind change should not error: %v", err)
	}
	got, _, _ := s.GetActor(ctx, "x")
	if got.ActorKind != "human" {
		t.Fatalf("new kind should win: %+v", got)
	}
}
