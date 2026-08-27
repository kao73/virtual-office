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

// Ограждения, которые роль вправе потребовать от прогона.
//
// Имена живут здесь, рядом с разбором роли: опечатка в `guards` — это ограждение,
// которое молча не сработает, и узнать о ней надо на загрузке. Сами проверки —
// в пакете guard; он же обязан покрывать весь этот список.
const (
	// GuardChangeDirOnly — вся работа прогона лежит в каталоге изменения,
	// а при исходе done ещё и закоммичена.
	GuardChangeDirOnly = "change_dir_only"
	// GuardPlanMarksOnly — в плане изменились только отметки пунктов.
	GuardPlanMarksOnly = "plan_marks_only"
)

// KnownGuards — все ограждения, известные контракту роли.
func KnownGuards() []string { return []string{GuardChangeDirOnly, GuardPlanMarksOnly} }

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

	// Guards — детерминированные проверки прогона, которые роль требует к себе.
	// Роль называет их по имени и не знает, как они устроены; исполняет их
	// раннер — в хуке, пока агент жив, и у себя после прогона.
	Guards []string `yaml:"guards"`
	// WriteScope — куда роли положено писать. Пусто означает «правил нет»:
	// границу задаёт состав инструментов, как у ревьюера и implementer'а.
	WriteScope WriteScope `yaml:"write_scope"`

	dir        string // каталог роли; от него отсчитываются prompt и includes
	configRoot string // корень конфиг-репозитория; от него отсчитываются skills и hooks
}

type Tools struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// WriteScope — область записи роли: намерение, а не правило.
//
// Роль называет, куда ей положено писать; во что это превратится, решает адаптер
// (в Claude Code — правило разрешений на путь). Написать здесь готовое правило
// значило бы поселить в спецификации роли устройство конкретного агента.
type WriteScope struct {
	// Dir — область. Пока значение одно: ScopeChangeDir.
	Dir string `yaml:"dir"`
	// Ignore — что не считать работой роли: каталоги окружения и кэшей,
	// которые заводят не агент и не задача. Ограждения судят по этому списку.
	Ignore []string `yaml:"ignore"`
}

type Hooks struct {
	Stop []string `yaml:"stop"`
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

	// Неизвестное ограждение — это ограждение, которого нет: раннер о нём
	// не знает, а роль считает себя огороженной.
	for _, name := range r.Guards {
		if !slices.Contains(KnownGuards(), name) {
			errs = append(errs, fmt.Errorf("guards: ограждение %q не существует; есть %s", name, strings.Join(KnownGuards(), ", ")))
		}
	}

	switch {
	case r.WriteScope.Dir == "" && len(r.WriteScope.Ignore) > 0:
		errs = append(errs, errors.New("write_scope.ignore задан без write_scope.dir: исключать нечего, области записи нет"))
	case r.WriteScope.Dir != "" && r.WriteScope.Dir != ScopeChangeDir:
		errs = append(errs, fmt.Errorf("write_scope.dir=%q: известно одно значение — %s", r.WriteScope.Dir, ScopeChangeDir))
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
	// Роль, объявившая область записи, обязана дать и заготовки: каталог изменения
	// готовит раннер, и обнаружить пропажу шаблона в середине прогона поздно.
	for _, path := range r.TemplateFiles() {
		if _, err := os.Stat(path); err != nil {
			errs = append(errs, fmt.Errorf("шаблон артефакта не найден: %w", err))
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

// TemplateFile — заготовка артефакта в каталоге роли.
func (r Role) TemplateFile(name string) string {
	return filepath.Join(r.dir, TemplatesDir, name)
}

// TemplateFiles — заготовки, которые роль обязана иметь: они нужны ровно тогда,
// когда роль объявила область записи, — раннер готовит по ним каталог изменения.
func (r Role) TemplateFiles() []string {
	if r.WriteScope.Dir != ScopeChangeDir {
		return nil
	}
	names := ChangeFiles()
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, r.TemplateFile(name))
	}
	return paths
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
