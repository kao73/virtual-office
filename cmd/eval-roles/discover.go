package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// discoverCases обходит evalsRoot/<role>/<case-id>/ и возвращает подходящие
// каталоги кейсов, отсортированные для стабильного вывода. Отсутствующий или
// пустой evalsRoot — не ошибка: результат просто пуст.
func discoverCases(evalsRoot, roleFilter, caseFilter string) ([]string, error) {
	rolePattern := "*"
	if roleFilter != "" {
		rolePattern = roleFilter
	}
	casePattern := "*"
	if caseFilter != "" {
		casePattern = caseFilter
	}

	matches, err := filepath.Glob(filepath.Join(evalsRoot, rolePattern, casePattern))
	if err != nil {
		return nil, fmt.Errorf("evals/ не прочитан: %w", err)
	}

	var dirs []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() {
			continue
		}
		dirs = append(dirs, m)
	}
	sort.Strings(dirs)
	return dirs, nil
}
