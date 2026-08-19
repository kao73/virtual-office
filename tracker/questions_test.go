package tracker

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
)

// accounts — учётки офиса: всё, написанное не ими, считается словами человека.
var accounts = []string{"office", "office-analyst", "office-implementer", "office-reviewer"}

// fixtureQuestions — три вопроса разной природы: с вариантами, да/нет и свободный.
func fixtureQuestions() []runner.Question {
	return []runner.Question{
		{ID: "Q1", Text: "Идемпотентность или скорость?", Options: []runner.Option{
			{ID: "a", Label: "идемпотентность"},
			{ID: "b", Label: "скорость"},
		}},
		{ID: "Q2", Text: "Оставить старый endpoint?", Options: []runner.Option{
			{ID: "yes", Label: "да"},
			{ID: "no", Label: "нет"},
		}},
		{ID: "Q3", Text: "Какой формат даты в экспорте?"},
	}
}

// asked — отчёт роли с вопросами, напечатанный тем же кодом, что печатает раннер.
// Пересказывать грамматику в тесте нельзя: разойдись рендер с разбором — тест
// проверял бы себя, а не протокол.
func asked(role string, questions []runner.Question, minute int) Comment {
	m := Marker{RunID: runID, Role: role, Outcome: "needs_human", Next: "human", ConfigSHA: "5bc6a3b0"}
	body := ReportBody(m, runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.",
		NextOwner: "human", Questions: questions,
	}, "", runner.Usage{})
	return comment("office-"+role, body, minute)
}

// Раннер разбирает свой же блок: результат прогона живёт в архиве той машины,
// где прогон был, а тикет видят все.
func TestQuestionsRoundTrip(t *testing.T) {
	want := fixtureQuestions()
	body := ReportBody(Marker{RunID: runID, Role: "analyst", Outcome: "needs_human", ConfigSHA: "5bc6a3b0"},
		runner.Result{Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human", Questions: want},
		"", runner.Usage{})

	got := ParseQuestions(body)
	if len(got) != len(want) {
		t.Fatalf("разобрано %d вопросов, ожидалось %d:\n%s", len(got), len(want), body)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Text != want[i].Text {
			t.Errorf("вопрос %d: %+v, ожидался %+v", i, got[i], want[i])
		}
		if len(got[i].Options) != len(want[i].Options) {
			t.Fatalf("вопрос %s: вариантов %d, ожидалось %d", want[i].ID, len(got[i].Options), len(want[i].Options))
		}
		for j := range want[i].Options {
			if got[i].Options[j] != want[i].Options[j] {
				t.Errorf("вопрос %s, вариант %d: %+v, ожидался %+v", want[i].ID, j, got[i].Options[j], want[i].Options[j])
			}
		}
	}

	// Человеку в отчёте нужна не только сама тройка вопросов, но и то, как на неё
	// отвечать: без подсказки формат знает только раннер.
	if !strings.Contains(body, "Ответьте комментарием: `Q1: a`, `Q3: <текст>`") {
		t.Errorf("в отчёте нет подсказки про формат ответа:\n%s", body)
	}
}

// Комментарий без раздела вопросов даёт пустой список, а не мусор: разбор
// зовётся на любом последнем отчёте роли.
func TestParseQuestionsWithoutBlock(t *testing.T) {
	body := ReportBody(Marker{RunID: runID, Role: "analyst", Outcome: "done", ConfigSHA: "5bc6a3b0"},
		runner.Result{Outcome: runner.OutcomeDone, Summary: "План готов.", NextOwner: "implementer"}, "", runner.Usage{})

	if got := ParseQuestions(body); len(got) != 0 {
		t.Errorf("в отчёте без вопросов нашлись вопросы: %+v", got)
	}
}

func TestHumanAnswers(t *testing.T) {
	cases := map[string]struct {
		comments []Comment
		want     map[string]string // Q → как это выглядит в ответе; пусто — ответа нет
		none     bool              // разбирать нечего
	}{
		"выбор вариантов и свободный ответ": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: b\nQ3: ISO-8601", 2),
			},
			want: map[string]string{"Q1": "b", "Q2": "", "Q3": "ISO-8601"},
		},
		"ответ в двух репликах": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: a", 2),
				comment("человек", "И ещё:\nQ2: no", 3),
			},
			want: map[string]string{"Q1": "a", "Q2": "no", "Q3": ""},
		},
		"человек передумал": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: a", 2),
				comment("человек", "Передумал:\nQ1: b", 3),
			},
			want: map[string]string{"Q1": "b", "Q2": "", "Q3": ""},
		},
		"ответ подписью варианта и в другом регистре": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: СКОРОСТЬ", 2),
			},
			want: map[string]string{"Q1": "СКОРОСТЬ", "Q2": "", "Q3": ""},
		},
		"двоеточие внутри ответа": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q3: формат: ISO-8601 с зоной", 2),
			},
			want: map[string]string{"Q1": "", "Q2": "", "Q3": "формат: ISO-8601 с зоной"},
		},
		// Ответ мимо вариантов раннер не отвергает: решать за человека — значит
		// терять то, что он сказал.
		"ответ мимо вариантов": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: и то и другое", 2),
			},
			want: map[string]string{"Q1": "и то и другое", "Q2": "", "Q3": ""},
		},
		// Метка есть, а вопроса с ней не было: разбирать нечего, слова человека
		// целы в хвосте переписки.
		"метка чужого вопроса": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q9: да", 2),
			},
			none: true,
		},
		// Метка посреди фразы ответом не считается: «не понял Q1: что это значит»
		// — вопрос человека, а не выбор. Строка протокола начинается с метки,
		// и подсказка в отчёте просит именно этого.
		"метка посреди фразы": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Не понял Q1: что значит идемпотентность?", 2),
			},
			none: true,
		},
		"ответ прозой": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Делай как быстрее, формат любой.", 2),
			},
			none: true,
		},
		// Разбор ответа сам пишет запись в тикет, и вторая реплика человека идёт
		// уже после неё: окно ответов режется отчётом роли, а не любой записью офиса.
		"вторая реплика после разбора": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: a", 2),
				notice("analyst", EventHumanReply, 3),
				comment("человек", "И вот ещё:\nQ3: ISO-8601", 4),
			},
			want: map[string]string{"Q1": "a", "Q2": "", "Q3": "ISO-8601"},
		},
		// Офис говорит своими же словами: цитата метки в записи раннера ответом
		// человека не является.
		"метка в записи офиса": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("office", "Напоминаю: ждём Q1: b", 2),
			},
			none: true,
		},
		// Есть отчёт новее — значит по ответам уже отработали. Иначе выбор прилип
		// бы к роли навсегда и вернулся к ней позавчерашним как свежий.
		"вопросы уже отработаны": {
			comments: []Comment{
				asked("analyst", fixtureQuestions(), 1),
				comment("человек", "Q1: b", 2),
				report("analyst", "done", 3),
				comment("человек", "Ещё мысль.", 4),
			},
			none: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := HumanAnswers(tc.comments, "analyst", accounts)
			if tc.none {
				if len(got) != 0 {
					t.Fatalf("разобрано лишнее: %+v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ответов %d, ожидалось %d: %+v", len(got), len(tc.want), got)
			}
			for _, answer := range got {
				want, known := tc.want[answer.Question.ID]
				if !known {
					t.Errorf("лишний вопрос в ответах: %s", answer.Question.ID)
					continue
				}
				if answer.Text != want {
					t.Errorf("%s: ответ %q, ожидался %q", answer.Question.ID, answer.Text, want)
				}
			}
		})
	}
}

