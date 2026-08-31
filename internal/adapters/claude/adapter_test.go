package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

const roleYAML = `name: tester
prompt: role.md
includes:
  - ../_base/base.md
skills: []
tools:
  allow: ["Read", "Write", "Bash(git *)"]
  deny: ["Bash(rm *)"]
hooks:
  stop:
    - hooks/require-result.sh
limits:
  max_turns: 5
  timeout_sec: 60
result_file: .agent/result.json
`

// Роль, которой не дали инструментов записи: так устроен reviewer.
const readOnlyRoleYAML = `name: tester
prompt: role.md
includes:
  - ../_base/base.md
skills: []
tools:
  allow: ["Read", "Bash(git diff*)"]
  deny: ["Edit"]
hooks:
  stop:
    - hooks/require-result.sh
limits:
  max_turns: 5
  timeout_sec: 60
result_file: .agent/result.json
`

func fixtureOffice(t *testing.T, yaml string, skills ...string) string {
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

	write(filepath.Join("roles", "_base", "base.md"), "# Базовые правила\n\nБудь честен.\n")
	write(filepath.Join("roles", "tester", "role.md"), "# Роль: tester\n\nДелай, что сказано.\n")
	write(filepath.Join("roles", "tester", "role.yaml"), yaml)
	write(filepath.Join("hooks", "require-result.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "hooks", "require-result.sh"), 0o755); err != nil {
		t.Fatalf("хук не сделан исполняемым: %v", err)
	}
	for _, skill := range skills {
		write(filepath.Join("skills", skill, "SKILL.md"), "# "+skill+"\n")
	}

	return root
}

// stubValidator — заглушка бинарника ограждения для тестов, которым важно только
// его размещение. Настоящий собирается в hook_test.go.
func stubValidator(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), runner.ValidatorName)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("заглушка валидатора не записана: %v", err)
	}
	return path
}

func fixtureLaunch(t *testing.T, yaml string, skills ...string) (*runner.Launch, runner.Role, string) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "тестовый-ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, yaml, skills...)
	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}

	workdir := t.TempDir()
	launch, err := Build(role, workdir, runner.Run{RunID: "550e8400-e29b-41d4-a716-446655440000", Role: "tester"}, stubValidator(t))
	if err != nil {
		t.Fatalf("запуск не собран: %v", err)
	}
	t.Cleanup(func() { _ = launch.Cleanup() })

	return launch, role, workdir
}

// argValue возвращает значение флага в командной строке.
func argValue(t *testing.T, argv []string, flag string) string {
	t.Helper()
	i := slices.Index(argv, flag)
	if i < 0 {
		t.Fatalf("в командной строке нет %s: %q", flag, argv)
	}
	if i+1 >= len(argv) {
		t.Fatalf("у %s нет значения: %q", flag, argv)
	}
	return argv[i+1]
}

// Файл результата — часть контракта прогона, а не работа роли: завершиться без него
// нельзя. Значит право записать его даёт обвязка, как и подгрузку скиллов, — иначе
// роль без инструментов записи не может закончиться ничем, кроме синтетического failed.
func TestBuildLetsAnyRoleWriteItsResult(t *testing.T) {
	launch, role, workdir := fixtureLaunch(t, readOnlyRoleYAML)

	if tools := strings.Split(argValue(t, launch.Argv, "--tools"), ","); !slices.Contains(tools, WriteTool) {
		t.Errorf("роли нечем записать результат: %q", tools)
	}
	// Имя в правиле — Edit, а не Write: путь задаётся именем семейства файловых
	// инструментов. Путь абсолютный и в форме `//`. Обе особенности измерены
	// прогонами, см. docs/notes/claude-cli.md.
	rule := FileRule + "(/" + filepath.Join(workdir, role.ResultFile) + ")"
	if !strings.Contains(launch.Settings, rule) {
		t.Errorf("в разрешениях нет %s:\n%s", rule, launch.Settings)
	}
	// Разрешение точечное: право записать отчёт не должно оказаться правом править код.
	if strings.Contains(launch.Settings, `"`+WriteTool+`"`) {
		t.Errorf("роль без Write получила запись куда угодно:\n%s", launch.Settings)
	}
}

