package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sharedwatch/internal/app"
	"sharedwatch/internal/config"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
	"sharedwatch/internal/output"
)

// randRead / hex are tiny wrappers so newRandID below doesn't need a fresh
// import block from the various id-generation helpers scattered across the
// internal/ tree.
func randRead(b []byte) (int, error) { return rand.Read(b) }
func hexEncode(b []byte) string      { return hex.EncodeToString(b) }

// Version is overridden at build time with -ldflags "-X main.Version=…".
var Version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root := flag.NewFlagSet("sharedwatch", flag.ContinueOnError)
	root.SetOutput(os.Stderr)
	configPath := root.String("config", "config.yaml", "path to config file (silently ignored if missing)")
	var watchPaths csvList
	root.Var(&watchPaths, "watch-path", "override watch_path; repeatable (comma-aware) for multi-root setups")
	var rootDefs rootDefList
	root.Var(&rootDefs, "root", "register a watch root (definition only on root flagset): --root <label>=<path> or --root <path>; repeatable")
	dbPath := root.String("db", "", "override db_path")
	dataDir := root.String("data-dir", "", "override data_dir")
	logFormat := root.String("log-format", "text", "log format: text|json")
	logLevel := root.String("log-level", "info", "log level: debug|info|warn|error")
	var extraIgnores csvList
	root.Var(&extraIgnores, "ignore", "extra ignore pattern (repeatable; also accepts comma-separated)")
	var extraIncludes csvList
	root.Var(&extraIncludes, "include", "include-only pattern (repeatable; empty = include all)")
	hashFlag := root.String("hash", "", "enable content hashing: on|off (default off)")
	producerOverride := root.String("producer", "", "override producer_id stamped on emitted events")
	var rootAttr attrFlags
	bindAttrFlags(root, &rootAttr, "applied to every event emitted during this invocation")
	showVersion := root.Bool("version", false, "print version and exit")
	root.Usage = func() { usage(root) }

	args := os.Args[1:]
	// Allow top-level flags to appear before or after the subcommand.
	args = reorderFlags(args, root)
	if err := root.Parse(args); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("sharedwatch", Version)
		return
	}

	rest := root.Args()
	if len(rest) == 0 {
		usage(root)
		return
	}

	// Commands that don't need state — handle before opening the DB so they
	// work on a fresh checkout with no write access to the default data dir.
	switch rest[0] {
	case "version":
		fmt.Println("sharedwatch", Version)
		return
	case "help", "-h", "--help":
		usage(root)
		return
	case "update":
		handleUpdate(ctx, rest[1:])
		return
	}

	known := map[string]bool{
		"run": true, "status": true, "mode": true, "digest": true,
		"reconcile": true, "test": true, "consume": true, "init": true,
		"events": true, "sql": true, "schema": true, "actor": true,
		"overview": true,
		"intent":   true,
		"lease":    true,
		"roots":    true,
	}
	if !known[rest[0]] {
		fmt.Fprintln(os.Stderr, "unknown subcommand:", rest[0])
		usage(root)
		os.Exit(2)
	}

	cfg := config.Default()
	if loaded, err := config.Load(*configPath, cfg); err != nil {
		fatal(fmt.Errorf("load config %s: %w", *configPath, err))
	} else {
		cfg = loaded
	}
	if len(watchPaths) > 0 || len(rootDefs) > 0 {
		// Build WatchRoots from --watch-path entries (unlabeled, auto-labeled
		// later) plus --root <label>=<path> entries (explicit-labeled).
		// Legacy single --watch-path (one entry, no --root) keeps the
		// historical single-root behaviour by still populating cfg.WatchPath
		// below — see normalization.
		var combined []config.WatchRoot
		for _, p := range watchPaths {
			combined = append(combined, config.WatchRoot{Path: p})
		}
		combined = append(combined, []config.WatchRoot(rootDefs)...)
		if len(combined) == 1 && len(rootDefs) == 0 {
			// Single --watch-path; preserve legacy single-root semantics
			// (Label = "", events stamped with watch_root = "").
			cfg.WatchPath = combined[0].Path
			cfg.WatchRoots = nil
		} else {
			normalized, err := config.NormalizeRoots(combined)
			if err != nil {
				fatal(fmt.Errorf("--watch-path/--root: %w", err))
			}
			cfg.WatchRoots = normalized
			cfg.WatchPath = "" // multi-root supersedes legacy single-path field
		}
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if len(extraIgnores) > 0 {
		cfg.IgnorePatterns = append(cfg.IgnorePatterns, extraIgnores...)
	}
	if len(extraIncludes) > 0 {
		cfg.IncludePatterns = append(cfg.IncludePatterns, extraIncludes...)
	}
	switch strings.ToLower(*hashFlag) {
	case "":
		// keep default
	case "on", "true", "yes", "1":
		cfg.HashEnabled = true
	case "off", "false", "no", "0":
		cfg.HashEnabled = false
	default:
		fatal(fmt.Errorf("--hash: want on|off, got %q", *hashFlag))
	}
	if *producerOverride != "" {
		cfg.ProducerID = *producerOverride
	}
	if !rootAttr.isEmpty() {
		cfg.PayloadJSON = events.BuildPayloadV1(rootAttr.toPayload())
	}

	logger, err := buildLogger(*logFormat, *logLevel)
	if err != nil {
		fatal(err)
	}
	slog.SetDefault(logger)

	a, err := app.NewWithLogger(ctx, cfg, logger)
	if err != nil {
		fatal(err)
	}
	defer a.Close()

	switch rest[0] {
	case "run":
		if err := a.Run(ctx); err != nil && err != context.Canceled {
			fatal(err)
		}
	case "status":
		handleStatus(ctx, a, rest[1:])
	case "mode":
		handleMode(ctx, a, rest[1:])
	case "digest":
		handleDigest(ctx, a, rest[1:])
	case "reconcile":
		handleReconcile(ctx, a, rest[1:])
	case "test":
		handleTest(ctx, a, rest[1:], rootAttr)
	case "consume":
		handleConsume(ctx, a)
	case "init":
		handleInit(a)
	case "events":
		handleEvents(ctx, a, rest[1:])
	case "sql":
		handleSQL(ctx, a, rest[1:])
	case "schema":
		handleSchema(ctx, a, rest[1:])
	case "actor":
		handleActor(ctx, a, rest[1:])
	case "overview":
		handleOverview(ctx, a, rest[1:])
	case "intent":
		handleIntent(ctx, a, rest[1:])
	case "lease":
		handleLease(ctx, a, rest[1:])
	case "roots":
		handleRoots(ctx, a, rest[1:])
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", rest[0])
		usage(root)
		os.Exit(2)
	}
}

