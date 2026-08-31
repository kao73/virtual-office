package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
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

// discoverFixtureTaskKey ищет каталог изменения Comet Native, который
// фикстура завела заранее (docs/comet/changes/<name>), и возвращает <name>
// как ключ задачи для run-agent --task-key. warning не пуст, когда найденное
// (или его отсутствие при неоднозначности) стоит показать человеку — это не
// повод остановить прогон, кейс идёт дальше как и раньше, без ключа.
//
// run-agent, в отличие от продового раннера, трекера не видит и TaskKey
// не задаёт (cmd/run-agent/main.go: "Пуст при ручном запуске"), а без него
// composeContext считает CometChangeDirRel("") == "docs/comet/changes/manual"
// — имя, которого ни у одной фикстуры нет, — и строка «Каталог изменения»
// в context.md не появляется вовсе, даже когда фикстура честно завела
// изменение под собственным именем. Роль в этом случае идёт по ветке «нет
// активного изменения» из своего же role.md — иногда безобидно (agent сам
// находит изменение по содержимому task.md), а иногда нет: без словесной
// подсказки в task.md роль эту ветку и остаётся, не пробуя Comet Native
// вовсе (живой прогон, reviewer/capability-spot-defect, задача 16).
//
// Круговой проход CometChangeName(name) == name НЕ гарантирован техникой —
// только соглашением: эвал-фикстуры собирают руками, не самим Comet, и
// опечатка в имени каталога (подчёркивание, заглавная буква — всё, что
// возьмёт файловая система, но переиначит cometSafeName) молча возвращает
// ровно тот баг, который эта функция чинит: --task-key уйдёт с «неправильным»
// именем, composeContext не найдёт под ним каталог и просто не напишет
// строку — узнать об этом можно только следующим платным живым прогоном
// (найдено ревью задачи 19, живой mismatch подтверждён не был — риск
// теоретический, но дешёвый в проверке). Поэтому это единственный случай,
// требующий предупреждения, а не тихого fallback.
func discoverFixtureTaskKey(fixtureDir string) (name, warning string) {
	entries, err := os.ReadDir(filepath.Join(fixtureDir, runner.CometChangesDir))
	if err != nil {
		return "", ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if name != "" {
			return "", fmt.Sprintf("больше одного каталога изменения в docs/comet/changes/ (%q и %q) — "+
				"--task-key не передан, роль пойдёт без него", name, e.Name())
		}
		name = e.Name()
	}
	if name != "" && runner.CometChangeName(name) != name {
		return "", fmt.Sprintf("каталог изменения docs/comet/changes/%s — не то имя, которое "+
			"CometChangeName вернёт для самого себя (%s) — --task-key не передан, "+
			"composeContext не найдёт его снова", name, runner.CometChangeName(name))
	}
	return name, ""
}
