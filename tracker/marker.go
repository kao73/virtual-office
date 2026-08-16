package tracker

import (
	"slices"
	"strings"

	"github.com/kao73/virtual-office/runner"
)

// Prefix — с чего начинается любая запись офиса в трекере.
const Prefix = "[office "

// События системных записей — тех, что делает не прогон, а сам раннер.
const (
	// EventLeaseExpired — reaper вернул задачу с истёкшей арендой.
	EventLeaseExpired = "lease-expired"
	// EventHumanReply — раннер увидел ответ человека и вернул задачу в работу.
	EventHumanReply = "human-reply"
	// EventLeaseLost — прогон дожил до конца, потеряв аренду; в трекер он
	// ничего не пишет, кроме этого предупреждения.
	EventLeaseLost = "lease-lost"
)

// short — сколько символов идентификатора попадает в маркер. Полный UUID
// в первой строке мешает читать, а восьми хватает, чтобы найти прогон в архиве.
const short = 8

// Marker — первая строка комментария офиса. По ней раннер отличает свои записи
// от человеческих, режет историю и понимает, на какой вопрос отвечает человек.
//
// Ровно одно из полей Outcome и Event заполнено: комментарий — это либо отчёт
// прогона, либо системная запись.
type Marker struct {
	RunID     string // полный run_id; в строку идут первые восемь символов
	Role      string
	Outcome   string // исход прогона: done, needs_human, blocked, failed
	Event     string // событие системной записи
	ConfigSHA string // SHA конфига, возможно с суффиксом -dirty
}

// String собирает первую строку комментария.
func (m Marker) String() string {
	kind, value := "outcome", m.Outcome
	if m.Outcome == "" {
		kind, value = "event", m.Event
	}
	return Prefix + strings.Join([]string{
		"run:" + shorten(m.RunID),
		"role:" + m.Role,
		kind + ":" + value,
		"config:" + shortenSHA(m.ConfigSHA),
	}, " ") + "]"
}

// Valid — заполнено ли ровно одно из Outcome и Event, и есть ли роль.
func (m Marker) Valid() bool {
	oneKind := (m.Outcome == "") != (m.Event == "")
	return oneKind && m.Role != "" && m.RunID != ""
}

// ParseMarker разбирает первую строку комментария. Разбор строгий: неизвестный
// ключ или оба вида сразу — это не маркер. Тихо проглоченная опечатка сдвинула бы
// границу истории, и агент получил бы чужой контекст, ничем себя не выдав.
//
// Полный run_id из маркера не восстанавливается: там живут первые восемь символов.
func ParseMarker(line string) (Marker, bool) {
	if !strings.HasPrefix(line, Prefix) || !strings.HasSuffix(line, "]") {
		return Marker{}, false
	}

	var m Marker
	for _, field := range strings.Fields(strings.TrimSuffix(strings.TrimPrefix(line, Prefix), "]")) {
		key, value, found := strings.Cut(field, ":")
		if !found || value == "" {
			return Marker{}, false
		}
		switch key {
		case "run":
			m.RunID = value
		case "role":
			m.Role = value
		case "outcome":
			m.Outcome = value
		case "event":
			m.Event = value
		case "config":
			m.ConfigSHA = value
		default:
			return Marker{}, false
		}
	}
	if !m.Valid() {
		return Marker{}, false
	}
	return m, true
}

// MarkerOf разбирает маркер комментария. Смотрит только первую строку: в теле
// человеческого комментария может оказаться что угодно, вплоть до цитаты маркера.
func MarkerOf(body string) (Marker, bool) {
	first, _, _ := strings.Cut(body, "\n")
	return ParseMarker(strings.TrimRight(first, "\r"))
}

// TailAfterRole отдаёт комментарии после последнего **отчёта** указанной роли.
// Это и есть контекст, который получит агент: перечитывать историю тикета
// целиком он не должен (DESIGN §2.4).
//
// Границу двигают только отчёты о прогонах. Системные записи — «аренда истекла»,
// «ответ человека разобран» — остаются внутри хвоста, и это важно: иначе
// подтверждение разбора, написанное после ответа человека, отрезало бы сам ответ,
// и агент не увидел бы того, ради чего его и разбудили.
//
// Записи других ролей границу не двигают тоже: у каждой роли своя нить разговора.
func TailAfterRole(comments []Comment, role string) []Comment {
	last := lastOfRole(comments, role, func(m Marker) bool { return m.Outcome != "" })
	if last < 0 {
		return comments
	}
	return comments[last+1:]
}

// HumanReply ищет неразобранный ответ человека на вопрос роли.
//
// Ответ — комментарий любой учётки не из agents, написанный после маркера
// `outcome:needs_human` этой роли. Смотрим именно на последний маркер роли:
// если после вопроса роль уже отчитывалась снова или раннер уже отметил разбор
// ответа записью `event:human-reply`, вопрос закрыт. Иначе каждый следующий tick
// разбирал бы тот же ответ заново.
func HumanReply(comments []Comment, role string, agents []string) (Comment, bool) {
	// Здесь, в отличие от нарезки хвоста, годится любая запись роли: подтверждение
	// разбора `event:human-reply` обязано закрывать вопрос.
	last := lastOfRole(comments, role, func(Marker) bool { return true })
	if last < 0 {
		return Comment{}, false
	}
	// Имена исходов берутся из контракта «раннер ↔ агент», а не заводятся заново:
	// два списка одних и тех же значений разъезжаются.
	m, _ := MarkerOf(comments[last].Body)
	if m.Outcome != string(runner.OutcomeNeedsHuman) {
		return Comment{}, false
	}

	for _, c := range comments[last+1:] {
		if !slices.Contains(agents, c.Author) {
			return c, true
		}
	}
	return Comment{}, false
}

// lastOfRole — индекс последней записи офиса, помеченной этой ролью и подошедшей
// под match.
func lastOfRole(comments []Comment, role string, match func(Marker) bool) int {
	for i := len(comments) - 1; i >= 0; i-- {
		if m, ok := MarkerOf(comments[i].Body); ok && m.Role == role && match(m) {
			return i
		}
	}
	return -1
}

func shorten(s string) string {
	if len(s) <= short {
		return s
	}
	return s[:short]
}

// shortenSHA режет SHA до восьми символов, сохраняя пометку -dirty: без неё
// маркер утверждал бы, что агенту достался коммит, которого агент не видел.
func shortenSHA(sha string) string {
	if suffix := "-dirty"; strings.HasSuffix(sha, suffix) {
		return shorten(strings.TrimSuffix(sha, suffix)) + suffix
	}
	return shorten(sha)
}
