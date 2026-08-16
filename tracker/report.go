package tracker

import (
	"fmt"
	"strings"

	"github.com/kao73/virtual-office/runner"
)

// ReportBody собирает комментарий об исходе прогона.
//
// Устройство простое: маркер первой строкой — для раннера, дальше проза —
// для человека. Многословное в тикет не едет: в артефактах лежат ссылки
// на ветку, пути и коммиты, а сам материал — в git и в архиве прогонов.
//
// Пустые разделы не печатаются: комментарий с заголовком «Вопросы» и пустотой
// под ним читается как потерянный текст.
func ReportBody(m Marker, res runner.Result, branch string) string {
	var b strings.Builder

	b.WriteString(m.String())
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(res.Summary))
	b.WriteString("\n")

	if details := strings.TrimSpace(res.DetailsMD); details != "" {
		fmt.Fprintf(&b, "\n## Подробности\n\n%s\n", details)
	}

	if len(res.Questions) > 0 {
		b.WriteString("\n## Вопросы\n\n")
		for _, q := range res.Questions {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(q.Text))
			for _, option := range q.Options {
				fmt.Fprintf(&b, "  - %s\n", option)
			}
		}
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

	return b.String()
}

// NoticeBody собирает системную запись: маркер и одна мысль прозой.
// Ими раннер объясняет человеку то, чего агент не делал, — возврат задачи
// по истёкшей аренде, разбор ответа, потерю аренды.
func NoticeBody(m Marker, text string) string {
	return m.String() + "\n" + strings.TrimSpace(text) + "\n"
}
