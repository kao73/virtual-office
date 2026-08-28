package main

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"src/**", "src/foo.go", true},
		{"src/**", "src/a/b.go", true},
		{"src/**", "other/x.go", false},
		{"docs/changes/_manual/**", "docs/changes/_manual/brief.md", true},
		{"*.md", "a.md", true},
		{"*.md", "dir/a.md", false},
		{"**", "anything/at/all.go", true},
	}
	for _, tc := range cases {
		got, err := globMatch(tc.pattern, tc.path)
		if err != nil {
			t.Errorf("globMatch(%q, %q) errored: %v", tc.pattern, tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestGlobMatchReportsBadPattern(t *testing.T) {
	if _, err := globMatch("[", "a.go"); err == nil {
		t.Error("malformed pattern not reported as error")
	}
}
