package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"sharedwatch/internal/app"
)

// handleRoots implements the `roots` subcommand: prints the watched folders
// uniformly for both single-root and multi-root configurations.
//
// Why this exists separately from `status`: the StatusSnapshot's Roots slice
// is intentionally empty in single-root mode to preserve the pre-SW-AGENT-3
// JSON contract. This handler synthesizes a one-row view from the legacy
// single-root config so "what folders am I watching?" always returns
// something concrete.
func handleRoots(ctx context.Context, a *app.App, args []string) {
	fs := flag.NewFlagSet("roots", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit as JSON envelope (format_version: 1)")
	_ = fs.Parse(args)

	roots, err := collectRootsView(ctx, a)
	if err != nil {
		fatal(err)
	}

	if *asJSON {
		envelope := struct {
			FormatVersion int            `json:"format_version"`
			Mode          string         `json:"mode"`
			Roots         []app.RootView `json:"roots"`
		}{
			FormatVersion: 1,
			Mode:          rootsMode(a),
			Roots:         roots,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(envelope)
		return
	}

	if len(roots) == 0 {
		fmt.Println("no watch roots configured (run `sharedwatch init` to set up the default folder)")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tPATH\tPENDING\tLAST EVENT")
	for _, r := range roots {
		label := r.Label
		if label == "" {
			label = "(default)"
		}
		last := "-"
		if r.LastEventAt != nil {
			last = r.LastEventAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", label, r.Path, r.Pending, last)
	}
	_ = tw.Flush()
}

// collectRootsView returns a uniform list of watched folders regardless of
// single-root vs multi-root mode. In single-root mode it synthesizes one
// entry from Cfg.WatchPath; in multi-root it defers to App.RootsView.
func collectRootsView(ctx context.Context, a *app.App) ([]app.RootView, error) {
	if len(a.Cfg.WatchRoots) > 0 {
		return a.RootsView(ctx)
	}
	if strings.TrimSpace(a.Cfg.WatchPath) == "" {
		return nil, nil
	}
	pending, err := a.Store.PendingCountByRoot(ctx, "")
	if err != nil {
		return nil, err
	}
	last, err := a.Store.LastEventAtByRoot(ctx, "")
	if err != nil {
		return nil, err
	}
	view := app.RootView{
		Label:   "",
		Path:    a.Cfg.WatchPath,
		Pending: pending,
	}
	if !last.IsZero() {
		t := last.UTC()
		view.LastEventAt = &t
	}
	return []app.RootView{view}, nil
}

func rootsMode(a *app.App) string {
	if len(a.Cfg.WatchRoots) > 0 {
		return "multi"
	}
	return "single"
}