func handleStatus(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit status as JSON")
	showActors := fs.Bool("actors", false, "include the actors[] registry array")
	_ = fs.Parse(args)
	if *asJSON {
		snap, err := a.StatusSnapshot(ctx)
		if err != nil {
			fatal(err)
		}
		if *showActors {
			actors, err := a.ActorsView(ctx)
			if err != nil {
				fatal(err)
			}
			snap.Actors = actors
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			fatal(err)
		}
		return
	}
	out, err := a.Status(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Println(out)
	if *showActors {
		actors, err := a.ActorsView(ctx)
		if err != nil {
			fatal(err)
		}
		if len(actors) == 0 {
			fmt.Println("actors: (none registered)")
			return
		}
		fmt.Println("actors:")
		for _, ar := range actors {
			staleMark := ""
			if ar.Stale {
				staleMark = " STALE"
			}
			fmt.Printf("  %s  kind=%s  focus=%s  last_heartbeat=%s%s\n",
				ar.ActorID, defaultDash(ar.ActorKind), defaultDash(ar.Focus),
				ar.LastHeartbeat.UTC().Format(time.RFC3339), staleMark)
		}
	}
}

func defaultDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func handleMode(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("mode requires subcommand"))
	}
	switch args[0] {
	case "active":
		fs := flag.NewFlagSet("mode active", flag.ExitOnError)
		ttl := fs.Duration("ttl", 30*time.Minute, "active mode ttl")
		_ = fs.Parse(args[1:])
		if err := a.SetActive(ctx, *ttl); err != nil {
			fatal(err)
		}
		rt, err := a.Store.GetRuntime(ctx)
		if err != nil {
			fatal(err)
		}
		// Report the *effective* deadline (SetActive applies the default TTL if
		// the user passed <= 0, so the requested value may not match).
		fmt.Printf("active mode enabled until %s\n", rt.ActiveUntil.Format(time.RFC3339))
	case "passive":
		if err := a.SetPassive(ctx); err != nil {
			fatal(err)
		}
		fmt.Println("passive mode enabled")
	default:
		fatal(fmt.Errorf("unknown mode subcommand: %s", args[0]))
	}
}

func handleDigest(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("digest requires subcommand"))
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("digest list", flag.ExitOnError)
		limit := fs.Int("limit", 20, "max digests to list")
		status := fs.String("status", "", "filter by status: pending|read|archived (empty = all)")
		var watchRoots csvList
		fs.Var(&watchRoots, "root", "filter by watch_root label (repeatable; comma-aware). Use 'mixed' to find spans, '' for legacy single-root.")
		_ = fs.Parse(args[1:])
		for _, r := range watchRoots {
			if strings.Contains(r, "=") {
				fatal(fmt.Errorf("--root on digest list is a FILTER (just the label). Definitions go on the root command."))
			}
		}
		digests, err := a.Store.ListDigestsByFilter(ctx, db.DigestFilter{
			Limit: *limit, Status: *status, WatchRoots: []string(watchRoots),
		})
		if err != nil {
			fatal(err)
		}
		if len(digests) == 0 {
			switch {
			case *status != "" && len(watchRoots) > 0:
				fmt.Printf("no digests with status=%s and root in %v\n", *status, []string(watchRoots))
			case *status != "":
				fmt.Printf("no digests with status=%s\n", *status)
			case len(watchRoots) > 0:
				fmt.Printf("no digests with root in %v\n", []string(watchRoots))
			default:
				fmt.Println("no digests yet — try `sharedwatch consume` after some activity")
			}
			return
		}
		// Show watch_root column when present and non-empty for any returned row.
		showRoot := false
		for _, d := range digests {
			if d.WatchRoot != "" {
				showRoot = true
				break
			}
		}
		for _, d := range digests {
			if showRoot {
				root := d.WatchRoot
				if root == "" {
					root = "-"
				}
				fmt.Printf("%s %s root=%s events=%d status=%s %s\n", d.ID, d.CreatedAt.Format(time.RFC3339), root, d.EventCount, d.Status, firstLine(d.Summary))
			} else {
				fmt.Printf("%s %s events=%d status=%s %s\n", d.ID, d.CreatedAt.Format(time.RFC3339), d.EventCount, d.Status, firstLine(d.Summary))
			}
		}
	case "show":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: sharedwatch digest show <id>"))
		}
		d, err := a.GetDigest(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		_ = a.Store.MarkDigestRead(ctx, args[1])
		fmt.Printf("id=%s\ncreated_at=%s\nwindow=%s..%s\nmode=%s\nevents=%d\nstatus=%s\nsummary=\n%s\n",
			d.ID, d.CreatedAt.Format(time.RFC3339), d.WindowStart.Format(time.RFC3339), d.WindowEnd.Format(time.RFC3339),
			d.Mode, d.EventCount, d.Status, d.Summary)
	case "archive":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: sharedwatch digest archive <id>"))
		}
		if err := a.Store.MarkDigestArchived(ctx, args[1]); err != nil {
			fatal(err)
		}
		fmt.Printf("archived %s\n", args[1])
	default:
		fatal(fmt.Errorf("unknown digest subcommand: %s", args[0]))
	}
}

func handleReconcile(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 || args[0] != "now" {
		fatal(fmt.Errorf("usage: sharedwatch reconcile now"))
	}
	count, err := a.Reconcile.RunNow(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("reconcile checkpoint recorded recovery_events=%d\n", count)
}

func handleTest(ctx context.Context, a *app.App, args []string, rootAttr attrFlags) {
	if len(args) < 1 || args[0] != "emit" {
		fatal(fmt.Errorf("usage: sharedwatch test emit [relpath] [--payload <json> | --actor X --session Y --task Z ...]"))
	}
	fs := flag.NewFlagSet("test emit", flag.ExitOnError)
	payload := fs.String("payload", "", "raw JSON payload (mutually exclusive with --actor / --session / ...)")
	var subAttr attrFlags
	bindAttrFlags(fs, &subAttr, "overrides any root-level attribution for this emit")
	rest := args[1:]
	// Pre-extract a non-flag relpath if it's the first positional.
	rel := "test-event.md"
	parseFrom := rest
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		rel = rest[0]
		parseFrom = rest[1:]
	}
	_ = fs.Parse(parseFrom)

	merged := rootAttr.merge(subAttr)
	rawPayload := strings.TrimSpace(*payload)
	if rawPayload != "" && !merged.isEmpty() {
		fatal(fmt.Errorf("--payload is mutually exclusive with --actor / --session / --task / --intent / --addressee / --ref / --tag"))
	}
	if rawPayload != "" {
		var probe map[string]any
		if err := json.Unmarshal([]byte(rawPayload), &probe); err != nil {
			fatal(fmt.Errorf("--payload must be a JSON object: %w", err))
		}
	}
	finalPayload := rawPayload
	if finalPayload == "" && !merged.isEmpty() {
		finalPayload = events.BuildPayloadV1(merged.toPayload())
	}
	e, err := a.TestEmitWithPayload(ctx, rel, finalPayload)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("emitted %s %s\n", e.ID, e.RelPath)
}

