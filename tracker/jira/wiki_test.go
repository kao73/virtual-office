package jira

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
)

// Перевод построчно: что во что превращается. Таблица здесь уместнее отдельных
// тестов — правил много, каждое в одну строку, и разъехаться они могут только
// все сразу.
func TestWikiTranslatesNamedConstructs(t *testing.T) {
	cases := []struct{ name, md, want string }{
		{"заголовок второго уровня", "## Подробности", "h2. Подробности"},
		{"заголовок первого уровня", "# Зачем", "h1. Зачем"},
		{"заголовок третьего уровня", "### Риски", "h3. Риски"},
		{"пункт списка", "- ветка: agent/VO-1", "* ветка: agent/VO-1"},
		{"пункт через звёздочку", "* пункт", "* пункт"},
		{"вложенный пункт", "  - вложенный", "** вложенный"},
		{"нумерованный пункт", "1. первый", "# первый"},
		{"вложенный нумерованный", "  2. второй", "## второй"},
		{"цитата", "> он сказал", "bq. он сказал"},
		{"черта", "---", "----"},
		{"код в строке", "смотри `result.json` рядом", "смотри {{result.json}} рядом"},
		{"жирный", "это **важно** знать", "это *важно* знать"},
		{"ссылка на адрес", "см. [проба](https://example.com/x)", "см. [проба|https://example.com/x]"},
		{"ссылка на путь", "см. [контракт](docs/contracts/agent-io.md)", "см. контракт (docs/contracts/agent-io.md)"},
		{"скобка в самом пути", "см. [пробу](docs/a[1].md)", `см. пробу (docs/a\[1].md)`},
		{"почта", "пиши [сюда](mailto:kao@example.com)", "пиши [сюда|mailto:kao@example.com]"},
		{"два жирных куска", "**раз** и **два**", "*раз* и *два*"},
		{"галочка плана", "- [x] пункт сделан", `* \[x] пункт сделан`},
		{"сноска", "см. пункт [1] в списке", `см. пункт \[1] в списке`},
		{"скобки в коде строки", "отметил `- [x] пункт`", `отметил {{- \[x] пункт}}`},
		{"уже экранированная", `текст \[x] как есть`, `текст \[x] как есть`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wiki(c.md); got != c.want {
				t.Errorf("%q → %q, ожидалось %q", c.md, got, c.want)
			}
		})
	}
}

// Чего конвертер не делает — тоже правило, и оно проверяется так же, как то,
// что он делает. Без этих строк «не трогаем» держалось бы на честном слове.
func TestWikiLeavesUnnamedConstructs(t *testing.T) {
	cases := []struct{ name, md string }{
		{"одиночная звёздочка", "тут *курсив* по-markdown"},
		{"зачёркнутое", "было ~~не так~~"},
		{"html", "<b>жирный</b> тегом"},
		{"голый адрес", "https://github.com/kao73/office-pr-probe/pull/1"},
		{"закрывающая скобка", "конец списка] сам по себе"},
		{"фигурные скобки в прозе", "шаблон {ключ} остаётся собой"},
		{"строка цены", "Прогон: $0.2133, 40 с, 12 шагов."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wiki(c.md); got != c.md {
				t.Errorf("%q переведено в %q, а не должно было", c.md, got)
			}
		})
	}
}

// Блок кода — не проза: внутри не переводится ничего, иначе агент скопировал бы
// из тикета команду, которой не запускал.
func TestWikiKeepsCodeBlockVerbatim(t *testing.T) {
	body := "```go\n// ## не заголовок\nfmt.Println(\"**не жирный**\")\n```"
	want := "{code:go}\n// ## не заголовок\nfmt.Println(\"**не жирный**\")\n{code}"
	if got := wiki(body); got != want {
		t.Errorf("блок кода переведён как\n%s\nожидалось\n%s", got, want)
	}

	if got := wiki("```\nголый забор\n```"); got != "{code}\nголый забор\n{code}" {
		t.Errorf("забор без языка переведён как %q", got)
	}

	// Незакрытый забор закрывается сам: недописанный блок лучше разъехавшейся
	// разметки до конца записи.
	if got := wiki("```\nзабыли закрыть"); got != "{code}\nзабыли закрыть\n{code}" {
		t.Errorf("незакрытый забор переведён как %q", got)
	}
}

