// Package mock — файловый трекер: каталог на задачу, YAML с полями, комментарии
// отдельными файлами. Им отлаживают конвейер, не поднимая JIRA.
//
// Он не изображает трекер, а работает как трекер, включая гонку за задачу.
// Захват идёт единственным переименованием файла аренды, поэтому тест на два
// конкурирующих tick проверяет настоящий CAS: переименовать один и тот же файл
// могут не двое — проигравший получит «нет такого файла».
//
// Хранилище:
//
//	<root>/<KEY>/task.yaml              поля задачи
//	<root>/<KEY>/lease.free             аренды нет
//	<root>/<KEY>/lease.<unix>.<run_id>  аренда до <unix>, владелец <run_id>
//	<root>/<KEY>/comments/NNNN.md       комментарии по порядку
//
// Владелец и срок живут в **имени** файла аренды, а не внутри него. Так смена
// состояния аренды — одно атомарное действие: нет промежутка, в котором аренда
// уже захвачена, но срок ещё не записан, и её можно было бы счесть истёкшей.
package mock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
)

// Имена файлов хранилища.
const (
	taskFileName   = "task.yaml"
	commentsDir    = "comments"
	attachmentsDir = "attachments"
	leasePrefix    = "lease."
	leaseFree      = leasePrefix + "free"
)

// DirName — подкаталог хозяйства раннера, в котором живёт файловый трекер.
const DirName = "mock"

// Account — учётка, которой подписаны записи офиса.
const Account = "office"

// Tracker — файловый трекер.
type Tracker struct {
	root string

	// Now — часы. Отдельным полем, потому что аренда и её истечение —
	// это ровно про время, и проверять их надо не ожиданием в тестах.
	Now func() time.Time
	// Whose — учётка, которой подписаны комментарии раннера.
	Whose string
}

var _ tracker.Tracker = (*Tracker)(nil)

// New открывает файловый трекер в указанном каталоге.
func New(root string) *Tracker {
	return &Tracker{root: root, Now: time.Now, Whose: Account}
}

// Default — трекер в хозяйстве раннера: ${OFFICE_HOME}/mock.
func Default() (*Tracker, error) {
	home, err := runner.Home()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(home, DirName)), nil
}

// Root — каталог хранилища.
func (t *Tracker) Root() string { return t.root }

// RoleAccount — учётка роли. Конфигурации у файлового трекера нет, поэтому имя —
// соглашение: в JIRA то же самое задаётся в ${OFFICE_HOME}/tracker.yaml.
func RoleAccount(role string) string { return Account + "-" + role }

// As — тот же трекер под другой учёткой. Хранилище общее: учётка меняет только
// подпись под комментариями, а не то, где лежат задачи.
func (t *Tracker) As(account string) *Tracker {
	clone := *t
	clone.Whose = account
	return &clone
}

// Whoami — учётка самого раннера.
func (t *Tracker) Whoami() (string, error) { return t.Whose, nil }

// Add заводит задачу. Метод не из контракта: в JIRA задачи заводит человек,
// а здесь — CLI `runner mock add`.
func (t *Tracker) Add(task tracker.Task) error {
	dir := t.dir(task.Key)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("задача %s уже есть", task.Key)
	}
	if err := os.MkdirAll(filepath.Join(dir, commentsDir), 0o755); err != nil {
		return fmt.Errorf("каталог задачи не создан: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, attachmentsDir), 0o755); err != nil {
		return fmt.Errorf("каталог вложений не создан: %w", err)
	}
	if err := writeTask(dir, task); err != nil {
		return err
	}
	// Свободная аренда — тот самый файл, который потом переименует захватчик.
	// Без него захватывать было бы нечего.
	return os.WriteFile(filepath.Join(dir, leaseFree), nil, 0o644)
}