func handleEvents(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("events requires subcommand: list | cursor | retry | recover-stuck"))
	}
	switch args[0] {
	case "list":
		handleEventsList(ctx, a, args[1:])
	case "stats":
		handleEventsStats(ctx, a, args[1:])
	case "cursor":
		handleEventsCursor(ctx, a, args[1:])
	case "retry":
		fs := flag.NewFlagSet("events retry", flag.ExitOnError)
		maxRetries := fs.Int("max-retries", 0, "skip events that have already failed N+ times (0 = no cap)")
		_ = fs.Parse(args[1:])
		n, err := a.Store.RequeueFailedEventsWithLimit(ctx, *maxRetries)
		if err != nil {
			fatal(err)
		}
		if n == 0 {
			fmt.Println("no failed events to retry")
			return
		}
		fmt.Printf("requeued %d failed event(s) to pending\n", n)
	case "recover-stuck":
		fs := flag.NewFlagSet("events recover-stuck", flag.ExitOnError)
		older := fs.Duration("older-than", 5*time.Minute, "consider 'processing' events older than this duration as stuck")
		_ = fs.Parse(args[1:])
		n, err := a.Store.RecoverStuckProcessing(ctx, *older)
		if err != nil {
			fatal(err)
		}
		if n == 0 {
			fmt.Println("no stuck processing events")
			return
		}
		fmt.Printf("flipped %d stuck processing event(s) back to pending\n", n)
	default:
		fatal(fmt.Errorf("unknown events subcommand: %s", args[0]))
	}
}

func handleEventsStats(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("events stats", flag.ExitOnError)
	root := fs.String("root", "", "REQUIRED — single watch_root label to scope the aggregation")
	since := fs.Duration("since", 24*time.Hour, "lookback window (default 24h)")
	formatFlag := fs.String("format", "text", "text|json")
	_ = fs.Parse(args)
	if *root == "" {
		fmt.Fprintln(os.Stderr, "events stats requires --root <label>. For across-roots counts, use `sharedwatch overview`.")
		os.Exit(2)
	}
	if strings.Contains(*root, "=") {
		fatal(fmt.Errorf("--root on events stats is a FILTER (just the label). Definitions go on the root command."))
	}
	stats, err := a.ComputeEventsStats(ctx, *root, *since)
	if err != nil {
		fatal(err)
	}
	switch strings.ToLower(*formatFlag) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(stats); err != nil {
			fatal(err)
		}
	default:
		fmt.Printf("events stats — root=%s window=%s..%s\n", stats.Root, stats.Window.Start.Format(time.RFC3339), stats.Window.End.Format(time.RFC3339))
		if len(stats.ByType) > 0 {
			fmt.Println("  by_type:")
			for t, n := range stats.ByType {
				fmt.Printf("    %-16s %d\n", t, n)
			}
		}
		if len(stats.ByActor) > 0 {
			fmt.Println("  by_actor:")
			for actor, n := range stats.ByActor {
				fmt.Printf("    %-32s %d\n", actor, n)
			}
		}
		if len(stats.TopPaths) > 0 {
			fmt.Println("  top_paths:")
			for _, ps := range stats.TopPaths {
				fmt.Printf("    %s  events=%d  last=%s  actors=%v\n", ps.Path, ps.Events, ps.LastAt.Format(time.RFC3339), ps.Actors)
			}
		}
		if len(stats.Hourly) > 0 {
			fmt.Println("  hourly:")
			// SW-AGENT-13 compression: collapse adjacent quiet ranges (no
			// events) into a single "quiet HH:MM–HH:MM (Nh)" line so a 24h
			// window with sparse activity doesn't render 24 lines.
			lastHour := stats.Hourly[len(stats.Hourly)-1].Hour
			emitted := map[time.Time]int{}
			for _, h := range stats.Hourly {
				emitted[h.Hour] = h.Events
			}
			startWin := stats.Hourly[0].Hour
			for h := startWin; !h.After(lastHour); h = h.Add(time.Hour) {
				if n, ok := emitted[h]; ok {
					fmt.Printf("    %s  %d\n", h.Format("2006-01-02T15:04"), n)
					continue
				}
				// Quiet run starting at h — coalesce forward.
				runStart := h
				for !h.After(lastHour) {
					if _, ok := emitted[h.Add(time.Hour)]; ok {
						break
					}
					h = h.Add(time.Hour)
				}
				hours := int(h.Sub(runStart)/time.Hour) + 1
				if hours == 1 {
					fmt.Printf("    %s  quiet\n", runStart.Format("2006-01-02T15:04"))
				} else {
					fmt.Printf("    quiet %s–%s (%dh)\n", runStart.Format("15:04"), h.Add(time.Hour).Format("15:04"), hours)
				}
			}
		}
		if len(stats.Drill) > 0 {
			fmt.Println("  drill:")
			for k, v := range stats.Drill {
				fmt.Printf("    %s\n      → %s\n", k, v)
			}
		}
	}
}