func TestWikiTranslatesTable(t *testing.T) {
	body := "| событие | кто |\n|---|---|\n| `merged` | система |"
	want := "|| событие || кто ||\n| {{merged}} | система |"
	if got := wiki(body); got != want {
		t.Errorf("таблица переведена как\n%s\nожидалось\n%s", got, want)
	}

	// Палки без строки-разделителя таблицей не являются: это просто текст.
	plain := "| а | б |\nпросто текст"
	if got := wiki(plain); got != plain {
		t.Errorf("текст с палками принят за таблицу: %q", got)
	}
}

// Маркер узнаётся разбором, а не позицией: правило, проверяемое по следствию,
// однажды проверит не то.
func TestWikiKeepsMarkerLine(t *testing.T) {
	marker := "[office run:abc12345 role:reviewer outcome:done next:human config:9f2e1c]"
	got := wiki(marker + "\n## Подробности")
	if !strings.HasPrefix(got, marker+"\n") {
		t.Errorf("маркер переведён: %q", got)
	}
	if !strings.Contains(got, "h2. Подробности") {
		t.Errorf("проза после маркера не переведена: %q", got)
	}

	// Первая строка, не разбирающаяся как маркер, — обычный текст, и переводится.
	if got := wiki("## Не маркер"); got != "h2. Не маркер" {
		t.Errorf("первая строка без маркера не переведена: %q", got)
	}
}

// Раздел вопросов — протокол, а не украшение: раннер его печатает и он же потом
// разбирает. Граница у перевода та же, что у ParseQuestions.
func TestWikiKeepsQuestionsBlock(t *testing.T) {
	body := strings.Join([]string{
		"## Подробности",
		"",
		"проза с `кодом`",
		"",
		"## Вопросы",
		"",
		"Q1: Идемпотентность или скорость?",
		"  a) идемпотентность",
		"  b) скорость",
		"",
		"Ответьте комментарием: `Q1: a`; можно и прозой.",
		"",
		"## Артефакты",
		"",
		"- ветка: agent/VO-1",
	}, "\n")

	got := wiki(body)
	for _, keep := range []string{
		"Q1: Идемпотентность или скорость?",
		"  a) идемпотентность",
		"Ответьте комментарием: `Q1: a`; можно и прозой.",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("строка протокола не доехала: %q\nтело:\n%s", keep, got)
		}
	}
	// Заголовок раздела — единственная его строка, которая переводится: `##`
	// в wiki означает вложенный пункт списка, и непереведённый заголовок
	// исчезал бы. Якорем раздела служит текст заголовка, а не точная строка.
	if strings.Contains(got, "## Вопросы") || !strings.Contains(got, "h2. Вопросы") {
		t.Errorf("заголовок раздела вопросов не переведён:\n%s", got)
	}
	if !tracker.IsQuestionsHeading("h2. Вопросы") {
		t.Error("переведённый заголовок не узнаётся якорем раздела")
	}
	if questions := tracker.ParseQuestions(got); len(questions) != 1 || len(questions[0].Options) != 2 {
		t.Errorf("переведённое тело больше не разбирается: %+v", questions)
	}
	for _, want := range []string{"h2. Подробности", "проза с {{кодом}}", "h2. Артефакты", "* ветка: agent/VO-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("проза вне раздела не переведена: %q\nтело:\n%s", want, got)
		}
	}
}

