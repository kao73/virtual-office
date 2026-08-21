package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/budget"
)

// Граница «репозиторий описывает офис, ${OFFICE_HOME} — инстанс» держится двумя
// разными способами, и этот — второй.
//
// Первый способ — загрузчик: он отвергает машинный ключ в projects.yaml поимённо.
// Но ключей у машинного знания больше, чем имён: абсолютный путь может забрести
// в роль, а customfield_* — в граф, и никакой загрузчик их там не ждёт. Поэтому
// проверка идёт по самим файлам.
//
// Проверяется **причина, а не метка**. Долг, ради которого это заведено, звучал
// как «config_sha всегда -dirty», но метка — следствие; причина в том, что
// полигонные значения закоммичены. Тест смотрит на причину: пропадут значения —
// метка починится сама.
func TestRepoCarriesNoMachineValues(t *testing.T) {
	root := ".."

	// Файлы, которые читает раннер, и файлы ролей, уезжающие агенту.
	//
	// Чего здесь нет и почему: заметки и отчёты рассказывают о прогонах на конкретной
	// машине, и путь в них — часть рассказа; `tracker.example.yaml` и задания
	// `bootstrap/` — образцы, они и существуют затем, чтобы показать машинные
	// значения; код и его пробы (`adapters/`, `backends/`, `tracker/`, `scripts/`) —
	// там путь бывает данными теста, а `customfield_` именем поля в разборе.
	// Названные поимённо обязаны существовать: переименовали файл — тест должен
	// это заметить, а не промолчать, потеряв половину охвата.
	//
	// Бюджеты сюда не входят: оба их файла необязательны, и требовать поставки
	// значило бы спорить с контрактом. Пока файл есть — он проверяется наравне
	// с прочими, ниже.
	named := []string{
		filepath.Join(root, WorkflowFile),
		filepath.Join(root, ProjectsFile),
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
