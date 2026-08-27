package tracker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
)

// QuestionsHeading — заголовок раздела вопросов, как его печатает раннер.
// Офис пишет markdown; читая раздел обратно, его узнают шире — см. IsQuestionsHeading.
const QuestionsHeading = "## Вопросы"

// questionsTitle — текст заголовка без разметки. Якорем раздела служит именно он:
// разметку вокруг него трекер вправе заменить своей.
const questionsTitle = "Вопросы"

// headingLine — строка-заголовок в любой из двух разметок, которыми офис пишет
// в трекеры: markdown (`## Текст`) и wiki JIRA Server (`h2. Текст`).
//
// Обе формы разбираются здесь, а не в реализации трекера, и это не уступка JIRA.
// Раздел вопросов — протокол: раннер его печатает и он же потом разбирает, а между
// печатью и разбором лежит трекер, который вправе хранить тело в своей разметке.
// Значит, якорем не может быть точная строка — им может быть только заголовок
// с этим текстом, как бы он ни был написан.
//
// Отступ допускается не больше трёх пробелов — правило самого markdown: четыре
// пробела означают блок кода, и заголовок в нём заголовком не является.
var headingLine = regexp.MustCompile(`^ {0,3}(?:#{1,6}|[hH][1-6]\.)\s+(.*)$`)

// heading — текст заголовка, если строка им является.
func heading(line string) (string, bool) {
	m := headingLine.FindStringSubmatch(strings.TrimRight(line, " \t"))
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// IsQuestionsHeading — открывает ли строка раздел вопросов.
func IsQuestionsHeading(line string) bool {
	title, ok := heading(line)
	return ok && title == questionsTitle
}

// Раздел «Вопросы» — машиночитаемая часть протокола, а не украшение отчёта.
// Раннер печатает его сам и сам же потом разбирает: тикет — единственное
// хранилище, общее для двух прогонов на разных машинах, и вопрос, заданный
// вчера, узнаётся только оттуда.
//
// Грамматика:
//
//	## Вопросы
//
//	Q1: Идемпотентность или скорость?
//	  a) идемпотентность
//	  b) скорость
//
//	Q3: Какой формат даты в экспорте?
//
//	Ответьте комментарием: `Q1: a`, `Q3: <текст>`; можно и прозой.
var (
	questionLine = regexp.MustCompile(`^(Q\d+):\s+(.+)$`)
	optionLine   = regexp.MustCompile(`^\s+(\S+)\)\s+(.+)$`)
	// answerLine — ответ человека. Разделитель не один: канонический вид
	// подсказан в самом отчёте, но человек пишет как пишет, и терять его выбор
	// из-за тире вместо двоеточия было бы глупо.
	answerLine = regexp.MustCompile(`^\s*(Q\d+)\s*[:.)\-–—]\s*(.+)$`)
)

// answerHint — последняя строка раздела: как отвечать. Печатается с примерами
// из самих вопросов, а не абстрактными: человек копирует то, что видит.
func answerHint(questions []runner.Question) string {
	var examples []string
	for _, q := range questions {
		if len(q.Options) > 0 {
			examples = append(examples, fmt.Sprintf("`%s: %s`", q.ID, q.Options[0].ID))
			break
		}
	}
	for _, q := range questions {
		if len(q.Options) == 0 {
			examples = append(examples, fmt.Sprintf("`%s: <текст>`", q.ID))
			break
		}
	}
	if len(examples) == 0 {
		return ""
	}
	return answerHintPrefix + strings.Join(examples, ", ") + "; можно и прозой."
}

// QuestionsBlock печатает раздел вопросов для тела отчёта.
func QuestionsBlock(questions []runner.Question) string {
	if len(questions) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(QuestionsHeading + "\n")
	for _, q := range questions {
		fmt.Fprintf(&b, "\n%s: %s\n", q.ID, strings.TrimSpace(q.Text))
		for _, o := range q.Options {
			fmt.Fprintf(&b, "  %s) %s\n", o.ID, strings.TrimSpace(o.Label))
		}
	}
	if hint := answerHint(questions); hint != "" {
		fmt.Fprintf(&b, "\n%s\n", hint)
	}
	return b.String()
}

// ParseQuestions достаёт вопросы из тела отчёта — того самого, который раннер
// напечатал прошлым прогоном.
//
// Разбор свой же, и это не расточительство, а единственный способ: результат
// прогона живёт в архиве на той машине, где прогон был, а тикет видят все.
// Комментарий без раздела вопросов даёт пустой список.
func ParseQuestions(body string) []runner.Question {
	var questions []runner.Question

	inside := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case IsQuestionsHeading(line):
			inside = true
			continue
		case !inside:
			continue
		// Раздел кончается следующим заголовком или подсказкой про ответ:
		// дальше идёт проза для человека, и метки в ней уже не наши.
		case IsHeading(line):
			return questions
		case IsAnswerHint(line):
			return questions
		}

		if m := questionLine.FindStringSubmatch(line); m != nil {
			questions = append(questions, runner.Question{ID: m[1], Text: strings.TrimSpace(m[2])})
			continue
		}
		if m := optionLine.FindStringSubmatch(line); m != nil && len(questions) > 0 {
			last := &questions[len(questions)-1]
			last.Options = append(last.Options, runner.Option{ID: m[1], Label: strings.TrimSpace(m[2])})
		}
	}
	return questions
}