func handleEventsList(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("events list", flag.ExitOnError)
	since := fs.String("since", "", "RFC3339 lower bound on created_at (inclusive)")
	until := fs.String("until", "", "RFC3339 upper bound on created_at (exclusive)")
	pathGlob := fs.String("path-glob", "", "filter rel_path by glob (supports `**`)")
	limit := fs.Int("limit", 100, "max rows (0 = no cap)")
	order := fs.String("order", "desc", "asc|desc by created_at")
	formatFlag := fs.String("format", "text", "text|json|jsonl|csv")
	fields := fs.String("fields", "", "comma-separated column projection (default: all)")
	sinceCursor := fs.String("since-cursor", "", "opaque cursor token; mutually exclusive with --since")
	cursorName := fs.String("cursor-name", "", "named server-side cursor")
	noAdvance := fs.Bool("no-advance", false, "with --cursor-name, do not write the new position back")
	var types, sources, statuses, producers, watchRoots csvList
	fs.Var(&types, "type", "filter by type (repeatable)")
	fs.Var(&sources, "source", "filter by source (repeatable)")
	fs.Var(&statuses, "status", "filter by status (repeatable)")
	fs.Var(&producers, "producer", "filter by producer_id (repeatable)")
	fs.Var(&watchRoots, "root", "filter by watch_root label (repeatable; comma-aware). Empty string '' selects legacy single-root events.")
	includeRoot := fs.Bool("include-watch-root", false, "always show the watch_root column in output (auto-shown when result spans multiple roots)")
	payloadKey := fs.String("payload-key", "", "post-filter: payload_json[<key>] must equal --payload-value")
	payloadValue := fs.String("payload-value", "", "see --payload-key")
	_ = fs.Parse(args)

	// Reject `--root foo=/path` on read commands — definitions belong on root.
	for _, r := range watchRoots {
		if strings.Contains(r, "=") {
			fatal(fmt.Errorf("--root on events list is a FILTER (just the label). To define a root use `--root <label>=<path>` on the root command (before the subcommand)."))
		}
	}

	if *sinceCursor != "" && *since != "" {
		fatal(fmt.Errorf("--since and --since-cursor are mutually exclusive"))
	}

	// When a cursor is in play, results MUST iterate ASC so the cursor advances
	// monotonically. Otherwise the user's --order preference wins.
	cursorMode := *sinceCursor != "" || *cursorName != ""
	orderAsc := strings.EqualFold(*order, "asc") || cursorMode

	filter := db.EventFilter{
		Types:        []string(types),
		Sources:      []string(sources),
		Statuses:     []string(statuses),
		ProducerIDs:  []string(producers),
		WatchRoots:   []string(watchRoots),
		PathGlob:     *pathGlob,
		PayloadKey:   *payloadKey,
		PayloadValue: *payloadValue,
		Limit:        *limit,
		OrderAsc:     orderAsc,
	}
	if *since != "" {
		t, err := parseSinceUntil(*since)
		if err != nil {
			fatal(fmt.Errorf("--since: %w", err))
		}
		filter.Since = t
	}
	if *until != "" {
		t, err := parseSinceUntil(*until)
		if err != nil {
			fatal(fmt.Errorf("--until: %w", err))
		}
		filter.Until = t
	}

	switch {
	case *sinceCursor != "":
		pos, err := db.DecodeCursor(*sinceCursor)
		if err != nil {
			fatal(err)
		}
		filter.AfterNano = pos.CreatedAt.UnixNano()
		filter.AfterID = pos.ID
	case *cursorName != "":
		c, ok, err := a.Store.GetCursor(ctx, *cursorName)
		if err != nil {
			fatal(err)
		}
		if ok {
			filter.AfterNano = c.Position.CreatedAt.UnixNano()
			filter.AfterID = c.Position.ID
		}
	}

	evs, err := a.Store.QueryEvents(ctx, filter)
	if err != nil {
		fatal(err)
	}

	cols, rows := eventsAsRows(evs, *includeRoot || len(watchRoots) > 0)
	res := output.Result{Columns: cols, Rows: rows}

	// Cursor advance: pick the last event in iteration order (ASC) so the
	// new cursor sits at "everything up to and including this".
	if len(evs) > 0 && cursorMode {
		last := evs[len(evs)-1]
		next := db.EncodeCursor(db.CursorPosition{CreatedAt: last.Timestamp, ID: last.ID})
		res.NextCursor = next
		if *cursorName != "" && !*noAdvance {
			if err := a.Store.UpsertCursor(ctx, *cursorName, db.CursorPosition{CreatedAt: last.Timestamp, ID: last.ID}); err != nil {
				fatal(err)
			}
		}
	}

	if *fields != "" {
		res = res.Project(splitFields(*fields))
	}
	f, err := output.ParseFormat(*formatFlag)
	if err != nil {
		fatal(err)
	}
	if err := output.Render(os.Stdout, f, res); err != nil {
		fatal(err)
	}
}

func handleEventsCursor(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("events cursor requires subcommand: list | reset <name> | set <name> --since-cursor <tok> | encode | decode <tok>"))
	}
	switch args[0] {
	case "list":
		cs, err := a.Store.ListCursors(ctx)
		if err != nil {
			fatal(err)
		}
		if len(cs) == 0 {
			fmt.Println("no cursors")
			return
		}
		for _, c := range cs {
			fmt.Printf("%s\tposition=%s id=%s updated=%s\n", c.Name, c.Position.CreatedAt.Format(time.RFC3339Nano), c.Position.ID, c.UpdatedAt.Format(time.RFC3339))
		}
	case "reset":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: events cursor reset <name>"))
		}
		if err := a.Store.DeleteCursor(ctx, args[1]); err != nil {
			// Idempotent: silently treat not-found as success.
			if !strings.Contains(err.Error(), "cursor not found") {
				fatal(err)
			}
		}
		fmt.Printf("reset %s\n", args[1])
	case "set":
		fs := flag.NewFlagSet("events cursor set", flag.ExitOnError)
		tok := fs.String("since-cursor", "", "token from events cursor encode (required)")
		_ = fs.Parse(args[2:])
		if len(args) < 2 || *tok == "" {
			fatal(fmt.Errorf("usage: events cursor set <name> --since-cursor <token>"))
		}
		pos, err := db.DecodeCursor(*tok)
		if err != nil {
			fatal(err)
		}
		if err := a.Store.UpsertCursor(ctx, args[1], pos); err != nil {
			fatal(err)
		}
		fmt.Printf("set %s position=%s id=%s\n", args[1], pos.CreatedAt.Format(time.RFC3339Nano), pos.ID)
	case "encode":
		fs := flag.NewFlagSet("events cursor encode", flag.ExitOnError)
		ts := fs.String("created-at", "", "RFC3339Nano timestamp (required)")
		id := fs.String("id", "", "event id (required)")
		_ = fs.Parse(args[1:])
		if *ts == "" || *id == "" {
			fatal(fmt.Errorf("usage: events cursor encode --created-at <ts> --id <evt_id>"))
		}
		t, err := time.Parse(time.RFC3339Nano, *ts)
		if err != nil {
			fatal(err)
		}
		fmt.Println(db.EncodeCursor(db.CursorPosition{CreatedAt: t, ID: *id}))
	case "decode":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: events cursor decode <token>"))
		}
		pos, err := db.DecodeCursor(args[1])
		if err != nil {
			fatal(err)
		}
		fmt.Printf("created_at=%s id=%s\n", pos.CreatedAt.Format(time.RFC3339Nano), pos.ID)
	default:
		fatal(fmt.Errorf("unknown events cursor subcommand: %s", args[0]))
	}
}

