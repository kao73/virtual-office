package tracker

import (
	"slices"
	"strings"
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

// LeaseExpiries — сколько раз подряд у роли истекала аренда, считая с конца
// истории.
//
// Смерть прогона считается отдельно от провалов агента и **не полем в трекере,
// а маркерами в самом тикете**: они там уже есть, а лишнее кастомное поле — это
// правка контракта, обеих реализаций и настройки инстанса.
//
// «Подряд» получается само собой: любой другой маркер этой роли обрывает счёт.
// Отчёт означает, что прогон дошёл до конца, запись о разборе ответа — что
// в задачу вмешался человек; и то и другое делает прежние смерти прошлым.
// Записи чужих ролей и комментарии без маркера не значат ни того, ни другого:
// они пропускаются, не обрывая серии.
func LeaseExpiries(comments []Comment, role string) int {
	streak := 0
	for i := len(comments) - 1; i >= 0; i-- {
		marker, found := MarkerOf(comments[i].Body)
		if !found || marker.Role != role {
			continue
		}
		if marker.Event != EventLeaseExpired {
			break
		}
		streak++
	}
	return streak
}

// HumanReply ищет неразобранный ответ человека и роль, которой после него
// достанется задача.
//
// Ответ — реплика любой учётки не из agents, написанная после **последней записи
// офиса**, какой бы та ни была. Вопрос роли, отчёт о провале с исчерпанными
// попытками, серия смертей прогона — задачу в ожидание отправляет не только
// агент, и человек обязан уметь вернуть её в работу в любом из этих случаев.
// Прежнее правило требовало маркер `outcome:needs_human`, и всё, что заблокировал
// сам раннер, застревало навсегда.
//
// Смотреть надо именно на последнюю запись: подтверждение разбора
// `event:human-reply` — тоже запись офиса, и она закрывает вопрос. Иначе каждый
// следующий tick разбирал бы тот же ответ заново.
//
// Роль берётся из той же последней записи: разговор ведёт она, ей задача
// и вернётся. Спросил reviewer — вернётся в его очередь, а не в очередь
// implementer'а.
func HumanReply(comments []Comment, agents []string) (Comment, string, bool) {
	last := -1
	for i := len(comments) - 1; i >= 0 && last < 0; i-- {
		if _, found := MarkerOf(comments[i].Body); found {
			last = i
		}
	}
	if last < 0 {
		return Comment{}, "", false
	}

	marker, _ := MarkerOf(comments[last].Body)
	for _, c := range comments[last+1:] {
		if !slices.Contains(agents, c.Author) {
			return c, marker.Role, true
		}
	}
	return Comment{}, "", false
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

// shorten режет идентификатор по символам, а не по байтам: обрезка посреди
// многобайтового символа испортила бы строку. Настоящие run_id — UUID, но
// подделанные в отладке бывают любыми.
func shorten(s string) string {
	runes := []rune(s)
	if len(runes) <= short {
		return s
	}
	return string(runes[:short])
}

// shortenSHA режет SHA до восьми символов, сохраняя пометку -dirty: без неё
// маркер утверждал бы, что агенту достался коммит, которого агент не видел.
func shortenSHA(sha string) string {
	if suffix := "-dirty"; strings.HasSuffix(sha, suffix) {
		return shorten(strings.TrimSuffix(sha, suffix)) + suffix
	}
	return shorten(sha)
}
