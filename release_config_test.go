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
	// Внутри шага goreleaser-action может стоять сколько угодно строк
	// (distribution:, install-only:, комментарий) — ищем ближайший version:
	// после него, но не дальше начала следующего шага.
	step := read(t, filepath.Join(".github", "workflows", "release.yml"))
	if _, after, found := strings.Cut(step, "goreleaser-action@"); found {
		step, _, _ = strings.Cut(after, "\n      - ")
	} else {
		t.Fatal("в release.yml не найден шаг goreleaser-action")
	}
	workflow := regexp.MustCompile(`version: (v[\d.]+)`).FindStringSubmatch(step)
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

// Все сборки обязаны идти одним рецептом: разойдись ldflags, релиз уехал бы
// с runner v0.7.0 и run-agent <commit>, которые распаковали бы разные офисы и
// подписали прогоны разными личностями. Держат это якоря — значит, каждая
// сборка, кроме определяющей якорь, обязана ссылаться, а не задавать своё.
func TestAllBuildsShareOneRecipe(t *testing.T) {
	config := read(t, ".goreleaser.yaml")
	keys := map[string]string{"env": "build_env", "flags": "build_flags", "ldflags": "build_ldflags", "targets": "build_targets"}
	for _, anchor := range keys {
		// Пробел после якоря не требуем: в блочном YAML значение уходит на
		// следующую строку, и счёт по «&имя » дал бы ноль на законной правке.
		defined := regexp.MustCompile(`&`+anchor+`\b`).FindAllString(config, -1)
		if len(defined) != 1 {
			t.Errorf("якорь %s определён %d раз, ожидался один", anchor, len(defined))
		}
	}

	builds, _, found := strings.Cut(section(t, config, "builds:"), "\narchives:")
	if !found {
		builds = section(t, config, "builds:")
	}
	blocks := strings.Split(builds, "\n  - id: ")
	if len(blocks) < 3 { // [до первой сборки, runner, run-agent]
		t.Fatalf("в .goreleaser.yaml найдено %d сборок, ожидалось не меньше двух", len(blocks)-1)
	}
	// GoReleaser умеет overrides с собственными ldflags под цель — рецепт
	// раздваивается и так, без второго верхнего ключа.
	if strings.Contains(builds, "overrides:") {
		t.Error("в сборках есть overrides: рецепт может разойтись мимо якорей")
	}
	for _, block := range blocks[1:] {
		name, _, _ := strings.Cut(block, "\n")
		for key, anchor := range keys {
			line := regexp.MustCompile(`(?m)^\s*` + key + `: *(.*)$`).FindStringSubmatch(block)
			if line == nil {
				t.Errorf("в сборке %s нет %s:", name, key)
				continue
			}
			value := strings.TrimSpace(line[1])
			if strings.HasPrefix(value, "&"+anchor) || value == "*"+anchor {
				continue
			}
			t.Errorf("в сборке %s ключ %s: задан значением %q, а не якорем и не ссылкой на него", name, key, value)
		}
	}
}

// section — кусок конфигурации от ключа верхнего уровня до конца файла.
func section(t *testing.T, config, key string) string {
	t.Helper()
	_, rest, found := strings.Cut(config, "\n"+key)
	if !found {
		t.Fatalf("в .goreleaser.yaml не найден раздел %s", key)
	}
	return rest
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
		// Песочница sbx — Linux той же архитектуры: на darwin прогон с
		// бэкендом sbx просит ограждение linux/<arch>, и если его нет в
		// поставке, упрётся в отказ уже у пользователя. Правило проверяется
		// здесь, а не в validators_release_test.go: тот под darwin и на
		// Linux-раннере CI не исполняется никогда.
		if sandbox := "linux-" + parts[1]; parts[0] != "linux" && !slices.Contains(embedded, sandbox) {
			t.Errorf("%s встраивает %v, но не ограждение своей песочницы %s", name, embedded, sandbox)
		}
	}
}

// Каталог маркера передаётся хуку литералом dist (.goreleaser.yaml), и там же
// его ищут release.yml и release-snapshot.sh. Ключ dist: в конфигурации увёл
// бы сборку в другой каталог: хук упал бы на записи маркера, и диагноз вышел
// бы про права, а не про рассинхрон.
func TestMarkerDirMatchesDist(t *testing.T) {
	config := read(t, ".goreleaser.yaml")
	if regexp.MustCompile(`(?m)^dist:`).MatchString(config) {
		t.Error(".goreleaser.yaml задаёт свой dist:, а маркер личности пишется в dist/")
	}
	if !strings.Contains(config, "{{ .IsSnapshot }} dist") {
		t.Error("хук check-release-identity.sh больше не получает dist аргументом")
	}
	for _, path := range []string{filepath.Join(".github", "workflows", "release.yml"), filepath.Join("scripts", "release-snapshot.sh")} {
		if !strings.Contains(read(t, path), "dist/identity-checked-") {
			t.Errorf("%s не проверяет маркер dist/identity-checked-*", path)
		}
	}
}