func handleSQL(ctx context.Context, a *app.App, args []string) {
	// `-` (stdin marker) is a positional, but users may put flags after it
	// (`sql - --format jsonl`). Pull it out before flag parsing so the parser
	// doesn't stop on the first positional.
	useStdin := false
	cleaned := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-" {
			useStdin = true
			continue
		}
		cleaned = append(cleaned, a)
	}

	fs := flag.NewFlagSet("sql", flag.ExitOnError)
	file := fs.String("file", "", "read SQL from a file")
	formatFlag := fs.String("format", "text", "text|json|jsonl|csv")
	allowWrite := fs.Bool("write", false, "allow non-SELECT statements (default: read-only)")
	explain := fs.Bool("explain", false, "print EXPLAIN QUERY PLAN before executing")
	_ = fs.Parse(cleaned)
	positional := fs.Args()

	var query string
	switch {
	case *file != "":
		b, err := os.ReadFile(*file)
		if err != nil {
			fatal(err)
		}
		query = string(b)
	case useStdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fatal(err)
		}
		query = string(b)
	case len(positional) >= 1:
		query = strings.Join(positional, " ")
	default:
		fatal(fmt.Errorf("usage: sharedwatch sql <sql> | - | --file path"))
	}
	query = strings.TrimSpace(query)
	if query == "" {
		fatal(fmt.Errorf("empty query"))
	}

	if *explain {
		cols, rows, err := a.Store.RawSQL(ctx, "EXPLAIN QUERY PLAN "+query, false)
		if err != nil {
			fatal(err)
		}
		fmt.Fprintln(os.Stderr, "# EXPLAIN QUERY PLAN")
		_ = output.Render(os.Stderr, output.FormatText, output.Result{Columns: cols, Rows: rows})
	}

	cols, rows, err := a.Store.RawSQL(ctx, query, *allowWrite)
	if err != nil {
		if errors.Is(err, db.ErrWriteSQLDenied) {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, "hint: rerun with --write to allow mutating statements")
			os.Exit(2)
		}
		fatal(err)
	}

	f, err := output.ParseFormat(*formatFlag)
	if err != nil {
		fatal(err)
	}
	if err := output.Render(os.Stdout, f, output.Result{Columns: cols, Rows: rows}); err != nil {
		fatal(err)
	}
}

func handleIntent(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("usage: sharedwatch intent declare|list|revoke ..."))
	}
	switch args[0] {
	case "declare":
		fs := flag.NewFlagSet("intent declare", flag.ExitOnError)
		actor := fs.String("actor", "", "actor declaring the intent (required)")
		ttl := fs.Duration("ttl", 10*time.Minute, "how long the intent remains active")
		task := fs.String("task", "", "short task label")
		intentText := fs.String("intent", "", "one-sentence reason")
		metadata := fs.String("metadata", "", "free-form JSON object")
		rest := args[1:]
		pathGlob := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			pathGlob = rest[0]
			rest = rest[1:]
		}
		_ = fs.Parse(rest)
		if pathGlob == "" || *actor == "" {
			fatal(fmt.Errorf("usage: sharedwatch intent declare <path-glob> --actor <id> [--ttl 10m] [--task ...] [--intent ...]"))
		}
		if *ttl <= 0 {
			fatal(fmt.Errorf("--ttl must be positive"))
		}
		if *metadata != "" {
			var probe map[string]any
			if err := json.Unmarshal([]byte(*metadata), &probe); err != nil {
				fatal(fmt.Errorf("--metadata must be a JSON object: %w", err))
			}
		}
		id := newRandID("int")
		now := time.Now().UTC()
		if err := a.Store.InsertIntent(ctx, db.IntentRecord{
			IntentID: id, ActorID: *actor, PathGlob: pathGlob,
			DeclaredAt: now, ExpiresAt: now.Add(*ttl),
			Task: *task, Intent: *intentText, MetadataJSON: *metadata,
		}); err != nil {
			fatal(err)
		}
		fmt.Printf("intent %s expires %s\n", id, now.Add(*ttl).Format(time.RFC3339))
	case "list":
		fs := flag.NewFlagSet("intent list", flag.ExitOnError)
		actor := fs.String("actor", "", "filter by actor")
		pathGlob := fs.String("path-glob", "", "filter by exact path_glob match")
		all := fs.Bool("all", false, "include expired (default: only unexpired)")
		asJSON := fs.Bool("json", false, "emit JSON instead of text")
		_ = fs.Parse(args[1:])
		rows, err := a.Store.ListIntents(ctx, db.IntentFilter{ActorID: *actor, PathGlob: *pathGlob, IncludeExpired: *all})
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rows); err != nil {
				fatal(err)
			}
			return
		}
		if len(rows) == 0 {
			fmt.Println("no intents")
			return
		}
		for _, r := range rows {
			fmt.Printf("%s  actor=%s  path=%s  task=%s  expires=%s\n", r.IntentID, r.ActorID, r.PathGlob, defaultDash(r.Task), r.ExpiresAt.Format(time.RFC3339))
		}
	case "revoke":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: sharedwatch intent revoke <intent-id>"))
		}
		if err := a.Store.RevokeIntent(ctx, args[1]); err != nil {
			fatal(err)
		}
		fmt.Printf("revoked %s\n", args[1])
	default:
		fatal(fmt.Errorf("unknown intent subcommand: %s (expected: declare | list | revoke)", args[0]))
	}
}

