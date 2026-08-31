package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeFixtureCommitsFixtureTree(t *testing.T) {
	caseDir := t.TempDir()
	fixtureSrc := filepath.Join(caseDir, "fixture")
	if err := os.MkdirAll(fixtureSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureSrc, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureDir, commit, err := materializeFixture(caseDir)
	if err != nil {
		t.Fatalf("fixture не материализован: %v", err)
	}
	defer os.RemoveAll(fixtureDir)

	if commit == "" {
		t.Error("нет initial commit SHA")
	}
	if _, err := os.Stat(filepath.Join(fixtureDir, "hello.txt")); err != nil {
		t.Errorf("файл фикстуры не скопирован: %v", err)
	}
	status, err := exec.Command("git", "-C", fixtureDir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		t.Errorf("рабочее дерево не чистое после коммита: %s", status)
	}
}

func TestMaterializeFixtureFailsWithoutFixtureDir(t *testing.T) {
	caseDir := t.TempDir() // без подкаталога "fixture"
	if _, _, err := materializeFixture(caseDir); err == nil {
		t.Error("отсутствие fixture/ не замечено")
	}
}

func TestDiscoverFixtureTaskKeyFindsSeededChange(t *testing.T) {
	fixtureDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fixtureDir, "docs/comet/changes/eval-brief"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, warning := discoverFixtureTaskKey(fixtureDir)
	if got != "eval-brief" {
		t.Errorf("discoverFixtureTaskKey() name = %q, ожидалось %q", got, "eval-brief")
	}
	if warning != "" {
		t.Errorf("предупреждение на честном совпадении: %q", warning)
	}
}

func TestDiscoverFixtureTaskKeyEmptyWithoutChange(t *testing.T) {
	fixtureDir := t.TempDir() // без docs/comet/changes вовсе
	got, warning := discoverFixtureTaskKey(fixtureDir)
	if got != "" {
		t.Errorf("discoverFixtureTaskKey() name = %q, ожидалась пустая строка", got)
	}
	if warning != "" {
		t.Errorf("предупреждение при законном «изменения нет»: %q", warning)
	}
}

// Больше одного изменения в фикстуре не должно случаться (каждый golden case
// проверяет один сценарий), но угадывать, какое из двух — не наше дело:
// пустой ключ здесь безопаснее, чем случайный выбор. Отличие от «изменения
// нет вовсе» — это уже не законный случай, и о нём стоит сказать вслух.
func TestDiscoverFixtureTaskKeyEmptyWithMultipleChanges(t *testing.T) {
	fixtureDir := t.TempDir()
	for _, name := range []string{"eval-a", "eval-b"} {
		if err := os.MkdirAll(filepath.Join(fixtureDir, "docs/comet/changes", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, warning := discoverFixtureTaskKey(fixtureDir)
	if got != "" {
		t.Errorf("discoverFixtureTaskKey() name = %q при неоднозначности, ожидалась пустая строка", got)
	}
	if warning == "" {
		t.Error("неоднозначность (два каталога изменения) прошла молча, без предупреждения")
	}
}

// Найдено ревью задачи 19: круговой проход CometChangeName(name) == name
// не гарантирован техникой — фикстуры собирают руками. Опечатка в имени
// каталога (здесь — подчёркивание вместо дефиса) молча воспроизводила бы
// ровно тот баг, который эта функция чинит: --task-key ушёл бы с «неверным»
// именем, composeContext не нашёл бы под ним каталог, и строка «Каталог
// изменения» пропала бы так же, как до фикса — без единого слова о причине.
func TestDiscoverFixtureTaskKeyWarnsOnUnsafeDirName(t *testing.T) {
	fixtureDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fixtureDir, "docs/comet/changes/eval_brief"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, warning := discoverFixtureTaskKey(fixtureDir)
	if got != "" {
		t.Errorf("discoverFixtureTaskKey() name = %q при небезопасном имени, ожидалась пустая строка "+
			"(--task-key с санитизированным именем всё равно не совпал бы с настоящим каталогом)", got)
	}
	if warning == "" {
		t.Error("несовпадающее санитизированное имя прошло молча, без предупреждения")
	}
}