// Сеть роли — не дело агента: ни флагом, ни промптом её не задать. Адаптер
// перекладывает список в запуск, а применяет его бэкенд, у которого сеть
// вообще управляема.
func TestBuildCarriesNetworkToBackend(t *testing.T) {
	yaml := roleYAML + "network:\n  allow: [pypi.org, \"*.pythonhosted.org\"]\n"
	launch, _, _ := fixtureLaunch(t, yaml)

	for _, want := range []string{"pypi.org", "*.pythonhosted.org"} {
		if !slices.Contains(launch.NetworkAllow, want) {
			t.Errorf("домен роли %q не доехал до запуска: %v", want, launch.NetworkAllow)
		}
	}
	// В командную строку агента им попадать не за чем: сеть закрывает песочница,
	// а не сам агент, и уговорить её изнутри он не должен.
	if line := strings.Join(launch.Argv, " "); strings.Contains(line, "pypi.org") {
		t.Errorf("домены роли попали в командную строку агента: %s", line)
	}
}

// Куда ходит сам агент — знание адаптера, а не роли: роль про Claude Code
// не знает ничего. Измерено вживую: при закрытой сети без этого домена агент
// не проходит авторизацию и падает, ещё не начав работу.
func TestBuildOpensAgentOwnAPI(t *testing.T) {
	launch, _, _ := fixtureLaunch(t, roleYAML)

	if !slices.Contains(launch.NetworkAllow, AgentAPIHost) {
		t.Errorf("агенту закрыт его собственный API: %v", launch.NetworkAllow)
	}
}

// Роль, назвавшая тот же домен, не должна порождать его дважды: список едет
// в правило политики как есть, и повтор в нём — мусор.
func TestBuildDoesNotRepeatHosts(t *testing.T) {
	yaml := roleYAML + "network:\n  allow: [" + AgentAPIHost + ", pypi.org]\n"
	launch, _, _ := fixtureLaunch(t, yaml)

	seen := map[string]int{}
	for _, host := range launch.NetworkAllow {
		seen[host]++
	}
	if seen[AgentAPIHost] != 1 {
		t.Errorf("домен %s назван %d раза: %v", AgentAPIHost, seen[AgentAPIHost], launch.NetworkAllow)
	}
}

