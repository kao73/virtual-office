package claude

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
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

// Ограждение — единственная страховка от завершения без результата, и оно
// никак не проявляет себя на счастливом пути. Проверяем его целиком: скрипт
// из репозитория плюс собранную адаптером команду, вместе с экранированием.
func TestStopHookGuardsResultFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, roleYAML)
	shipped, err := os.ReadFile(filepath.Join("..", "..", "hooks", "require-result.sh"))
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

	// Пробел в пути: если адаптер потеряет кавычки, команда развалится на два аргумента.
	workdir := filepath.Join(t.TempDir(), "проект с пробелом")
	if err := os.MkdirAll(filepath.Join(workdir, runner.Dir), 0o755); err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	launch, err := Build(role, workdir, runner.Run{RunID: "id", Role: "tester"})
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

			out, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
			code := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("ограждение не запустилось: %v", err)
			}

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
	shipped, err := os.ReadFile(filepath.Join("..", "..", "hooks", "require-result.sh"))
	if err != nil {
		t.Fatalf("скрипт ограждения не прочитан: %v", err)
	}
	script := filepath.Join(t.TempDir(), "require-result.sh")
	if err := os.WriteFile(script, shipped, 0o755); err != nil {
		t.Fatalf("скрипт не подложен: %v", err)
	}

	out, _ := exec.Command("/bin/sh", script, filepath.Join(t.TempDir(), "нет.json")).CombinedOutput()
	for _, want := range []string{"failed", "blocked"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("в сообщении ограждения не назван исход %q: %s", want, out)
		}
	}
}
