package runner

import (
	"os"
	"path/filepath"
	"slices"
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
		// Домен со схемой или путём не совпадёт ни с чем, и роль молча останется
		// без сети — узнать об этом можно будет только по провалу прогона.
		"домен со схемой": {
			yaml:     fixtureRoleYAML + "network:\n  allow: [\"https://pypi.org\"]\n",
			wantPart: "network.allow",
		},
		"домен с путём": {
			yaml:     fixtureRoleYAML + "network:\n  allow: [\"pypi.org/simple\"]\n",
			wantPart: "network.allow",
		},
		"пустой домен": {
			yaml:     fixtureRoleYAML + "network:\n  allow: [\"\"]\n",
			wantPart: "network.allow",
		},
		"домен с пробелом": {
			yaml:     fixtureRoleYAML + "network:\n  allow: [\"pypi.org files.pythonhosted.org\"]\n",
			wantPart: "network.allow",
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

// Сеть роли необязательна, и пусто означает не «что угодно», а «ничего сверх
// того, что нужно самому агенту». Умолчание должно быть закрытым.
func TestRoleWithoutNetworkAsksForNothing(t *testing.T) {
	role, err := LoadRole(fixtureOffice(t, fixtureRoleYAML), "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	if len(role.Network.Allow) != 0 {
		t.Errorf("роль без раздела network просит домены: %v", role.Network.Allow)
	}
}

// Домены роли доезжают как есть: их разбирает не раннер, а песочница —
// подстановки и wildcard'ы её дело.
func TestRoleKeepsNetworkAllowAsWritten(t *testing.T) {
	yaml := fixtureRoleYAML + "network:\n  allow: [pypi.org, \"*.pythonhosted.org\", \"registry.npmjs.org:443\"]\n"
	role, err := LoadRole(fixtureOffice(t, yaml), "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	want := []string{"pypi.org", "*.pythonhosted.org", "registry.npmjs.org:443"}
	if !slices.Equal(role.Network.Allow, want) {
		t.Errorf("домены роли %v, ожидались %v", role.Network.Allow, want)
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

// Запрет Write целиком отнимает у роли право записать собственный результат:
// запреты сильнее разрешений, и точечное разрешение обвязки погибнет вместе с ним.
// Прогон такой роли не может закончиться ничем, кроме синтетического failed.
func TestLoadRoleRejectsBlanketWriteDenial(t *testing.T) {
	root := fixtureOffice(t, strings.Replace(fixtureRoleYAML, `deny: []`, `deny: ["Write"]`, 1))

	_, err := LoadRole(root, "tester")
	if err == nil {
		t.Fatal("роль запрещает Write целиком, но принята")
	}
	if !strings.Contains(err.Error(), "результат") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
}

// Каждая роль, которая лежит в репозитории, обязана проходить собственную
// проверку. Перечислять их в тесте поимённо нельзя: забытая новая роль сломается
// не здесь, а на живом прогоне.
func TestShippedRolesAreValid(t *testing.T) {
	for _, name := range shippedRoles(t) {
		role, err := LoadRole("..", name)
		if err != nil {
			t.Errorf("roles/%s не проходит проверку: %v", name, err)
			continue
		}
		if _, err := role.SystemPrompt(); err != nil {
			t.Errorf("промпт roles/%s не собирается: %v", name, err)
		}
	}
}

// Право на запись — единственное, что отличает reviewer'а от implementer'а.
// Потерять это различие правкой role.yaml легко, и заметить её было бы нечем:
// роль с Write просто начала бы чинить чужую работу вместо разбора.
func TestReviewerRoleCannotWrite(t *testing.T) {
	role, err := LoadRole("..", "reviewer")
	if err != nil {
		t.Fatalf("roles/reviewer не прочитана: %v", err)
	}

	for _, rule := range role.Tools.Allow {
		for _, writing := range []string{"Edit", "Write", "NotebookEdit", "Bash(git add", "Bash(git commit"} {
			if strings.HasPrefix(rule, writing) {
				t.Errorf("reviewer разрешает править: %q", rule)
			}
		}
	}
	for _, want := range []string{"Read", "Bash(git diff*)"} {
		if !slices.Contains(role.Tools.Allow, want) {
			t.Errorf("reviewer лишён %q — ему нечем читать работу", want)
		}
	}
}

// shippedRoles — имена ролей репозитория. Каталоги с подчёркиванием ролью
// не являются: в _base лежат общие куски промпта.
func shippedRoles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", RolesDir))
	if err != nil {
		t.Fatalf("каталог ролей не прочитан: %v", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("в репозитории не нашлось ни одной роли")
	}
	return names
}
