package jira

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kao73/virtual-office/internal/tracker"
)

// Разметка записи. Офис пишет markdown — им пишут роли, им же читается файловый
// трекер, — а JIRA Server понимает свою wiki-разметку и markdown показывает
// как есть: решётками и дефисами.
//
// Перевод делается **на записи** и только на ней. JIRA v2 хранит тело как есть,
// поэтому обратно оно читается тем же текстом, что уехал; обратный перевод был бы
// вторым конвертером, теряющим на каждом круге, и машинные куски записи от него
// всё равно пришлось бы прятать. Цена решения названа: отчёт, прочитанный
// из тикета, приедет wiki-разметкой и в тело pull request, и в хвост переписки,
// который раннер собирает агенту. Читаемо и там, и там.
//
// Переводится названное ниже, остальное едет как есть. Спецсимволы wiki конвертер
// не экранирует — кроме одного: закрыв их все, он испортил бы и читаемость тела,
// и его обратное чтение, а выигрыш был бы редкой фигурной скобкой в прозе.
// Исключение — открывающая квадратная скобка, и оно измерено, а не выведено:
// см. escapeBrackets.

var (
	// Отступ до трёх пробелов — правило самого markdown; четыре означают блок кода.
	// Тот же отступ признаёт tracker.IsHeading, которым здесь ищется конец раздела
	// вопросов, и это не совпадение: границу раздела при записи и при разборе
	// задаёт одно правило, разойдись они — вопрос потерялся бы молча. Само
	// tracker.IsHeading шире здешнего: оно знает и разметку трекера (`h2. Текст`).
	mdHeading = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*)$`)
	mdBullet  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	mdNumber  = regexp.MustCompile(`^(\s*)\d+[.)]\s+(.*)$`)
	mdQuote   = regexp.MustCompile(`^>\s?(.*)$`)

	// mdRow — строка таблицы, mdSep — разделитель под её шапкой. Шапка узнаётся
	// только по разделителю: без него `| а | б |` — просто текст с палками.
	mdRow = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	mdSep = regexp.MustCompile(`^\s*\|(\s*:?-+:?\s*\|)+\s*$`)

	mdCode = regexp.MustCompile("`([^`]+)`")
	mdLink = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
	// Адрес, которому в wiki есть куда вести. Всё остальное — путь в репозитории
	// или якорь, и ссылкой оно не становится.
	linkTarget = regexp.MustCompile(`^(?:https?://|mailto:)`)

	// Открывающая квадратная скобка — уже экранированная или нет.
	bracket = regexp.MustCompile(`\\?\[`)
	mdBold  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// wiki переводит тело записи из markdown в разметку JIRA Server.
func wiki(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Маркер — машинный кусок, и узнаётся он **разбором, а не позицией**:
		// правило, проверяемое по следствию, однажды проверит не то
		// (DESIGN.md §2.3). Комментарий без маркера первой строкой переводится
		// весь, и это верно — маркера в нём нет.
		if i == 0 {
			if _, ok := tracker.MarkerOf(line); ok {
				out = append(out, line)
				continue
			}
		}

		// Раздел вопросов — тоже протокол: его печатает раннер и он же потом
		// разбирает. Тело раздела едет буква в букву: метки вопросов, отступы
		// вариантов и подсказка про ответ — всё это разбирается обратно.
		//
		// Заголовок — исключение, и оно живое: `##` в wiki означает вложенный
		// пункт нумерованного списка, поэтому непереведённый заголовок исчезал,
		// а на его месте оказывался пустой пункт с «Вопросы» под ним. Раздел,
		// который человек обязан прочитать и на который обязан ответить, выглядел
		// сломанным. Поэтому заголовок переводится, а якорем раздела служит его
		// текст, а не точная строка (tracker.IsQuestionsHeading).
		if tracker.IsQuestionsHeading(line) {
			end := i + 1
			// Раздел кончается там же, где его кончает разбор: следующим
			// заголовком либо подсказкой про ответ. Второе — не педантизм:
			// без него всё, что раннер напишет после вопросов без заголовка,
			// молча уехало бы в тикет непереведённым.
			for end < len(lines) && !tracker.IsHeading(lines[end]) {
				if tracker.IsAnswerHint(lines[end]) {
					end++
					break
				}
				end++
			}
			out = append(out, headingOf(line))
			out = append(out, lines[i+1:end]...)
			i = end - 1
			continue
		}

		// Блок кода: содержимое — не проза, и внутри не переводится ничего.
		if fenced, lang := fence(line); fenced {
			out = append(out, codeOpen(lang))
			end := i + 1
			for end < len(lines) {
				if closing, _ := fence(lines[end]); closing {
					break
				}
				out = append(out, lines[end])
				end++
			}
			out = append(out, "{code}")
			// Незакрытый забор закрывается здесь же: недописанный блок лучше
			// съехавшей разметки до конца записи.
			i = end
			continue
		}

		// Шапка таблицы: в wiki она отбивается двойными палками, а строки-
		// разделителя нет вовсе.
		if mdRow.MatchString(line) && i+1 < len(lines) && mdSep.MatchString(lines[i+1]) {
			out = append(out, header(line))
			i++
			continue
		}

		if rule(line) {
			out = append(out, "----")
			continue
		}
		if mdHeading.MatchString(line) {
			out = append(out, headingOf(line))
			continue
		}
		if m := mdQuote.FindStringSubmatch(line); m != nil {
			out = append(out, "bq. "+inline(m[1]))
			continue
		}
		if m := mdBullet.FindStringSubmatch(line); m != nil {
			out = append(out, strings.Repeat("*", depth(m[1]))+" "+inline(m[2]))
			continue
		}
		if m := mdNumber.FindStringSubmatch(line); m != nil {
			out = append(out, strings.Repeat("#", depth(m[1]))+" "+inline(m[2]))
			continue
		}

		out = append(out, inline(line))
	}

	return strings.Join(out, "\n")
}

// fence — открывает или закрывает ли строка блок кода, и на каком языке.
func fence(line string) (bool, string) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "```") {
		return false, ""
	}
	return true, strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
}

