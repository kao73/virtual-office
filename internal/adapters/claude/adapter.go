// Package claude переводит спецификацию роли в запуск Claude Code.
// Агент ничего не знает о role.yaml: всю трансляцию в нативные механизмы
// (системный промпт, settings.json, плагин со скиллами, флаги) делает адаптер.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// Executable — CLI агента.
const Executable = "claude"

// SkillTool — инструмент, которым агент вызывает подключённые скиллы.
// Его добавляет раннер, а не роль: скиллы подключает он же.
const SkillTool = "Skill"

// WriteTool — инструмент записи файлов. Роли он достаётся всегда, но ровно на один
// путь — файл результата: закончить прогон без него нельзя, а роль без права писать
// (reviewer) не смогла бы закончить его вовсе. Проверено прогоном: агент честно
// перебрал Bash и Write, получил отказ на обоих и завершился без результата.
const WriteTool = runner.WriteTool

// FileRule — имя, которым в правиле разрешения задаётся путь для записи. Оно одно
// на всё семейство файловых инструментов и покрывает Write; правило `Write(<путь>)`
// не совпадает ни с чем. Измерено, см. docs/notes/claude-cli.md.
const FileRule = "Edit"

// UserPrompt — стартовое сообщение. Всё остальное агент читает из каталога обмена.
const UserPrompt = "Начни с чтения `.agent/task.md` и `.agent/context.md`, затем выполни задачу и запиши файл результата."

// EmailDomain — домен почты агентов. Не резолвится: письма туда никто не шлёт,
// адрес нужен git'у как обязательное поле.
const EmailDomain = "office.local"

// AgentAPIHost — куда ходит сам Claude Code. Знание это адаптерское: роль про
// устройство агента не знает ничего и называть его домены не должна.
//
// Открывать его приходится нам, и это измерено, а не предположено. Встроенный
// кит песочницы даёт агенту `claude.com`, `downloads.claude.ai` и
// `mcp-proxy.anthropic.com` — но не API. При закрытой сети агент из-за этого
// не проходит авторизацию вовсе: `403 Blocked by network policy: domain
// api.anthropic.com:443`, работа не начинается. С одним этим доменом — работает.
// См. docs/notes/sbx.md.
const AgentAPIHost = "api.anthropic.com"

// hostVars — переменные хоста, без которых не работают git и сам агент при запуске
// без изоляции. Остальное окружение до агента не доходит: запуск должен зависеть
// от роли, а не от того, что случилось в шелле оператора.
//
// HOME в списке нет намеренно: он не воспроизводится с хоста, а подменяется, см. ownHome.
var hostVars = []string{"PATH", "USER", "SHELL", "TMPDIR", "LANG", "LC_ALL", "TERM"}

// sshWithoutIdentities — как git зовёт ssh при запуске без изоляции.
//
// Подменённого HOME здесь мало: домашний каталог ssh берёт из getpwuid, а не из HOME,
// и находит ключи владельца машины даже с чужим HOME — проверено вживую, аутентификация
// на GitHub проходила. Поэтому личности отбираются явно: ни файл ключа, ни агент.
const sshWithoutIdentities = "ssh -o IdentitiesOnly=yes -o IdentityAgent=none -o IdentityFile=/dev/null"

