package digest

import (
	"strings"
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func TestRenderHumanSummary(t *testing.T) {
	now := time.Now().UTC()
	oldPath := "/x/old.md"
	out := RenderHumanSummary([]events.Event{{Type: events.TypeRenamed, RelPath: "new.md", OldPath: &oldPath, Timestamp: now}})
	if !strings.Contains(out, "new.md <= /x/old.md") {
		t.Fatalf("unexpected output: %s", out)
	}
}
