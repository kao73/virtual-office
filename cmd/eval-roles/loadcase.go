package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadCase разбирает <dir>/expect.yaml в Case и сверяет её с собственным
// каталогом кейса: role обязан совпадать с именем родительского каталога,
// а checks не должен быть пуст.
func LoadCase(dir string) (Case, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "expect.yaml"))
	if err != nil {
		return Case{}, fmt.Errorf("expect.yaml не прочитан: %w", err)
	}

	var c Case
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Case{}, fmt.Errorf("expect.yaml не разобран: %w", err)
	}
	c.dir = dir

	wantRole := filepath.Base(filepath.Dir(dir))
	if c.Role != wantRole {
		return Case{}, fmt.Errorf("expect.yaml: role=%q не совпадает с каталогом роли %q", c.Role, wantRole)
	}
	if len(c.Checks) == 0 {
		return Case{}, errors.New("expect.yaml: checks пуст")
	}
	for _, chk := range c.Checks {
		// Только fixture_tests: пустой command не провалился бы сам —
		// `sh -c ""` выходит с кодом 0, и проверка молча зазеленела бы,
		// ничего не проверив. У outcome и diff_scope такой ловушки нет:
		// пустой Expect не совпадёт ни с одним исходом, а пустой Allow
		// у diff_scope — законное значение (запрет любых изменений).
		if chk.Kind == "fixture_tests" && strings.TrimSpace(chk.Command) == "" {
			return Case{}, fmt.Errorf("expect.yaml: fixture_tests-проверка без command")
		}
	}
	return c, nil
}
