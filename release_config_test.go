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
	// Ищем version: внутри блока goreleaser-action, а не первый в файле:
	// setup-go и прочие шаги пишут своё version: и молча подменили бы предмет.
	workflow := regexp.MustCompile(`goreleaser-action@[^\n]*\n(?:[^\n]*\n)??\s*version: (v[\d.]+)`).FindStringSubmatch(read(t, filepath.Join(".github", "workflows", "release.yml")))
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

// Оба бинарника обязаны собираться одним рецептом: разойдись ldflags, релиз
// уехал бы с runner v0.7.0 и run-agent <commit>, которые распаковали бы
// разные офисы и подписали прогоны разными личностями. Держат это якоря —
// значит, у run-agent не должно быть собственных значений.
func TestBothBuildsShareOneRecipe(t *testing.T) {
	config := read(t, ".goreleaser.yaml")
	for _, anchor := range []string{"build_env", "build_flags", "build_ldflags", "build_targets"} {
		if n := strings.Count(config, "&"+anchor+" "); n != 1 {
			t.Errorf("якорь %s определён %d раз, ожидался один", anchor, n)
		}
		if n := strings.Count(config, "*"+anchor); n != 1 {
			t.Errorf("на якорь %s ссылаются %d раз, ожидался один (вторая сборка)", anchor, n)
		}
	}
	// В блоке run-agent — только ссылки: литеральные flags/ldflags/env там
	// означают, что рецепт снова раздвоился.
	_, agent, found := strings.Cut(config, "- id: run-agent")
	if !found {
		t.Fatal("в .goreleaser.yaml не найдена сборка run-agent")
	}
	if end := strings.Index(agent, "\narchives:"); end >= 0 {
		agent = agent[:end]
	}
	for _, key := range []string{"env:", "flags:", "ldflags:", "targets:"} {
		line := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + ` *(.*)$`).FindStringSubmatch(agent)
		if line == nil {
			t.Errorf("в сборке run-agent нет %s", key)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line[1]), "*") {
			t.Errorf("в сборке run-agent %s задан значением %q, а не ссылкой на якорь", key, line[1])
		}
	}
}

// Файл validators_<os>_<arch>.go обязан встраивать ограждение своей платформы:
// перепутанный путь компилируется, validators_release_test.go проверяет ELF
// и наличие, но не архитектуру, а в песочнице чужой чекер даёт код 126 —
// который хук считает неблокирующим, и ограждение молча перестаёт ограждать.
func TestEmbeddedValidatorsMatchTheirFile(t *testing.T) {
	names, err := filepath.Glob("validators_*_*.go")
	if err != nil {
		t.Fatal(err)
	}
	embed := regexp.MustCompile(`payload/validators/validate-result-([a-z0-9]+)-([a-z0-9]+)`)
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "validators_"), ".go"), "_")
		own := parts[0] + "-" + parts[1]
		found := embed.FindAllStringSubmatch(read(t, name), -1)
		if len(found) == 0 {
			t.Errorf("%s не встраивает ни одного ограждения", name)
			continue
		}
		var embedded []string
		for _, m := range found {
			embedded = append(embedded, m[1]+"-"+m[2])
		}
		if !slices.Contains(embedded, own) {
			t.Errorf("%s встраивает %v, но не ограждение своей платформы %s", name, embedded, own)
		}
	}
}