// Круг «записали — прочитали» на живом теле отчёта. Проверяется не разметка,
// а то, ради чего от неё оберегают машинные куски: раннер обязан узнать
// в прочитанном свой маркер, свои вопросы и свой адрес.
func TestReportSurvivesRoundTrip(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.runID = "прогон-1"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

	marker := tracker.Marker{
		RunID: "abc12345", Role: "reviewer", Outcome: "needs_human",
		Next: "human", ConfigSHA: "9f2e1c",
	}
	res := runner.Result{
		Outcome:   "needs_human",
		Summary:   "Разбор упёрся в **вопрос** к человеку.",
		DetailsMD: "## Что смотрел\n\n- `pipeline/prpass.go`\n- тесты\n",
		Questions: []runner.Question{{
			ID: "Q1", Text: "Идемпотентность или скорость?",
			Options: []runner.Option{{ID: "a", Label: "идемпотентность"}, {ID: "b", Label: "скорость"}},
		}},
		Artifacts: []string{"https://github.com/kao73/office-pr-probe/pull/1"},
		NextOwner: "human",
	}
	body := tracker.ReportBody(marker, res, "agent/VO-1", runner.Usage{CostUSD: 0.21, DurationMS: 40000, Turns: 12})

	if err := tr.Comment("VO-1", tracker.ByRun("прогон-1"), body); err != nil {
		t.Fatalf("комментарий не записан: %v", err)
	}
	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	stored := task.Comments[0].Body

	back, ok := tracker.MarkerOf(stored)
	if !ok {
		t.Fatalf("маркер не разобрался после круга:\n%s", stored)
	}
	if back != marker {
		t.Errorf("маркер приехал как %+v, отправляли %+v", back, marker)
	}

	questions := tracker.ParseQuestions(stored)
	if len(questions) != 1 || questions[0].ID != "Q1" || len(questions[0].Options) != 2 {
		t.Errorf("вопросы после круга: %+v\nтело:\n%s", questions, stored)
	}
	if questions[0].Options[1].Label != "скорость" {
		t.Errorf("подпись варианта потеряна: %+v", questions[0].Options)
	}
	if !strings.Contains(stored, "https://github.com/kao73/office-pr-probe/pull/1") {
		t.Errorf("адрес в артефактах изменился:\n%s", stored)
	}

	// А проза при этом переведена — иначе круг ничего не доказывал бы.
	if !strings.Contains(stored, "*вопрос*") || !strings.Contains(stored, "h2. Что смотрел") {
		t.Errorf("проза не переведена:\n%s", stored)
	}
}

// Якорь раздела вопросов признаёт и уже переведённую форму `h2. Вопросы`, на которой
// правило заголовков markdown не совпадает вовсе. Перевод обязан пережить её — иначе
// такое тело роняло бы весь тик. Отступ здесь не различие, а просто ещё одна форма.
func TestWikiSurvivesQuestionsHeadingInAnyForm(t *testing.T) {
	cases := map[string]string{
		"с отступом":      "  ## Вопросы\n\nQ1: точно?\n",
		"уже в wiki":      "h2. Вопросы\n\nQ1: точно?\n",
		"третьим уровнем": "### Вопросы\n\nQ1: точно?\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := wiki(body)
			if !strings.Contains(got, "Q1: точно?") {
				t.Errorf("вопрос потерян: %q", got)
			}
			if questions := tracker.ParseQuestions(got); len(questions) != 1 {
				t.Errorf("раздел не разбирается после перевода: %q", got)
			}
		})
	}
}

// Экранируется только та скобка, которая делает из прозы ссылку. Ссылка, которую
// построил сам конвертер, остаётся ссылкой — иначе перевод адреса был бы напрасен.
func TestWikiKeepsOwnLinksUnescaped(t *testing.T) {
	got := wiki("см. [проба](https://example.com/x) и пункт [1]")
	if !strings.Contains(got, "[проба|https://example.com/x]") {
		t.Errorf("своя ссылка экранирована: %q", got)
	}
	if !strings.Contains(got, `\[1]`) {
		t.Errorf("сноска не экранирована: %q", got)
	}
}

// Внутри блока кода не экранируется ничего: там текст, который человек копирует,
// а JIRA скобки в блоке и так не трогает — проверено на живом инстансе.
func TestWikiKeepsBracketsInCodeBlock(t *testing.T) {
	got := wiki("```go\nif a[i] == b[j] { return true }\n```")
	if !strings.Contains(got, "a[i] == b[j]") {
		t.Errorf("скобки в блоке кода тронуты: %q", got)
	}
}

