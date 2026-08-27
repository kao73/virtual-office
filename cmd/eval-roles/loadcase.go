package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadCase parses <dir>/expect.yaml into a Case and validates it against the
// case's own directory: role must match the parent directory name, and
// checks must not be empty.
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
	return c, nil
}
