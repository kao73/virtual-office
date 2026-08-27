package guard_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/guard"
	"github.com/kao73/virtual-office/internal/runner"
)

// scene — рабочая папка прогона в том виде, в каком её видит ограждение:
// репозиторий, каталог изменения, точка отсчёта и файл результата.
type scene struct {
	t       *testing.T
	workdir string
	key     string
}

func newScene(t *testing.T) *scene {
	t.Helper()
	s := &scene{t: t, workdir: t.TempDir(), key: "OFF-1"}
	s.git("init", "-q")
	s.write("README.md", "# проект\n")
	s.git("add", "README.md")
	s.commit("первый")
	// Каталог обмена прячется от git тем же способом, что и на живом прогоне:
	// иначе снимок статуса увидит собственный конверт раннера.
	if err := runner.ExcludeAgentDir(s.workdir); err != nil {
		t.Fatalf("каталог обмена не исключён: %v", err)
	}
	return s
}

func (s *scene) git(args ...string) string {
	s.t.Helper()
	full := append([]string{"-C", s.workdir, "-c", "user.email=t@example.test", "-c", "user.name=test"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (s *scene) commit(message string) { s.git("commit", "-q", "-m", message) }

func (s *scene) write(rel, body string) {
	s.t.Helper()
	path := filepath.Join(s.workdir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatalf("каталог %s не создан: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		s.t.Fatalf("%s не записан: %v", rel, err)
	}
}

func (s *scene) remove(rel string) {
	s.t.Helper()
	if err := os.Remove(filepath.Join(s.workdir, rel)); err != nil {
		s.t.Fatalf("%s не удалён: %v", rel, err)
	}
}

// plan — путь плана внутри каталога изменения, относительно рабочей папки.
func (s *scene) plan() string {
	return filepath.Join(runner.ChangeDirRel(s.key), runner.FileTasks)
}

// start снимает точку отсчёта: HEAD и состояние папки на старте прогона.
// Всё, что сделано до него, — не работа этого прогона.
func (s *scene) start(outcome runner.Outcome) guard.Env {
	s.t.Helper()

	status, err := runner.WorktreeStatus(s.workdir)
	if err != nil {
		s.t.Fatalf("состояние не снято: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(s.workdir, runner.Dir), 0o755); err != nil {
		s.t.Fatalf("каталог обмена не создан: %v", err)
	}
	if err := os.WriteFile(runner.BaseStatusPath(s.workdir), status, 0o644); err != nil {
		s.t.Fatalf("снимок не записан: %v", err)
	}

	result := filepath.Join(s.workdir, runner.Dir, runner.FileResult)
	if outcome != "" {
		s.write(filepath.Join(runner.Dir, runner.FileResult),
			`{"outcome":"`+string(outcome)+`","summary":"итог","next_owner":"implementer"}`)
	}

	return guard.EnvFrom(map[string]string{
		runner.EnvTaskKey:     s.key,
		runner.EnvChangeDir:   runner.ChangeDir(s.workdir, s.key),
		runner.EnvBaseCommit:  strings.TrimSpace(s.git("rev-parse", "HEAD")),
		runner.EnvBaseStatus:  runner.BaseStatusPath(s.workdir),
		runner.EnvWriteIgnore: ".venv,__pycache__",
		runner.EnvResultFile:  result,
	})
}

// fillPlan заполняет каталог изменения так, как это делает аналитик.
func (s *scene) fillPlan() {
	dir := runner.ChangeDirRel(s.key)
	s.write(filepath.Join(dir, runner.FileBrief), "# Зачем\n\nПочинить оплату.\n")
	s.write(filepath.Join(dir, runner.FileDesign), "# Как\n\nТрогаем billing.\n")
	s.write(s.plan(), "# Что делать\n\n- [ ] написать тест\n- [ ] починить\n")
}

func mustPass(t *testing.T, name string, env guard.Env) {
	t.Helper()
	if err := guard.Check(name, env); err != nil {
		t.Fatalf("ограждение %s не пропустило честный прогон: %v", name, err)
	}
}

func mustBlock(t *testing.T, name string, env guard.Env, want string) {
	t.Helper()
	err := guard.Check(name, env)
	if err == nil {
		t.Fatalf("ограждение %s пропустило нарушение", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("ограждение %s сказало не о том: %v", name, err)
	}
}

// План, написанный и закоммиченный в свой каталог, — ровно то, чего от роли ждут.
func TestChangeDirOnlyPassesCommittedPlan(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)

	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")

	mustPass(t, runner.GuardChangeDirOnly, env)
}

// Работа вне каталога изменения — и закоммиченная, и оставленная в папке.
func TestChangeDirOnlyBlocksWorkOutside(t *testing.T) {
	t.Run("коммит", func(t *testing.T) {
		s := newScene(t)
		env := s.start(runner.OutcomeDone)

		s.fillPlan()
		s.write("src/pay.py", "def pay(): ...\n")
		s.git("add", "-A")
		s.commit("план и заодно код")

		mustBlock(t, runner.GuardChangeDirOnly, env, "src/pay.py")
	})

	t.Run("незакоммиченное", func(t *testing.T) {
		s := newScene(t)
		env := s.start(runner.OutcomeDone)

		s.fillPlan()
		s.git("add", runner.ChangeDirRel(s.key))
		s.commit("план")
		s.write("src/pay.py", "def pay(): ...\n")

		mustBlock(t, runner.GuardChangeDirOnly, env, "src/pay.py")
	})
}

// Рабочая папка переиспользуется, и чужая незакоммиченная правка лежит в ней
// ещё до первого шага роли. Судится дельта, а не всё, что в папке.
func TestChangeDirOnlyIgnoresDirtFromBefore(t *testing.T) {
	s := newScene(t)
	s.write("src/legacy.py", "недоделанное с прошлого прогона\n")
	env := s.start(runner.OutcomeDone)

	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")

	mustPass(t, runner.GuardChangeDirOnly, env)
}

// Окружение и кэши, оставшиеся от запущенных тестов, работой роли не считаются:
// их называет сама роль в write_scope.ignore.
func TestChangeDirOnlyIgnoresNamedNoise(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)

	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")
	s.write(".venv/lib/python3.13/site-packages/pytest.py", "# кэш\n")
	s.write("src/__pycache__/pay.pyc", "мусор\n")

	mustPass(t, runner.GuardChangeDirOnly, env)
}

// Исход done означает, что план готов и виден следующей роли. Незакоммиченный
// файл для неё не существует — в том числе заготовка, до которой не дошли руки.
func TestChangeDirOnlyBlocksUncommittedPlanOnDone(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)

	s.fillPlan()

	mustBlock(t, runner.GuardChangeDirOnly, env, "не закоммичен")
}

// Тот же прогон, но с исходом, который готовности не объявляет: работа
// продолжится следующим прогоном, и незаконченное в папке — не нарушение.
func TestChangeDirOnlyAllowsWorkInProgress(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeNeedsHuman)

	s.fillPlan()

	mustPass(t, runner.GuardChangeDirOnly, env)
}

// Плана нет вовсе, а прогон объявляет готовность: тогда не с чем работать
// следующей роли, и done принимать нельзя.
func TestChangeDirOnlyBlocksDoneWithoutPlan(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)

	dir := runner.ChangeDirRel(s.key)
	s.write(filepath.Join(dir, runner.FileBrief), "# Зачем\n\nПочинить оплату.\n")
	s.git("add", dir)
	s.commit("только постановка")

	mustBlock(t, runner.GuardChangeDirOnly, env, "плана нет")
}

// Отметки сделанного — единственное, что разработчику положено менять в плане.
func TestPlanMarksOnlyPassesMarks(t *testing.T) {
	s := newScene(t)
	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")
	env := s.start(runner.OutcomeDone)

	s.write(s.plan(), "# Что делать\n\n- [x] написать тест\n- [ ] починить\n")

	mustPass(t, runner.GuardPlanMarksOnly, env)
}

// Перевод строки Windows и пробелы в хвосте разницей не считаются: спор о них
// не про план.
func TestPlanMarksOnlyIgnoresWhitespace(t *testing.T) {
	s := newScene(t)
	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")
	env := s.start(runner.OutcomeDone)

	s.write(s.plan(), "# Что делать\r\n\r\n- [X] написать тест   \r\n- [ ] починить\r\n")

	mustPass(t, runner.GuardPlanMarksOnly, env)
}

// План — контракт между ролями: переписать его молча нельзя ни в рабочей папке,
// ни коммитом.
func TestPlanMarksOnlyBlocksRewrite(t *testing.T) {
	s := newScene(t)
	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")
	env := s.start(runner.OutcomeDone)

	s.write(s.plan(), "# Что делать\n\n- [x] написать тест\n- [ ] починить\n- [ ] и ещё вот это\n")

	mustBlock(t, runner.GuardPlanMarksOnly, env, "изменён")
}

func TestPlanMarksOnlyBlocksDeletion(t *testing.T) {
	s := newScene(t)
	s.fillPlan()
	s.git("add", runner.ChangeDirRel(s.key))
	s.commit("план")
	env := s.start(runner.OutcomeDone)

	s.remove(s.plan())

	mustBlock(t, runner.GuardPlanMarksOnly, env, "удалён")
}

// Задача пришла в очередь без плана — проверять нечего, и это обычное дело,
// а не нарушение.
func TestPlanMarksOnlyPassesWithoutPlan(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)

	s.write("src/pay.py", "def pay(): ...\n")
	s.git("add", "-A")
	s.commit("работа без плана")

	mustPass(t, runner.GuardPlanMarksOnly, env)
}

