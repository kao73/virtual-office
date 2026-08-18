package tracker

import (
	"slices"
	"strings"

	"github.com/kao73/virtual-office/runner"
)

// Prefix — с чего начинается любая запись офиса в трекере.
const Prefix = "[office "

// События записей раннера — тех, что делает не агент, а обвязка вокруг него.
//
// Часть из них системные: их пишут, когда живой аренды нет и быть не должно
// (reaper вернул чужую задачу, разобран ответ человека, прогон дожил до конца
// без аренды). Остальные раннер делает внутри прогона, при живой аренде,
// и подписывает прогоном — см. Actor и «Правило владения» в контракте.
const (
	// EventLeaseExpired — reaper вернул задачу с истёкшей арендой.
	EventLeaseExpired = "lease-expired"
	// EventHumanReply — раннер увидел ответ человека и вернул задачу в работу.
	EventHumanReply = "human-reply"
	// EventLeaseLost — прогон дожил до конца, потеряв аренду; в трекер он
	// ничего не пишет, кроме этого предупреждения.
	EventLeaseLost = "lease-lost"

	// EventPushFailed — работа осталась только в рабочей папке: ветку не приняли.
	// Пишется внутри прогона, поэтому подписывается им.
	EventPushFailed = "push-failed"
	// EventPushFailuresExhausted — пуш не удаётся подряд столько раз, что дальше
	// пробовать бессмысленно: дело не в задаче.
	EventPushFailuresExhausted = "push-failures-exhausted"
	// EventReturnRoundsExhausted — роль отдаёт задачу одному и тому же владельцу
	// подряд столько раз, что дальше крутить бессмысленно; спор решает человек.
	EventReturnRoundsExhausted = "return-rounds-exhausted"
	// EventRouteUnknown — агент назвал следующим владельцем роль графа, для которой
	// у этого исхода маршрута нет. По умолчанию такую задачу везти нельзя: у ревьюера
	// умолчание — терминал, и работа, которую вернули аналитику, была бы принята.
	EventRouteUnknown = "route-unknown"

	// EventBudgetExceeded — задача перевалила за свой предел расхода, но работа
	// продолжается: предел в режиме warn. Пишется до захвата, когда аренды ещё нет,
	// и потому системная.
	EventBudgetExceeded = "budget-exceeded"
	// EventBudgetExhausted — предел задачи в режиме stop: работа не начинается,
	// задача уходит к человеку.
	EventBudgetExhausted = "budget-exhausted"
	// EventRunBudgetExceeded — один прогон обошёлся дороже предела. Прерывать
	// его нечем: цена известна, когда работа уже сделана и оплачена.
	EventRunBudgetExceeded = "run-budget-exceeded"
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
	RunID   string // полный run_id; в строку идут первые восемь символов
	Role    string
	Outcome string // исход прогона: done, needs_human, blocked, failed
	Event   string // событие системной записи
	// Next — кому прогон передал задачу: `next_owner` из его результата.
	// Заявка агента, а не маршрут: куда задача уехала на самом деле, решает
	// граф. Без него «вернул на доработку» и «одобрил» в переписке
	// неразличимы, а круги «правки → ревью» считать нечем. Необязателен:
	// у системных записей его нет вовсе, у отчётов этапа 2 не было.
	Next      string
	ConfigSHA string // SHA конфига, возможно с суффиксом -dirty
}

// String собирает первую строку комментария.
func (m Marker) String() string {
	kind, value := "outcome", m.Outcome
	if m.Outcome == "" {
		kind, value = "event", m.Event
	}

	fields := []string{"run:" + shorten(m.RunID), "role:" + m.Role, kind + ":" + value}
	// Пустое значение поля маркером не является вовсе (см. ParseMarker),
	// поэтому пустой next в строку не идёт.
	if m.Next != "" && m.Outcome != "" {
		fields = append(fields, "next:"+m.Next)
	}
	return Prefix + strings.Join(append(fields, "config:"+shortenSHA(m.ConfigSHA)), " ") + "]"
}

// Valid — заполнено ли ровно одно из Outcome и Event, и есть ли роль.
// Кому передана задача — свойство отчёта о прогоне: у системной записи исхода
// нет, и передавать ей нечего.
func (m Marker) Valid() bool {
	oneKind := (m.Outcome == "") != (m.Event == "")
	return oneKind && m.Role != "" && m.RunID != "" && (m.Next == "" || m.Outcome != "")
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
		case "next":
			m.Next = value
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
	return eventStreak(comments, role, EventLeaseExpired)
}