func TestBuildCommandLine(t *testing.T) {
	launch, role, _ := fixtureLaunch(t, roleYAML)

	if launch.Argv[0] != Executable {
		t.Errorf("запускается %q вместо %q", launch.Argv[0], Executable)
	}
	if !slices.Contains(launch.Argv, "--print") {
		t.Error("нет --print: без него агент уйдёт в интерактивный режим и повиснет")
	}
	if got := argValue(t, launch.Argv, "--session-id"); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("сессия не помечена run_id: %q", got)
	}
	if got := argValue(t, launch.Argv, "--max-turns"); got != "5" {
		t.Errorf("предел шагов %q, в роли 5", got)
	}
	if got := argValue(t, launch.Argv, "--permission-mode"); got != "dontAsk" {
		t.Errorf("режим разрешений %q: в headless диалог показать некому", got)
	}
	if !slices.Contains(launch.Argv, "--setting-sources=") {
		t.Errorf("источники настроек не отсечены, подтянутся чужие слои: %q", launch.Argv)
	}
	// Пустой элемент командной строки изолированный бэкенд не примет: sbx отвечает
	// «cmd element is empty». Флаги с пустым значением пишутся через равенство.
	for i, arg := range launch.Argv {
		if arg == "" {
			t.Errorf("аргумент %d пуст: такую команду песочница отвергнет", i)
		}
	}
	// Роль разрешает три инструмента; без --tools агенту достались бы все встроенные,
	// включая сетевые и порождающие процессы.
	if got := argValue(t, launch.Argv, "--tools"); got != "Read,Write,Bash" {
		t.Errorf("набор инструментов %q, роль разрешает Read, Write, Bash(git *)", got)
	}
	// Стартовое сообщение обязано идти через stdin: вариадические флаги
	// (--tools, --allowedTools, --add-dir) съедают позиционный аргумент,
	// и агент запускается без промпта.
	if launch.Stdin != UserPrompt {
		t.Errorf("на stdin подаётся %q вместо стартового сообщения", launch.Stdin)
	}
	if slices.Contains(launch.Argv, UserPrompt) {
		t.Errorf("стартовое сообщение осталось в командной строке: %q", launch.Argv)
	}
	if launch.Timeout.Seconds() != float64(role.Limits.TimeoutSec) {
		t.Errorf("таймаут %s, в роли %d с", launch.Timeout, role.Limits.TimeoutSec)
	}

	promptFile := argValue(t, launch.Argv, "--append-system-prompt-file")
	written, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("файл системного промпта не прочитан: %v", err)
	}
	if string(written) != launch.SystemPrompt {
		t.Error("на диске лежит не тот промпт, который показывает --dry-run")
	}
	if !strings.Contains(launch.SystemPrompt, "Делай, что сказано") {
		t.Error("в системном промпте нет промпта роли")
	}
}

func TestToolNames(t *testing.T) {
	cases := map[string]struct {
		rules []string
		want  string
	}{
		"правила одного инструмента схлопываются": {
			rules: []string{"Read", "Bash(git *)", "Bash(uv *)", "Bash(pytest *)"},
			want:  "Read,Bash",
		},
		"порядок сохраняется": {
			rules: []string{"Write", "Read", "Edit"},
			want:  "Write,Read,Edit",
		},
		"пробелы вокруг имени не мешают": {
			rules: []string{" Read ", "Bash (git *)"},
			want:  "Read,Bash",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(toolNames(tc.rules), ","); got != tc.want {
				t.Errorf("получено %q, ожидалось %q", got, tc.want)
			}
		})
	}
}

func TestBuildIsolatesHostConfig(t *testing.T) {
	t.Setenv("OFFICE_HOST_ONLY", "не должно доехать")
	launch, _, _ := fixtureLaunch(t, roleYAML)

	all := strings.Join(append(slices.Clone(launch.Env), launch.HostEnv...), "\n")
	if strings.Contains(all, "OFFICE_HOST_ONLY") {
		t.Error("посторонняя переменная хоста доехала до агента")
	}
	// CLAUDE_CONFIG_DIR задаёт сам запуск, значит он нужен в любом окружении,
	// изолированном или нет, и лежать обязан в Env, а не в HostEnv.
	if !slices.Contains(launch.Env, "CLAUDE_CONFIG_DIR="+launch.ConfigDir) {
		t.Errorf("конфиг-каталог не подменён: %q", launch.Env)
	}
	if fi, err := os.Stat(launch.ConfigDir); err != nil || !fi.IsDir() {
		t.Errorf("изолированный конфиг-каталог не создан: %v", err)
	}
	entries, err := os.ReadDir(launch.ConfigDir)
	if err != nil || len(entries) != 0 {
		t.Errorf("конфиг-каталог не пуст: %v, %d записей", err, len(entries))
	}
}

