package claude

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

// stopHookCommand достаёт из сгенерированных настроек команду ограждения —
// ровно ту строку, которую агент отдаст шеллу.
func stopHookCommand(t *testing.T, settings string) string {
	t.Helper()
	var s settingsFile
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		t.Fatalf("настройки не разобраны: %v", err)
	}
	matchers, found := s.Hooks["Stop"]
	if !found || len(matchers) == 0 || len(matchers[0].Hooks) == 0 {
		t.Fatalf("ограждения Stop нет в настройках:\n%s", settings)
	}
	return matchers[0].Hooks[0].Command
}

// realValidator собирает настоящий бинарник ограждения из этого репозитория.
// Заглушкой тут не обойтись: проверяется как раз то, что ограждение отвергает
// ровно то же, что раннер.
func realValidator(t *testing.T) string {
	t.Helper()
	t.Setenv(runner.HomeEnv, t.TempDir())
	path, err := runner.EnsureValidator(runner.Office{Root: filepath.Join("..", "..", ".."), Source: runner.SourceClone}, runner.HostPlatform())
	if err != nil {
		t.Fatalf("валидатор не собран: %v", err)
	}
	return path
}

// shipGuard подкладывает в фикстуру настоящий скрипт ограждения из репозитория.
func shipGuard(t *testing.T, root string) {
	t.Helper()
	shipped, err := os.ReadFile(filepath.Join("..", "..", "..", "hooks", "require-result.sh"))
	if err != nil {
		t.Fatalf("скрипт ограждения не прочитан: %v", err)
	}
	guard := filepath.Join(root, "hooks", "require-result.sh")
	if err := os.WriteFile(guard, shipped, 0o755); err != nil {
		t.Fatalf("скрипт ограждения не подложен в фикстуру: %v", err)
	}
	// WriteFile не меняет права уже существующего файла, а фикстура его создала.
	if err := os.Chmod(guard, 0o755); err != nil {
		t.Fatalf("скрипт ограждения не сделан исполняемым: %v", err)
	}
}

// exitCode запускает команду шеллом и возвращает её код выхода и вывод.
func exitCode(t *testing.T, argv ...string) (int, string) {
	t.Helper()
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), string(out)
	case err != nil:
		t.Fatalf("ограждение не запустилось: %v", err)
	}
	return 0, string(out)
}

// Ограждение — единственная страховка от завершения без результата, и оно
// никак не проявляет себя на счастливом пути. Проверяем его целиком: скрипт
// из репозитория, собранный валидатор и команду адаптера вместе с экранированием.
func TestStopHookGuardsResultFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	validator := realValidator(t)
	root := fixtureOffice(t, roleYAML)
	shipGuard(t, root)

	// Пробел в пути: если адаптер потеряет кавычки, команда развалится на два аргумента.
	workdir := filepath.Join(t.TempDir(), "проект с пробелом")
	if err := os.MkdirAll(filepath.Join(workdir, runner.Dir), 0o755); err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	launch, err := Build(role, workdir, runner.Run{RunID: "id", Role: "tester"}, validator)
	if err != nil {
		t.Fatalf("запуск не собран: %v", err)
	}
	defer launch.Cleanup()

	command := stopHookCommand(t, launch.Settings)
	resultPath := filepath.Join(workdir, role.ResultFile)

	cases := []struct {
		name     string
		content  string // пустая строка означает «файла нет»
		wantCode int
	}{
		{"файла нет", "", 2},
		{"файл пуст", " ", 2},
		{"нет поля outcome", `{"summary":"сделал","next_owner":"none"}`, 2},
		// Ровно эта форма прошла ограждение на этапе 1 и была отвергнута раннером:
		// работа была сделана, а прогон засчитан как failed.
		{"нет next_owner", `{"outcome":"done","summary":"сделал"}`, 2},
		{"неизвестное поле", `{"outcome":"done","summary":"с.","next_owner":"none","лишнее":1}`, 2},
		{"валидный результат", `{"outcome":"done","summary":"с.","next_owner":"none"}`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(resultPath)
			if tc.content != "" {
				if err := os.WriteFile(resultPath, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("файл результата не записан: %v", err)
				}
			}

			code, out := exitCode(t, "/bin/sh", "-c", command)
			if code != tc.wantCode {
				t.Errorf("код %d, ожидался %d; вывод: %s", code, tc.wantCode, out)
			}
			// Код 2 блокирует завершение, и текст из stderr уходит агенту:
			// молчаливая блокировка оставила бы его без подсказки, что делать.
			if tc.wantCode == 2 && len(out) == 0 {
				t.Error("ограждение блокирует молча: агенту не за что зацепиться")
			}
		})
	}
}

