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

// Regression test for the dogfood-discovered bug: a `.git` ignore pattern was
// only matching the literal file named ".git", so files inside `.git/`
// directories leaked into the journal. Fixed by checking each path segment.
func TestIgnoredMatchesDirectorySegment(t *testing.T) {
	patterns := []string{".git", "node_modules", ".DS_Store", "*.swp"}
	cases := []struct {
		path string
		want bool
	}{
		{".git/objects/abc", true}, // bug: was returning false
		{".git/HEAD", true},
		{"auth/.git/config", true}, // .git anywhere in the path
		{"node_modules/foo/index.js", true},
		{"deep/sub/node_modules/x", true},
		{"src/.DS_Store", true},
		{"src/auth.go.swp", true},
		{"src/auth.go", false},          // not ignored
		{"docs/git-tutorial.md", false}, // segment is "git-tutorial.md", not ".git"
		{".gitignore", false},           // ".gitignore" != ".git"
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := Ignored(tc.path, patterns); got != tc.want {
				t.Fatalf("Ignored(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
