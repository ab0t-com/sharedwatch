package events

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsV1Empty(t *testing.T) {
	if !IsV1Empty(PayloadV1{}) {
		t.Fatal("zero-valued PayloadV1 should be empty")
	}
	if IsV1Empty(PayloadV1{Actor: "x"}) {
		t.Fatal("Actor set should not be empty")
	}
	if IsV1Empty(PayloadV1{Tags: []string{"x"}}) {
		t.Fatal("Tags set should not be empty")
	}
	// SchemaVersion alone does NOT count as attribution — IsV1Empty looks at
	// the user-supplied fields. Build will rewrite SchemaVersion anyway.
	if !IsV1Empty(PayloadV1{SchemaVersion: 1}) {
		t.Fatal("SchemaVersion-only should be treated as empty")
	}
}

func TestBuildPayloadV1_Empty(t *testing.T) {
	if got := BuildPayloadV1(PayloadV1{}); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestBuildPayloadV1_OnlyActor(t *testing.T) {
	got := BuildPayloadV1(PayloadV1{Actor: "claude-1"})
	want := `{"schema_version":1,"actor":"claude-1"}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildPayloadV1_AllFields(t *testing.T) {
	p := PayloadV1{
		Actor:      "claude-1",
		ActorKind:  "ai_agent",
		Session:    "sess-abc",
		Task:       "refactor-auth",
		Intent:     "split JWT",
		Addressee:  "human-mike",
		RefEventID: "evt_a1b2",
		Tags:       []string{"refactor", "auth"},
	}
	got := BuildPayloadV1(p)
	// Decode + re-encode through a map would lose order; instead assert the
	// substring + first-key contract directly.
	if !strings.HasPrefix(got, `{"schema_version":1,`) {
		t.Fatalf("schema_version must be the first key, got %q", got)
	}
	var back PayloadV1
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("payload should round-trip through json.Unmarshal: %v", err)
	}
	if back.Actor != p.Actor || back.ActorKind != p.ActorKind || back.Session != p.Session ||
		back.Task != p.Task || back.Intent != p.Intent || back.Addressee != p.Addressee ||
		back.RefEventID != p.RefEventID || len(back.Tags) != 2 {
		t.Fatalf("round-trip lost fields: in=%+v out=%+v", p, back)
	}
	if back.SchemaVersion != 1 {
		t.Fatalf("schema_version must be 1 on output, got %d", back.SchemaVersion)
	}
}

func TestBuildPayloadV1_OmitsEmptyOptionalFields(t *testing.T) {
	got := BuildPayloadV1(PayloadV1{Actor: "x", Session: ""})
	if strings.Contains(got, "session") {
		t.Fatalf("empty session must be omitted, got %q", got)
	}
	if strings.Contains(got, "tags") {
		t.Fatalf("nil tags must be omitted, got %q", got)
	}
}

func TestBuildPayloadV1_OverridesCallerSchemaVersion(t *testing.T) {
	// Even if a caller hand-builds a struct with the wrong schema_version,
	// the output is normalized to 1. This protects the contract.
	got := BuildPayloadV1(PayloadV1{SchemaVersion: 42, Actor: "x"})
	if !strings.HasPrefix(got, `{"schema_version":1,`) {
		t.Fatalf("schema_version must be coerced to 1, got %q", got)
	}
}

func TestExtractActor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"empty object", "{}", ""},
		{"actor present", `{"schema_version":1,"actor":"claude-1"}`, "claude-1"},
		{"actor with extra keys", `{"actor":"x","session":"s","unknown_key":"y"}`, "x"},
		{"actor absent", `{"session":"s"}`, ""},
		{"forward-version still extracts", `{"schema_version":99,"actor":"forward"}`, "forward"},
		{"malformed JSON returns empty", `{not json`, ""},
		{"non-object returns empty", `"plain string"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractActor(tc.in); got != tc.want {
				t.Fatalf("ExtractActor(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