func handleLease(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("usage: sharedwatch lease grant|release|list|renew ..."))
	}
	switch args[0] {
	case "grant":
		fs := flag.NewFlagSet("lease grant", flag.ExitOnError)
		actor := fs.String("actor", "", "actor acquiring the lease (required)")
		ttl := fs.Duration("ttl", 5*time.Minute, "how long the lease remains active (max 1h)")
		exclusive := fs.Bool("exclusive", false, "fail if another lease covers this path")
		metadata := fs.String("metadata", "", "free-form JSON object")
		asJSON := fs.Bool("format", false, "deprecated; emits JSON")
		rest := args[1:]
		pathGlob := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			pathGlob = rest[0]
			rest = rest[1:]
		}
		_ = fs.Parse(rest)
		if pathGlob == "" || *actor == "" {
			fatal(fmt.Errorf("usage: sharedwatch lease grant <path-glob> --actor <id> [--ttl 5m] [--exclusive]"))
		}
		if *ttl <= 0 || *ttl > db.LeaseTTLMax {
			fatal(fmt.Errorf("--ttl must be in (0, %s]", db.LeaseTTLMax))
		}
		// Detect conflicts for the response. Always grants — caller decides
		// whether to proceed unless --exclusive was passed (in which case we
		// refuse on conflict).
		existing, err := a.Store.ListLeases(ctx, db.LeaseFilter{PathGlob: pathGlob})
		if err != nil {
			fatal(err)
		}
		var conflicts []string
		for _, l := range existing {
			if l.ActorID != *actor {
				conflicts = append(conflicts, l.LeaseID)
			}
		}
		if *exclusive && len(conflicts) > 0 {
			fatal(fmt.Errorf("exclusive grant refused; conflicting leases: %v", conflicts))
		}
		id := newRandID("lse")
		now := time.Now().UTC()
		if err := a.Store.InsertLease(ctx, db.LeaseRecord{
			LeaseID: id, ActorID: *actor, PathGlob: pathGlob,
			GrantedAt: now, ExpiresAt: now.Add(*ttl),
			Exclusive: *exclusive, MetadataJSON: *metadata,
		}); err != nil {
			fatal(err)
		}
		resp := map[string]any{"lease_id": id, "granted": true, "expires_at": now.Add(*ttl).Format(time.RFC3339Nano)}
		if len(conflicts) > 0 {
			resp["conflict_with"] = conflicts
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
		_ = *asJSON
	case "release":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: sharedwatch lease release <lease-id>"))
		}
		if err := a.Store.ReleaseLease(ctx, args[1]); err != nil {
			fatal(err)
		}
		fmt.Printf("released %s\n", args[1])
	case "renew":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: sharedwatch lease renew <lease-id> [--ttl 5m]"))
		}
		fs := flag.NewFlagSet("lease renew", flag.ExitOnError)
		ttl := fs.Duration("ttl", 5*time.Minute, "extend lease by this much (capped to remaining time within LeaseTTLMax)")
		_ = fs.Parse(args[2:])
		r, err := a.Store.RenewLease(ctx, args[1], *ttl)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("renewed %s; renewal_count=%d; expires=%s\n", r.LeaseID, r.RenewalCount, r.ExpiresAt.Format(time.RFC3339))
	case "list":
		fs := flag.NewFlagSet("lease list", flag.ExitOnError)
		actor := fs.String("actor", "", "filter by actor")
		pathGlob := fs.String("path-glob", "", "filter by path_glob")
		all := fs.Bool("all", false, "include expired leases")
		asJSON := fs.Bool("json", false, "emit JSON instead of text")
		_ = fs.Parse(args[1:])
		rows, err := a.Store.ListLeases(ctx, db.LeaseFilter{ActorID: *actor, PathGlob: *pathGlob, IncludeExpired: *all})
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rows)
			return
		}
		if len(rows) == 0 {
			fmt.Println("no leases")
			return
		}
		for _, r := range rows {
			exc := ""
			if r.Exclusive {
				exc = " EXCL"
			}
			fmt.Printf("%s  actor=%s  path=%s  renewals=%d  expires=%s%s\n", r.LeaseID, r.ActorID, r.PathGlob, r.RenewalCount, r.ExpiresAt.Format(time.RFC3339), exc)
		}
	default:
		fatal(fmt.Errorf("unknown lease subcommand: %s (expected: grant | release | renew | list)", args[0]))
	}
}

// newRandID generates short prefixed random ids for intent/lease records.
// Mirrors the convention from internal/watcher (newID) without leaking that
// helper across packages.
func newRandID(prefix string) string {
	var b [8]byte
	_, _ = randRead(b[:])
	return prefix + "_" + hexEncode(b[:])
}

func handleOverview(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("overview", flag.ExitOnError)
	since := fs.Duration("since", 24*time.Hour, "lookback window for the aggregations (default 24h)")
	formatFlag := fs.String("format", "text", "text|json")
	_ = fs.Parse(args)

	ov, err := a.ComputeOverview(ctx, *since)
	if err != nil {
		fatal(err)
	}
	switch strings.ToLower(*formatFlag) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(ov); err != nil {
			fatal(err)
		}
	default:
		fmt.Printf("sharedwatch overview (since %s)\n", ov.Since)
		fmt.Printf("  mode=%s pending=%d failed=%d events_in_range=%d\n", ov.Mode, ov.Pending, ov.Failed, ov.EventsInRange)
		if len(ov.Roots) > 0 {
			fmt.Println("  roots:")
			for _, r := range ov.Roots {
				last := "-"
				if r.LastEventAt != nil {
					last = r.LastEventAt.UTC().Format(time.RFC3339)
				}
				fmt.Printf("    %s  pending=%d  last_event_at=%s  (%s)\n", r.Label, r.Pending, last, r.Path)
			}
		}
		if len(ov.ByType) > 0 {
			fmt.Println("  by_type:")
			for _, c := range ov.ByType {
				fmt.Printf("    %-16s %d\n", c.Type, c.Count)
			}
		}
		if len(ov.TopActors) > 0 {
			fmt.Println("  top_actors:")
			for _, ac := range ov.TopActors {
				fmt.Printf("    %-32s %d\n", ac.Actor, ac.Count)
			}
		}
		if len(ov.Drill) > 0 {
			fmt.Println("  drill:")
			for k, v := range ov.Drill {
				fmt.Printf("    %s\n      → %s\n", k, v)
			}
		}
	}
}

func handleActor(ctx context.Context, a *app.App, args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("usage: sharedwatch actor heartbeat <actor-id> [--focus <glob>] [--kind <k>] [--label <l>] [--metadata <json>]"))
	}
	switch args[0] {
	case "heartbeat":
		handleActorHeartbeat(ctx, a, args[1:])
	default:
		fatal(fmt.Errorf("unknown actor subcommand: %s (expected: heartbeat)", args[0]))
	}
}

func handleActorHeartbeat(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("actor heartbeat", flag.ExitOnError)
	focus := fs.String("focus", "", "path-glob describing what this actor is focused on")
	kind := fs.String("kind", "", "human | ai_agent | automation")
	label := fs.String("label", "", "human-readable display label")
	metadata := fs.String("metadata", "", "free-form JSON object stored alongside the actor")
	rest := args
	actorID := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		actorID = rest[0]
		rest = rest[1:]
	}
	_ = fs.Parse(rest)
	if actorID == "" {
		fatal(fmt.Errorf("usage: sharedwatch actor heartbeat <actor-id> [flags]"))
	}
	if *metadata != "" {
		var probe map[string]any
		if err := json.Unmarshal([]byte(*metadata), &probe); err != nil {
			fatal(fmt.Errorf("--metadata must be a JSON object: %w", err))
		}
	}
	if err := a.Store.UpsertActorHeartbeat(ctx, db.ActorRecord{
		ActorID:      actorID,
		Label:        *label,
		ActorKind:    *kind,
		Focus:        *focus,
		MetadataJSON: *metadata,
	}); err != nil {
		fatal(err)
	}
	fmt.Printf("heartbeat %s\n", actorID)
}

func handleSchema(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("schema", flag.ExitOnError)
	formatFlag := fs.String("format", "text", "text|json")
	_ = fs.Parse(args)
	tables, err := a.Store.Schema(ctx)
	if err != nil {
		fatal(err)
	}
	wantTable := ""
	if rest := fs.Args(); len(rest) > 0 {
		wantTable = rest[0]
	}
	if wantTable != "" {
		filtered := tables[:0]
		for _, t := range tables {
			if t.Name == wantTable {
				filtered = append(filtered, t)
			}
		}
		tables = filtered
		if len(tables) == 0 {
			fatal(fmt.Errorf("no such table: %s", wantTable))
		}
	}
	switch strings.ToLower(*formatFlag) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(tables); err != nil {
			fatal(err)
		}
	default:
		for _, t := range tables {
			fmt.Println(t.SQL + ";")
			fmt.Println()
		}
	}
}