// Keys — ключи всех задач хранилища, по порядку.
func (t *Tracker) Keys() ([]string, error) {
	entries, err := os.ReadDir(t.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("хранилище %s не прочитано: %w", t.root, err)
	}

	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			keys = append(keys, e.Name())
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// Get — задача целиком, с арендой и всеми комментариями.
func (t *Tracker) Get(key string) (tracker.Task, error) {
	dir := t.dir(key)
	task, err := readTask(dir)
	if err != nil {
		return tracker.Task{}, err
	}
	task.Key = key

	lease, err := readLease(dir)
	if err != nil {
		return tracker.Task{}, err
	}
	task.RunID, task.LeaseUntil = lease.runID, lease.until

	if task.Comments, err = readComments(dir); err != nil {
		return tracker.Task{}, err
	}
	return task, nil
}

// ListReady — кандидаты в статусе проекта: без живой аренды, по возрастанию ключа.
// Порядок задан жёстко, чтобы два прогона выбирали задачи одинаково.
func (t *Tracker) ListReady(project, status string) ([]tracker.TaskRef, error) {
	now := t.Now()
	return t.list(func(task tracker.Task) bool {
		return task.Project == project && task.Status == status && !task.LeaseAlive(now)
	})
}

// ListExpired — задачи проекта с истёкшей арендой: сырьё для reaper.
func (t *Tracker) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	return t.list(func(task tracker.Task) bool {
		return task.Project == project && task.RunID != "" && !task.LeaseAlive(now)
	})
}

// List — задачи проекта в названных статусах, включая захваченные.
func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	return t.list(func(task tracker.Task) bool {
		return task.Project == project && slices.Contains(statuses, task.Status)
	})
}

func (t *Tracker) list(match func(tracker.Task) bool) ([]tracker.TaskRef, error) {
	keys, err := t.Keys()
	if err != nil {
		return nil, err
	}

	var refs []tracker.TaskRef
	for _, key := range keys {
		task, err := t.Get(key)
		if err != nil {
			return nil, err
		}
		if !match(task) {
			continue
		}
		ref := task.Ref()
		ref.Updated = t.updated(key)
		refs = append(refs, ref)
	}
	return refs, nil
}

// updated — когда задачу трогали в последний раз. Отдельного поля у файлового
// трекера нет, и заводить его незачем: время правки файла задачи — то же самое,
// и врать оно не умеет. Нечитаемое время не ошибка: возраст — справка человеку.
func (t *Tracker) updated(key string) time.Time {
	info, err := os.Stat(filepath.Join(t.dir(key), taskFileName))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Claim — захват задачи.
//
// Всё решает одно переименование: файл аренды, который мы прочитали, можно
// переименовать только один раз. Опоздавший получит «нет такого файла» — это
// и есть проигрыш в гонке, а не ошибка хранилища.
func (t *Tracker) Claim(req tracker.ClaimRequest) error {
	dir := t.dir(req.Key)
	task, err := t.Get(req.Key)
	if err != nil {
		return err
	}

	now := t.Now()
	switch {
	case task.Status != req.ExpectStatus:
		return fmt.Errorf("%w: %s в статусе %q, а захват шёл из %q",
			tracker.ErrClaimLost, req.Key, task.Status, req.ExpectStatus)
	case task.LeaseAlive(now):
		return fmt.Errorf("%w: %s арендована прогоном %s до %s",
			tracker.ErrClaimLost, req.Key, task.RunID, task.LeaseUntil.Format(time.RFC3339))
	}

	from := leaseFree
	if task.RunID != "" {
		from = leaseName(task.RunID, task.LeaseUntil)
	}
	to := leaseName(req.RunID, req.LeaseUntil)
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
		return fmt.Errorf("%w: %s захватил кто-то другой", tracker.ErrClaimLost, req.Key)
	}

	// Аренда наша — можно записывать поля. Владелец здесь только для человека:
	// сверяется всё по run_id из имени файла аренды.
	//
	// Статус меняется, только если роли есть куда переводить задачу: рабочий
	// статус необязателен, и без него «в работе» означает живую аренду там же,
	// откуда роль читает. Правило то же, что в JIRA, — разъедься реализации
	// здесь, расхождение вылезло бы на живой доске.
	task.Owner = req.Owner
	if req.WorkingStatus != "" {
		task.Status = req.WorkingStatus
	}
	if err := writeTask(dir, task); err != nil {
		return err
	}

	// Перечитать и убедиться, что владелец — мы. В файловом трекере это
	// избыточно, но контракт требует того же, что от JIRA, где атомарности нет.
	fresh, err := t.Get(req.Key)
	if err != nil {
		return err
	}
	if fresh.RunID != req.RunID {
		return fmt.Errorf("%w: после захвата %s владеет %s", tracker.ErrClaimLost, req.Key, fresh.RunID)
	}
	return nil
}