// answerHintPrefix — начало последней строки раздела вопросов: как отвечать.
// Строка эта — граница раздела, а не украшение: на ней разбор останавливается,
// и по ней же трекер понимает, где кончается непереводимое тело.
const answerHintPrefix = "Ответьте комментарием: "

// IsAnswerHint — та ли это строка, которой раздел вопросов кончается.
func IsAnswerHint(line string) bool {
	return strings.HasPrefix(line, strings.TrimSuffix(answerHintPrefix, ": "))
}

// IsHeading — заголовок ли это, безразлично какой. Им кончается раздел вопросов —
// и при разборе, и при записи: трекер, переводящий тело в свою разметку, обязан
// найти границу раздела там же, где её потом найдёт ParseQuestions.
func IsHeading(line string) bool {
	_, ok := heading(line)
	return ok
}

// Answer — ответ человека на один вопрос прогона.
type Answer struct {
	Question runner.Question
	// Text — что человек написал, как есть. Пусто — на этот вопрос он не ответил.
	Text string
	// Label — подпись выбранного варианта. Пусто означает либо свободный вопрос,
	// либо ответ мимо вариантов; и то и другое законно, решает роль.
	Label string
}

// Answered — ответил ли человек на этот вопрос.
func (a Answer) Answered() bool { return a.Text != "" }

// OffOptions — назван ли ответ, которого не было в вариантах. Валидировать
// человека раннер не станет: отвергнуть его ответ значит его потерять.
func (a Answer) OffOptions() bool {
	return a.Answered() && a.Label == "" && len(a.Question.Options) > 0
}

// HumanAnswers — ответы человека на вопросы последнего отчёта роли.
//
// Разбор без состояния: между вопросом и ответом лежит другой тик, а то
// и другой процесс, и помнить заданный вопрос раннеру негде. Поэтому вопросы
// каждый раз читаются из тикета заново.
//
// Берётся **последний** отчёт роли, а не последний отчёт с вопросами. Разница
// принципиальная: есть отчёт новее — значит на вопросы уже ответили и работа
// по ним сделана. Иначе ответ прилип бы к роли навсегда, и на круге
// implementer → analyst аналитик получил бы позавчерашний выбор как свежий.
//
// Окно ответов — всё, что человек сказал после этого отчёта. Не «до следующей
// записи офиса»: разбор ответа сам пишет запись `human-reply`, и вторая реплика
// человека после неё выпала бы.
//
// Пустой ответ означает, что разбирать нечего: ни одной строки `Qn: ...`
// человек не написал. Его слова от этого не пропадают — они в хвосте переписки,
// как было всегда.
func HumanAnswers(comments []Comment, role string, accounts []string) []Answer {
	last := lastOfRole(comments, role, func(m Marker) bool { return m.Outcome != "" })
	if last < 0 {
		return nil
	}
	questions := ParseQuestions(comments[last].Body)
	if len(questions) == 0 {
		return nil
	}

	said := map[string]string{}
	for _, c := range comments[last+1:] {
		if slices.Contains(accounts, c.Author) {
			continue // это офис: свои же вопросы за ответ не считаем
		}
		for id, text := range answers(c.Body) {
			said[id] = text // человек вправе передумать: побеждает сказанное позже
		}
	}
	if len(said) == 0 {
		return nil
	}

	out := make([]Answer, 0, len(questions))
	answered := 0
	for _, q := range questions {
		answer := Answer{Question: q, Text: strings.TrimSpace(said[q.ID])}
		if answer.Text != "" {
			answer.Label = labelOf(q, answer.Text)
			answered++
		}
		out = append(out, answer)
	}
	// Метки нашлись, но ни одна не о наших вопросах: человек говорил о чём-то
	// своём. Раздел «Ответы человека», в котором на всё «ответа нет», сообщил бы
	// ровно ничего, а сказанное лежит в хвосте переписки целиком.
	if answered == 0 {
		return nil
	}
	return out
}

// answers достаёт из реплики человека строки вида `Q1: b`.
func answers(body string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if m := answerLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			found[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return found
}

// labelOf сопоставляет ответ с вариантом — по идентификатору, а если человек
// написал словами, то и по подписи. Регистр не важен: `Q1: B` — тот же выбор,
// что `Q1: b`.
func labelOf(q runner.Question, answer string) string {
	for _, o := range q.Options {
		if strings.EqualFold(answer, o.ID) || strings.EqualFold(answer, o.Label) {
			return o.Label
		}
	}
	return ""
}