// credentialVars — переменные авторизации в порядке приоритета. Порядок совпадает
// с поведением самого CLI: заданный API-ключ побеждает подписку. Передаётся только
// выбранная переменная — вторая в процесс не попадает.
var credentialVars = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// Build собирает запуск агента по роли, рабочей папке и паспорту прогона.
//
// Всё, что нужно агенту, материализуется в каталоге запуска: промпт, настройки,
// скрипты ограждений, бинарник проверки результата, скиллы. Конфиг-репозиторий
// агенту не отдаётся — ни целиком, ни частями.
//
// validator — путь к собранному бинарнику проверки результата (`runner.EnsureValidator`).
// Собирает его раннер, а не адаптер: платформу знает бэкенд, а Go-тулчейн адаптеру
// не нужен ни для чего другого.
func Build(role runner.Role, workdir string, run runner.Run, validator string) (*runner.Launch, error) {
	credVar, credValue, err := credential()
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "office-run-")
	if err != nil {
		return nil, fmt.Errorf("временный каталог запуска не создан: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }
	abort := func(err error) (*runner.Launch, error) {
		_ = cleanup()
		return nil, err
	}

	// roleDir агенту отдаётся только на чтение, configDir — на запись:
	// в него агент пишет собственную конфигурацию.
	roleDir := filepath.Join(tmp, "role")
	configDir := filepath.Join(tmp, "config")
	homeDir := filepath.Join(tmp, "home")
	for _, dir := range []string{roleDir, configDir, homeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return abort(fmt.Errorf("каталог запуска не создан: %w", err))
		}
	}

	systemPrompt, err := role.SystemPrompt()
	if err != nil {
		return abort(err)
	}
	promptPath := filepath.Join(roleDir, "system-prompt.md")
	if err := os.WriteFile(promptPath, []byte(systemPrompt), 0o644); err != nil {
		return abort(fmt.Errorf("системный промпт не записан: %w", err))
	}

	hooks, checker, err := copyHooks(roleDir, role, validator)
	if err != nil {
		return abort(err)
	}

	settings, err := buildSettings(role, workdir, run, hooks, checker)
	if err != nil {
		return abort(err)
	}
	settingsPath := filepath.Join(roleDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		return abort(fmt.Errorf("settings.json не записан: %w", err))
	}

	// Набор инструментов складывается из двух источников: что разрешила роль
	// и что задействовал сам раннер. Второе в tools.allow не пишут — роль
	// перечисляет там работу с файлами и командами, а не механизм подгрузки
	// скиллов, — но без него подключённые скиллы агенту нечем вызвать.
	tools := toolNames(role.Tools.Allow)
	if !slices.Contains(tools, WriteTool) {
		tools = append(tools, WriteTool)
	}
	// Область записи роли превращается в правило на путь, а состав --tools
	// собирается из имён правил `allow` — но голого `Edit` роль там держать
	// не вправе, он разрешил бы правку всего. Поэтому инструмент добавляет
	// адаптер, ровно тогда, когда сам синтезировал правило: иначе у роли
	// с областью записи не оказалось бы инструмента вовсе.
	if writeScopeRule(role, workdir, run) != "" && !slices.Contains(tools, FileRule) {
		tools = append(tools, FileRule)
	}

	pluginDir := ""
	if len(role.Skills) > 0 {
		if pluginDir, err = buildPlugin(roleDir, role); err != nil {
			return abort(err)
		}
		if !slices.Contains(tools, SkillTool) {
			tools = append(tools, SkillTool)
		}
	}

	argv := []string{
		Executable,
		"--print",
		// Построчный поток событий вместо одного блоба в конце: по логу видно,
		// что агент делает прямо сейчас. Итоговое событие result несёт стоимость,
		// длительность и число шагов.
		"--output-format", "stream-json", "--verbose",
		"--session-id", run.RunID,
		"--append-system-prompt-file", promptPath,
		"--settings", settingsPath,
		// Пустой список источников — значит не читать ни пользовательский слой,
		// ни слои проекта-клиента. Пишется одним токеном через равенство:
		// sbx отвергает команду с пустым элементом («cmd element is empty»).
		"--setting-sources=",
		// Сужает сам набор инструментов. Без этого агенту доступны все встроенные,
		// включая сетевые и порождающие процессы: permissions.allow управляет
		// автоодобрением, а не составом.
		"--tools", strings.Join(tools, ","),
		// headless-режим: диалогов подтверждения всё равно никто не увидит.
		// Не путать с ограничением состава команд — permissions.allow не
		// запрещает ничего технически (агент решает сам, что не описано
		// ни в allow, ни в deny); реальная граница — только
		// permissions.deny. Измерено 2026-08-27,
		// docs/notes/followup-network-and-permissions.md,
		// docs/contracts/role-sandbox-permissions.md.
		"--permission-mode", "dontAsk",
		"--max-turns", strconv.Itoa(role.Limits.MaxTurns),
	}

	if pluginDir != "" {
		argv = append(argv, "--plugin-dir", pluginDir)
	}

	hostEnv := make([]string, 0, len(hostVars)+2)
	for _, name := range hostVars {
		if value, ok := os.LookupEnv(name); ok {
			hostEnv = append(hostEnv, name+"="+value)
		}
	}
	hostEnv = append(hostEnv, "HOME="+homeDir, "GIT_SSH_COMMAND="+sshWithoutIdentities)

	return &runner.Launch{
		ID:   run.RunID,
		Argv: argv,
		// Стартовое сообщение идёт через stdin, а не позиционным аргументом:
		// вариадические флаги вроде --tools забирают все следующие за ними слова
		// и проглотили бы промпт.
		Stdin:   UserPrompt,
		Workdir: workdir,
		Timeout: time.Duration(role.Limits.TimeoutSec) * time.Second,

		Env: append(append([]string{
			"CLAUDE_CONFIG_DIR=" + configDir,
			credVar + "=" + credValue,
		}, identityVars(role)...), officeVars(role, workdir, run)...),
		HostEnv: hostEnv,

		Workspaces: []runner.Workspace{
			{Path: workdir},
			{Path: roleDir, ReadOnly: true},
			{Path: configDir},
		},

		// Сеть роли идёт мимо агента: в его командную строку и настройки ей
		// попадать незачем — уговорить изнутри то, что закрыто снаружи, нельзя,
		// и не должно быть похоже, что можно.
		//
		// К списку роли добавляется API самого агента: роль про Claude Code
		// не знает ничего, а без этого домена работа не начинается вовсе.
		NetworkAllow: networkAllow(role),

		SystemPrompt: systemPrompt,
		UserPrompt:   UserPrompt,
		Settings:     settings,
		Skills:       role.Skills,
		ConfigDir:    configDir,
		SecretVars:   []string{credVar},
		Cleanup:      cleanup,
	}, nil
}

