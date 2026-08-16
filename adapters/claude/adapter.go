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
	"strconv"
	"strings"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// Executable — CLI агента.
const Executable = "claude"

// UserPrompt — стартовое сообщение. Всё остальное агент читает из каталога обмена.
const UserPrompt = "Начни с чтения `.agent/task.md` и `.agent/context.md`, затем выполни задачу и запиши файл результата."

// passThroughVars — переменные хоста, без которых не работают git и сам агент.
// Остальное окружение до агента не доходит: запуск должен зависеть от роли,
// а не от того, что случилось в шелле оператора.
var passThroughVars = []string{"PATH", "HOME", "USER", "SHELL", "TMPDIR", "LANG", "LC_ALL", "TERM"}

// credentialVars — переменные авторизации в порядке приоритета. Порядок совпадает
// с поведением самого CLI: заданный API-ключ побеждает подписку. Передаётся только
// выбранная переменная — вторая в процесс не попадает.
var credentialVars = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// Build собирает запуск агента по роли, рабочей папке и паспорту прогона.
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

	// Изолированный каталог конфигурации: пустой, поэтому хостовые настройки,
	// скиллы и плагины из ~/.claude агенту не достаются.
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return abort(fmt.Errorf("каталог конфигурации не создан: %w", err))
	}

	systemPrompt, err := role.SystemPrompt()
	if err != nil {
		return abort(err)
	}
	promptPath := filepath.Join(tmp, "system-prompt.md")
	if err := os.WriteFile(promptPath, []byte(systemPrompt), 0o600); err != nil {
		return abort(fmt.Errorf("системный промпт не записан: %w", err))
	}

	settings, err := buildSettings(role, workdir)
	if err != nil {
		return abort(err)
	}
	settingsPath := filepath.Join(tmp, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
		return abort(fmt.Errorf("settings.json не записан: %w", err))
	}

	argv := []string{
		Executable,
		"--print",
		"--output-format", "json",
		"--session-id", run.RunID,
		"--append-system-prompt-file", promptPath,
		"--settings", settingsPath,
		// Пусто — значит не читать ни пользовательский слой, ни слои проекта-клиента.
		"--setting-sources", "",
		// Сужает сам набор инструментов. Без этого агенту доступны все встроенные,
		// включая сетевые и порождающие процессы: permissions.allow управляет
		// автоодобрением, а не составом.
		"--tools", strings.Join(toolNames(role.Tools.Allow), ","),
		// Запрещает всё, чего нет в permissions.allow: диалогов в headless всё равно никто не увидит.
		"--permission-mode", "dontAsk",
		"--max-turns", strconv.Itoa(role.Limits.MaxTurns),
	}

	if len(role.Skills) > 0 {
		pluginDir, err := buildPlugin(tmp, role)
		if err != nil {
			return abort(err)
		}
		argv = append(argv, "--plugin-dir", pluginDir)
	}

	env := make([]string, 0, len(passThroughVars)+2)
	for _, name := range passThroughVars {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, "CLAUDE_CONFIG_DIR="+configDir, credVar+"="+credValue)

	return &runner.Launch{
		Argv: argv,
		Env:  env,
		// Стартовое сообщение идёт через stdin, а не позиционным аргументом:
		// вариадические флаги вроде --tools забирают все следующие за ними слова
		// и проглотили бы промпт.
		Stdin:        UserPrompt,
		Workdir:      workdir,
		Timeout:      time.Duration(role.Limits.TimeoutSec) * time.Second,
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
func credential() (name, value string, err error) {
	for _, name := range credentialVars {
		if value := os.Getenv(name); value != "" {
			return name, value, nil
		}
	}
	return "", "", fmt.Errorf(
		"не задан ни %s: агенту нечем авторизоваться, см. docs/notes/auth.md",
		strings.Join(credentialVars, ", ни "))
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
func buildSettings(role runner.Role, workdir string) (string, error) {
	s := settingsFile{
		Permissions: permissionsBlock{Allow: role.Tools.Allow, Deny: role.Tools.Deny},
	}

	if scripts := role.HookFiles(); len(scripts) > 0 {
		resultPath := filepath.Join(workdir, role.ResultFile)
		commands := make([]hookCommand, 0, len(scripts))
		for _, script := range scripts {
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
func buildPlugin(tmp string, role runner.Role) (string, error) {
	dir := filepath.Join(tmp, "plugin")
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		return "", fmt.Errorf("каталог плагина не создан: %w", err)
	}

	manifest := fmt.Sprintf("{\n  \"name\": \"office-role-%s\"\n}\n", role.Name)
	manifestPath := filepath.Join(dir, ".claude-plugin", "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
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