// Без явной личности агент коммитит от имени владельца машины: на local она берётся
// из ~/.gitconfig, в песочнице sbx наследует её с хоста. Для конвейера ролей это
// неверно — в истории проекта должно быть видно, какая роль что сделала.
func TestBuildSetsCommitIdentity(t *testing.T) {
	launch, role, _ := fixtureLaunch(t, roleYAML)

	want := []string{
		"GIT_AUTHOR_NAME=agent-" + role.Name,
		"GIT_AUTHOR_EMAIL=" + role.Name + "@" + EmailDomain,
		// Коммиттер задаётся наравне с автором: иначе автором будет роль,
		// а коммиттером — владелец машины, и `git log --format=%cn` покажет человека.
		"GIT_COMMITTER_NAME=agent-" + role.Name,
		"GIT_COMMITTER_EMAIL=" + role.Name + "@" + EmailDomain,
	}
	for _, kv := range want {
		// Личность нужна в любом окружении, изолированном или нет, значит её место
		// в Env, а не в HostEnv.
		if !slices.Contains(launch.Env, kv) {
			t.Errorf("нет %s: %q", kv, launch.Env)
		}
	}
}

// На бэкенде без изоляции агент бежит в настоящей файловой системе владельца машины.
// Хостовый HOME отдавал ему всё, что ищут по «~»: креды gh, docker, aws, npm.
// Запуску выдаётся собственный пустой HOME — эти пути просто перестают существовать.
//
// Границей это не является: ssh домашний каталог берёт из getpwuid, а не из HOME,
// и абсолютный путь к чужому файлу никуда не делся. Настоящая граница — песочница.
func TestBuildGivesRunItsOwnHome(t *testing.T) {
	hostHome, hasHostHome := os.LookupEnv("HOME")
	launch, _, _ := fixtureLaunch(t, roleYAML)

	home := ""
	for _, kv := range launch.HostEnv {
		if name, value, _ := strings.Cut(kv, "="); name == "HOME" {
			home = value
		}
	}
	if home == "" {
		t.Fatalf("HOME запуску не задан вовсе: %q", launch.HostEnv)
	}
	if hasHostHome && home == hostHome {
		t.Errorf("агенту достался хостовый HOME %s: креды владельца машины у него под рукой", home)
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("домашний каталог запуска не создан: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("домашний каталог запуска не пуст: %d записей", len(entries))
	}

	// Домашний каталог живёт во временном хозяйстве запуска и уезжает вместе с ним:
	// иначе от каждого прогона остаётся мусор.
	if !strings.HasPrefix(home, filepath.Dir(launch.ConfigDir)) {
		t.Errorf("домашний каталог %s вне временного хозяйства запуска", home)
	}
}

// Подменённый HOME ssh не останавливает: домашний каталог он берёт из getpwuid
// и находит ключи владельца машины. Проверено вживую — аутентификация на GitHub
// проходила при синтетическом HOME. Забираем у git сами личности.
func TestBuildDeniesGitSSHIdentities(t *testing.T) {
	launch, _, _ := fixtureLaunch(t, roleYAML)

	var command string
	for _, kv := range launch.HostEnv {
		if name, value, _ := strings.Cut(kv, "="); name == "GIT_SSH_COMMAND" {
			command = value
		}
	}
	if command == "" {
		t.Fatalf("GIT_SSH_COMMAND не задан: ssh найдёт ключи владельца машины: %q", launch.HostEnv)
	}
	for _, opt := range []string{"IdentitiesOnly=yes", "IdentityAgent=none", "IdentityFile=/dev/null"} {
		if !strings.Contains(command, opt) {
			t.Errorf("в GIT_SSH_COMMAND нет %s: %q", opt, command)
		}
	}
	// В песочнице кредов нет вовсе, а хостовые пути внутри указывают в пустоту:
	// обе переменные осмыслены только там, где изоляции нет.
	if slices.ContainsFunc(launch.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "GIT_SSH_COMMAND=") || strings.HasPrefix(kv, "HOME=")
	}) {
		t.Errorf("ограничение неизолированного запуска уехало в Env: %q", launch.Env)
	}
}

