package db

import "testing"

func TestLeaseGlobMatchesPath(t *testing.T) {
	cases := []struct {
		name      string
		glob      string
		path      string
		wantMatch bool
	}{
		{"exact match", "auth/login.go", "auth/login.go", true},
		{"exact non-match", "auth/login.go", "auth/oauth.go", false},
		{"single-star matches one segment", "auth/*", "auth/login.go", true},
		{"single-star does NOT cross slash", "auth/*", "auth/sub/oauth.go", false},
		{"double-star matches any depth", "auth/**", "auth/login.go", true},
		{"double-star matches deep paths", "auth/**", "auth/sub/very/deep/oauth.go", true},
		{"double-star scoped to prefix", "auth/**", "billing/charge.go", false},
		{"double-star at root", "**", "anywhere.go", true},
		{"double-star with suffix matches anywhere", "**/login.go", "auth/login.go", true},
		{"empty glob never matches", "", "auth/login.go", false},
		{"empty path never matches", "auth/**", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LeaseGlobMatchesPath(tc.glob, tc.path); got != tc.wantMatch {
				t.Fatalf("LeaseGlobMatchesPath(%q, %q) = %v, want %v", tc.glob, tc.path, got, tc.wantMatch)
			}
		})
	}
}