// Renew продлевает свою живую аренду. Истёкшую продлевать поздно: её мог забрать
// другой прогон, и продление вернуло бы задаче двух владельцев.
func (t *Tracker) Renew(key, runID string, leaseUntil time.Time) error {
	dir := t.dir(key)
	task, err := t.Get(key)
	if err != nil {
		return err
	}
	if err := tracker.CheckOwner(task, tracker.ByRun(runID), t.Now()); err != nil {
		return err
	}

	from := leaseName(task.RunID, task.LeaseUntil)
	to := leaseName(runID, leaseUntil)
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
		return fmt.Errorf("%w: аренда %s ушла из-под рук", tracker.ErrNotOwner, key)
	}
	return nil
}

// Release снимает аренду, не трогая статус: задачу двигает Transition.
func (t *Tracker) Release(key string, by tracker.Actor) error {
	dir := t.dir(key)
	task, err := t.Get(key)
	if err != nil {
		return err
	}
	if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
		return err
	}
	if task.RunID == "" {
		return nil // аренды и так нет
	}

	from := leaseName(task.RunID, task.LeaseUntil)
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, leaseFree)); err != nil {
		return fmt.Errorf("%w: аренда %s ушла из-под рук", tracker.ErrNotOwner, key)
	}
	return nil
}

// Transition двигает задачу по графу.
func (t *Tracker) Transition(key string, by tracker.Actor, toStatus string) error {
	return t.mutate(key, by, func(task *tracker.Task) { task.Status = toStatus })
}

// SetHumanFlag выставляет атрибут «ждёт человека».
func (t *Tracker) SetHumanFlag(key string, by tracker.Actor, on bool) error {
	return t.mutate(key, by, func(task *tracker.Task) { task.HumanFlag = on })
}

// SetAttempts записывает счётчик попыток.
func (t *Tracker) SetAttempts(key string, by tracker.Actor, n int) error {
	return t.mutate(key, by, func(task *tracker.Task) { task.Attempts = n })
}

// createdStatus — начальный статус тикета, заведённого CreateTask. Analysis —
// тот же вход, что человек даёт обычной задаче, переводя её из Backlog
// («берите в работу», workflow.yaml). TaskInput статуса не несёт (design
// doc) — решать его обязана реализация, а не вызывающий код.
const createdStatus = "Analysis"

// nextKey подбирает следующий свободный ключ проекта: <project>-N, где N —
// максимум существующих номеров этого проекта плюс один. Считаются только
// каталоги с настоящей задачей (есть task.yaml) — голый каталог без него
// не задача, а обрубок незавершённого CreateTask или чужая подготовка, и
// занимать номер не должен: иначе nextKey тихо перескочит его и коллизию
// поймать будет не на чем. От коллизии двух параллельных CreateTask,
// подобравших один и тот же номер, эта функция сама не защищает — защищает
// os.Mkdir в CreateTask.
func (t *Tracker) nextKey(project string) (string, error) {
	keys, err := t.Keys()
	if err != nil {
		return "", err
	}
	prefix := project + "-"
	max := 0
	for _, key := range keys {
		n, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(t.dir(key), taskFileName)); err != nil {
			continue
		}
		if v, err := strconv.Atoi(n); err == nil && v > max {
			max = v
		}
	}
	return fmt.Sprintf("%s%d", prefix, max+1), nil
}

