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
		// write_scope/guards — снятый механизм: где роли писать и как соотноситься
		// с другими ролями, решает текст role.md, а не поле контракта. Роль, ещё
		// называющая эти ключи, не молча игнорируется — разбор строгий.
		"снятое поле guards": {
			yaml:     fixtureRoleYAML + "guards: [change_dir_only]\n",
			wantPart: "не разобран",
		},
		"снятое поле write_scope": {
			yaml:     fixtureRoleYAML + "write_scope:\n  dir: change_dir\n",
			wantPart: "не разобран",
		},
		"pre_tool_use без matcher": {
			yaml: strings.Replace(fixtureRoleYAML,
				"hooks:\n  stop:\n    - hooks/require-result.sh\n",
				"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
					"    - matcher: \"\"\n      command: skills/comet/scripts/comet-hook-router.mjs\n", 1),
			wantPart: "matcher пуст",
		},
		"pre_tool_use без command": {
			yaml: strings.Replace(fixtureRoleYAML,
				"hooks:\n  stop:\n    - hooks/require-result.sh\n",
				"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
					"    - matcher: \"Write|Edit\"\n      command: \"\"\n", 1),
			wantPart: "command пуст",
		},
		"pre_tool_use ссылается на неподключённый скилл": {
			yaml: strings.Replace(fixtureRoleYAML,
				"hooks:\n  stop:\n    - hooks/require-result.sh\n",
				"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
					"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs\n", 1),
			wantPart: "нет в skills",
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

// Скрипт хука существует, но подключённого скилла, к которому он относится,
// в roleYAML нет вовсе — SkillDirs() его даже не проверяет, потому что о нём
// не знает список skills:. Отдельный тест: табличный TestLoadRoleRejects выше
// не создаёт файлов сверх стандартной фикстуры, а этому нужен настоящий
// каталог скилла без самого файла скрипта внутри него.
func TestLoadRoleRejectsPreToolUseMissingScript(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, "skills: []", "skills: [comet]", 1)
	yaml = strings.Replace(yaml,
		"hooks:\n  stop:\n    - hooks/require-result.sh\n",
		"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
			"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs\n", 1)
	root := fixtureOffice(t, yaml)
	// Каталог скилла существует (иначе SkillDirs() отверг бы роль раньше и по
	// другой причине), а самого скрипта внутри — нет.
	if err := os.MkdirAll(filepath.Join(root, "skills", "comet"), 0o755); err != nil {
		t.Fatalf("каталог скилла не создан: %v", err)
	}

	_, err := LoadRole(root, "tester")
	if err == nil {
		t.Fatal("скрипт хука отсутствует, но роль принята")
	}
	if !strings.Contains(err.Error(), "файл хука не найден") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
}

// Хук-роутер Comet enforces фазовые границы записи технически, а не только
// текстом role.md (design doc "Phase-scoped writes are hook-enforced") — и
// корректно объявленный hooks.pre_tool_use обязан разбираться, а не только
// отвергаться.
func TestLoadRoleAcceptsPreToolUseHook(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, "skills: []", "skills: [comet]", 1)
	yaml = strings.Replace(yaml,
		"hooks:\n  stop:\n    - hooks/require-result.sh\n",
		"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
			"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root \"$WORKDIR\"\n", 1)
	root := fixtureOffice(t, yaml)
	scriptDir := filepath.Join(root, "skills", "comet", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("каталог скрипта не создан: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "comet-hook-router.mjs")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatalf("скрипт хука не записан: %v", err)
	}
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("скрипт хука не сделан исполняемым: %v", err)
	}

	role, err := LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("корректный pre_tool_use хук отвергнут: %v", err)
	}
	if len(role.Hooks.PreToolUse) != 1 {
		t.Fatalf("hooks.pre_tool_use не разобран: %+v", role.Hooks)
	}
	got := role.Hooks.PreToolUse[0]
	if got.Matcher != "Write|Edit" {
		t.Errorf("matcher=%q, ожидался Write|Edit", got.Matcher)
	}
	if !strings.HasPrefix(got.Command, "skills/comet/scripts/comet-hook-router.mjs") {
		t.Errorf("command=%q искажён при разборе", got.Command)
	}
}

