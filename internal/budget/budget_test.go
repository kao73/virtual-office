package budget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// missing — путь к накладке, которой нет: у большинства проверок машинного
// перекрытия нет вовсе, и это законное состояние.
func missing(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), File)
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), File)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("файл бюджетов не записан: %v", err)
	}
	return path
}

// Естественное состояние офиса — учёт без ограничений: прогоны считаются,
// задачи берутся. Отсутствие обоих файлов поэтому не ошибка и не повод
// не запуститься, в отличие от workflow.yaml, без которого раннеру нечем решать.
func TestNoFileMeansNoLimits(t *testing.T) {
	budgets, err := Load(missing(t), missing(t))
	if err != nil {
		t.Fatalf("отсутствие файла принято за беду: %v", err)
	}
	if budgets.PerTask.Set() || budgets.PerRoleDaily.Set() || budgets.PerRun.Set() {
		t.Errorf("без файла нашлись лимиты: %+v", budgets)
	}
}

// Файл из одних комментариев — обычный способ временно снять все пределы,
// не удаляя объяснений. Пустой документ здесь значит то же, что отсутствие файла.
func TestEmptyFileMeansNoLimits(t *testing.T) {
	for name, body := range map[string]string{
		"пусто":            "",
		"одни комментарии": "# пределы сняты на время отладки\n",
	} {
		t.Run(name, func(t *testing.T) {
			budgets, err := Load(writeFile(t, body), missing(t))
			if err != nil {
				t.Fatalf("пустой файл принят за беду: %v", err)
			}
			if budgets.Any() {
				t.Errorf("в пустом файле нашлись лимиты: %+v", budgets)
			}
		})
	}
}

// Режим по умолчанию — предупреждение. Тот, кто вписал одно число, не должен
// обнаружить, что раннер перестал брать задачи.
func TestModeDefaultsToWarn(t *testing.T) {
	budgets, err := Load(writeFile(t, "per_task: { usd: 5 }\n"), missing(t))
	if err != nil {
		t.Fatalf("бюджеты не прочитаны: %v", err)
	}
	if !budgets.PerTask.Set() || budgets.PerTask.Stops() {
		t.Errorf("лимит без режима: %+v", budgets.PerTask)
	}
}

func TestLoadReadsAllThreeLimits(t *testing.T) {
	budgets, err := Load(writeFile(t, `
per_task:       { usd: 5, on_exceed: stop }
per_role_daily: { usd: 20, on_exceed: warn }
per_run:        { usd: 1 }
`), missing(t))
	if err != nil {
		t.Fatalf("бюджеты не прочитаны: %v", err)
	}
	if budgets.PerTask.USD != 5 || !budgets.PerTask.Stops() {
		t.Errorf("per_task разобран как %+v", budgets.PerTask)
	}
	if budgets.PerRoleDaily.USD != 20 || budgets.PerRoleDaily.Stops() {
		t.Errorf("per_role_daily разобран как %+v", budgets.PerRoleDaily)
	}
	if budgets.PerRun.USD != 1 {
		t.Errorf("per_run разобран как %+v", budgets.PerRun)
	}
}

// Опечатка в файле бюджетов молча снимает ограничение — а узнать об этом
// человек может только по счёту. Поэтому разбор строгий, как у графа.
func TestLoadRejectsNonsense(t *testing.T) {
	cases := map[string]string{
		"неизвестный лимит":     "per_day: { usd: 5 }\n",
		"неизвестное поле":      "per_task: { usd: 5, mode: stop }\n",
		"неизвестный режим":     "per_task: { usd: 5, on_exceed: убить }\n",
		"отрицательный предел":  "per_task: { usd: -1 }\n",
		"режим без значения":    "per_task: { on_exceed: stop }\n",
		"прерывание по прогону": "per_run: { usd: 1, on_exceed: stop }\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFile(t, body), missing(t)); err == nil {
				t.Error("файл принят молча")
			}
		})
	}
}

// per_run прогон не прерывает: к моменту, когда цена известна, работа уже сделана
// и оплачена. Жёсткая граница у прогона своя — max_turns в роли, — и объяснить
// это надо там, где человек написал stop.
func TestPerRunStopExplainsItself(t *testing.T) {
	_, err := Load(writeFile(t, "per_run: { usd: 1, on_exceed: stop }\n"), missing(t))
	if err == nil {
		t.Fatal("per_run со stop принят молча")
	}
	if !strings.Contains(err.Error(), "max_turns") {
		t.Errorf("объяснение не называет настоящую границу: %v", err)
	}
}

// Предел срабатывает на равенстве: потратив ровно бюджет, задача его исчерпала.
func TestExceededCountsEqualityAsExhausted(t *testing.T) {
	limit := Limit{USD: 5}
	cases := map[float64]bool{4.99: false, 5: true, 5.01: true}
	for spent, want := range cases {
		if got := limit.Exceeded(spent); got != want {
			t.Errorf("на $%.2f предел сработал %v, ожидалось %v", spent, got, want)
		}
	}
	// Ненастроенный лимит не срабатывает никогда, сколько бы ни потратили.
	if (Limit{}).Exceeded(1000) {
		t.Error("сработал лимит, которого нет")
	}
}

// Накладка машины перекрывает дефолты офиса по имени предела и целиком:
// названный в ней предел заменяет дефолтный вместе с режимом, не названный
// остаётся как был.
func TestMachineOverlayReplacesNamedLimits(t *testing.T) {
	office := writeFile(t, `
per_task:       { usd: 5, on_exceed: stop }
per_role_daily: { usd: 20, on_exceed: warn }
`)
	machine := filepath.Join(t.TempDir(), File)
	if err := os.WriteFile(machine, []byte("per_task: { usd: 50 }\n"), 0o644); err != nil {
		t.Fatalf("накладка не записана: %v", err)
	}

	budgets, err := Load(office, machine)
	if err != nil {
		t.Fatalf("бюджеты не прочитаны: %v", err)
	}
	if budgets.PerTask.USD != 50 {
		t.Errorf("названный предел не перекрыт: %+v", budgets.PerTask)
	}
	// Режим тоже пришёл из накладки, а не остался от дефолта: предел заменяется
	// целиком, иначе вышла бы политика, которой не писал никто.
	if budgets.PerTask.Stops() {
		t.Errorf("режим остался от дефолта: %+v", budgets.PerTask)
	}
	if budgets.PerRoleDaily.USD != 20 || budgets.PerRoleDaily.Stops() {
		t.Errorf("неназванный предел тронут: %+v", budgets.PerRoleDaily)
	}
}

// Накладка вправе и снять предел. Решает названность в файле, а не значение:
// `usd: 0` означает «предела нет», и по ненулевой сумме такую накладку было бы
// не отличить от её отсутствия.
func TestMachineOverlayCanRemoveLimit(t *testing.T) {
	office := writeFile(t, "per_run: { usd: 1, on_exceed: warn }\n")
	machine := filepath.Join(t.TempDir(), File)
	if err := os.WriteFile(machine, []byte("per_run: { usd: 0 }\n"), 0o644); err != nil {
		t.Fatalf("накладка не записана: %v", err)
	}

	budgets, err := Load(office, machine)
	if err != nil {
		t.Fatalf("бюджеты не прочитаны: %v", err)
	}
	if budgets.PerRun.Set() {
		t.Errorf("предел не снят накладкой: %+v", budgets.PerRun)
	}
}
