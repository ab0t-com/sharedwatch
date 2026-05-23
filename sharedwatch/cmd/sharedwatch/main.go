package main

import (
	"context"
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

// Version is overridden at build time with -ldflags "-X main.Version=…".
var Version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root := flag.NewFlagSet("sharedwatch", flag.ContinueOnError)
	root.SetOutput(os.Stderr)
	configPath := root.String("config", "config.yaml", "path to config file (silently ignored if missing)")
	watchPath := root.String("watch-path", "", "override watch_path")
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
	}

	known := map[string]bool{
		"run": true, "status": true, "mode": true, "digest": true,
		"reconcile": true, "test": true, "consume": true, "init": true,
		"events": true, "sql": true, "schema": true, "actor": true,
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
	if *watchPath != "" {
		cfg.WatchPath = *watchPath
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
		_ = fs.Parse(args[1:])
		digests, err := a.Store.ListDigestsFiltered(ctx, *limit, *status)
		if err != nil {
			fatal(err)
		}
		if len(digests) == 0 {
			if *status != "" {
				fmt.Printf("no digests with status=%s\n", *status)
			} else {
				fmt.Println("no digests yet — try `sharedwatch consume` after some activity")
			}
			return
		}
		for _, d := range digests {
			fmt.Printf("%s %s events=%d status=%s %s\n", d.ID, d.CreatedAt.Format(time.RFC3339), d.EventCount, d.Status, firstLine(d.Summary))
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
	var types, sources, statuses, producers csvList
	fs.Var(&types, "type", "filter by type (repeatable)")
	fs.Var(&sources, "source", "filter by source (repeatable)")
	fs.Var(&statuses, "status", "filter by status (repeatable)")
	fs.Var(&producers, "producer", "filter by producer_id (repeatable)")
	payloadKey := fs.String("payload-key", "", "post-filter: payload_json[<key>] must equal --payload-value")
	payloadValue := fs.String("payload-value", "", "see --payload-key")
	_ = fs.Parse(args)

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
		PathGlob:     *pathGlob,
		PayloadKey:   *payloadKey,
		PayloadValue: *payloadValue,
		Limit:        *limit,
		OrderAsc:     orderAsc,
	}
	if *since != "" {
		t, err := time.Parse(time.RFC3339Nano, *since)
		if err != nil {
			fatal(fmt.Errorf("--since: %w", err))
		}
		filter.Since = t
	}
	if *until != "" {
		t, err := time.Parse(time.RFC3339Nano, *until)
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

	cols, rows := eventsAsRows(evs)
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

func eventsAsRows(evs []events.Event) ([]string, [][]any) {
	cols := []string{"id", "type", "rel_path", "old_path", "source", "status", "retry_count", "created_at", "file_size", "mtime", "content_hash", "coalesced_into", "producer_id", "payload_json"}
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
		rows = append(rows, []any{
			e.ID, string(e.Type), e.RelPath, oldPath, string(e.Source), string(e.Status),
			e.RetryCount, e.Timestamp.UTC().Format(time.RFC3339Nano), e.Size, mtime, hash, coalescedInto, e.ProducerID, payload,
		})
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
  mode active [--ttl 30m]   enable active (fast-cadence) mode with TTL
  mode passive              force passive mode
  consume                   process pending events once and create a digest
  digest list [--limit N]   list recent digests (newest first)
  digest show <id>          print one digest in full (marks it read)
  digest archive <id>       mark a digest archived
  reconcile now             run reconcile pass immediately
  events list [flags]       read-only event query (--since, --type, --path-glob, --format, ...)
  events cursor list|reset <name>|set <name>|encode|decode   manage iteration cursors
  events retry [--max-retries N]    requeue failed events back to pending
  events recover-stuck [--older-than 5m]   flip stuck processing events back to pending
  sql <sql>|-|--file path   run a SQL query (SELECT-only by default; --write to allow mutations)
  schema [<table>]          print live DDL from the DB (--format text|json)
  test emit [relpath]       inject a synthetic event for end-to-end testing
                            (--payload <json> OR attribution flags below)
  version                   print version and exit
  help                      print this help

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
  sharedwatch --watch-path ./scratch --db ./queue.db run
  sharedwatch --config ./config.yaml status --json
  sharedwatch test emit hello.md && sharedwatch consume && sharedwatch digest list`)
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
