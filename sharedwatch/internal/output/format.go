// Package output renders tabular data (columns + rows) in the formats agents
// and humans want: text, json, jsonl, csv. Designed to be a shared backend
// for `events list` and `sql`.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const FormatVersion = 1

type Format string

const (
	FormatText  Format = "text"
	FormatJSON  Format = "json"
	FormatJSONL Format = "jsonl"
	FormatCSV   Format = "csv"
)

func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "", "text":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	case "jsonl":
		return FormatJSONL, nil
	case "csv":
		return FormatCSV, nil
	default:
		return "", fmt.Errorf("unknown format: %s (want text|json|jsonl|csv)", s)
	}
}

// Result is what a renderer takes.
type Result struct {
	Columns    []string
	Rows       [][]any
	NextCursor string // empty if not applicable
}

// Project returns r with only the named columns retained, preserving order.
// Unknown field names are silently dropped (logged at the call site if desired).
func (r Result) Project(fields []string) Result {
	if len(fields) == 0 {
		return r
	}
	keep := make([]int, 0, len(fields))
	cols := make([]string, 0, len(fields))
	colIdx := map[string]int{}
	for i, c := range r.Columns {
		colIdx[c] = i
	}
	for _, f := range fields {
		if i, ok := colIdx[f]; ok {
			keep = append(keep, i)
			cols = append(cols, f)
		}
	}
	rows := make([][]any, len(r.Rows))
	for i, row := range r.Rows {
		out := make([]any, len(keep))
		for j, k := range keep {
			out[j] = row[k]
		}
		rows[i] = out
	}
	return Result{Columns: cols, Rows: rows, NextCursor: r.NextCursor}
}

// Render writes r in the given format to w.
func Render(w io.Writer, f Format, r Result) error {
	switch f {
	case FormatText:
		return renderText(w, r)
	case FormatJSON:
		return renderJSON(w, r)
	case FormatJSONL:
		return renderJSONL(w, r)
	case FormatCSV:
		return renderCSV(w, r)
	default:
		return fmt.Errorf("unsupported format: %s", f)
	}
}

func renderText(w io.Writer, r Result) error {
	// Tab-aligned. Header on top. Cursor as trailing comment line.
	if len(r.Columns) == 0 && len(r.Rows) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, strings.Join(r.Columns, "\t")); err != nil {
		return err
	}
	for _, row := range r.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = stringify(v)
		}
		if _, err := fmt.Fprintln(w, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	if r.NextCursor != "" {
		if _, err := fmt.Fprintf(w, "# next_cursor=%s\n", r.NextCursor); err != nil {
			return err
		}
	}
	return nil
}

func renderJSON(w io.Writer, r Result) error {
	envelope := map[string]any{
		"format_version": FormatVersion,
		"columns":        r.Columns,
		"rows":           rowsAsObjects(r),
	}
	if r.NextCursor != "" {
		envelope["next_cursor"] = r.NextCursor
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope)
}

func renderJSONL(w io.Writer, r Result) error {
	enc := json.NewEncoder(w)
	for _, row := range r.Rows {
		obj := make(map[string]any, len(r.Columns))
		for i, col := range r.Columns {
			obj[col] = jsonValue(row[i])
		}
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	if r.NextCursor != "" {
		if err := enc.Encode(map[string]any{"next_cursor": r.NextCursor, "format_version": FormatVersion}); err != nil {
			return err
		}
	}
	return nil
}

func renderCSV(w io.Writer, r Result) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(r.Columns); err != nil {
		return err
	}
	for _, row := range r.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = stringify(v)
		}
		if err := cw.Write(cells); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	if r.NextCursor != "" {
		if _, err := fmt.Fprintf(w, "# next_cursor,%s\n", r.NextCursor); err != nil {
			return err
		}
	}
	return nil
}

func rowsAsObjects(r Result) []map[string]any {
	out := make([]map[string]any, len(r.Rows))
	for i, row := range r.Rows {
		obj := make(map[string]any, len(r.Columns))
		for j, col := range r.Columns {
			obj[col] = jsonValue(row[j])
		}
		out[i] = obj
	}
	return out
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// jsonValue normalizes raw DB scan values so JSON output is consistent —
// []byte becomes string, etc.
func jsonValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
