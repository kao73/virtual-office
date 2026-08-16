package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureRoleYAML = `name: tester
prompt: role.md
includes:
  - ../_base/base.md
skills: []
tools:
  allow: ["Read", "Bash(git *)"]
  deny: []
hooks:
  stop:
    - hooks/require-result.sh
limits:
  max_turns: 5
  timeout_sec: 60
result_file: .agent/result.json
`

// fixtureOffice собирает минимальный конфиг-репозиторий с одной ролью.
func fixtureOffice(t *testing.T, roleYAML string) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("каталог %s не создан: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", rel, err)
		}
	}

	write(filepath.Join(RolesDir, "_base", "base.md"), "# Базовые правила\n\nБудь честен.\n")
	write(filepath.Join(RolesDir, "tester", "role.md"), "# Роль: tester\n\nДелай, что сказано.\n")
	write(filepath.Join(RolesDir, "tester", RoleFile), roleYAML)
	write(filepath.Join("hooks", "require-result.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "hooks", "require-result.sh"), 0o755); err != nil {
		t.Fatalf("хук не сделан исполняемым: %v", err)
	}

	return root
}

func TestLoadRole(t *testing.T) {
	role, err := LoadRole(fixtureOffice(t, fixtureRoleYAML), "tester")
	if err != nil {
		t.Fatalf("корректная роль не загружена: %v", err)
	}

	if role.Name != "tester" {
		t.Errorf("имя роли %q, ожидалось tester", role.Name)
	}
	if role.Limits.MaxTurns != 5 || role.Limits.TimeoutSec != 60 {
		t.Errorf("лимиты разобраны неверно: %+v", role.Limits)
	}
	if len(role.Tools.Allow) != 2 || role.Tools.Allow[1] != "Bash(git *)" {
		t.Errorf("правило инструмента искажено при разборе: %q", role.Tools.Allow)
	}
	if got := len(role.PromptFiles()); got != 2 {
		t.Errorf("файлов промпта %d, ожидалось 2 (include + собственный)", got)
	}
}

func TestLoadRoleRejects(t *testing.T) {
	cases := map[string]struct {
		yaml     string
		wantPart string
	}{
		"неизвестное поле": {
			yaml:     fixtureRoleYAML + "unexpected: 1\n",
			wantPart: "не разобран",
		},
		"имя не совпадает с каталогом": {
			yaml:     strings.Replace(fixtureRoleYAML, "name: tester", "name: other", 1),
			wantPart: "не совпадает с именем каталога",
		},
		"нет промпта": {
			yaml:     strings.Replace(fixtureRoleYAML, "prompt: role.md", "prompt: missing.md", 1),
			wantPart: "файл промпта не найден",
		},
		"нет хука": {
			yaml:     strings.Replace(fixtureRoleYAML, "hooks/require-result.sh", "hooks/missing.sh", 1),
			wantPart: "хук не найден",
		},
		"нет скилла": {
			yaml:     strings.Replace(fixtureRoleYAML, "skills: []", "skills: [nonexistent]", 1),
			wantPart: "скилл не найден",
		},
		"нулевой предел шагов": {
			yaml:     strings.Replace(fixtureRoleYAML, "max_turns: 5", "max_turns: 0", 1),
			wantPart: "max_turns",
		},
		"пустой allow": {
			yaml:     strings.Replace(fixtureRoleYAML, `allow: ["Read", "Bash(git *)"]`, "allow: []", 1),
			wantPart: "агенту нечем работать",
		},
		"результат вне workdir": {
			yaml:     strings.Replace(fixtureRoleYAML, "result_file: .agent/result.json", "result_file: ../result.json", 1),
			wantPart: "не выходить из workdir",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadRole(fixtureOffice(t, tc.yaml), "tester")
			if err == nil {
				t.Fatal("роль нарушает контракт, но принята")
			}
			if !strings.Contains(err.Error(), tc.wantPart) {
				t.Errorf("ошибка не объясняет нарушение\nполучено: %v\nожидалась подстрока: %q", err, tc.wantPart)
			}
		})
	}
}

func TestSystemPromptGluesIncludesThenRoleThenSpec(t *testing.T) {
	role, err := LoadRole(fixtureOffice(t, fixtureRoleYAML), "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}

	prompt, err := role.SystemPrompt()
	if err != nil {
		t.Fatalf("промпт не собран: %v", err)
	}

	base := strings.Index(prompt, "Будь честен")
	own := strings.Index(prompt, "Делай, что сказано")
	spec := strings.Index(prompt, "Файл результата")
	if base < 0 || own < 0 || spec < 0 {
		t.Fatalf("в промпте не хватает частей:\n%s", prompt)
	}
	if !(base < own && own < spec) {
		t.Errorf("порядок склейки нарушен: include=%d роль=%d спецификация=%d", base, own, spec)
	}
	// Спецификация исхода обязана назвать конкретный файл: агент не может
	// прочитать docs/contracts/agent-io.md из репозитория клиента.
	if !strings.Contains(prompt, role.ResultFile) {
		t.Error("в промпте нет пути к файлу результата")
	}
}

// Неисполняемый хук — худший вид поломки: Claude Code сочтёт код 126
// неблокирующей ошибкой, и ограждение перестанет ограждать беззвучно.
func TestLoadRoleRejectsNonExecutableHook(t *testing.T) {
	root := fixtureOffice(t, fixtureRoleYAML)
	if err := os.Chmod(filepath.Join(root, "hooks", "require-result.sh"), 0o644); err != nil {
		t.Fatalf("права не сняты: %v", err)
	}

	_, err := LoadRole(root, "tester")
	if err == nil {
		t.Fatal("хук неисполняемый, но роль принята")
	}
	if !strings.Contains(err.Error(), "не исполняемый") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
}

// Роль, которая лежит в репозитории, обязана проходить собственную проверку.
func TestShippedImplementerRoleIsValid(t *testing.T) {
	role, err := LoadRole("..", "implementer")
	if err != nil {
		t.Fatalf("roles/implementer не проходит проверку: %v", err)
	}
	if _, err := role.SystemPrompt(); err != nil {
		t.Errorf("промпт roles/implementer не собирается: %v", err)
	}
}