func splitFields(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// eventsAsRows projects a slice of events into the columns/rows shape used by
// `events list`. When includeWatchRoot is true OR the result set spans more
// than one distinct watch_root value, the watch_root column is included;
// otherwise it is omitted so single-root output stays byte-identical to the
// pre-SW-AGENT-3 shape.
func eventsAsRows(evs []events.Event, includeWatchRoot bool) ([]string, [][]any) {
	showRoot := includeWatchRoot
	if !showRoot {
		// Auto-show when the result spans multiple roots.
		seen := ""
		for i, e := range evs {
			if i == 0 {
				seen = e.WatchRoot
				continue
			}
			if e.WatchRoot != seen {
				showRoot = true
				break
			}
		}
	}
	cols := []string{"id", "type", "rel_path", "old_path", "source", "status", "retry_count", "created_at", "file_size", "mtime", "content_hash", "coalesced_into", "producer_id", "payload_json"}
	if showRoot {
		cols = append(cols, "watch_root")
	}
	rows := make([][]any, 0, len(evs))
	for _, e := range evs {
		var oldPath any
		if e.OldPath != nil {
			oldPath = *e.OldPath
		}
		var mtime any
		if !e.MTime.IsZero() {
			mtime = e.MTime.UTC().Format(time.RFC3339Nano)
		}
		var hash any
		if e.Hash != "" {
			hash = e.Hash
		}
		var coalescedInto any
		if e.CoalescedInto != nil {
			coalescedInto = *e.CoalescedInto
		}
		var payload any
		if e.PayloadJSON != "" && e.PayloadJSON != "{}" {
			payload = e.PayloadJSON
		}
		row := []any{
			e.ID, string(e.Type), e.RelPath, oldPath, string(e.Source), string(e.Status),
			e.RetryCount, e.Timestamp.UTC().Format(time.RFC3339Nano), e.Size, mtime, hash, coalescedInto, e.ProducerID, payload,
		}
		if showRoot {
			row = append(row, e.WatchRoot)
		}
		rows = append(rows, row)
	}
	return cols, rows
}

func handleInit(a *app.App) {
	// app.New already created WatchPath and the DB. Print a small
	// confirmation so users know where data lives.
	fmt.Printf("watch_path=%s\ndb_path=%s\ndata_dir=%s\n", a.Cfg.WatchPath, a.Cfg.DBPath, a.Cfg.DataDir)
}

func buildLogger(format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level: %s", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(format) {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	case "text", "":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("unknown log format: %s", format)
	}
}

func handleConsume(ctx context.Context, a *app.App) {
	d, ok, err := a.ConsumeNow(ctx)
	if err != nil {
		fatal(err)
	}
	if !ok {
		fmt.Println("no pending events")
		return
	}
	fmt.Printf("digest created %s: %s\n", d.ID, firstLine(d.Summary))
}

