package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestIntentCRUD(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if err := s.InsertIntent(ctx, IntentRecord{
		IntentID: "i1", ActorID: "claude-1", PathGlob: "auth/**",
		DeclaredAt: now, ExpiresAt: now.Add(10 * time.Minute),
		Task: "refactor", Intent: "split jwt",
	}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListIntents(ctx, IntentFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d rows err=%v", len(list), err)
	}
	if list[0].Task != "refactor" || list[0].Intent != "split jwt" {
		t.Fatalf("metadata lost: %+v", list[0])
	}

	// Filter by actor
	auth, _ := s.ListIntents(ctx, IntentFilter{ActorID: "claude-1"})
	if len(auth) != 1 {
		t.Fatalf("actor filter: %d", len(auth))
	}
	none, _ := s.ListIntents(ctx, IntentFilter{ActorID: "nobody"})
	if len(none) != 0 {
		t.Fatalf("non-matching actor: %d", len(none))
	}

	if err := s.RevokeIntent(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListIntents(ctx, IntentFilter{})
	if len(after) != 0 {
		t.Fatalf("revoke did not delete: %d remain", len(after))
	}
}

func TestPruneExpiredIntents(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if err := s.InsertIntent(ctx, IntentRecord{
		IntentID: "fresh", ActorID: "x", PathGlob: "a",
		DeclaredAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Backdate one as expired.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO intents (intent_id, actor_id, path_glob, declared_at, expires_at) VALUES ('stale', 'x', 'a', ?, ?)`,
		now.Add(-time.Hour).Format(time.RFC3339Nano), now.Add(-time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}

	n, err := s.PruneExpiredIntents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected to prune 1, got %d", n)
	}
	remaining, _ := s.ListIntents(ctx, IntentFilter{IncludeExpired: true})
	if len(remaining) != 1 || remaining[0].IntentID != "fresh" {
		t.Fatalf("wrong remaining: %+v", remaining)
	}
}

func TestLeaseGrantRenewRelease(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if err := s.InsertLease(ctx, LeaseRecord{
		LeaseID: "l1", ActorID: "claude-1", PathGlob: "auth/login.go",
		GrantedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	updated, err := s.RenewLease(ctx, "l1", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RenewalCount != 1 {
		t.Fatalf("renewal_count: want 1, got %d", updated.RenewalCount)
	}
	if !updated.ExpiresAt.After(now.Add(5 * time.Minute)) {
		t.Fatalf("renew did not extend expires_at: %v", updated.ExpiresAt)
	}

	if err := s.ReleaseLease(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListLeases(ctx, LeaseFilter{IncludeExpired: true})
	if len(list) != 0 {
		t.Fatalf("release did not delete: %d remain", len(list))
	}
}

func TestLeaseTTLMaxEnforced(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	err = s.InsertLease(ctx, LeaseRecord{
		LeaseID: "too-long", ActorID: "x", PathGlob: "p",
		GrantedAt: now, ExpiresAt: now.Add(LeaseTTLMax + time.Minute),
	})
	if err == nil {
		t.Fatal("expected error when TTL exceeds LeaseTTLMax")
	}
}

func TestLeaseRenewCannotExceedMaxFromGrantedAt(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if err := s.InsertLease(ctx, LeaseRecord{
		LeaseID: "l1", ActorID: "x", PathGlob: "p",
		GrantedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	// Renew with a huge TTL — must clamp at GrantedAt + LeaseTTLMax.
	r, err := s.RenewLease(ctx, "l1", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	maxAllowed := now.Add(LeaseTTLMax)
	if r.ExpiresAt.After(maxAllowed.Add(time.Second)) {
		t.Fatalf("renew exceeded LeaseTTLMax: expires=%v cap=%v", r.ExpiresAt, maxAllowed)
	}
}