// Изолированному бэкенду надо знать, что отдать агенту и на каких правах.
func TestBuildWorkspaces(t *testing.T) {
	launch, _, workdir := fixtureLaunch(t, roleYAML)

	byPath := make(map[string]runner.Workspace, len(launch.Workspaces))
	for _, ws := range launch.Workspaces {
		byPath[ws.Path] = ws
	}

	work, found := byPath[workdir]
	if !found {
		t.Fatalf("рабочая папка не отдана агенту: %+v", launch.Workspaces)
	}
	if work.ReadOnly {
		t.Error("рабочая папка отдана только на чтение: агенту некуда писать результат")
	}

	cfg, found := byPath[launch.ConfigDir]
	if !found || cfg.ReadOnly {
		t.Errorf("конфиг-каталог должен быть отдан на запись: %+v", launch.Workspaces)
	}

	// Материалы роли агент менять не должен: промпт, разрешения и ограждения
	// не подлежат правке тем, кого они ограничивают.
	roleDir := filepath.Dir(argValue(t, launch.Argv, "--settings"))
	ws, found := byPath[roleDir]
	if !found {
		t.Fatalf("материалы роли не отданы агенту: %+v", launch.Workspaces)
	}
	if !ws.ReadOnly {
		t.Error("материалы роли отданы на запись: агент может переписать собственные ограничения")
	}
}

// Ограждения переносятся в каталог запуска: иначе изолированному бэкенду
// пришлось бы отдавать агенту конфиг-репозиторий ради пары скриптов.
func TestBuildCopiesHooksOutOfConfigRepo(t *testing.T) {
	launch, _, _ := fixtureLaunch(t, roleYAML)

	command := stopHookCommand(t, launch.Settings)
	roleDir := filepath.Dir(argValue(t, launch.Argv, "--settings"))
	if !strings.Contains(command, roleDir) {
		t.Errorf("ограждение запускается не из каталога запуска: %s", command)
	}

	copied := filepath.Join(roleDir, "hooks", "require-result.sh")
	info, err := os.Stat(copied)
	if err != nil {
		t.Fatalf("ограждение не скопировано: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("копия ограждения не исполняемая: код 126 считается неблокирующей ошибкой")
	}
}

func TestBuildPassesOnlyChosenCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "токен")

	root := fixtureOffice(t, roleYAML)
	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	launch, err := Build(role, t.TempDir(), runner.Run{RunID: "id", Role: "tester"}, stubValidator(t))
	if err != nil {
		t.Fatalf("запуск не собран: %v", err)
	}
	defer launch.Cleanup()

	if !slices.Contains(launch.Env, "ANTHROPIC_API_KEY=ключ") {
		t.Error("API-ключ не передан, хотя задан: приоритет должен совпадать с поведением CLI")
	}
	all := strings.Join(append(slices.Clone(launch.Env), launch.HostEnv...), "\n")
	if strings.Contains(all, "токен") {
		t.Error("невыбранный кред доехал до агента: в процессе должен быть только один")
	}
	if !slices.Contains(launch.SecretVars, "ANTHROPIC_API_KEY") {
		t.Errorf("секрет не помечен для маскирования: %q", launch.SecretVars)
	}
}

func TestBuildWithoutCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, roleYAML)
	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}

	// Падать надо до запуска: иначе об отсутствии авторизации узнаём из середины прогона.
	if _, err := Build(role, t.TempDir(), runner.Run{RunID: "id", Role: "tester"}, stubValidator(t)); err == nil {
		t.Fatal("кредов нет, но запуск собран")
	} else if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("ошибка не называет оба пути авторизации: %v", err)
	}
}

func TestBuildSettings(t *testing.T) {
	launch, _, workdir := fixtureLaunch(t, roleYAML)

	for _, want := range []string{`"Read"`, `"Bash(git *)"`, `"Bash(rm *)"`, `"Stop"`} {
		if !strings.Contains(launch.Settings, want) {
			t.Errorf("в settings.json нет %s:\n%s", want, launch.Settings)
		}
	}
	// Хуку передаётся абсолютный путь: иначе ограждение ищет результат не там.
	if want := filepath.Join(workdir, ".agent", "result.json"); !strings.Contains(launch.Settings, want) {
		t.Errorf("в команде хука нет абсолютного пути к результату %s:\n%s", want, launch.Settings)
	}

	settingsFile := argValue(t, launch.Argv, "--settings")
	written, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json не прочитан: %v", err)
	}
	if string(written) != launch.Settings {
		t.Error("на диске лежат не те настройки, которые показывает --dry-run")
	}
}