// codeOpen — открывающая скобка блока кода. Язык называется, только если он был
// назван: пустое `{code:}` JIRA понимает хуже, чем просто `{code}`.
func codeOpen(lang string) string {
	if lang == "" {
		return "{code}"
	}
	return "{code:" + lang + "}"
}

// rule — горизонтальная черта. Пишется руками, а не регулярным выражением:
// «три одинаковых символа подряд» — это обратная ссылка, которой в RE2 нет.
func rule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	switch trimmed[0] {
	case '-', '*', '_':
	default:
		return false
	}
	return strings.Count(trimmed, string(trimmed[0])) == len(trimmed)
}

// header переводит шапку таблицы: `| а | б |` → `|| а || б ||`.
func header(line string) string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i, cell := range cells {
		cells[i] = inline(strings.TrimSpace(cell))
	}
	return "|| " + strings.Join(cells, " || ") + " ||"
}

// depth — уровень вложенности списка по отступу: два пробела на уровень.
// Табуляция считается за два пробела — иначе пункт с табом уехал бы на уровень,
// которого автор не имел в виду.
func depth(indent string) int {
	return len(strings.ReplaceAll(indent, "\t", "  "))/2 + 1
}

// inline переводит разметку внутри строки: ссылки, куски кода, жирный текст.
// Скобки экранируются и в прозе между ними, и внутри куска кода — моноширинный
// шрифт от wiki не защищает.
func inline(s string) string {
	var b strings.Builder
	for s != "" {
		code := mdCode.FindStringSubmatchIndex(s)
		lnk := mdLink.FindStringIndex(s)
		switch {
		case code == nil && lnk == nil:
			b.WriteString(escapeBrackets(bold(s)))
			return b.String()

		// Что раньше в строке, то и разбирается первым, и это по существу.
		// Внутри куска кода ни ссылка, ни жирный текст разметкой не являются —
		// переводить их значило бы менять код, который человек скопирует. Но и
		// ссылка бывает с кодом в подписи: [`docs/x.md`](docs/x.md). Разбери код
		// первым — ссылка развалится, и от неё останется хвост `](docs/x.md)`.
		case lnk != nil && (code == nil || lnk[0] <= code[0]):
			b.WriteString(escapeBrackets(bold(s[:lnk[0]])))
			b.WriteString(link(s[lnk[0]:lnk[1]]))
			s = s[lnk[1]:]

		default:
			b.WriteString(escapeBrackets(bold(s[:code[0]])))
			// Скобки экранируются и внутри кода в строке: моноширинный шрифт
			// от wiki не защищает — `{{[x]}}` JIRA красит так же, как голое `[x]`.
			// Проверено на живом инстансе.
			b.WriteString("{{" + escapeBrackets(s[code[2]:code[3]]) + "}}")
			s = s[code[1]:]
		}
	}
	return b.String()
}