// networkAllow — куда открывается сеть прогона: то, что нужно самому агенту,
// плюс то, что назвала роль. Повторы убираются: список едет в правило политики
// как есть, и дубликат в нём — мусор.
func networkAllow(role runner.Role) []string {
	hosts := append([]string{AgentAPIHost}, role.Network.Allow...)
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// officeVars — параметры прогона для ограждений. Хук получает их из окружения,
// а не аргументами: аргументы задаёт адаптер, и второй адаптер, написанный под
// другого агента, повторил бы их по-своему. Имена `OFFICE_*` — агент-нейтральная
// часть контракта прогона, см. docs/contracts/agent-io.md.
//
// Что в них класть, решает раннер: наблюдает прогон он, и то же самое значение
// нужно ему самому, когда он проверяет ограждения после прогона. Дело адаптера —
// перенести это в окружение запуска.
//
// Пустые значения не передаются вовсе: «переменной нет» и «значение пустое» —
// одно и то же, и в шелле это одна форма `${VAR:-}`.
func officeVars(role runner.Role, workdir string, run runner.Run) []string {
	vars := runner.RunEnv(role, workdir, run)

	env := make([]string, 0, len(vars))
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if vars[name] != "" {
			env = append(env, name+"="+vars[name])
		}
	}
	return env
}

// writeScopeRule — правило разрешений для области записи роли. Пусто означает,
// что роль области не объявляла: границу ей задаёт состав инструментов.
//
// Роль называет намерение («каталог изменения»), а во что оно превратится, знает
// адаптер: путь собирается из ключа задачи, форма `//<путь>` — абсолютная,
// маска `**` покрывает файлы внутри.
func writeScopeRule(role runner.Role, workdir string, run runner.Run) string {
	if role.WriteScope.Dir != runner.ScopeChangeDir {
		return ""
	}
	return FileRule + "(/" + runner.ChangeDir(workdir, run.TaskKey) + "/**)"
}

// identityVars задают личность коммитов. Переменные окружения выбраны потому, что
// перебивают любой git-конфиг и не требуют от агента права звать `git config`.
//
// Коммиттер задаётся наравне с автором: git берёт их из разных переменных, и без
// второй пары автором будет роль, а коммиттером — владелец машины.
func identityVars(role runner.Role) []string {
	name := "agent-" + role.Name
	email := role.Name + "@" + EmailDomain
	return []string{
		"GIT_AUTHOR_NAME=" + name,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name,
		"GIT_COMMITTER_EMAIL=" + email,
	}
}

// credential выбирает способ авторизации по правилам docs/notes/auth.md.
func credential() (string, string, error) {
	for _, name := range credentialVars {
		if value := os.Getenv(name); value != "" {
			return name, value, nil
		}
	}
	return "", "", fmt.Errorf(
		"не задан ни %s: агенту нечем авторизоваться, см. docs/notes/auth.md",
		strings.Join(credentialVars, ", ни "))
}

// copyHooks переносит скрипты ограждений и бинарник проверки результата в каталог
// запуска. Иначе изолированному бэкенду пришлось бы отдавать агенту конфиг-репозиторий
// целиком ради пары файлов.
//
// Валидатор кладётся рядом со скриптами под фиксированным именем: скрипт ищет его
// у себя под боком, потому что своего пути внутри изоляции он не знает.
func copyHooks(roleDir string, role runner.Role, validator string) ([]string, string, error) {
	sources := role.HookFiles()
	if len(sources) == 0 && len(role.Guards) == 0 {
		return nil, "", nil
	}
	if validator == "" {
		return nil, "", errors.New("роль ставит ограждение, но бинарник проверки результата не собран: ограждению нечем проверять")
	}

	dir := filepath.Join(roleDir, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("каталог ограждений не создан: %w", err)
	}

	copied := make([]string, 0, len(sources))
	for _, src := range sources {
		dst := filepath.Join(dir, filepath.Base(src))
		if err := copyExecutable(src, dst); err != nil {
			return nil, "", fmt.Errorf("ограждение не скопировано: %w", err)
		}
		copied = append(copied, dst)
	}

	checker := filepath.Join(dir, runner.ValidatorName)
	if err := copyExecutable(validator, checker); err != nil {
		return nil, "", fmt.Errorf("проверка результата не скопирована: %w", err)
	}
	return copied, checker, nil
}