// Вопросы одной роли — не контекст другой: у каждой своя нить разговора,
// и ответ адресован тому, кто спрашивал.
func TestHumanAnswersOnlyForAskingRole(t *testing.T) {
	comments := []Comment{
		asked("analyst", fixtureQuestions(), 1),
		comment("человек", "Q1: b", 2),
	}

	if got := HumanAnswers(comments, "implementer", accounts); len(got) != 0 {
		t.Errorf("ответы аналитику попали разработчику: %+v", got)
	}
}

// Выбранный вариант раннер называет подписью: агенту нужен смысл ответа,
// а не буква.
func TestHumanAnswersResolveLabels(t *testing.T) {
	comments := []Comment{
		asked("analyst", fixtureQuestions(), 1),
		comment("человек", "Q1: b\nQ2: yes\nQ3: ISO-8601", 2),
	}

	got := HumanAnswers(comments, "analyst", accounts)
	want := map[string]string{"Q1": "скорость", "Q2": "да", "Q3": ""}
	for _, answer := range got {
		if answer.Label != want[answer.Question.ID] {
			t.Errorf("%s: подпись %q, ожидалась %q", answer.Question.ID, answer.Label, want[answer.Question.ID])
		}
		if answer.OffOptions() {
			t.Errorf("%s: ответ сочтён посторонним", answer.Question.ID)
		}
	}
}

func TestAnswerOffOptions(t *testing.T) {
	comments := []Comment{
		asked("analyst", fixtureQuestions(), 1),
		comment("человек", "Q1: и то и другое", 2),
	}

	got := HumanAnswers(comments, "analyst", accounts)
	if len(got) == 0 || !got[0].OffOptions() {
		t.Fatalf("ответ мимо вариантов не помечен: %+v", got)
	}
	if got[0].Text != "и то и другое" {
		t.Errorf("ответ искажён: %q", got[0].Text)
	}
}

// Живой прогон отвечает не буквами: идентификаторы вариантов приезжают словами,
// а подписи — со скобками (`other) Другая (указать)`). Разбор обязан это
// переживать: форма взята из настоящего результата, см. docs/notes/stage-4-smoke.md.
func TestQuestionsRoundTripRealShape(t *testing.T) {
	want := []runner.Question{
		{ID: "Q1", Text: "Какую платёжную систему интегрировать?", Options: []runner.Option{
			{ID: "stripe", Label: "Stripe"},
			{ID: "yookassa", Label: "ЮKassa"},
			{ID: "other", Label: "Другая (указать)"},
		}},
		{ID: "Q2", Text: "На каком стеке делать интеграцию, если репозиторий пуст?"},
	}

	block := asked("analyst", want, 1)
	got := ParseQuestions(block.Body)
	if len(got) != 2 || len(got[0].Options) != 3 {
		t.Fatalf("разобрано %+v", got)
	}
	if got[0].Options[2] != (runner.Option{ID: "other", Label: "Другая (указать)"}) {
		t.Errorf("вариант со скобками искажён: %+v", got[0].Options[2])
	}

	answers := HumanAnswers([]Comment{block, comment("человек", "Q1: other\nQ2: python", 2)}, "analyst", accounts)
	if len(answers) != 2 {
		t.Fatalf("ответов %d, ожидалось 2: %+v", len(answers), answers)
	}
	if answers[0].Label != "Другая (указать)" {
		t.Errorf("выбор словом не сопоставлен с вариантом: %+v", answers[0])
	}
	if answers[1].Text != "python" || answers[1].Label != "" {
		t.Errorf("свободный ответ искажён: %+v", answers[1])
	}
}
