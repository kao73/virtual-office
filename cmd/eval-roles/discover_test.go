package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverCasesFindsRoleCaseDirectories(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"analyst/case-a", "analyst/case-b", "implementer/case-a"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := discoverCases(root, "", "")
	if err != nil {
		t.Fatalf("discoverCases: %v", err)
	}
	if len(dirs) != 3 {
		t.Errorf("найдено %d кейсов, ожидалось 3: %v", len(dirs), dirs)
	}
}

func TestDiscoverCasesFiltersByRoleAndCase(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"analyst/case-a", "analyst/case-b", "implementer/case-a"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := discoverCases(root, "analyst", "case-b")
	if err != nil {
		t.Fatalf("discoverCases: %v", err)
	}
	if len(dirs) != 1 || filepath.Base(dirs[0]) != "case-b" {
		t.Errorf("фильтр вернул %v", dirs)
	}
}

// tasks.md 2.1: пустое (или отсутствующее) дерево evals/ — не ошибка.
func TestDiscoverCasesEmptyTreeIsNotAnError(t *testing.T) {
	dirs, err := discoverCases(filepath.Join(t.TempDir(), "no-such-evals"), "", "")
	if err != nil {
		t.Fatalf("пустое дерево сочтено ошибкой: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("нашлись кейсы там, где их нет: %v", dirs)
	}
}
