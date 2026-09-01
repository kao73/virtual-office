package runner

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// RolesDir — каталог с ролями внутри конфиг-репозитория.
const RolesDir = "roles"

// SkillsDir — каталог со скиллами внутри конфиг-репозитория.
const SkillsDir = "skills"

// RoleFile — машиночитаемая спецификация роли.
const RoleFile = "role.yaml"

// WriteTool — инструмент записи файлов. Роль называет его в tools.allow, если ей
// положено править файлы проекта; право записать собственный результат она получает
// в любом случае и отдельно — см. адаптер.
const WriteTool = "Write"

// Role — спецификация роли. Агент её не видит: перевод в нативные механизмы
// конкретного агента делает адаптер.
type Role struct {
	Name       string   `yaml:"name"`
	Prompt     string   `yaml:"prompt"`
	Includes   []string `yaml:"includes"`
	Skills     []string `yaml:"skills"`
	Tools      Tools    `yaml:"tools"`
	Hooks      Hooks    `yaml:"hooks"`
	Limits     Limits   `yaml:"limits"`
	Network    Network  `yaml:"network"`
	ResultFile string   `yaml:"result_file"`

	dir        string // каталог роли; от него отсчитываются prompt и includes
	configRoot string // корень конфиг-репозитория; от него отсчитываются skills и hooks
}

type Tools struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type Hooks struct {
	Stop []string `yaml:"stop"`
	// PreToolUse — фазовые ограждения записи. В отличие от Stop, скрипт хука
	// не копируется адаптером отдельно: он живёт внутри скилла, который роль
	// и так подключает (Command называет путь вида "skills/<скилл>/...",
	// проверяется ниже), и адаптер лишь подставляет его абсолютный путь
	// внутри собранного плагина в момент запуска (internal/adapters/claude).
	PreToolUse []PreToolUseHook `yaml:"pre_tool_use"`
}

// PreToolUseHook — один matcher/command из hooks.pre_tool_use.
//
// Command — это то, что в итоге исполнит шелл, только с двумя подстановками
// позже, уже в адаптере: первое слово (путь вида "skills/<скилл>/...")
// становится абсолютным путём внутри собранного плагина, а буквальная
// подстрока "$WORKDIR" — реальным workdir запуска. Ни то, ни другое здесь ещё
// не подставляется: role.yaml не знает ни каталога плагина, ни workdir.
type PreToolUseHook struct {
	Matcher string `yaml:"matcher"`
	Command string `yaml:"command"`
}

type Limits struct {
	MaxTurns   int `yaml:"max_turns"`
	TimeoutSec int `yaml:"timeout_sec"`
}

// Network — какая сеть нужна роли сверх той, что нужна самому агенту.
//
// Раздел необязателен, и пусто означает «ничего»: умолчание закрытое. Роль,
// которой нужны пакеты для тестов, называет их источники поимённо — так в самой
// роли видно, куда она ходит, и видно это до прогона, а не по факту.
//
// Раннер домены не разбирает и не достраивает: подстановки, порты и подсети —
// дело песочницы, а не роли. Отвергается только то, что заведомо не совпадёт
// ни с чем: схема, путь, пробел внутри.
type Network struct {
	Allow []string `yaml:"allow"`
}

// LoadRole читает и проверяет roles/<name>/role.yaml.
// Разбор строгий: неизвестное поле — ошибка, а не молча забытая настройка.
func LoadRole(configRoot, name string) (Role, error) {
	dir := filepath.Join(configRoot, RolesDir, name)
	path := filepath.Join(dir, RoleFile)

	raw, err := os.ReadFile(path)
	if err != nil {
		return Role{}, fmt.Errorf("роль %q не прочитана: %w", name, err)
	}

	var r Role
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return Role{}, fmt.Errorf("%s не разобран: %w", path, err)
	}
	r.dir = dir
	r.configRoot = configRoot

	if err := r.validate(name); err != nil {
		return Role{}, fmt.Errorf("%s нарушает контракт роли: %w", path, err)
	}
	return r, nil
}

