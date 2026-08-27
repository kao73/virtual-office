package main

import (
	"path/filepath"
	"strings"
)

// globMatch reports whether path matches pattern, where "**" matches zero or
// more whole path segments (including none) and any other segment follows
// filepath.Match's single-segment semantics ("*" does not cross "/").
func globMatch(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		if matchSegments(pat[1:], name) {
			return true
		}
		if len(name) == 0 {
			return false
		}
		return matchSegments(pat, name[1:])
	}
	if len(name) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], name[1:])
}