func TestBuildCopiesOnlyRoleSkills(t *testing.T) {
	withSkill := strings.Replace(roleYAML, "skills: []", "skills: [нужный]", 1)
	launch, _, _ := fixtureLaunch(t, withSkill, "нужный", "лишний")

	pluginDir := argValue(t, launch.Argv, "--plugin-dir")
	if _, err := os.Stat(filepath.Join(pluginDir, ".claude-plugin", "plugin.json")); err != nil {
		t.Errorf("манифест плагина не собран: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "skills", "нужный", "SKILL.md")); err != nil {
		t.Errorf("скилл роли не скопирован: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "skills", "лишний")); err == nil {
		t.Error("скопирован скилл, которого нет в роли: агент видит лишнее")
	}
	if !slices.Equal(launch.Skills, []string{"нужный"}) {
		t.Errorf("список видимых скиллов %q", launch.Skills)
	}
}

func TestBuildWithoutSkillsSkipsPlugin(t *testing.T) {
	launch, _, _ := fixtureLaunch(t, roleYAML)

	if slices.Contains(launch.Argv, "--plugin-dir") {
		t.Errorf("роль не подключает скиллов, но плагин передан: %q", launch.Argv)
	}
	if tools := argValue(t, launch.Argv, "--tools"); strings.Contains(tools, SkillTool) {
		t.Errorf("роль не подключает скиллов, но %s в наборе инструментов: %q", SkillTool, tools)
	}
}

// Плагин со скиллами подключает раннер — значит и инструмент для их вызова
// добавляет он. Иначе скиллы загружены, но вызвать их нечем, и механизм
// молча не работает.
func TestBuildAddsSkillToolWhenRoleUsesSkills(t *testing.T) {
	withSkill := strings.Replace(roleYAML, "skills: []", "skills: [нужный]", 1)
	launch, _, _ := fixtureLaunch(t, withSkill, "нужный")

	tools := strings.Split(argValue(t, launch.Argv, "--tools"), ",")
	if !slices.Contains(tools, SkillTool) {
		t.Errorf("роль подключает скиллы, но вызвать их нечем: %q", tools)
	}
	if !slices.Contains(launch.Argv, "--plugin-dir") {
		t.Errorf("скиллы объявлены, но плагин не передан: %q", launch.Argv)
	}
}

const preToolUseRoleYAML = `name: tester
prompt: role.md
includes:
  - ../_base/base.md
skills: [comet]
tools:
  allow: ["Read", "Write", "Bash(git *)"]
  deny: []
hooks:
  stop:
    - hooks/require-result.sh
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"
limits:
  max_turns: 5
  timeout_sec: 60
result_file: .agent/result.json
`

// Хук-роутер Comet enforces фазовые границы записи технически (design doc
// "Phase-scoped writes are hook-enforced"), и адаптер обязан передать его
// агенту с уже разрешённым абсолютным путём — внутри собранного плагина
// голая команда "skills/comet/..." не значит ничего для шелла.
func TestBuildWiresPreToolUseHook(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, preToolUseRoleYAML, "comet")
	scriptDir := filepath.Join(root, "skills", "comet", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("каталог скрипта не создан: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "comet-hook-router.mjs")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatalf("скрипт хука не записан: %v", err)
	}
	// LoadRole теперь проверяет исполняемый бит pre_tool_use хука (final-review Fix 1).
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("скрипт хука не сделан исполняемым: %v", err)
	}

	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	workdir := t.TempDir()
	launch, err := Build(role, workdir, runner.Run{RunID: "id", Role: "tester"}, stubValidator(t))
	if err != nil {
		t.Fatalf("запуск не собран: %v", err)
	}
	t.Cleanup(func() { _ = launch.Cleanup() })

	var s settingsFile
	if err := json.Unmarshal([]byte(launch.Settings), &s); err != nil {
		t.Fatalf("настройки не разобраны: %v", err)
	}
	matchers, found := s.Hooks["PreToolUse"]
	if !found || len(matchers) != 1 || len(matchers[0].Hooks) != 1 {
		t.Fatalf("хука PreToolUse нет в настройках:\n%s", launch.Settings)
	}
	if matchers[0].Matcher != "Write|Edit" {
		t.Errorf("matcher=%q, ожидался Write|Edit", matchers[0].Matcher)
	}

	command := matchers[0].Hooks[0].Command
	pluginDir := argValue(t, launch.Argv, "--plugin-dir")
	wantScript := filepath.Join(pluginDir, "skills", "comet", "scripts", "comet-hook-router.mjs")
	if !strings.Contains(command, wantScript) {
		t.Errorf("команда хука не указывает на собранный плагин:\nхотели %s\nполучили %s", wantScript, command)
	}
	if !strings.Contains(command, workdir) {
		t.Errorf("$WORKDIR не подставлен: %s", command)
	}
	if strings.Contains(command, "$WORKDIR") {
		t.Errorf("литеральный $WORKDIR остался в команде: %s", command)
	}
	// Stop-хук остаётся на месте — новый тип не вытесняет старый.
	if _, found := s.Hooks["Stop"]; !found {
		t.Error("PreToolUse вытеснил Stop из настроек")
	}
}

// Роль вправе перечислить Skill и сама — дублировать его не нужно.
func TestBuildDoesNotDuplicateSkillTool(t *testing.T) {
	withSkill := strings.Replace(roleYAML, "skills: []", "skills: [нужный]", 1)
	withSkill = strings.Replace(withSkill, `allow: ["Read", "Write", "Bash(git *)"]`,
		`allow: ["Read", "Write", "Bash(git *)", "Skill"]`, 1)
	launch, _, _ := fixtureLaunch(t, withSkill, "нужный")

	tools := strings.Split(argValue(t, launch.Argv, "--tools"), ",")
	if n := strings.Count(strings.Join(tools, ","), SkillTool); n != 1 {
		t.Errorf("%s встречается %d раз: %q", SkillTool, n, tools)
	}
}

func TestCleanupRemovesTemporaries(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, roleYAML)
	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	launch, err := Build(role, t.TempDir(), runner.Run{RunID: "id", Role: "tester"}, stubValidator(t))
	if err != nil {
		t.Fatalf("запуск не собран: %v", err)
	}

	if err := launch.Cleanup(); err != nil {
		t.Fatalf("временное хозяйство не убрано: %v", err)
	}
	if _, err := os.Stat(launch.ConfigDir); err == nil {
		t.Error("конфиг-каталог пережил уборку: секреты и настройки роли остаются на диске")
	}
}

// Пуш делает раннер, а не агент, и держится это не только на запрете `git push`
// в роли: токена у агента нет вовсе. Белый список переменных хоста общий, но
// именно эта переменная — граница замысла, и она заслуживает собственной проверки:
// припиши её к hostVars по недосмотру, и роль получит право пушить молча.
func TestBuildKeepsPushTokenFromAgent(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_секрет-для-пуша")
	launch, _, _ := fixtureLaunch(t, roleYAML)

	all := strings.Join(append(slices.Clone(launch.Env), launch.HostEnv...), "\n")
	if strings.Contains(all, "GITHUB_TOKEN") || strings.Contains(all, "ghp_секрет-для-пуша") {
		t.Error("токен пуша доехал до агента: роль получила право публиковать работу")
	}
}
