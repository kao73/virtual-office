package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/budget"
	"github.com/kao73/virtual-office/internal/runner"
)

// Граница «репозиторий описывает офис, ${OFFICE_HOME} — инстанс» держится двумя
// разными способами, и этот — второй.
//
// Первый способ — загрузчик: он принимает под defaults только network и tools
// и отвергает projects.yaml из прежней раскладки.
//
// Проверяется **причина, а не метка**. Долг, ради которого это заведено, звучал
// как «config_sha всегда -dirty», но метка — следствие; причина в том, что
// полигонные значения закоммичены. Тест смотрит на причину: пропадут значения —
// метка починится сама.
func TestRepoCarriesNoMachineValues(t *testing.T) {
	root := filepath.Join("..", "..")

	// Файлы, которые читает раннер, и файлы ролей, уезжающие агенту.
	//
	// Чего здесь нет и почему: заметки и отчёты рассказывают о прогонах на конкретной
	// машине, и путь в них — часть рассказа; `tracker.example.yaml` и задания
	// `bootstrap/` — образцы, они и существуют затем, чтобы показать машинные
	// значения; код и его пробы (`internal/adapters/`, `internal/backends/`, `internal/tracker/`, `scripts/`) —
	// там путь бывает данными теста, а `customfield_` именем поля в разборе.
	// Названные поимённо обязаны существовать: переименовали файл — тест должен
	// это заметить, а не промолчать, потеряв половину охвата.
	//
	// Бюджеты сюда не входят: оба их файла необязательны, и требовать поставки
	// значило бы спорить с контрактом. Пока файл есть — он проверяется наравне
	// с прочими, ниже.
	named := []string{
		filepath.Join(root, WorkflowFile),
		// Образец проектов — исключение среди образцов: в нём нет ни одного
		// машинного значения нарочно (D8 в design.md изменения install), и он
		// обязан пройти этот тест как есть.
		filepath.Join(root, ProjectsLocalExampleFile),
	}
	for _, path := range named {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("файл офиса не найден: %v", err)
		}
	}
	named = append(named, filepath.Join(root, budget.File))

	files := named
	// Роли и их шаблоны уезжают агенту целиком, хуки исполняются на машине —
	// машинному значению там не место так же, как в графе.
	for _, pattern := range []string{
		filepath.Join(root, "roles", "*", "*"),
		filepath.Join(root, "roles", "*", "*", "*"),
		filepath.Join(root, "hooks", "*"),
		// skills/ сегодня пуст, но уезжает агенту так же, как роли: заполнится —
		// охват не должен потеряться молча.
		filepath.Join(root, "skills", "*", "*"),
		// bin/ — обёртки, которыми раннер запускают, и они читают ${OFFICE_HOME}:
		// путь, вписанный туда руками, был бы машинным значением. (Исполняемое
		// в поставке есть и в hooks/, и в scripts/: первое здесь же в обходе,
		// второе исключено выше как пробы.)
		filepath.Join(root, "bin", "*"),
	} {
		found, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("%s не перечислен: %v", pattern, err)
		}
		files = append(files, found...)
	}

	// Машинные значения законны только в образцах — они и существуют затем, чтобы
	// эти значения показать, — и потому в обход не попадают: tracker.example.yaml
	// и задания bootstrap/. Раннер образцов не читает.
	//
	// Список примет неполон и полным быть не может: «абсолютный путь» вообще
	// ловится только регулярным выражением, которое ловит и /rest/api/2 в прозе.
	// Взяты корни, откуда пути берутся на живых машинах.
	machineValue := regexp.MustCompile(`(^|[^\w])(/Users/|/home/|/opt/|/var/|customfield_)`)

	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s не прочитан: %v", path, err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if machineValue.MatchString(line) {
				t.Errorf("%s:%d машинное значение в файле офиса: %s\n"+
					"его место в ${OFFICE_HOME}: пути и номера полей у каждой машины свои",
					filepath.Base(path), n+1, strings.TrimSpace(line))
			}
		}
	}
}

// Базовые правила ролей — единственный слой правил, который поставляется
// репозиторием: он лежит рядом с ролями, к которым относится, а не в файле
// проектов, которого в репозитории больше нет. Гарантия, что origin
// принадлежит раннеру, а не агенту, держится одной строкой этого файла,
// и исчезнуть молча она не должна — ни переименованием файла, ни правкой.
func TestShippedBaseRulesKeepGitRemoteDeny(t *testing.T) {
	path := filepath.Join("..", "..", runner.RolesDir, runner.BaseDir, runner.BaseRulesFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("базовые правила ролей не поставлены: %v", err)
	}
	if !strings.Contains(string(raw), `"Bash(git *push*)"`) {
		t.Errorf("%s не запрещает git push: пуш — дело раннера, а не агента", path)
	}
}