// Каталога изменения у прогона нет вовсе: для одного ограждения это обычное
// дело, для другого — беда обвязки, и молчать о ней нельзя.
func TestGuardsWithoutChangeDir(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)
	env.ChangeDir = ""

	mustPass(t, runner.GuardPlanMarksOnly, env)
	mustBlock(t, runner.GuardChangeDirOnly, env, "не задан")
}

// Имена ограждений живут в контракте роли, проверки — здесь. Разойдись эти два
// списка, роль считала бы себя огороженной, а раннер молчал бы.
func TestEveryKnownGuardIsImplemented(t *testing.T) {
	s := newScene(t)
	env := s.start("")

	for _, name := range runner.KnownGuards() {
		if err := guard.Check(name, env); err != nil && strings.Contains(err.Error(), "не реализовано") {
			t.Errorf("ограждение %s объявлено контрактом, но не реализовано", name)
		}
	}
	if err := guard.Check("выдуманное", env); err == nil {
		t.Error("неизвестное ограждение прошло молча")
	}
}

// CheckAll останавливается на первом непройденном и называет его: чинить их
// агенту всё равно по одному.
func TestCheckAllNamesFirstFailure(t *testing.T) {
	s := newScene(t)
	env := s.start(runner.OutcomeDone)
	s.fillPlan()

	name, err := guard.CheckAll(runner.KnownGuards(), env)
	if err == nil {
		t.Fatal("нарушение прошло")
	}
	if name != runner.GuardChangeDirOnly {
		t.Errorf("названо ограждение %q, ожидалось %q", name, runner.GuardChangeDirOnly)
	}
}

// Заготовки каталога кладёт раннер **до** старта прогона, и в снимке они уже
// есть — дельта их не покажет. Внутри каталога изменения это ничего не меняет:
// план, не уехавший в git, для следующей роли не существует, кто бы его там
// ни оставил.
func TestChangeDirOnlyBlocksTemplatesLeftUncommitted(t *testing.T) {
	s := newScene(t)
	s.fillPlan()
	env := s.start(runner.OutcomeDone)

	mustBlock(t, runner.GuardChangeDirOnly, env, "не закоммичен")
}
