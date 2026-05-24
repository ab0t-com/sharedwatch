package main

import (
	"testing"

	"sharedwatch/internal/config"
)

func TestApplyEnvToConfig_Overlays(t *testing.T) {
	t.Setenv(envActor, "claude-x")
	t.Setenv(envFormat, "jsonl")
	t.Setenv(envHints, "agent")
	// Leave the others unset to verify they don't trample existing cfg fields.

	cfg := config.Config{
		Actor:         "from-config",
		ActorKind:     "ai_agent",
		DefaultFormat: "text",
	}
	out := applyEnvToConfig(cfg)

	if out.Actor != "claude-x" {
		t.Errorf("env should override config: Actor = %q, want claude-x", out.Actor)
	}
	if out.ActorKind != "ai_agent" {
		t.Errorf("unset env should leave config untouched: ActorKind = %q, want ai_agent", out.ActorKind)
	}
	if out.DefaultFormat != "jsonl" {
		t.Errorf("env should override config: DefaultFormat = %q, want jsonl", out.DefaultFormat)
	}
	if out.Hints != "agent" {
		t.Errorf("env should populate empty config: Hints = %q, want agent", out.Hints)
	}
}

func TestApplyEnvToConfig_AllUnsetPreservesConfig(t *testing.T) {
	// No env vars set — config should pass through unchanged.
	for _, name := range knownEnvVars {
		t.Setenv(name, "")
	}
	cfg := config.Config{Actor: "from-config", DefaultFormat: "csv"}
	out := applyEnvToConfig(cfg)
	if out.Actor != "from-config" || out.DefaultFormat != "csv" {
		t.Errorf("with all env unset, config should pass through: got %+v", out)
	}
}

func TestMergeAttrFlags_FlagWinsOverConfigAndEnv(t *testing.T) {
	cfg := config.Config{
		Actor:     "from-config",
		ActorKind: "ai_agent",
		Session:   "sess-config",
	}
	flag := attrFlags{
		actor: "from-flag", // wins
		// session left empty — should inherit from cfg
	}
	out := mergeAttrFlags(flag, cfg)
	if out.actor != "from-flag" {
		t.Errorf("flag should win: actor = %q, want from-flag", out.actor)
	}
	if out.session != "sess-config" {
		t.Errorf("empty flag should inherit from cfg: session = %q, want sess-config", out.session)
	}
	if out.actorKind != "ai_agent" {
		t.Errorf("config field preserved when flag empty: actorKind = %q, want ai_agent", out.actorKind)
	}
}

func TestMergeAttrFlags_EmptyEverywhereStaysEmpty(t *testing.T) {
	out := mergeAttrFlags(attrFlags{}, config.Config{})
	if !out.isEmpty() {
		t.Errorf("empty flag + empty cfg should produce empty attrFlags, got %+v", out)
	}
}

func TestFlagDefault(t *testing.T) {
	if got := flagDefault("from-cfg", "builtin"); got != "from-cfg" {
		t.Errorf("cfg value should win when non-empty: got %q", got)
	}
	if got := flagDefault("", "builtin"); got != "builtin" {
		t.Errorf("builtin should win when cfg empty: got %q", got)
	}
}
