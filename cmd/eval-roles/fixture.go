package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// materializeFixture копирует caseDir/fixture в свежий временный каталог,
// коммитит его одним git-коммитом и возвращает этот каталог вместе с SHA
// коммита. Уборка возвращённого каталога — на вызывающем.
func materializeFixture(caseDir string) (fixtureDir, initialCommit string, err error) {
	fixtureSrc := filepath.Join(caseDir, "fixture")
	if info, statErr := os.Stat(fixtureSrc); statErr != nil || !info.IsDir() {
		return "", "", fmt.Errorf("fixture/ не найден в %s", caseDir)
	}

	fixtureDir, err = os.MkdirTemp("", "eval-roles-fixture-*")
	if err != nil {
		return "", "", fmt.Errorf("временный каталог не создан: %w", err)
	}
	if err := os.CopyFS(fixtureDir, os.DirFS(fixtureSrc)); err != nil {
		return "", "", fmt.Errorf("fixture/ не скопирован: %w", err)
	}

	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-q", "-m", "eval fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", fixtureDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=eval-roles", "GIT_AUTHOR_EMAIL=eval-roles@office.local",
			"GIT_COMMITTER_NAME=eval-roles", "GIT_COMMITTER_EMAIL=eval-roles@office.local")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
	}

	head, err := exec.Command("git", "-C", fixtureDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("HEAD не определён: %w: %s", err, exitStderr(err))
	}
	return fixtureDir, strings.TrimSpace(string(head)), nil
}
