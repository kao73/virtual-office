// Package claude переводит спецификацию роли в запуск Claude Code.
// Агент ничего не знает о role.yaml: всю трансляцию в нативные механизмы
// (системный промпт, settings.json, плагин со скиллами, флаги) делает адаптер.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// Executable — CLI агента.
const Executable = "claude"

// SkillTool — инструмент, которым агент вызывает подключённые скиллы.
// Его добавляет раннер, а не роль: скиллы подключает он же.
const SkillTool = "Skill"

// UserPrompt — стартовое сообщение. Всё остальное агент читает из каталога обмена.
const UserPrompt = "Начни с чтения `.agent/task.md` и `.agent/context.md`, затем выполни задачу и запиши файл результата."

// hostVars — переменные хоста, без которых не работают git и сам агент при запуске
// без изоляции. Остальное окружение до агента не доходит: запуск должен зависеть
// от роли, а не от того, что случилось в шелле оператора.
var hostVars = []string{"PATH", "HOME", "USER", "SHELL", "TMPDIR", "LANG", "LC_ALL", "TERM"}

// credentialVars — переменные авторизации в порядке приоритета. Порядок совпадает
// с поведением самого CLI: заданный API-ключ побеждает подписку. Передаётся только
// выбранная переменная — вторая в процесс не попадает.
var credentialVars = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// Build собирает запуск агента по роли, рабочей папке и паспорту прогона.
//
// Всё, что нужно агенту, материализуется в каталоге запуска: промпт, настройки,
// скрипты ограждений, скиллы. Конфиг-репозиторий агенту не отдаётся — ни целиком,
// ни частями.
func Build(role runner.Role, workdir string, run runner.Run) (*runner.Launch, error) {
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
	for _, dir := range []string{roleDir, configDir} {
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

	hooks, err := copyHooks(roleDir, role)
	if err != nil {
		return abort(err)
	}

	settings, err := buildSettings(role, workdir, hooks)
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
		// Запрещает всё, чего нет в permissions.allow: диалогов в headless всё равно никто не увидит.
		"--permission-mode", "dontAsk",
		"--max-turns", strconv.Itoa(role.Limits.MaxTurns),
	}

	if pluginDir != "" {
		argv = append(argv, "--plugin-dir", pluginDir)
	}

	hostEnv := make([]string, 0, len(hostVars))
	for _, name := range hostVars {
		if value, ok := os.LookupEnv(name); ok {
			hostEnv = append(hostEnv, name+"="+value)
		}
	}

	return &runner.Launch{
		ID:   run.RunID,
		Argv: argv,
		// Стартовое сообщение идёт через stdin, а не позиционным аргументом:
		// вариадические флаги вроде --tools забирают все следующие за ними слова
		// и проглотили бы промпт.
		Stdin:   UserPrompt,
		Workdir: workdir,
		Timeout: time.Duration(role.Limits.TimeoutSec) * time.Second,

		Env:     []string{"CLAUDE_CONFIG_DIR=" + configDir, credVar + "=" + credValue},
		HostEnv: hostEnv,

		Workspaces: []runner.Workspace{
			{Path: workdir},
			{Path: roleDir, ReadOnly: true},
			{Path: configDir},
		},

		SystemPrompt: systemPrompt,
		UserPrompt:   UserPrompt,
		Settings:     settings,
		Skills:       role.Skills,
		ConfigDir:    configDir,
		SecretVars:   []string{credVar},
		Cleanup:      cleanup,
	}, nil
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

// copyHooks переносит скрипты ограждений в каталог запуска. Иначе изолированному
// бэкенду пришлось бы отдавать агенту конфиг-репозиторий целиком ради пары скриптов.
func copyHooks(roleDir string, role runner.Role) ([]string, error) {
	sources := role.HookFiles()
	if len(sources) == 0 {
		return nil, nil
	}

	dir := filepath.Join(roleDir, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("каталог ограждений не создан: %w", err)
	}

	copied := make([]string, 0, len(sources))
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("ограждение не прочитано: %w", err)
		}
		dst := filepath.Join(dir, filepath.Base(src))
		// Бит исполняемости обязателен: без него код выхода 126, а он считается
		// неблокирующей ошибкой, и ограждение молча перестаёт ограждать.
		if err := os.WriteFile(dst, raw, 0o755); err != nil {
			return nil, fmt.Errorf("ограждение не скопировано: %w", err)
		}
		if err := os.Chmod(dst, 0o755); err != nil {
			return nil, fmt.Errorf("ограждение не сделано исполняемым: %w", err)
		}
		copied = append(copied, dst)
	}
	return copied, nil
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

// buildSettings переводит tools и hooks роли в settings.json.
// Путь к файлу результата подставляется в команду хука абсолютным: так ограждению
// не приходится гадать, откуда его запустили.
func buildSettings(role runner.Role, workdir string, hooks []string) (string, error) {
	s := settingsFile{
		Permissions: permissionsBlock{Allow: role.Tools.Allow, Deny: role.Tools.Deny},
	}

	if len(hooks) > 0 {
		resultPath := filepath.Join(workdir, role.ResultFile)
		commands := make([]hookCommand, 0, len(hooks))
		for _, script := range hooks {
			commands = append(commands, hookCommand{
				Type:    "command",
				Command: shellQuote(script) + " " + shellQuote(resultPath),
				Timeout: 60,
			})
		}
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
