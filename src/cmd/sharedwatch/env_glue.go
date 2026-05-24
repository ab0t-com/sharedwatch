package main

import (
	"os"

	"sharedwatch/internal/config"
)

// Canonical SHAREDWATCH_* env var names. Keep in sync with:
//   - ticket-defaults-config-introspection-stop-20260524_044950.md §3.3
//   - man/sharedwatch.1 ENVIRONMENT
//   - src/README.md "Environment variables" table
//   - config show output
const (
	envActor      = "SHAREDWATCH_ACTOR"
	envActorKind  = "SHAREDWATCH_ACTOR_KIND"
	envSession    = "SHAREDWATCH_SESSION"
	envTask       = "SHAREDWATCH_TASK"
	envAddressee  = "SHAREDWATCH_ADDRESSEE"
	envFormat     = "SHAREDWATCH_FORMAT"
	envRoot       = "SHAREDWATCH_ROOT"
	envCursorName = "SHAREDWATCH_CURSOR_NAME"
	envHints      = "SHAREDWATCH_HINTS"
)

// knownEnvVars is the canonical list, in display order, for `config show`.
var knownEnvVars = []string{
	envActor, envActorKind, envSession, envTask, envAddressee,
	envFormat, envRoot, envCursorName, envHints,
}

// applyEnvToConfig overlays SHAREDWATCH_* env vars onto the loaded config,
// implementing the env > config layer of the resolution chain. The flag
// layer (highest precedence) is applied separately at flag-bind time —
// each handler is responsible for treating an empty flag value as "use
// cfg.X" via flagDefault().
//
// Only non-empty env vars overlay. Empty env vars leave the config value
// untouched (env unset ≠ env="").
func applyEnvToConfig(cfg config.Config) config.Config {
	if v := os.Getenv(envActor); v != "" {
		cfg.Actor = v
	}
	if v := os.Getenv(envActorKind); v != "" {
		cfg.ActorKind = v
	}
	if v := os.Getenv(envSession); v != "" {
		cfg.Session = v
	}
	if v := os.Getenv(envTask); v != "" {
		cfg.Task = v
	}
	if v := os.Getenv(envAddressee); v != "" {
		cfg.Addressee = v
	}
	if v := os.Getenv(envFormat); v != "" {
		cfg.DefaultFormat = v
	}
	if v := os.Getenv(envRoot); v != "" {
		cfg.DefaultRoot = v
	}
	if v := os.Getenv(envCursorName); v != "" {
		cfg.CursorName = v
	}
	if v := os.Getenv(envHints); v != "" {
		cfg.Hints = v
	}
	return cfg
}

// mergeAttrFlags composes the final attribution-flag set from all three
// sources, in precedence order: flag > env (already in cfg via
// applyEnvToConfig) > config > nothing. The returned attrFlags is what
// gets serialized into payload_json.
//
// `flagAttr` is the attrFlags bound directly from the command line.
// `cfg` is the resolved Config (already has env overlaid).
func mergeAttrFlags(flagAttr attrFlags, cfg config.Config) attrFlags {
	// Start from config (which has env overlaid).
	out := attrFlags{
		actor:     cfg.Actor,
		actorKind: cfg.ActorKind,
		session:   cfg.Session,
		task:      cfg.Task,
		addressee: cfg.Addressee,
	}
	// Flag layer wins — defer to attrFlags.merge which already implements
	// "non-empty flag fields overwrite; tags follow replace-if-any rule".
	return out.merge(flagAttr)
}

// flagDefault returns cfgVal when non-empty, otherwise the built-in
// fallback. Use at fs.String() bind time so a command's --format default
// honours config + env without per-handler boilerplate. Pattern:
//
//	formatFlag := fs.String("format", flagDefault(a.Cfg.DefaultFormat, "text"), "...")
func flagDefault(cfgVal, builtin string) string {
	if cfgVal != "" {
		return cfgVal
	}
	return builtin
}

// resolveFormat normalises the (--format <str>, --json <bool>) flag pair to a
// single format string. Both flags exist on subcommands that originally only
// had --format (events list, events stats, sql, overview, schema) so agents
// don't have to remember which subcommands use which form — see SW-AGENT-25
// (F36-C). If --json is true, the supplied jsonDefault ("json" or "jsonl"
// depending on the command's natural shape) wins. Otherwise the --format
// string value is returned as-is.
func resolveFormat(formatFlag string, asJSON bool, jsonDefault string) string {
	if asJSON {
		return jsonDefault
	}
	return formatFlag
}
