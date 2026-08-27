package tracker

import (
	"fmt"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// ReportBody собирает комментарий об исходе прогона.
//
// Устройство простое: маркер первой строкой — для раннера, дальше проза —
// для человека. Многословное в тикет не едет: в артефактах лежат ссылки
// на ветку, пути и коммиты, а сам материал — в git и в архиве прогонов.
//
// Пустые разделы не печатаются: комментарий с заголовком «Вопросы» и пустотой
// под ним читается как потерянный текст.
func ReportBody(m Marker, res runner.Result, branch string, usage runner.Usage) string {
	var b strings.Builder

	b.WriteString(m.String())
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(res.Summary))
	b.WriteString("\n")

	if details := strings.TrimSpace(res.DetailsMD); details != "" {
		fmt.Fprintf(&b, "\n## Подробности\n\n%s\n", details)
	}

	// Вопросы печатаются по грамматике протокола, а не как придётся: этот же
	// раздел раннер потом разбирает, чтобы понять, на что человек отвечает.
	if block := QuestionsBlock(res.Questions); block != "" {
		fmt.Fprintf(&b, "\n%s", block)
	}

	if blocker := strings.TrimSpace(res.Blocker); blocker != "" {
		fmt.Fprintf(&b, "\n## Блокер\n\n%s\n", blocker)
	}

	if branch != "" || len(res.Artifacts) > 0 {
		b.WriteString("\n## Артефакты\n\n")
		if branch != "" {
			fmt.Fprintf(&b, "- ветка: %s\n", branch)
		}
		for _, artifact := range res.Artifacts {
			fmt.Fprintf(&b, "- %s\n", artifact)
		}
	}

	// Цена — последней строкой и в теле, а не в маркере: маркер читает раннер,
	// а расход нужен человеку. Так на доске видно, во что обошлась задача,
	// и так же его когда-нибудь сможет собрать из переписки другая машина —
	// реестр прогонов у каждой свой.
	if spend := SpendLine(usage); spend != "" {
		fmt.Fprintf(&b, "\n%s\n", spend)
	}

	return b.String()
}

// SpendLine — во что обошёлся прогон, одной строкой для человека.
// Неизвестный расход не превращается в «$0.0000»: ноль — это цена, а её
// в таком прогоне никто не называл.
//
// Экспортирована ради двух путей, где отчёта агента не пишется вовсе:
// неудачной публикации и потерянной аренды. Там о прогоне рассказывает запись
// раннера, и цена должна быть названа теми же словами.
func SpendLine(u runner.Usage) string {
	if !u.Known() {
		return ""
	}
	return fmt.Sprintf("Прогон: $%.4f, %s, %s.", u.CostUSD, spendTime(u.Duration()), spendTurns(u.Turns))
}

// spendTime огрубляет длительность: секунды до минуты, минуты и секунды дальше.
// Часов нет намеренно — прогон, идущий час, упирается в таймаут роли раньше.
func spendTime(d time.Duration) string {
	if seconds := int(d.Seconds()); seconds < 60 {
		return fmt.Sprintf("%d с", seconds)
	}
	return fmt.Sprintf("%d м %d с", int(d.Minutes()), int(d.Seconds())%60)
}

// spendTurns склоняет шаги: «3 шага» и «5 шагов» — разные слова, и строку
// читает человек.
func spendTurns(n int) string {
	word := "шагов"
	switch last := n % 10; {
	case n%100 >= 11 && n%100 <= 14:
	case last == 1:
		word = "шаг"
	case last >= 2 && last <= 4:
		word = "шага"
	}
	return fmt.Sprintf("%d %s", n, word)
}

// NoticeBody собирает системную запись: маркер и одна мысль прозой.
// Ими раннер объясняет человеку то, чего агент не делал, — возврат задачи
// по истёкшей аренде, разбор ответа, потерю аренды.
func NoticeBody(m Marker, text string) string {
	return m.String() + "\n" + strings.TrimSpace(text) + "\n"
}
