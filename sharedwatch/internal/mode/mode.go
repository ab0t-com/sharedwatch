package mode

import "time"

type Runtime struct {
	Mode             string    `json:"mode"`
	ActiveUntil      time.Time `json:"active_until"`
	LastConsumerRun  time.Time `json:"last_consumer_run"`
	LastReconcileRun time.Time `json:"last_reconcile_run"`
	LastEventAt      time.Time `json:"last_event_at"`
	LastSnapshotHash string    `json:"last_snapshot_hash"`
}

const (
	Passive = "passive"
	Active  = "active"
)

func DefaultRuntime() Runtime {
	return Runtime{Mode: Passive}
}

func (r Runtime) EffectiveMode(now time.Time) string {
	if r.Mode == Active && now.Before(r.ActiveUntil) {
		return Active
	}
	return Passive
}
