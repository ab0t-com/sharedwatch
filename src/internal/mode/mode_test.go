package mode

import (
	"testing"
	"time"
)

func TestEffectiveMode(t *testing.T) {
	now := time.Now().UTC()
	r := Runtime{Mode: Active, ActiveUntil: now.Add(10 * time.Minute)}
	if got := r.EffectiveMode(now); got != Active {
		t.Fatalf("expected active, got %s", got)
	}
	r.ActiveUntil = now.Add(-time.Minute)
	if got := r.EffectiveMode(now); got != Passive {
		t.Fatalf("expected passive after ttl, got %s", got)
	}
}