// CreateTask заводит новую задачу с ключом <project>-N. Директория задачи
// создаётся os.Mkdir, не MkdirAll: коллизия двух параллельных CreateTask,
// подобравших один и тот же номер, обязана упасть с ошибкой, а не молча
// переписать половину задачи другого — mock отлаживает конвейер, а не
// имитирует конкурентный трекер под нагрузкой (доккомментарий пакета).
func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
	key, err := t.nextKey(project)
	if err != nil {
		return tracker.TaskRef{}, err
	}
	dir := t.dir(key)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return tracker.TaskRef{}, fmt.Errorf("задача %s не создана: %w", key, err)
	}
	if err := os.Mkdir(filepath.Join(dir, commentsDir), 0o755); err != nil {
		return tracker.TaskRef{}, fmt.Errorf("каталог комментариев %s не создан: %w", key, err)
	}
	if err := os.Mkdir(filepath.Join(dir, attachmentsDir), 0o755); err != nil {
		return tracker.TaskRef{}, fmt.Errorf("каталог вложений %s не создан: %w", key, err)
	}

	task := tracker.Task{
		Key: key, Project: project, Summary: input.Summary, Description: input.Description,
		Status: createdStatus, Labels: input.Labels,
	}
	if err := writeTask(dir, task); err != nil {
		return tracker.TaskRef{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, leaseFree), nil, 0o644); err != nil {
		return tracker.TaskRef{}, fmt.Errorf("аренда %s не заведена: %w", key, err)
	}

	created, err := t.Get(key)
	if err != nil {
		return tracker.TaskRef{}, err
	}
	ref := created.Ref()
	ref.Updated = t.updated(key)
	return ref, nil
}

// FindByMarker — задачи проекта с данной меткой.
func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
	return t.list(func(task tracker.Task) bool {
		return task.Project == project && slices.Contains(task.Labels, marker)
	})
}

// AddAttachment сохраняет сырые данные вложением. name сегодня не влияет
// на путь хранения (файл называется по номеру, как и комментарии) —
// параметр существует ради паритета с jira, которой имя нужно для
// multipart-формы.
func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
	task, err := t.Get(key)
	if err != nil {
		return "", err
	}
	if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
		return "", err
	}

	dir := filepath.Join(t.dir(key), attachmentsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("вложения %s не прочитаны: %w", key, err)
	}

	f, id, err := nextExclusive(dir, len(entries), "")
	if err != nil {
		return "", fmt.Errorf("вложение %s не записано: %w", key, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("вложение %s не записано: %w", key, err)
	}
	return id, nil
}

// GetAttachment читает вложение обратно, байт в байт.
func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(t.dir(key), attachmentsDir, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: вложение %s/%s", tracker.ErrNotFound, key, id)
	}
	if err != nil {
		return nil, fmt.Errorf("вложение %s/%s не прочитано: %w", key, id, err)
	}
	return data, nil
}

// LinkDependsOn — заглушка, замещается настоящей реализацией в задаче 4
// плана docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md.
func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
	return errors.New("mock.LinkDependsOn: пока не реализовано")
}

// Comment пишет комментарий от имени офиса.
func (t *Tracker) Comment(key string, by tracker.Actor, body string) error {
	task, err := t.Get(key)
	if err != nil {
		return err
	}
	if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
		return err
	}
	return t.AddComment(key, t.Whose, body)
}

