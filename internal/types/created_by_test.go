package types

import "testing"

func TestCanonicalCreatedBy(t *testing.T) {
	tests := map[string]string{
		"dearing":                    "dearing",
		"beads_aegis/crew/dearing":   "dearing",
		"/srv/aegis/crew/ellie/":     "ellie",
		"email@example.com":          "email@example.com",
		"non-crew/path-shaped-value": "non-crew/path-shaped-value",
	}
	for raw, want := range tests {
		issue := Issue{CreatedBy: raw}
		if got := issue.CanonicalCreatedBy(); got != want {
			t.Errorf("CanonicalCreatedBy(%q) = %q, want %q", raw, got, want)
		}
	}
}