// Ссылка, у которой в подписи код. Роли пишут такие постоянно, а разбор кода
// раньше ссылки развалил бы её на три куска и оставил хвост `](путь)`.
func TestWikiTranslatesLinkWithCodeLabel(t *testing.T) {
	cases := []struct{ name, md, want string }{
		{"путь в подписи", "см. [`docs/x.md`](docs/x.md)", "см. {{docs/x.md}} (docs/x.md)"},
		{"адрес с кодом в подписи", "см. [`проба`](https://example.com/x)", "см. [проба|https://example.com/x]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wiki(c.md); got != c.want {
				t.Errorf("%q → %q, ожидалось %q", c.md, got, c.want)
			}
		})
	}
}

// Границу раздела вопросов при записи и при разборе задаёт одно правило.
// Заголовок следующего раздела с отступом обязан закрыть раздел здесь так же,
// как его закрывает tracker.ParseQuestions.
func TestWikiEndsQuestionsBlockOnIndentedHeading(t *testing.T) {
	body := "## Вопросы\n\nQ1: точно?\n\n  ## Артефакты\n\n- ветка: agent/OFF-1\n"
	got := wiki(body)
	if !strings.Contains(got, "h2. Артефакты") {
		t.Errorf("заголовок с отступом не закрыл раздел: %q", got)
	}
	if !strings.Contains(got, "* ветка: agent/OFF-1") {
		t.Errorf("хвост тела не переведён: %q", got)
	}
	if len(tracker.ParseQuestions(got)) != 1 {
		t.Errorf("раздел не разбирается: %q", got)
	}
}

// Четыре пробела слева — блок кода markdown, а не заголовок. Иначе решётка
// в примере кода уехала бы в разметку JIRA заголовком.
func TestWikiLeavesIndentedCodeAlone(t *testing.T) {
	body := "    ## это пример, а не заголовок"
	if got := wiki(body); got != body {
		t.Errorf("отступ в четыре пробела принят за заголовок: %q", got)
	}
}

// Границу раздела вопросов перевод и разбор ищут одним правилом. Тело, пришедшее
// уже в разметке трекера, — случай не сегодняшний (Comment получает markdown),
// но правило должно быть одно, иначе хвост записи однажды уедет непереведённым.
func TestWikiEndsQuestionsBlockOnWikiHeading(t *testing.T) {
	body := "h2. Вопросы\n\nQ1: точно?\n\nh2. Артефакты\n\n- ветка: agent/OFF-1\n"
	got := wiki(body)
	if !strings.Contains(got, "* ветка: agent/OFF-1") {
		t.Errorf("заголовок в разметке трекера не закрыл раздел: %q", got)
	}
	if len(tracker.ParseQuestions(got)) != 1 {
		t.Errorf("раздел не разбирается: %q", got)
	}
}

// Раздел вопросов кончается там же, где его кончает разбор: заголовком или
// подсказкой про ответ. Без второй границы всё, что раннер напишет после
// вопросов без заголовка, уехало бы в тикет непереведённым.
func TestWikiEndsQuestionsBlockOnAnswerHint(t *testing.T) {
	body := strings.Join([]string{
		"## Вопросы",
		"",
		"Q1: точно?",
		"",
		"Ответьте комментарием: `Q1: a`; можно и прозой.",
		"",
		"Прогон: $0.2133, 40 с, 12 шагов. Смотри `pipeline/prpass.go`.",
	}, "\n")

	got := wiki(body)
	if !strings.Contains(got, "Ответьте комментарием: `Q1: a`; можно и прозой.") {
		t.Errorf("подсказка переведена, а должна ехать как есть: %q", got)
	}
	if !strings.Contains(got, "{{pipeline/prpass.go}}") {
		t.Errorf("хвост после подсказки не переведён: %q", got)
	}
	if len(tracker.ParseQuestions(got)) != 1 {
		t.Errorf("раздел не разбирается: %q", got)
	}
}