// Move переводит задачу рукой человека и снимает метку ожидания, если она стояла.
//
// Метод не из контракта, как и AddComment: он делает то, что в JIRA человек делает
// мышкой на доске, и потому не спрашивает ни аренды, ни владельца. Без него
// файловый трекер не умеет главного человеческого действия — сказать «берите
// в работу», переведя задачу в очередь роли.
//
// Метка снимается здесь же намеренно. В JIRA её снимает человек — там перенос
// и снятие метки видны глазами и делаются в одном экране; здесь же забытая метка
// оставила бы задачу «ждущей человека» в списке навсегда, а `runner mock` — это
// инструмент ручных сценариев, где лишний шаг просто забывают.
func (t *Tracker) Move(key, toStatus string) error {
	dir := t.dir(key)
	task, err := t.Get(key)
	if err != nil {
		return err
	}
	task.Status = toStatus
	task.HumanFlag = false
	return writeTask(dir, task)
}

// AddComment пишет комментарий от произвольной учётки без проверки владения.
// Метод не из контракта: так в хранилище попадает голос человека — из CLI
// `runner mock comment`. Человек комментирует когда хочет, аренда ему не нужна.
func (t *Tracker) AddComment(key, author, body string) error {
	dir := filepath.Join(t.dir(key), commentsDir)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%w: %s", tracker.ErrNotFound, key)
	}

	existing, err := commentFiles(dir)
	if err != nil {
		return err
	}

	header, err := yaml.Marshal(commentHead{Author: author, Created: t.Now()})
	if err != nil {
		return fmt.Errorf("шапка комментария не собрана: %w", err)
	}
	content := "---\n" + string(header) + "---\n" + body + "\n"

	f, _, err := nextExclusive(dir, len(existing), ".md")
	if err != nil {
		return fmt.Errorf("комментарий не записан: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("комментарий не записан: %w", err)
	}
	return nil
}

// nextExclusive создаёт файл со следующим по счёту именем в каталоге,
// эксклюзивно: два конкурентных писателя гарантированно получают разные
// номера. Общий приём для комментариев (AddComment) и вложений
// (AddAttachment) — вместо двух копий одного и того же цикла.
func nextExclusive(dir string, existing int, suffix string) (*os.File, string, error) {
	for n := existing + 1; n < existing+16; n++ {
		name := fmt.Sprintf("%04d%s", n, suffix)
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("файл %s не создан: %w", name, err)
		}
		return f, name, nil
	}
	return nil, "", errors.New("не нашлось свободного номера")
}

// mutate — чтение, проверка права, правка, запись.
func (t *Tracker) mutate(key string, by tracker.Actor, change func(*tracker.Task)) error {
	dir := t.dir(key)
	task, err := t.Get(key)
	if err != nil {
		return err
	}
	if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
		return err
	}
	change(&task)
	return writeTask(dir, task)
}

func (t *Tracker) dir(key string) string { return filepath.Join(t.root, key) }

// taskFile — формат хранения. Он отдельно от tracker.Task потому, что аренда
// в файле полей не лежит: она вся в имени файла аренды.
type taskFile struct {
	Project     string   `yaml:"project"`
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description,omitempty"`
	Status      string   `yaml:"status"`
	Labels      []string `yaml:"labels,omitempty"`
	Owner       string   `yaml:"owner,omitempty"`
	Attempts    int      `yaml:"attempts"`
	HumanFlag   bool     `yaml:"human_flag"`
}

func readTask(dir string) (tracker.Task, error) {
	raw, err := os.ReadFile(filepath.Join(dir, taskFileName))
	if errors.Is(err, os.ErrNotExist) {
		return tracker.Task{}, fmt.Errorf("%w: %s", tracker.ErrNotFound, filepath.Base(dir))
	}
	if err != nil {
		return tracker.Task{}, fmt.Errorf("задача не прочитана: %w", err)
	}

	var f taskFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return tracker.Task{}, fmt.Errorf("%s/%s не разобран: %w", filepath.Base(dir), taskFileName, err)
	}
	return tracker.Task{
		Project: f.Project, Summary: f.Summary, Description: f.Description,
		Status: f.Status, Labels: f.Labels, Owner: f.Owner,
		Attempts: f.Attempts, HumanFlag: f.HumanFlag,
	}, nil
}