// Неисполняемый скрипт pre_tool_use — тот же провал, что и у Stop-хука: код 126
// от неисполняемого файла Claude Code сочтёт неблокирующей ошибкой хука, и
// фазовое ограждение перестанет ограждать беззвучно (ровно баг финального
// ревью: comet-hook-router.mjs внутри самого пакета @rpamis/comet — тоже
// mode 644, и наш адаптер обязан отловить это на загрузке роли, а не в проде).
func TestLoadRoleRejectsNonExecutablePreToolUseHook(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, "skills: []", "skills: [comet]", 1)
	yaml = strings.Replace(yaml,
		"hooks:\n  stop:\n    - hooks/require-result.sh\n",
		"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
			"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root \"$WORKDIR\"\n", 1)
	root := fixtureOffice(t, yaml)
	scriptDir := filepath.Join(root, "skills", "comet", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("каталог скрипта не создан: %v", err)
	}
	// 0o644 нарочно: скрипт есть, не каталог, но не исполняемый.
	if err := os.WriteFile(filepath.Join(scriptDir, "comet-hook-router.mjs"), []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatalf("скрипт хука не записан: %v", err)
	}

	_, err := LoadRole(root, "tester")
	if err == nil {
		t.Fatal("скрипт хука неисполняемый, но роль принята")
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
		role, err := LoadRole(filepath.Join("..", ".."), name)
		if err != nil {
			t.Errorf("roles/%s не проходит проверку: %v", name, err)
			continue
		}
		if _, err := role.SystemPrompt(); err != nil {
			t.Errorf("промпт roles/%s не собирается: %v", name, err)
		}
	}
}

// Право на запись — единственное, что отличает reviewer'а от implementer'а,
// и держится оно тем, что в allow у ревьюера нет широкого, ничем не
// ограниченного Write или Edit — Write в --tools всё равно попадает (адаптер
// добавляет его любой роли всегда, internal/adapters/claude/adapter.go: WriteTool),
// но ограниченным ровно файлом результата (.agent/result.json), а не общим
// правом записи. Запрет Bash(git add/commit) тут ни при чём: tools.allow не
// технически ограничивает Bash (измерено 2026-08-27,
// docs/notes/followup-network-and-permissions.md), поэтому его отсутствие
// в allow ничего не доказывает. Реальная защита от add/commit/restore —
// в tools.deny, и её проверяет отдельный тест после задачи 7 плана
// (роль-специфичные deny остаются в role.yaml).
func TestReviewerRoleCannotWrite(t *testing.T) {
	role, err := LoadRole(filepath.Join("..", ".."), "reviewer")
	if err != nil {
		t.Fatalf("roles/reviewer не прочитана: %v", err)
	}

	for _, writing := range []string{"Edit", "Write", "NotebookEdit"} {
		if slices.Contains(role.Tools.Allow, writing) {
			t.Errorf("reviewer разрешает править: %q", writing)
		}
	}
	for _, want := range []string{"Read", "Bash(*)"} {
		if !slices.Contains(role.Tools.Allow, want) {
			t.Errorf("reviewer лишён %q — ему нечем читать и запускать проверки", want)
		}
	}

	// Роль-специфичный deny (add/commit/restore) остаётся в role.yaml
	// и после переноса общих семи строк в defaults.tools.deny — это то,
	// что защищает reviewer'а от правки, раз tools.allow не защищает
	// ничего технически.
	for _, want := range []string{"Bash(git *add*)", "Bash(git *commit*)", "Bash(git *restore*)"} {
		if !slices.Contains(role.Tools.Deny, want) {
			t.Errorf("reviewer лишён роль-специфичного deny %q", want)
		}
	}
}

// shippedRoles — имена ролей репозитория. Каталоги с подчёркиванием ролью
// не являются: в _base лежат общие куски промпта.
func shippedRoles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "..", RolesDir))
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
