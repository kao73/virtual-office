package tracker

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
)

func marker(outcome string) Marker {
	return Marker{RunID: runID, Role: "implementer", Outcome: outcome, ConfigSHA: "5bc6a3b0"}
}

// Комментарий офиса обязан опознаваться самим офисом: не опознав свой маркер,
// раннер посчитал бы отчёт прогона ответом человека.
func TestReportBodyStartsWithOwnMarker(t *testing.T) {
	body := ReportBody(marker("done"), runner.Result{
		Outcome:   runner.OutcomeDone,
		Summary:   "Добавил hello.py и тест, всё закоммичено.",
		NextOwner: "none",
	}, "")

	m, ok := MarkerOf(body)
	if !ok {
		t.Fatalf("свой же комментарий не опознан:\n%s", body)
	}
	if m.Role != "implementer" || m.Outcome != "done" {
		t.Errorf("маркер разобран как %+v", m)
	}
	if !strings.Contains(body, "Добавил hello.py") {
		t.Errorf("итога нет в теле:\n%s", body)
	}
}

// Человек читает комментарий глазами: вопросы, блокер и ссылки на работу должны
// быть видны, а не лежать в JSON.
func TestReportBodySectionsByOutcome(t *testing.T) {
	cases := []struct {
		name   string
		result runner.Result
		branch string
		want   []string
		absent []string
	}{
		{
			name: "вопросы с вариантами",
			result: runner.Result{
				Outcome:   runner.OutcomeNeedsHuman,
				Summary:   "Нужен выбор.",
				NextOwner: "human",
				Questions: []runner.Question{
					{Text: "Какую платёжную систему подключаем?", Options: []string{"Stripe", "ЮKassa"}},
					{Text: "Нужен ли возврат средств?"},
				},
			},
			want:   []string{"Какую платёжную систему", "Stripe", "ЮKassa", "Нужен ли возврат"},
			absent: []string{"Блокер", "Артефакты"},
		},
		{
			name: "блокер",
			result: runner.Result{
				Outcome:   runner.OutcomeBlocked,
				Summary:   "Не могу продолжить.",
				NextOwner: "human",
				Blocker:   "нет доступа к тестовой базе",
			},
			want:   []string{"нет доступа к тестовой базе"},
			absent: []string{"Вопросы"},
		},
		{
			name: "артефакты и ветка",
			result: runner.Result{
				Outcome:   runner.OutcomeDone,
				Summary:   "Сделано.",
				NextOwner: "none",
				Artifacts: []string{"hello.py", "a1b2c3d"},
			},
			branch: "agent/OFF-1",
			want:   []string{"agent/OFF-1", "hello.py", "a1b2c3d"},
			absent: []string{"Вопросы", "Блокер"},
		},
		{
			name: "подробности",
			result: runner.Result{
				Outcome:   runner.OutcomeFailed,
				Summary:   "Не вышло.",
				NextOwner: "human",
				DetailsMD: "Пробовал два пути, оба уперлись в отсутствие схемы.",
			},
			want:   []string{"оба уперлись"},
			absent: []string{"Вопросы", "Блокер", "Артефакты"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := ReportBody(marker(string(tc.result.Outcome)), tc.result, tc.branch)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("в теле нет %q:\n%s", want, body)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(body, absent) {
					t.Errorf("в теле лишний раздел %q:\n%s", absent, body)
				}
			}
		})
	}
}

// Системная запись — тоже комментарий офиса, и опознаваться должна так же.
func TestNoticeBody(t *testing.T) {
	m := Marker{RunID: runID, Role: "implementer", Event: EventLeaseExpired, ConfigSHA: "5bc6a3b0"}
	body := NoticeBody(m, "Аренда прогона run:488e8d8f истекла, возвращаю задачу в Ready.")

	parsed, ok := MarkerOf(body)
	if !ok {
		t.Fatalf("системная запись не опознана:\n%s", body)
	}
	if parsed.Event != EventLeaseExpired {
		t.Errorf("событие %q, ожидалось %q", parsed.Event, EventLeaseExpired)
	}
	if !strings.Contains(body, "возвращаю задачу") {
		t.Errorf("текста нет в теле:\n%s", body)
	}
}