func (r Role) validate(dirName string) error {
	var errs []error

	if r.Name != dirName {
		errs = append(errs, fmt.Errorf("name=%q не совпадает с именем каталога %q", r.Name, dirName))
	}
	if r.Prompt == "" {
		errs = append(errs, errors.New("prompt не задан"))
	}
	if r.ResultFile == "" {
		errs = append(errs, errors.New("result_file не задан"))
	} else if filepath.IsAbs(r.ResultFile) || strings.Contains(r.ResultFile, "..") {
		errs = append(errs, fmt.Errorf("result_file=%q: путь должен быть относительным и не выходить из workdir", r.ResultFile))
	}
	if r.Limits.MaxTurns <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_turns=%d: ожидается положительное число", r.Limits.MaxTurns))
	}
	if r.Limits.TimeoutSec <= 0 {
		errs = append(errs, fmt.Errorf("limits.timeout_sec=%d: ожидается положительное число", r.Limits.TimeoutSec))
	}
	if len(r.Tools.Allow) == 0 {
		errs = append(errs, errors.New("tools.allow пуст: агенту нечем работать"))
	}
	// Запись файла результата — часть контракта прогона, и право на неё роли выдаёт
	// адаптер, одним путём. Запрет инструмента целиком сильнее любого разрешения
	// и отнял бы у роли возможность закончиться иначе, чем синтетическим failed.
	// Чтобы запретить роли правку кода, хватает не давать Write в allow.
	if slices.Contains(r.Tools.Deny, WriteTool) {
		errs = append(errs, fmt.Errorf("tools.deny запрещает %s целиком: роли нечем будет записать результат", WriteTool))
	}

	// Домен, записанный как URL, не совпадёт ни с чем, и роль молча останется
	// без сети: узнать об этом можно будет только по провалу прогона.
	for i, host := range r.Network.Allow {
		if err := validHost(host); err != nil {
			errs = append(errs, fmt.Errorf("network.allow[%d]=%q: %w", i, host, err))
		}
	}

	// Всё, на что роль ссылается, должно существовать. Иначе о пропаже узнаём
	// в середине прогона, уже потратив токены.
	for _, path := range r.PromptFiles() {
		if _, err := os.Stat(path); err != nil {
			errs = append(errs, fmt.Errorf("файл промпта не найден: %w", err))
		}
	}
	for _, path := range r.SkillDirs() {
		if fi, err := os.Stat(path); err != nil {
			errs = append(errs, fmt.Errorf("скилл не найден: %w", err))
		} else if !fi.IsDir() {
			errs = append(errs, fmt.Errorf("скилл %s не каталог", path))
		}
	}
	for _, path := range r.HookFiles() {
		// Неисполняемый хук даёт код 126, а всё, кроме 2, считается неблокирующей
		// ошибкой — ограждение молча перестанет ограждать. Ловим на загрузке.
		switch fi, err := os.Stat(path); {
		case err != nil:
			errs = append(errs, fmt.Errorf("хук не найден: %w", err))
		case fi.Mode()&0o111 == 0:
			errs = append(errs, fmt.Errorf("хук %s не исполняемый: ограждение не сработает и не пожалуется", path))
		}
	}

	// hooks.pre_tool_use ссылается на файл внутри скилла, а не внутри hooks/:
	// сам скрипт (comet-hook-router.mjs) — часть вендоренного скилла, который
	// роль и так обязана подключить (design doc: "A role that declares
	// hooks.pre_tool_use without mounting the referenced skill is a config
	// error the loader should reject at role-load time").
	for i, h := range r.Hooks.PreToolUse {
		switch {
		case strings.TrimSpace(h.Matcher) == "":
			errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d].matcher пуст", i))
		case strings.TrimSpace(h.Command) == "":
			errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d].command пуст", i))
		default:
			script, _, _ := strings.Cut(h.Command, " ")
			skill, ok := skillFromHookScript(script)
			if !ok {
				errs = append(errs, fmt.Errorf(
					"hooks.pre_tool_use[%d].command=%q: путь должен начинаться с %s/<скилл>/",
					i, h.Command, SkillsDir))
				continue
			}
			if !slices.Contains(r.Skills, skill) {
				errs = append(errs, fmt.Errorf(
					"hooks.pre_tool_use[%d] ссылается на скилл %q, а его нет в skills:", i, skill))
			}
			switch fi, err := os.Stat(filepath.Join(r.configRoot, script)); {
			case err != nil:
				errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d]: файл хука не найден: %w", i, err))
			case fi.IsDir():
				errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d]: %s — каталог, а не файл", i, script))
			case fi.Mode()&0o111 == 0:
				// Тот же провал, что и у Stop-хука выше: код 126 от неисполняемого
				// файла Claude Code сочтёт неблокирующей ошибкой хука, и ограждение
				// перестанет ограждать беззвучно. Комет не гарантирует mode 755 у
				// вендоренного скрипта — пакет @rpamis/comet сам кладёт его 644.
				errs = append(errs, fmt.Errorf(
					"hooks.pre_tool_use[%d]: %s не исполняемый: ограждение не сработает и не пожалуется", i, script))
			}
		}
	}

	return errors.Join(errs...)
}

