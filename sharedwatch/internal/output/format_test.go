package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sample() Result {
	return Result{
		Columns:    []string{"id", "name"},
		Rows:       [][]any{{"1", "alpha"}, {"2", "beta"}},
		NextCursor: "cursortoken",
	}
}

func TestRenderText(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatText, sample()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "id\tname\n") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "1\talpha") {
		t.Fatalf("missing row: %q", out)
	}
	if !strings.Contains(out, "# next_cursor=cursortoken") {
		t.Fatalf("missing cursor trailer: %q", out)
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatJSON, sample()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if got["next_cursor"] != "cursortoken" {
		t.Fatalf("missing next_cursor: %+v", got)
	}
	rows := got["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestRenderJSONL(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatJSONL, sample()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 rows + cursor sentinel), got %d:\n%s", len(lines), buf.String())
	}
	var sentinel map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &sentinel); err != nil {
		t.Fatal(err)
	}
	if sentinel["next_cursor"] != "cursortoken" {
		t.Fatalf("last line missing cursor: %+v", sentinel)
	}
}

func TestRenderCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatCSV, sample()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "id,name\n") {
		t.Fatalf("missing CSV header: %q", out)
	}
	if !strings.Contains(out, "1,alpha\n") {
		t.Fatalf("missing CSV row: %q", out)
	}
	if !strings.Contains(out, "# next_cursor,cursortoken") {
		t.Fatalf("missing CSV cursor comment: %q", out)
	}
}

func TestProject(t *testing.T) {
	r := sample().Project([]string{"name"})
	if len(r.Columns) != 1 || r.Columns[0] != "name" {
		t.Fatalf("project failed: %+v", r.Columns)
	}
	if len(r.Rows[0]) != 1 || r.Rows[0][0] != "alpha" {
		t.Fatalf("project rows wrong: %+v", r.Rows)
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatJSON, Result{Columns: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["next_cursor"]; present {
		t.Fatalf("empty result should not carry cursor: %+v", got)
	}
}