// copyExecutable копирует файл, сохраняя бит исполняемости. Бит обязателен:
// без него запуск даёт код 126, а он считается неблокирующей ошибкой — ограждение
// молча перестаёт ограждать.
func copyExecutable(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, raw, 0o755); err != nil {
		return err
	}
	// WriteFile не меняет права уже существующего файла — выставляем явно.
	return os.Chmod(dst, 0o755)
}

type settingsFile struct {
	Permissions permissionsBlock         `json:"permissions"`
	Hooks       map[string][]hookMatcher `json:"hooks,omitempty"`
}

type permissionsBlock struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

type hookMatcher struct {
	Hooks []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// buildSettings переводит tools, hooks и ограждения роли в settings.json.
// Путь к файлу результата подставляется в команду хука абсолютным: так ограждению
// не приходится гадать, откуда его запустили.
func buildSettings(role runner.Role, workdir string, run runner.Run, hooks []string, checker string) (string, error) {
	resultPath := filepath.Join(workdir, role.ResultFile)

	// Право записать файл результата добавляется к разрешениям роли всегда и одним
	// путём: писать отчёт обязаны все, править код — не все. Роль, которой запись
	// разрешена целиком, от этого правила ничего не теряет.
	//
	// Путь абсолютный, в форме `//<путь>`: относительный отсчитывается от каталога
	// самого settings.json, а он лежит в хозяйстве запуска. Форма с одной косой
	// не совпадает ни с чем — измерено, см. docs/notes/claude-cli.md.
	allow := append(slices.Clone(role.Tools.Allow), FileRule+"(/"+resultPath+")")
	if rule := writeScopeRule(role, workdir, run); rule != "" {
		allow = append(allow, rule)
	}
	s := settingsFile{
		Permissions: permissionsBlock{Allow: allow, Deny: role.Tools.Deny},
	}

	commands := make([]hookCommand, 0, len(hooks)+len(role.Guards))
	for _, script := range hooks {
		commands = append(commands, hookCommand{
			Type:    "command",
			Command: shellQuote(script) + " " + shellQuote(resultPath),
			Timeout: 60,
		})
	}
	// Ограждения роли идут после скриптов, и порядок этот по делу: первым стоит
	// require-result.sh, и о невалидном результате агент узнаёт раньше, чем
	// о претензиях к содержанию работы. Аргумента-пути у них нет — обстановку
	// прогона они читают из окружения.
	for _, name := range role.Guards {
		commands = append(commands, hookCommand{
			Type:    "command",
			Command: shellQuote(checker) + " guard " + shellQuote(name),
			Timeout: 60,
		})
	}
	if len(commands) > 0 {
		s.Hooks = map[string][]hookMatcher{"Stop": {{Hooks: commands}}}
	}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("settings.json не сериализован: %w", err)
	}
	return string(raw) + "\n", nil
}

// buildPlugin собирает плагин из скиллов роли: агенту видны только они,
// а не всё содержимое skills/ в конфиг-репозитории.
func buildPlugin(roleDir string, role runner.Role) (string, error) {
	dir := filepath.Join(roleDir, "plugin")
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		return "", fmt.Errorf("каталог плагина не создан: %w", err)
	}

	manifest := fmt.Sprintf("{\n  \"name\": \"office-role-%s\"\n}\n", role.Name)
	manifestPath := filepath.Join(dir, ".claude-plugin", "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		return "", fmt.Errorf("манифест плагина не записан: %w", err)
	}

	sources := role.SkillDirs()
	if len(sources) != len(role.Skills) {
		return "", errors.New("список скиллов и список их каталогов разошлись")
	}
	for i, src := range sources {
		dst := filepath.Join(dir, "skills", role.Skills[i])
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("каталог скиллов не создан: %w", err)
		}
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			return "", fmt.Errorf("скилл %s не скопирован: %w", role.Skills[i], err)
		}
	}
	return dir, nil
}

// toolNames выделяет имена инструментов из правил разрешений: Bash(git *) → Bash.
// Порядок сохраняется, повторы убираются.
func toolNames(rules []string) []string {
	seen := make(map[string]bool, len(rules))
	names := make([]string, 0, len(rules))
	for _, rule := range rules {
		name, _, _ := strings.Cut(rule, "(")
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// shellQuote заворачивает путь в одинарные кавычки: команда хука выполняется шеллом.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
