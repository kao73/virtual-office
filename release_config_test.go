package office

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// read — файл релизной обвязки из корня модуля.
func read(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", name, err)
	}
	return string(raw)
}

// Список платформ релиза живёт в четырёх местах, и три из них расходятся
// громко: нет файла validators_<os>_<arch>.go — релизная сборка не
// компилируется, нет цели у build-validators.sh — не находится файл для
// embed. Четвёртое, `supported` в install.sh, расходится молча и в худшую
// сторону: релиз соберётся и уедет, а установщик откажется его ставить.
func TestReleasePlatformsAgree(t *testing.T) {
	// Опорный список — имена файлов с директивами embed: без них сборки нет.
	names, err := filepath.Glob("validators_*_*.go")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") { // validators_release_test.go — не платформа
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "validators_"), ".go"), "_")
		if len(parts) != 2 {
			t.Fatalf("имя %s не вида validators_<os>_<arch>.go", name)
		}
		want = append(want, parts[0]+"/"+parts[1])
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("не найдено ни одного validators_<os>_<arch>.go")
	}

	sorted := func(s []string) []string { s = slices.Clone(s); slices.Sort(s); return s }

	// scripts/build-validators.sh: `for target in darwin/arm64 linux/amd64 …`
	targets := regexp.MustCompile(`for target in ([^;]+); do`).FindStringSubmatch(read(t, filepath.Join("scripts", "build-validators.sh")))
	if targets == nil {
		t.Fatal("в build-validators.sh не найден список целей")
	}
	if got := sorted(strings.Fields(targets[1])); !slices.Equal(got, want) {
		t.Errorf("build-validators.sh собирает %v, а встраиваются %v", got, want)
	}

	// .goreleaser.yaml: targets: [darwin_arm64, linux_amd64, …]
	gr := regexp.MustCompile(`targets: &build_targets \[([^\]]+)\]`).FindStringSubmatch(read(t, ".goreleaser.yaml"))
	if gr == nil {
		t.Fatal("в .goreleaser.yaml не найден якорь build_targets")
	}
	var goreleaser []string
	for _, target := range strings.Split(gr[1], ",") {
		goreleaser = append(goreleaser, strings.ReplaceAll(strings.TrimSpace(target), "_", "/"))
	}
	if got := sorted(goreleaser); !slices.Equal(got, want) {
		t.Errorf("GoReleaser собирает %v, а встраиваются %v", got, want)
	}

	// install.sh: supported="darwin/arm64 linux/amd64 …"
	sup := regexp.MustCompile(`supported="([^"]+)"`).FindStringSubmatch(read(t, "install.sh"))
	if sup == nil {
		t.Fatal("в install.sh не найден список supported")
	}
	if got := sorted(strings.Fields(sup[1])); !slices.Equal(got, want) {
		t.Errorf("install.sh ставит на %v, а собирается %v: установщик откажет на платформе, которая есть в релизе", got, want)
	}
}

// Версия GoReleaser закреплена дважды — в workflow и в скрипте снапшота, —
// чтобы два разработчика и CI собирали одинаковый dist/. Расхождение молчит:
// каждый соберёт своей версией.
func TestGoReleaserPinsAgree(t *testing.T) {
	workflow := regexp.MustCompile(`version: (v[\d.]+)`).FindStringSubmatch(read(t, filepath.Join(".github", "workflows", "release.yml")))
	snapshot := regexp.MustCompile(`goreleaser/v2@(v[\d.]+)`).FindStringSubmatch(read(t, filepath.Join("scripts", "release-snapshot.sh")))
	switch {
	case workflow == nil:
		t.Fatal("в release.yml не найден пин GoReleaser")
	case snapshot == nil:
		t.Fatal("в release-snapshot.sh не найден пин GoReleaser")
	case workflow[1] != snapshot[1]:
		t.Errorf("release.yml берёт GoReleaser %s, release-snapshot.sh — %s", workflow[1], snapshot[1])
	}
}