// writeTask пишет поля задачи целиком, через временный файл: оборванная запись
// не должна оставить полтаска.
func writeTask(dir string, task tracker.Task) error {
	raw, err := yaml.Marshal(taskFile{
		Project: task.Project, Summary: task.Summary, Description: task.Description,
		Status: task.Status, Labels: task.Labels, Owner: task.Owner,
		Attempts: task.Attempts, HumanFlag: task.HumanFlag,
	})
	if err != nil {
		return fmt.Errorf("задача не сериализована: %w", err)
	}

	tmp, err := os.CreateTemp(dir, taskFileName+".*")
	if err != nil {
		return fmt.Errorf("временный файл задачи не создан: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("задача не записана: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("задача не записана: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, taskFileName))
}

// lease — состояние аренды, прочитанное из имени файла.
type lease struct {
	runID string
	until time.Time
}

// leaseName собирает имя файла аренды. Срок хранится в секундах: точнее
// файловому трекеру ни к чему, а имя остаётся читаемым.
func leaseName(runID string, until time.Time) string {
	return fmt.Sprintf("%s%d.%s", leasePrefix, until.Unix(), runID)
}

// leaseFile — имя файла аренды, каким оно лежит в каталоге сейчас.
func leaseFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("каталог задачи не прочитан: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), leasePrefix) {
			return e.Name(), nil
		}
	}
	// Файл аренды заводится вместе с задачей и дальше только переименовывается.
	// Его отсутствие означает порчу хранилища, а не свободную задачу.
	return "", fmt.Errorf("в %s нет файла аренды: хранилище испорчено", dir)
}

func readLease(dir string) (lease, error) {
	name, err := leaseFile(dir)
	if err != nil {
		return lease{}, err
	}
	if name == leaseFree {
		return lease{}, nil
	}

	unix, runID, found := strings.Cut(strings.TrimPrefix(name, leasePrefix), ".")
	seconds, err := strconv.ParseInt(unix, 10, 64)
	if !found || err != nil || runID == "" {
		return lease{}, fmt.Errorf("имя файла аренды %q не разобрано", name)
	}
	return lease{runID: runID, until: time.Unix(seconds, 0).UTC()}, nil
}

// commentHead — шапка файла комментария.
type commentHead struct {
	Author  string    `yaml:"author"`
	Created time.Time `yaml:"created"`
}

func commentFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("комментарии не прочитаны: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names) // номера с ведущими нулями сортируются как числа
	return names, nil
}

func readComments(dir string) ([]tracker.Comment, error) {
	commentsPath := filepath.Join(dir, commentsDir)
	names, err := commentFiles(commentsPath)
	if err != nil {
		return nil, err
	}

	comments := make([]tracker.Comment, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(commentsPath, name))
		if err != nil {
			return nil, fmt.Errorf("комментарий %s не прочитан: %w", name, err)
		}
		head, body, err := splitComment(string(raw))
		if err != nil {
			return nil, fmt.Errorf("комментарий %s не разобран: %w", name, err)
		}
		comments = append(comments, tracker.Comment{
			ID:      strings.TrimSuffix(name, ".md"),
			Author:  head.Author,
			Created: head.Created,
			Body:    body,
		})
	}
	return comments, nil
}

// splitComment делит файл на шапку и тело. Тело — всё, что после второго `---`,
// как есть: комментарий человека может содержать что угодно, включая свои `---`.
func splitComment(raw string) (commentHead, string, error) {
	const fence = "---\n"

	rest, found := strings.CutPrefix(raw, fence)
	if !found {
		return commentHead{}, "", errors.New("нет шапки")
	}
	header, body, found := strings.Cut(rest, fence)
	if !found {
		return commentHead{}, "", errors.New("шапка не закрыта")
	}

	var head commentHead
	if err := yaml.Unmarshal([]byte(header), &head); err != nil {
		return commentHead{}, "", err
	}
	return head, strings.TrimRight(body, "\n"), nil
}