// validHost проверяет запись в network.allow.
//
// Проверка нарочно бедная: домены разбирает песочница, и знать за неё, что такое
// правильный wildcard или допустимый порт, раннер не должен. Отвергается только
// то, что не совпадёт ни с чем ни при каком прочтении, — а такая запись опаснее
// ошибки разбора: роль остаётся без сети молча.
func validHost(host string) error {
	switch {
	case strings.TrimSpace(host) == "":
		return errors.New("пустое значение")
	case strings.Contains(host, "://"):
		return errors.New("это адрес со схемой, а нужно имя хоста (pypi.org)")
	case strings.Contains(host, "/"):
		return errors.New("это путь, а нужно имя хоста (pypi.org)")
	case strings.ContainsAny(host, " \t"):
		return errors.New("пробел внутри: каждый хост — отдельным элементом списка")
	}
	return nil
}

// PromptFiles — файлы, из которых собирается системный промпт, в порядке склейки:
// сперва includes, затем собственный промпт роли.
func (r Role) PromptFiles() []string {
	paths := make([]string, 0, len(r.Includes)+1)
	for _, inc := range r.Includes {
		paths = append(paths, filepath.Join(r.dir, inc))
	}
	if r.Prompt != "" {
		paths = append(paths, filepath.Join(r.dir, r.Prompt))
	}
	return paths
}

// SkillDirs — каталоги скиллов, которые видит роль.
func (r Role) SkillDirs() []string {
	paths := make([]string, 0, len(r.Skills))
	for _, s := range r.Skills {
		paths = append(paths, filepath.Join(r.configRoot, SkillsDir, s))
	}
	return paths
}

// HookFiles — скрипты ограждений, срабатывающих на попытке агента завершиться.
func (r Role) HookFiles() []string {
	paths := make([]string, 0, len(r.Hooks.Stop))
	for _, h := range r.Hooks.Stop {
		paths = append(paths, filepath.Join(r.configRoot, h))
	}
	return paths
}

// skillFromHookScript достаёт имя скилла из пути вида "skills/<имя>/...".
// Второе возвращаемое значение — false, если путь не такой формы вообще
// (не начинается с SkillsDir).
func skillFromHookScript(path string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 2 || parts[0] != SkillsDir {
		return "", false
	}
	return parts[1], true
}

// SystemPrompt собирает системный промпт: includes, промпт роли и спецификация
// файла результата. Спецификация вклеивается сюда потому, что агент работает
// в репозитории клиента и прочитать docs/contracts/agent-io.md не может.
func (r Role) SystemPrompt() (string, error) {
	var b strings.Builder
	for _, path := range r.PromptFiles() {
		text, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("промпт роли не собран: %w", err)
		}
		b.Write(bytes.TrimSpace(text))
		b.WriteString("\n\n")
	}
	b.WriteString(ResultSpec(r.ResultFile))
	return b.String(), nil
}
