package main

import (
	"encoding/json"
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

// Найдено живым прогоном (задача 21): фикстуры вроде
// capability-reads-brief-and-spec/capability-spot-defect предзаводят
// .comet/runtime/native/changes/<name>/state.json с записанным на момент
// авторства путём — он никогда не совпадёт с os.MkdirTemp-каталогом
// настоящего материализованного прогона. Comet Native при таком
// расхождении обновляет локальный кэш выполнения, но отказывается писать
// длящееся portable-состояние (comet-state.yaml) — воспроизведено вживую
// подменой пути на настоящий и повторным builder-handoff. Без починки пути
// это заводит каждый прогон такой фикстуры в то же расхождение заново.
func TestMaterializeFixtureRewritesLocalExecutionProjectRoot(t *testing.T) {
	caseDir := t.TempDir()
	fixtureSrc := filepath.Join(caseDir, "fixture")
	stateDir := filepath.Join(fixtureSrc, ".comet/runtime/native/changes/eval-brief")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := map[string]any{
		"schema": "comet.native.local-execution.v4",
		"change": "eval-brief",
		"workspace": map[string]any{
			"projectRoot":  "/where/fixture/was/authored",
			"worktreeRoot": "/where/fixture/was/authored",
			"branch":       "main",
		},
		"execution": nil,
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureDir, _, err := materializeFixture(caseDir)
	if err != nil {
		t.Fatalf("fixture не материализован: %v", err)
	}
	defer os.RemoveAll(fixtureDir)

	got, err := os.ReadFile(filepath.Join(fixtureDir, ".comet/runtime/native/changes/eval-brief/state.json"))
	if err != nil {
		t.Fatalf("state.json не прочитан: %v", err)
	}
	var doc struct {
		Workspace struct {
			ProjectRoot  string `json:"projectRoot"`
			WorktreeRoot string `json:"worktreeRoot"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("state.json не разобран: %v", err)
	}
	if doc.Workspace.ProjectRoot != fixtureDir {
		t.Errorf("projectRoot = %q, ожидался %q", doc.Workspace.ProjectRoot, fixtureDir)
	}
	if doc.Workspace.WorktreeRoot != fixtureDir {
		t.Errorf("worktreeRoot = %q, ожидался %q", doc.Workspace.WorktreeRoot, fixtureDir)
	}
}

// Фикстура без предзаведённого .comet/runtime — законный, куда более частый
// случай (см. TestMaterializeFixtureCommitsFixtureTree): rewriteLocalExecutionPaths
// не должна на нём спотыкаться.
func TestMaterializeFixtureToleratesNoLocalExecutionState(t *testing.T) {
	caseDir := t.TempDir()
	fixtureSrc := filepath.Join(caseDir, "fixture")
	if err := os.MkdirAll(fixtureSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureSrc, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureDir, _, err := materializeFixture(caseDir)
	if err != nil {
		t.Fatalf("fixture без .comet/runtime не материализована: %v", err)
	}
	defer os.RemoveAll(fixtureDir)
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