func usage(root *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, `sharedwatch — calm collaboration bus for a local shared folder

USAGE
  sharedwatch [global flags] <command> [command flags] [args]

COMMANDS
  init                      create the data dir + DB; print resolved paths
  run                       start watcher + consumer + reconcile loop
  status [--json]           print current state (mode, queue depth, last runs)
  roots [--json]            list the folders being watched (works for single + multi-root)
  mode active [--ttl 30m]   enable active (fast-cadence) mode with TTL
  mode passive              force passive mode
  consume                   process pending events once and create a digest
  digest list [--limit N]   list recent digests (newest first)
  digest show <id>          print one digest in full (marks it read)
  digest archive <id>       mark a digest archived
  reconcile now             run reconcile pass immediately
  events list [flags]       read-only event query (--since, --type, --path-glob, --format, ...)
  events stats --root <l>   per-root aggregates (counts by type, top actors)
  events cursor list|reset <name>|set <name>|encode|decode   manage iteration cursors
  events retry [--max-retries N]    requeue failed events back to pending
  events recover-stuck [--older-than 5m]   flip stuck processing events back to pending
  overview [--format json]  cross-root L1 summary with drill hints
  sql <sql>|-|--file path   run a SQL query (SELECT-only by default; --write to allow mutations)
  schema [<table>]          print live DDL from the DB (--format text|json)
  actor heartbeat <id>      register/heartbeat an actor in the actors registry
  intent declare <path>     declare cooperative intent on a path (--ttl, --actor, --intent)
  lease acquire <path-glob> acquire an advisory lease on a path glob (--ttl, --actor)
  test emit [relpath]       inject a synthetic event for end-to-end testing
                            (--payload <json> OR attribution flags below)
  update [--apply]          check for / install a newer release (safe: dry-run by default;
                            --apply downloads + SHA-256 verifies + atomic-swaps the binary)
  version                   print version and exit
  help                      print this help

DEFAULT PATHS (when --watch-path / --db / --data-dir are not set)
  watch_path = $XDG_DATA_HOME/sharedwatch/watch  (or ~/.local/share/sharedwatch/watch)
  db_path    = $XDG_DATA_HOME/sharedwatch/queue.db
  data_dir   = $XDG_DATA_HOME/sharedwatch
  config     = ./config.yaml (silently ignored if missing)
  Run 'sharedwatch init' once to materialise these and print the resolved paths.

ATTRIBUTION FLAGS (root or test-emit; populate events.payload_json v1)
  --actor <id>              stable id of the writer (required to use any other)
  --actor-kind <k>          human | ai_agent | automation
  --session <id>            logical-run identifier; group related events
  --task <label>            short human-meaningful work label
  --intent <text>           one-sentence reason
  --addressee <id>          who the change is FOR (peer or human)
  --ref <event-id>          causal predecessor event id (ref_event_id)
  --tag <t>                 payload tag (repeatable, comma-aware)

GLOBAL FLAGS`)
	root.PrintDefaults()
	fmt.Fprintln(os.Stderr, `
EXAMPLES

  # First-time setup: materialise default paths under $XDG_DATA_HOME.
  sharedwatch init
  sharedwatch run &

  # See what you're watching (single or multi-root).
  sharedwatch roots
  sharedwatch roots --json

  # Multi-root: define folders with labels, then filter by label.
  sharedwatch --root auth=/work/auth --root billing=/work/billing run &
  sharedwatch --root auth events list --since 1h --format jsonl

  # Stamp attribution on every event this invocation emits.
  sharedwatch --actor claude-coord --task refactor-auth \
              --intent "split monolithic login.go" run

  # "What's new since I last looked?" — server-side cursor.
  sharedwatch events list --cursor-name my-agent --format jsonl

  # Schema discovery (run once per agent session, cache the result).
  sharedwatch schema --format json

  # SQL escape hatch — SELECT-only by default; --write to allow mutations.
  sharedwatch sql "SELECT type, COUNT(*) FROM events GROUP BY type"

  # End-to-end smoke: inject an attributed event, consume, view digest.
  sharedwatch test emit hello.md --actor demo --task quick-smoke
  sharedwatch consume
  sharedwatch digest list

  # Keep the binary current. Dry run by default; --apply to install.
  sharedwatch update
  sharedwatch update --apply`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// attrFlags bundles the v1 attribution flags (`--actor`, `--session`, ...) so
// they can be registered on multiple flagsets (root + subcommand) and merged
// with subcommand-wins semantics.
type attrFlags struct {
	actor      string
	actorKind  string
	session    string
	task       string
	intent     string
	addressee  string
	refEventID string
	tags       csvList
}

func (a *attrFlags) isEmpty() bool {
	return a.actor == "" && a.actorKind == "" && a.session == "" && a.task == "" &&
		a.intent == "" && a.addressee == "" && a.refEventID == "" && len(a.tags) == 0
}

// merge layers `over` on top of `a`. Non-empty fields in `over` win; empty
// fields in `over` inherit from `a`. For tags: if `over` has any tags they
// replace `a`'s tags (no implicit append, to keep the rule predictable).
func (a attrFlags) merge(over attrFlags) attrFlags {
	out := a
	if over.actor != "" {
		out.actor = over.actor
	}
	if over.actorKind != "" {
		out.actorKind = over.actorKind
	}
	if over.session != "" {
		out.session = over.session
	}
	if over.task != "" {
		out.task = over.task
	}
	if over.intent != "" {
		out.intent = over.intent
	}
	if over.addressee != "" {
		out.addressee = over.addressee
	}
	if over.refEventID != "" {
		out.refEventID = over.refEventID
	}
	if len(over.tags) > 0 {
		out.tags = append([]string(nil), over.tags...)
	}
	return out
}

func (a attrFlags) toPayload() events.PayloadV1 {
	return events.PayloadV1{
		Actor:      a.actor,
		ActorKind:  a.actorKind,
		Session:    a.session,
		Task:       a.task,
		Intent:     a.intent,
		Addressee:  a.addressee,
		RefEventID: a.refEventID,
		Tags:       append([]string(nil), a.tags...),
	}
}

// bindAttrFlags registers --actor / --actor-kind / --session / --task /
// --intent / --addressee / --ref / --tag on fs, writing into dest. The scope
// string is appended to each --help line so users can tell root-level vs
// subcommand-level definitions apart.
func bindAttrFlags(fs *flag.FlagSet, dest *attrFlags, scope string) {
	suffix := ""
	if scope != "" {
		suffix = " (" + scope + ")"
	}
	fs.StringVar(&dest.actor, "actor", "", "payload_json.actor — stable id for the writer"+suffix)
	fs.StringVar(&dest.actorKind, "actor-kind", "", "payload_json.actor_kind — human|ai_agent|automation"+suffix)
	fs.StringVar(&dest.session, "session", "", "payload_json.session — logical run id"+suffix)
	fs.StringVar(&dest.task, "task", "", "payload_json.task — short human-meaningful work label"+suffix)
	fs.StringVar(&dest.intent, "intent", "", "payload_json.intent — one-sentence reason"+suffix)
	fs.StringVar(&dest.addressee, "addressee", "", "payload_json.addressee — who the change is FOR"+suffix)
	fs.StringVar(&dest.refEventID, "ref", "", "payload_json.ref_event_id — causal predecessor event id"+suffix)
	fs.Var(&dest.tags, "tag", "payload_json.tags entry (repeatable; comma-aware)"+suffix)
}

// parseSinceUntil accepts either an RFC3339(Nano) timestamp or a Go duration
// string (5m, 1h, 24h) and returns the corresponding absolute time. Durations
// are interpreted as "now - d" — the lower bound of a since-window.
// Dogfood-discovered fix (SZ.1): the docs and Skill examples promise duration
// support; the original parser only accepted RFC3339.
func parseSinceUntil(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().UTC().Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("not a valid RFC3339 timestamp or duration: %q", s)
}

// rootDefList is a flag.Value for root-level --root entries. Each value is
// either `label=path` (explicit label) or `path` (label auto-derived from the
// basename later in config.NormalizeRoots). Comma-aware: `--root a=/x,b=/y`
// is equivalent to `--root a=/x --root b=/y`.
type rootDefList []config.WatchRoot

func (r *rootDefList) String() string {
	parts := make([]string, 0, len(*r))
	for _, w := range *r {
		if w.Label != "" {
			parts = append(parts, w.Label+"="+w.Path)
		} else {
			parts = append(parts, w.Path)
		}
	}
	return strings.Join(parts, ",")
}

func (r *rootDefList) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if i := strings.Index(p, "="); i >= 0 {
			*r = append(*r, config.WatchRoot{Label: strings.TrimSpace(p[:i]), Path: strings.TrimSpace(p[i+1:])})
		} else {
			*r = append(*r, config.WatchRoot{Path: p})
		}
	}
	return nil
}

// csvList implements flag.Value for a repeatable string slice that also
// accepts comma-separated values per occurrence, so `--ignore a,b --ignore c`
// is equivalent to `--ignore a --ignore b --ignore c`.
type csvList []string

func (c *csvList) String() string {
	return strings.Join(*c, ",")
}

func (c *csvList) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			*c = append(*c, p)
		}
	}
	return nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// reorderFlags pulls known global flags to the front of args so that callers can
// write `sharedwatch status --json` (subcommand first) without flag parsing
// stopping at the subcommand. It only moves flags whose names match a flag
// registered on `root`.
func reorderFlags(args []string, root *flag.FlagSet) []string {
	var globals, rest []string
	known := map[string]bool{}
	root.VisitAll(func(f *flag.Flag) { known[f.Name] = true })
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) < 2 || a[0] != '-' {
			rest = append(rest, args[i:]...)
			break
		}
		name := a[1:]
		if len(name) > 0 && name[0] == '-' {
			name = name[1:]
		}
		eq := -1
		for j, c := range name {
			if c == '=' {
				eq = j
				break
			}
		}
		key := name
		if eq >= 0 {
			key = name[:eq]
		}
		if !known[key] {
			rest = append(rest, args[i:]...)
			break
		}
		globals = append(globals, a)
		if eq < 0 && !isBoolFlag(root, key) && i+1 < len(args) {
			i++
			globals = append(globals, args[i])
		}
	}
	return append(globals, rest...)
}

func isBoolFlag(root *flag.FlagSet, name string) bool {
	f := root.Lookup(name)
	if f == nil {
		return false
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
		return true
	}
	return false
}