// PushFailures — сколько раз подряд у роли не удалось опубликовать ветку.
//
// Считается тем же правилом и по той же причине, что смерти прогона: неудачный
// пуш — беда обвязки, а не провал агента. Смешать их со счётчиком попыток значило
// бы наказывать агента за сломанный remote и звать человека не с тем разговором.
func PushFailures(comments []Comment, role string) int {
	return eventStreak(comments, role, EventPushFailed)
}

// eventStreak — серия одинаковых записей роли с конца истории.
//
// «Подряд» получается само собой: любая другая запись этой роли обрывает счёт.
// Отчёт означает, что прогон дошёл до конца и опубликовался, запись о разборе
// ответа — что вмешался человек. Записи чужих ролей и проза без маркера
// не значат ни того, ни другого и серию не трогают.
func eventStreak(comments []Comment, role, event string) int {
	return streak(comments, func(_ Comment, m Marker, office bool) verdict {
		switch {
		case !office || m.Role != role:
			return passBy
		case m.Event == event:
			return countIn
		default:
			return stop
		}
	})
}

// ReturnRounds — сколько раз подряд роль отдала задачу одному и тому же
// владельцу, не сдвинув её вперёд.
//
// Считается по **паре (роль, next)**, а не по одной роли. На двух ролях разницы
// не было: у ревьюера передача вперёд — это `done` без `next`, а всё, что с ним,
// было возвратом. С тремя ролями пара обязательна: обычные `next:reviewer`
// implementer'а идут подряд десятками, и счёт по одной роли принял бы конвейер
// за спор — проверено пробоем, два прохода давали два «круга».
//
// Круг — отчёт `outcome:done` с названным владельцем, и владелец этот не человек
// и не «никто». Отчёт без `next` (маркеры этапа 2) серию обрывает, а не
// продолжает: кому ушла задача, он не говорит, и считать его кругом значило бы
// гадать.
//
// Обрывают серию, помимо этого: отчёт этой же роли с другим `next`, **любая
// системная запись этой роли** (`push-failed`, `lease-expired`), разбор ответа
// человека **любой** роли и реплика человека. Последние два — потому, что человек
// говорит о задаче целиком, а не о нити одной роли: иначе задачу, которую он
// только что разблокировал, тут же вернули бы ему обратно.
//
// Отсюда и отличие от LeaseExpiries, которому учётки не нужны вовсе: серии
// считают разное, поэтому обрываются по-разному, и общий обход только считает.
func ReturnRounds(comments []Comment, role, next string, agents []string) int {
	return streak(comments, func(c Comment, m Marker, office bool) verdict {
		switch {
		case office && m.Event == EventHumanReply:
			return stop
		case !office && !slices.Contains(agents, c.Author):
			return stop
		case !office || m.Role != role:
			return passBy
		case m.IsHandover() && m.Next == next:
			return countIn
		default:
			return stop
		}
	})
}

// HasEvent — была ли у задачи такая запись раннера. Историю смотрит целиком,
// а не с конца: это не серия, а факт.
//
// Нужно тем сообщениям, которые говорятся о задаче один раз за её жизнь, — вроде
// предупреждения о перерасходе. Повторять их каждый прогон значило бы заращивать
// тикет одинаковыми строчками, среди которых теряется разговор.
func HasEvent(comments []Comment, event string) bool {
	for _, c := range comments {
		if m, ok := MarkerOf(c.Body); ok && m.Event == event {
			return true
		}
	}
	return false
}

// IsHandover — передаёт ли отчёт задачу дальше по конвейеру, то есть закрывает
// круг «правки → ревью».
//
// Круг — это `outcome:done` с названным следующим владельцем, и владелец этот
// не человек и не «никто». Отчёт без `next` (маркеры этапа 2) кругом не считается:
// кому ушла задача, он не говорит, и считать его значило бы гадать.
func (m Marker) IsHandover() bool {
	return m.Outcome == string(runner.OutcomeDone) && m.Next != "" &&
		m.Next != runner.NextOwnerHuman && m.Next != runner.NextOwnerNone
}

// verdict — что запись значит для серии.
type verdict int

const (
	passBy  verdict = iota // серии не касается
	countIn                // серия продолжается
	stop                   // серия оборвана
)

// streak считает серию однородных записей с конца истории.
//
// Обход общий, а правила у каждой серии свои: смерть прогона говорит об одном
// (прогон не дожил до отчёта), круг ревью — о другом (роли не сошлись), и то,
// что обрывает одну, другую не касается. Поэтому судья приходит снаружи,
// а здесь остаётся только счёт.
func streak(comments []Comment, judge func(Comment, Marker, bool) verdict) int {
	count := 0
	for i := len(comments) - 1; i >= 0; i-- {
		marker, office := MarkerOf(comments[i].Body)
		switch judge(comments[i], marker, office) {
		case countIn:
			count++
		case stop:
			return count
		}
	}
	return count
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
