package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	payload "github.com/kao73/virtual-office"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
)

// Свежая машина: каталога нет — появляется ровно с двумя образцами, слово
// в слово из поставки, и с подсказкой, куда их копировать.
func TestInitLaysOutFreshHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "office-home") // ещё не существует
	t.Setenv(runner.HomeEnv, home)
	var out bytes.Buffer
	if err := initCommand(nil, &out); err != nil {
		t.Fatalf("init отказал: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", home, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	want := []string{tracker.ProjectsLocalExampleFile, jira.ExampleFile}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("в хозяйстве %v, ожидались ровно %v", names, want)
	}
	for _, name := range want {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatalf("%s не прочитан: %v", name, err)
		}
		exp, err := fs.ReadFile(payload.Payload, name)
		if err != nil {
			t.Fatalf("%s не найден в поставке: %v", name, err)
		}
		if !bytes.Equal(got, exp) {
			t.Errorf("%s отличается от образца в поставке", name)
		}
	}
	printed := out.String()
	if strings.Count(printed, "создан") != 2 {
		t.Errorf("ожидались два «создан»:\n%s", printed)
	}
	for _, want := range []string{tracker.ProjectsLocalFile, jira.TrackerFile, "дальше"} {
		if !strings.Contains(printed, want) {
			t.Errorf("вывод не называет %q:\n%s", want, printed)
		}
	}
}

// Настроенная машина: рабочие файлы и правленый образец не тронуты, код 0.
func TestInitLeavesConfiguredHomeAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)
	files := map[string]string{
		tracker.ProjectsLocalFile:        "PROJ: {repo_url: x, default_branch: main, tracker: mock}\n",
		jira.TrackerFile:                 "base_url: http://jira\n",
		tracker.ProjectsLocalExampleFile: "# правленый образец\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", name, err)
		}
	}
	var out bytes.Buffer
	if err := initCommand(nil, &out); err != nil { // nil error — код возврата 0
		t.Fatalf("init отказал на настроенном хозяйстве: %v", err)
	}
	for name, body := range files {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatalf("%s не прочитан: %v", name, err)
		}
		if string(got) != body {
			t.Errorf("%s изменён: было %q, стало %q", name, body, got)
		}
	}
	printed := out.String()
	if !strings.Contains(printed, "оставлен") || !strings.Contains(printed, tracker.ProjectsLocalExampleFile) {
		t.Errorf("вывод не сообщает об оставленном образце:\n%s", printed)
	}
	// jira.ExampleFile (единственный отсутствовавший образец) теперь существует
	if _, err := os.Stat(filepath.Join(home, jira.ExampleFile)); err != nil {
		t.Errorf("%s не создан: %v", jira.ExampleFile, err)
	}
}
