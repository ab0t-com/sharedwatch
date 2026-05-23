package digest

import (
	"fmt"
	"strings"
	"time"

	"sharedwatch/internal/events"
)

func RenderHumanSummary(evs []events.Event) string {
	if len(evs) == 0 {
		return "No events."
	}
	lines := []string{fmt.Sprintf("Shared drive digest at %s", time.Now().UTC().Format(time.RFC3339))}
	for _, e := range evs {
		line := fmt.Sprintf("- %s %s (%s)", e.Type, e.RelPath, e.Timestamp.UTC().Format(time.RFC3339))
		if e.Type == events.TypeRenamed && e.OldPath != nil {
			line = fmt.Sprintf("- %s %s <= %s (%s)", e.Type, e.RelPath, *e.OldPath, e.Timestamp.UTC().Format(time.RFC3339))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
