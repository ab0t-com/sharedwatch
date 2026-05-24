package hints

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProfile_Limit(t *testing.T) {
	cases := map[Profile]int{
		ProfileDefault: 4,
		ProfileAgent:   8,
		ProfileTerse:   1,
		ProfileOff:     0,
	}
	for p, want := range cases {
		if got := p.Limit(); got != want {
			t.Errorf("Profile(%q).Limit() = %d, want %d", p, got, want)
		}
	}
}

func TestProfile_IsValid(t *testing.T) {
	valid := []Profile{ProfileDefault, ProfileAgent, ProfileTerse, ProfileOff}
	for _, p := range valid {
		if !p.IsValid() {
			t.Errorf("Profile(%q).IsValid() = false, want true", p)
		}
	}
	if Profile("nonsense").IsValid() {
		t.Errorf("Profile(\"nonsense\").IsValid() = true, want false")
	}
}

func TestResolveProfile(t *testing.T) {
	cases := []struct {
		name   string
		flag   string
		env    string
		isJSON bool
		want   Profile
	}{
		{"flag wins over env", "agent", "off", false, ProfileAgent},
		{"env when no flag", "", "terse", false, ProfileTerse},
		{"json promotes to agent when no flag/env", "", "", true, ProfileAgent},
		{"default when nothing else", "", "", false, ProfileDefault},
		{"flag wins over json promotion", "off", "", true, ProfileOff},
		{"env wins over json promotion", "", "default", true, ProfileDefault},
		{"unrecognised flag falls through to env", "xyzzy", "agent", false, ProfileAgent},
		{"unrecognised everything → default", "xyzzy", "abc", false, ProfileDefault},
		{"trims and lowercases", "  AGENT  ", "", false, ProfileAgent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveProfile(c.flag, c.env, c.isJSON); got != c.want {
				t.Errorf("ResolveProfile(%q, %q, %v) = %q, want %q", c.flag, c.env, c.isJSON, got, c.want)
			}
		})
	}
}

func TestProvider_Status_Pending(t *testing.T) {
	hs := For("status", Context{Profile: ProfileDefault, Pending: 3})
	if len(hs.Hints) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(hs.Hints), hs.Hints)
	}
	if hs.Hints[0].Name != "consume_pending" {
		t.Errorf("want name=consume_pending, got %q", hs.Hints[0].Name)
	}
	if !strings.Contains(hs.Hints[0].Command, "consume") {
		t.Errorf("want command containing 'consume', got %q", hs.Hints[0].Command)
	}
}

func TestProvider_Status_Empty(t *testing.T) {
	hs := For("status", Context{Profile: ProfileDefault})
	if len(hs.Hints) != 0 {
		t.Errorf("want 0 hints for empty state, got %d: %+v", len(hs.Hints), hs.Hints)
	}
}

func TestProvider_Status_MultiRoot(t *testing.T) {
	hs := For("status", Context{
		Profile: ProfileAgent,
		Pending: 5,
		Failed:  2,
		Roots: []RootRef{
			{Label: "auth", Path: "/auth", Pending: 3},
			{Label: "billing", Path: "/billing", Pending: 2},
			{Label: "idle", Path: "/idle", Pending: 0},
		},
	})
	// expect: consume_pending, drain_failed, drill_root_auth, drill_root_billing
	// (drill_root_idle skipped because pending=0)
	wantNames := []string{"consume_pending", "drain_failed", "drill_root_auth", "drill_root_billing"}
	if len(hs.Hints) != len(wantNames) {
		t.Fatalf("want %d hints, got %d: %+v", len(wantNames), len(hs.Hints), hs.Hints)
	}
	for i, want := range wantNames {
		if hs.Hints[i].Name != want {
			t.Errorf("hint[%d].Name = %q, want %q", i, hs.Hints[i].Name, want)
		}
	}
}

func TestProvider_Status_DefaultProfileTruncates(t *testing.T) {
	// Default profile limit = 4. Build a state that would produce 6
	// (consume, drain, read, drill_a, drill_b, drill_c) and verify cap.
	hs := For("status", Context{
		Profile: ProfileDefault,
		Pending: 1, Failed: 1, UnreadDigests: 1,
		Roots: []RootRef{
			{Label: "a", Pending: 1},
			{Label: "b", Pending: 1},
			{Label: "c", Pending: 1},
		},
	})
	if len(hs.Hints) != 4 {
		t.Errorf("default profile should cap at 4 hints, got %d: %+v", len(hs.Hints), hs.Hints)
	}
}

func TestProvider_Roots_PerRoot(t *testing.T) {
	hs := For("roots", Context{
		Profile: ProfileAgent,
		Roots: []RootRef{
			{Label: "auth", Path: "/auth", Pending: 0},
			{Label: "billing", Path: "/billing", Pending: 5},
		},
	})
	if len(hs.Hints) != 2 {
		t.Fatalf("want 2 hints (one per root), got %d", len(hs.Hints))
	}
	if !strings.Contains(hs.Hints[0].Command, "--root auth") || !strings.Contains(hs.Hints[1].Command, "--root billing") {
		t.Errorf("roots hints don't reference both roots: %+v", hs.Hints)
	}
}

