package catalog

import "testing"

func TestIgnored(t *testing.T) {
	patterns := []string{".git", "*.tmp", "notes/private.md"}
	if !Ignored("foo.tmp", patterns) {
		t.Fatal("expected tmp file ignored")
	}
	if !Ignored("notes/private.md", patterns) {
		t.Fatal("expected exact path ignored")
	}
	if Ignored("docs/readme.md", patterns) {
		t.Fatal("did not expect docs/readme.md ignored")
	}
}