// bold — жирный текст: `**x**` → `*x*`.
//
// Одиночные `*x*` не трогаются: в markdown это курсив, но той же парой звёздочек
// начинается жирный текст, и отличить их построчно нельзя. Неверная догадка тут
// хуже непереведённого текста.
func bold(s string) string { return mdBold.ReplaceAllString(s, "*$1*") }

// escapeBrackets закрывает открывающую квадратную скобку.
//
// Это единственный символ, который конвертер экранирует, и причина у него своя:
// в wiki `[...]` — ссылка, а неразрешимую ссылку JIRA красит красным как ошибку.
// Красное в отчёте должно означать беду, а не процитированный пункт плана
// (`- [x] сделано`) или сноску (`см. [1]`) — и то и другое роли пишут постоянно.
//
// Закрывающая не трогается: без открывающей ссылка не начинается, и один слэш
// на скобку дешевле двух. Слэш остаётся в теле записи, но виден только тому, кто
// читает тело как простой текст, — агенту, которому раннер собирает хвост переписки.
// В теле pull request его нет: markdown понимает `\[` тем же экранированием —
// замерено, а не выведено (POST /markdown у GitHub).
//
// Уже экранированная скобка второй раз не экранируется.
func escapeBrackets(s string) string {
	return bracket.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, `\`) {
			return m
		}
		return `\[`
	})
}

// link переводит ссылку markdown — но только ту, которой есть куда вести.
//
// Голый URL ссылкой не оборачивается вовсе: он кликабелен и так, а обёртка вокруг
// него сломала бы чтение адреса из тела записи (`event:pr-opened`).
//
// Роли ссылаются в отчётах на пути в репозитории, и `[текст|docs/…]` JIRA
// разрешить не может: она красит такую строку красным как ошибку. Красное
// в отчёте должно означать беду, а не путь к файлу, поэтому ссылка без адреса
// превращается в прозу. Скобок при этом не остаётся — квадратная скобка в wiki
// сама по себе просится в ссылку.
func link(md string) string {
	m := mdLink.FindStringSubmatch(md)
	text, target := m[1], m[2]
	if linkTarget.MatchString(target) {
		// Разметку внутри подписи wiki не разбирает, поэтому обратные кавычки
		// показались бы там буквально. Снимаем их: подпись — это подпись.
		return "[" + strings.ReplaceAll(text, "`", "") + "|" + target + "]"
	}
	// Подпись становится прозой, и разметка внутри неё переводится обычным
	// порядком. Рекурсия конечна: подпись не может содержать закрывающей скобки,
	// то есть целой ссылки внутри неё не бывает. Скобка в самом пути закрывается
	// тоже — иначе `docs/a[1].md` покраснел бы ровно так же, как всё прочее.
	return inline(text) + " (" + escapeBrackets(target) + ")"
}

// headingOf переводит строку-заголовок: `## Текст` → `h2. Текст`.
//
// Строку, заголовком markdown не являющуюся, отдаёт как есть. Это не перестраховка:
// сюда приходит и заголовок раздела вопросов, а узнаётся он якорем
// `tracker.IsQuestionsHeading`, который признаёт **и уже переведённую форму**
// `h2. Вопросы`. На ней здешнее правило не совпадает, и разбор без проверки уронил бы
// весь тик по index out of range. Отступ различием не является: и здесь, и у якоря
// он допускается до трёх пробелов — одним правилом markdown.
func headingOf(line string) string {
	m := mdHeading.FindStringSubmatch(strings.TrimRight(line, " \t"))
	if m == nil {
		return line
	}
	return fmt.Sprintf("h%d. %s", len(m[1]), inline(m[2]))
}