func TestProvider_DigestList_NewestN(t *testing.T) {
	hs := For("digest list", Context{
		Profile: ProfileDefault,
		RecentDigests: []DigestRef{
			{ID: "dgs_001", EventCount: 3, Status: "pending"},
			{ID: "dgs_002", EventCount: 8, Status: "pending"},
			{ID: "dgs_003", EventCount: 1, Status: "read"},
		},
	})
	if len(hs.Hints) != 2 {
		t.Fatalf("want 2 hints (newest 2 digests), got %d", len(hs.Hints))
	}
	if !strings.Contains(hs.Hints[0].Command, "dgs_001") {
		t.Errorf("first hint should reference dgs_001, got %q", hs.Hints[0].Command)
	}
}

func TestProvider_Version_NewerExists(t *testing.T) {
	hs := For("version", Context{Profile: ProfileDefault, NewerExists: true, Latest: "v9.9.9"})
	if len(hs.Hints) != 1 {
		t.Fatalf("want 1 hint when newer exists, got %d", len(hs.Hints))
	}
	if hs.Hints[0].Name != "apply_update" {
		t.Errorf("want name=apply_update, got %q", hs.Hints[0].Name)
	}
}

func TestProvider_Version_Current(t *testing.T) {
	hs := For("version", Context{Profile: ProfileDefault})
	if len(hs.Hints) != 0 {
		t.Errorf("want 0 hints when current, got %d", len(hs.Hints))
	}
}

func TestProvider_EventsList_Zero(t *testing.T) {
	hs := For("events list", Context{Profile: ProfileDefault, HasCursor: true, CursorReturned: 0})
	if len(hs.Hints) != 1 || hs.Hints[0].Name != "check_status" {
		t.Errorf("want single check_status hint, got %+v", hs.Hints)
	}
}

func TestProvider_EventsList_Many(t *testing.T) {
	hs := For("events list", Context{Profile: ProfileDefault, HasCursor: true, CursorReturned: 75})
	if len(hs.Hints) != 1 || hs.Hints[0].Name != "summarise_via_stats" {
		t.Errorf("want single summarise_via_stats hint, got %+v", hs.Hints)
	}
}

func TestProvider_Overview_FailedAndActors(t *testing.T) {
	hs := For("overview", Context{
		Profile:   ProfileAgent,
		Failed:    2,
		TopActors: []string{"claude-x"},
		TopTypes:  []string{"file.modified"},
	})
	names := map[string]bool{}
	for _, h := range hs.Hints {
		names[h.Name] = true
	}
	for _, want := range []string{"events_recent", "events_by_type", "events_failed", "actor_claude-x", "type_file.modified"} {
		if !names[want] {
			t.Errorf("missing expected hint %q in overview output: %+v", want, hs.Hints)
		}
	}
}

func TestRender_Text_HasNextBlock(t *testing.T) {
	var buf bytes.Buffer
	hs := HintSet{Profile: ProfileDefault, Hints: []Hint{
		{Name: "consume_pending", Command: "sharedwatch consume", Reason: "process the 3 pending event(s)"},
	}}
	RenderText(&buf, hs)
	out := buf.String()
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("output should start with blank line, got %q", out)
	}
	if !strings.Contains(out, "Next:") {
		t.Errorf("output should contain 'Next:' header, got %q", out)
	}
	if !strings.Contains(out, "sharedwatch consume") {
		t.Errorf("output should contain command, got %q", out)
	}
}

func TestRender_Text_OffProfileEmpty(t *testing.T) {
	var buf bytes.Buffer
	hs := HintSet{Profile: ProfileOff, Hints: []Hint{{Name: "x", Command: "y"}}}
	RenderText(&buf, hs)
	if buf.Len() != 0 {
		t.Errorf("off profile should emit nothing, got %q", buf.String())
	}
}

func TestRender_Text_EmptyHintsEmpty(t *testing.T) {
	var buf bytes.Buffer
	RenderText(&buf, HintSet{Profile: ProfileDefault, Hints: nil})
	if buf.Len() != 0 {
		t.Errorf("empty hints should emit nothing, got %q", buf.String())
	}
}

func TestProfileOff_For(t *testing.T) {
	// Even if a provider would return hints, profile=off short-circuits.
	hs := For("status", Context{Profile: ProfileOff, Pending: 5})
	if len(hs.Hints) != 0 {
		t.Errorf("profile=off should return no hints regardless of state, got %+v", hs.Hints)
	}
}

func TestTerseProfile_StripsReasons(t *testing.T) {
	hs := For("status", Context{Profile: ProfileTerse, Pending: 1, Failed: 1})
	if len(hs.Hints) != 1 {
		t.Fatalf("terse profile should cap at 1 hint, got %d", len(hs.Hints))
	}
	if hs.Hints[0].Reason != "" {
		t.Errorf("terse profile should strip reasons, got %q", hs.Hints[0].Reason)
	}
}

func TestHintSet_JSONOmitEmpty(t *testing.T) {
	// A HintSet with no Hints should still marshal cleanly; the JSON
	// caller decides whether to emit the envelope at all.
	hs := HintSet{Profile: ProfileDefault, Hints: nil}
	b, err := json.Marshal(hs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"profile":"default"`) {
		t.Errorf("missing profile in output: %s", string(b))
	}
}

func TestRegisterClear_Roundtrip(t *testing.T) {
	// Registering a custom provider, then clearing, then re-checking
	// should remove the custom provider but the test cannot leave the
	// global registry empty for other tests — re-init after.
	Register("__test_only", func(Context) []Hint {
		return []Hint{{Name: "x", Command: "y"}}
	})
	hs := For("__test_only", Context{Profile: ProfileDefault})
	if len(hs.Hints) != 1 {
		t.Errorf("custom provider not registered: got %+v", hs.Hints)
	}
	// don't actually Clear() — would wipe the built-ins for the rest
	// of this test binary's run.
}
