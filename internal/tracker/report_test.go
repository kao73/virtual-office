package tracker

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
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
	}, "", runner.Usage{})

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
					{ID: "Q1", Text: "Какую платёжную систему подключаем?", Options: []runner.Option{
						{ID: "a", Label: "Stripe"}, {ID: "b", Label: "ЮKassa"},
					}},
					{ID: "Q2", Text: "Нужен ли возврат средств?"},
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
		{
			name: "разбивка",
			result: runner.Result{
				Outcome:   runner.OutcomeSplit,
				Summary:   "Постановка описывает две независимые сущности.",
				NextOwner: "human",
				Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
				Split: &runner.Split{Children: []runner.SplitChild{
					{ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий."},
					{ID: "transaction-crud", Title: "Transaction CRUD", Description: "Модель, миграция, CRUD операций.", DependsOn: []string{"category-crud"}},
				}},
			},
			// Человек отвечает на «разбить как предложено?», не видя result.json —
			// без заголовков и текста детей вопрос было бы не на что отвечать.
			want:   []string{"Category CRUD", "Модель, миграция, CRUD категорий", "Transaction CRUD", "category-crud", "зависит"},
			absent: []string{"Блокер", "Артефакты"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := ReportBody(marker(string(tc.result.Outcome)), tc.result, tc.branch, runner.Usage{})
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

// Цена задачи видна там же, где её история, — в тикете. В маркере ей не место:
// маркер читает раннер, а числа расхода нужны человеку, и меняются они у каждого
// прогона.
func TestReportBodyShowsSpending(t *testing.T) {
	body := ReportBody(marker("done"), runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Сделано.", NextOwner: "none",
	}, "", runner.Usage{CostUSD: 0.2227, DurationMS: 48797, Turns: 6})

	if !strings.Contains(body, "$0.2227") {
		t.Errorf("стоимости нет в теле:\n%s", body)
	}
	first, _, _ := strings.Cut(body, "\n")
	if strings.Contains(first, "0.2227") {
		t.Errorf("стоимость попала в маркер: %s", first)
	}
}

// Расход прогона, убитого на середине, неизвестен. Написать про него «$0.0000»
// значило бы соврать: ноль — это цена, а её никто не называл.
func TestReportBodySilentAboutUnknownSpending(t *testing.T) {
	body := ReportBody(marker("failed"), runner.Result{
		Outcome: runner.OutcomeFailed, Summary: "Не вышло.", NextOwner: "human",
	}, "", runner.Usage{})

	if strings.Contains(body, "$") {
		t.Errorf("о неизвестном расходе всё-таки сказано:\n%s", body)
	}
}

// Строку читает человек, поэтому время огрублено, а шаги склоняются.
func TestSpendLineReadsLikeRussian(t *testing.T) {
	cases := []struct {
		usage runner.Usage
		want  string
	}{
		{runner.Usage{CostUSD: 0.0922, DurationMS: 18258, Turns: 3}, "Прогон: $0.0922, 18 с, 3 шага."},
		{runner.Usage{CostUSD: 0.3572, DurationMS: 96673, Turns: 21}, "Прогон: $0.3572, 1 м 36 с, 21 шаг."},
		{runner.Usage{CostUSD: 1.5, DurationMS: 3661000, Turns: 5}, "Прогон: $1.5000, 61 м 1 с, 5 шагов."},
	}
	for _, tc := range cases {
		if got := SpendLine(tc.usage); got != tc.want {
			t.Errorf("строка расхода %q, ожидалось %q", got, tc.want)
		}
	}
	if got := SpendLine(runner.Usage{}); got != "" {
		t.Errorf("о неизвестном расходе сказано %q", got)
	}
}

// Описание и depends_on — вложенные пункты списка (`  - ...`), не ленивое
// продолжение абзаца (`  ...`): второе визуально склеивается со строкой
// заголовка и в markdown, и не переводится вовсе в JIRA wiki (jira/wiki.go
// переводит только строки, начинающиеся с "-"/"*"/"+" после отступа —
// mdBullet), из-за чего список рвётся на каждом ребёнке.
func TestSplitBlockNestsDescriptionAsListItem(t *testing.T) {
	block := SplitBlock(&runner.Split{Children: []runner.SplitChild{
		{ID: "a", Title: "Category CRUD", Description: "Модель, миграция, CRUD.", DependsOn: []string{"b"}},
	}})
	for _, want := range []string{"\n  - Модель, миграция, CRUD.\n", "\n  - зависит от: b\n"} {
		if !strings.Contains(block, want) {
			t.Errorf("нет вложенного пункта %q в:\n%s", want, block)
		}
	}
}

// Разбивка печатается раньше вопроса, который на неё ссылается: иначе
// человек читает «ответьте комментарием: Q1: <текст>» до того, как увидел,
// что вообще предложено, и подсказка про ответ повисает без контекста.
func TestReportBodyShowsSplitBeforeQuestions(t *testing.T) {
	body := ReportBody(marker("split"), runner.Result{
		Outcome:   runner.OutcomeSplit,
		Summary:   "Постановка описывает две сущности.",
		NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
		Split: &runner.Split{Children: []runner.SplitChild{
			{ID: "a", Title: "Category CRUD", Description: "Модель, миграция, CRUD."},
		}},
	}, "", runner.Usage{})

	split := strings.Index(body, "## Разбивка")
	questions := strings.Index(body, QuestionsHeading)
	if split == -1 || questions == -1 || split > questions {
		t.Errorf("«## Разбивка» (%d) не раньше «%s» (%d):\n%s", split, QuestionsHeading, questions, body)
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