// Упереться в ограждение и не знать выхода — прямой путь к циклу до предела шагов.
func TestStopHookNamesTheWayOut(t *testing.T) {
	validator := realValidator(t)
	dir := t.TempDir()

	shipped, err := os.ReadFile(filepath.Join("..", "..", "..", "hooks", "require-result.sh"))
	if err != nil {
		t.Fatalf("скрипт ограждения не прочитан: %v", err)
	}
	script := filepath.Join(dir, "require-result.sh")
	if err := os.WriteFile(script, shipped, 0o755); err != nil {
		t.Fatalf("скрипт не подложен: %v", err)
	}
	raw, err := os.ReadFile(validator)
	if err != nil {
		t.Fatalf("валидатор не прочитан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, runner.ValidatorName), raw, 0o755); err != nil {
		t.Fatalf("валидатор не положен рядом со скриптом: %v", err)
	}

	_, out := exitCode(t, "/bin/sh", script, filepath.Join(t.TempDir(), "нет.json"))
	for _, want := range []string{"failed", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("в сообщении ограждения не назван исход %q: %s", want, out)
		}
	}
}

// Пропавший бинарник обязан блокировать выход, а не пропускать его. Этап 1 уже
// показал, чем кончается ограждение, которое ошибается тихо: код 126 от
// неисполняемого скрипта считался неблокирующей ошибкой, и агент уходил без результата.
func TestStopHookBlocksWhenValidatorMissing(t *testing.T) {
	dir := t.TempDir()
	shipped, err := os.ReadFile(filepath.Join("..", "..", "..", "hooks", "require-result.sh"))
	if err != nil {
		t.Fatalf("скрипт ограждения не прочитан: %v", err)
	}
	script := filepath.Join(dir, "require-result.sh")
	if err := os.WriteFile(script, shipped, 0o755); err != nil {
		t.Fatalf("скрипт не подложен: %v", err)
	}

	result := filepath.Join(dir, "result.json")
	if err := os.WriteFile(result, []byte(`{"outcome":"done","summary":"с.","next_owner":"none"}`), 0o644); err != nil {
		t.Fatalf("файл результата не записан: %v", err)
	}

	code, out := exitCode(t, "/bin/sh", script, result)
	if code != 2 {
		t.Errorf("без валидатора код %d, ожидался 2: ограждение пропустило выход; вывод: %s", code, out)
	}
	if !strings.Contains(out, runner.ValidatorName) {
		t.Errorf("не названо, чего не хватает: %s", out)
	}
}

// Бинарник должен оказаться рядом со скриптом внутри изоляции: конфиг-репозиторий
// агенту не отдают, и взять валидатор ему больше неоткуда.
func TestBuildPlacesValidatorNextToHooks(t *testing.T) {
	launch, _, _ := fixtureLaunch(t, roleYAML)

	var roleDir string
	for _, ws := range launch.Workspaces {
		if ws.ReadOnly {
			roleDir = ws.Path
		}
	}
	if roleDir == "" {
		t.Fatal("каталог роли не отдан агенту")
	}

	path := filepath.Join(roleDir, "hooks", runner.ValidatorName)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("валидатор не положен рядом с ограждениями: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Error("валидатор не исполняемый: ограждение получит код 126 и промолчит")
	}
}

// Роль с ограждением и без валидатора — это ограждение, которое не ограждает.
// Пусть запуск не соберётся, чем соберётся вхолостую.
func TestBuildRefusesStopHookWithoutValidator(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, roleYAML)
	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}

	if _, err := Build(role, t.TempDir(), runner.Run{RunID: "id", Role: "tester"}, ""); err == nil {
		t.Error("запуск с ограждением собрался без валидатора")
	}
}
