package tracker

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// Prefix — с чего начинается любая запись офиса в трекере.
const Prefix = "[office "

// attachmentIDPattern — форма значения attachment:. И mock, и jira строят
// из этого значения путь/URL к вложению напрямую (mock.GetAttachment:
// filepath.Join, jira.GetAttachment: часть REST-пути) — а маркер разбирается
// по тексту комментария, не по праву владения его автора (pr-converge,
// принятый риск «подлинность маркера не проверяется»). Без ограничения
// алфавита значение вроде "../../OTHER-1/attachments/0" увело бы mock-чтение
// за пределы каталога задачи. Реальные id (`nextExclusive` у mock, числовые
// у jira) укладываются в куда более узкий алфавит — этот не режет ничего,
// что тракеры сами когда-либо порождают.
var attachmentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidAttachmentID проверяет id вложения по той же форме, что ParseMarker
// требует на чтении attachment:. Экспортирован ради записи: AddAttachment
// у jira отдаёт id, который назвал сам сервер, без всякой проверки — если
// он однажды выйдет за этот алфавит, маркер с ним всё равно не переживёт
// собственное чтение (ParseMarker его отвергнет), и офис перестанет видеть
// свой же отчёт. Лучше отказать здесь, на записи, с понятной причиной, чем
// молча сломаться там.
func ValidAttachmentID(id string) bool {
	return attachmentIDPattern.MatchString(id)
}

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
	// умолчание ведёт в очередь PR-прохода, и работа, которую вернули аналитику,
	// была бы принята и уехала бы открывать pull request.
	EventRouteUnknown = "route-unknown"

	// EventAgentUnavailable — прогон не начинался: агент не сделал ни шага
	// и не оставил следа. Обрыв связи, отказ API, исчерпанное окно подписки —
	// беда обвязки, а не работы, и попытка задачи на неё не тратится.
	EventAgentUnavailable = "agent-unavailable"
	// EventRunTruncated — прогон срезан на ходу: предел шагов роли или таймаут.
	// Агент работал и не успел отчитаться, поэтому это «продолжить», а не провал:
	// задача возвращается той же роли, рабочая папка сохраняется.
	EventRunTruncated = "run-truncated"
	// EventIdleRunsExhausted — прогоны роли подряд не доходят до результата,
	// и это предел. Счётчик у agent-unavailable и run-truncated общий: следствие
	// у них одно, а два раздельных счётчика чередование обошло бы.
	EventIdleRunsExhausted = "idle-runs-exhausted"

	// EventPROpened — офис открыл pull request; адрес лежит в теле записи.
	//
	// Это одно из двух событий, по которым выводится состояние PR: смотрится
	// последнее из них, а не факт в истории.
	EventPROpened = "pr-opened"
	// EventPRClosed — pull request закрыт без слияния. Второе событие семейства.
	EventPRClosed = "pr-closed"
	// EventMerged — pull request слит человеком; задача уходит в терминальный
	// статус. В семейство состояния не входит: после слияния открывать нечего.
	EventMerged = "merged"
	// EventPRSkipped — у проекта нет forge, и открывать PR негде. Задача уходит
	// туда же, куда ушла бы после слияния, но событие своё: слияния не было.
	EventPRSkipped = "pr-skipped"
	// EventMergeConflict — ветка задачи не сливается с базовой веткой прохода
	// (Project.PRBranch — не обязательно ветка по умолчанию) или отстала от неё
	// без всякого конфликта. Это работа, а не провал: попытка не тратится.
	// В семейство состояния не входит — открытый PR конфликт не закрывает.
	EventMergeConflict = "merge-conflict"
	// EventMergeRefused — forge отказал в слиянии pull request'а при
	// локально чистом состоянии (auto_merge.enabled). Это не работа
	// implementer'а — локально мержить нечего.
	EventMergeRefused = "merge-refused"
	// EventMergeRefusalsExhausted — forge отказывает в слиянии подряд
	// limits.max_merge_refusals раз при локально чистом состоянии. Задача уходит
	// к человеку, но pull request не закрыт — этот маркер НЕ входит в семейство
	// pr-opened/pr-closed (prEvents): в отличие от prAnomaly, он не лжёт
	// advancePR о состоянии PR. После того как человек уберёт причину отказа
	// (например, branch protection) и ответит, задача вернётся в followPR
	// (не в openPR — второй PR на уже открытую ветку не откроется) и слияние
	// попробуют снова.
	EventMergeRefusalsExhausted = "merge-refusals-exhausted"
	// EventPRReturnsExhausted — event:merge-conflict и event:merge-refused
	// суммарно подряд limits.max_pr_returns раз: PR-проход не сходится, а
	// чередованием одно от другого не отличить — то ли база не даёт ветке
	// устояться, то ли forge не даёт слить. Задача уходит к человеку, но
	// pull request не закрыт: этот маркер, как и merge-refusals-exhausted,
	// НЕ входит в семейство pr-opened/pr-closed (prEvents) и не лжёт
	// advancePR о состоянии PR.
	EventPRReturnsExhausted = "pr-returns-exhausted"
	// EventMergeUnavailable — слияние сейчас невозможно по причине, которую
	// самой задаче не решить: адрес pull request в переписке называет не тот
	// репозиторий (комментарий поправили руками, или он пришёл из чужого
	// офиса), или auto_merge.target_branch называет ветку, которой нет в
	// репозитории. Не отказ forge и не конфликт: попытки не было вовсе,
	// счётчики (max_merge_refusals, max_pr_returns) не трогаются, задача
	// остаётся на месте. Пишется по одному разу на причину (см.
	// tracker.EventCategories), а не на каждый тик — иначе тикет затопило бы
	// одинаковыми записями, пока причина не уберётся, — и не по последней
	// записи: так две разные причины, случившиеся один за другим, обе
	// останутся звучать, а не потеряются друг за другом.
	EventMergeUnavailable = "merge-unavailable"
	// EventMergePending — GitHub сам ещё не решил, годится ли pull request
	// к слиянию (forge.ErrNotReady): обязательные проверки или ревью не
	// завершены. Может пройти само за несколько тиков (обычный CI), а может
	// не пройти никогда (упавшая проверка, недостающее ревью) — оба случая
	// неразличимы на уровне одного ответа GitHub. Одна запись на весь эпизод
	// ожидания, не на каждый тик (mergeBlocked-стиль дедупликации в
	// mergePending, internal/pipeline/prpass.go); предел — отдельный,
	// заметно более терпеливый, чем max_merge_refusals, и считается временем
	// от этой записи (limits.max_merge_pending_sec, tracker.MergePendingSince),
	// а не числом тиков: тот исчерпался бы за 4-6 минут при дефолтном тике,
	// раньше, чем успевает пройти обычный CI. Нейтрально для PRReturns
	// и MergeRefusals (см. их) — само по себе ни на что не решилось.
	EventMergePending = "merge-pending"
	// EventMergePendingExhausted — event:merge-pending стоит дольше
	// limits.max_merge_pending_sec: слияние не становится готовым, и это,
	// скорее всего, не CI, а нечто, что само не пройдёт (упавшая проверка,
	// недостающее ревью). Задача уходит к человеку, pull request не закрыт —
	// вне prEvents, тем же приёмом, что merge-refusals-exhausted.
	EventMergePendingExhausted = "merge-pending-exhausted"

	// EventSplitCreated — CompleteSplits досоздал и связал всех детей
	// подтверждённого split-предложения, родитель закрыт.
	EventSplitCreated = "split-created"
	// EventSplitCreateFailed — попытка CompleteSplits на этом тикете не
	// удалась; идемпотентный опрос трекера делает повтор безопасным,
	// это не расход попытки агента.
	EventSplitCreateFailed = "split-create-failed"

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
	Outcome string // исход прогона: done, needs_human, blocked, failed, split
	Event   string // событие системной записи
	// Next — кому прогон передал задачу: `next_owner` из его результата.
	// Заявка агента, а не маршрут: куда задача уехала на самом деле, решает
	// граф. Без него «вернул на доработку» и «одобрил» в переписке
	// неразличимы, а круги «правки → ревью» считать нечем. Необязателен:
	// у системных записей его нет вовсе, у отчётов этапа 2 не было.
	Next string
	// Attachment — id вложения с сырыми данными исхода (сегодня только
	// split.children[]): второй раунд подтверждения split читает его,
	// не переразбирая человекочитаемый текст комментария (SplitConfirmed).
	// Значим только у отчётов, как и Next — у системных записей вложения
	// не бывает.
	Attachment string
	ConfigSHA  string // личность офиса: версия релиза или commit (с -dirty); имя историческое — ключ config: в тикетах
}

// String собирает первую строку комментария.
func (m Marker) String() string {
	kind, value := "outcome", m.Outcome
	if m.Outcome == "" {
		kind, value = "event", m.Event
	}

	fields := []string{"run:" + shorten(m.RunID), "role:" + m.Role, kind + ":" + value}
	// Пустое значение поля маркером не является вовсе (см. ParseMarker),
	// поэтому пустые next и attachment в строку не идут.
	if m.Next != "" && m.Outcome != "" {
		fields = append(fields, "next:"+m.Next)
	}
	if m.Attachment != "" && m.Outcome != "" {
		fields = append(fields, "attachment:"+m.Attachment)
	}
	return Prefix + strings.Join(append(fields, "config:"+shortenSHA(m.ConfigSHA)), " ") + "]"
}

// Valid — заполнено ли ровно одно из Outcome и Event, и есть ли роль.
// Кому передана задача — свойство отчёта о прогоне: у системной записи исхода
// нет, и передавать ей нечего.
func (m Marker) Valid() bool {
	oneKind := (m.Outcome == "") != (m.Event == "")
	return oneKind && m.Role != "" && m.RunID != "" &&
		(m.Next == "" || m.Outcome != "") && (m.Attachment == "" || m.Outcome != "")
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
		case "attachment":
			if !attachmentIDPattern.MatchString(value) {
				return Marker{}, false
			}
			m.Attachment = value
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

// MergeRefusals — сколько раз подряд forge отказывал в мерже при локально
// чистом состоянии. Считается тем же правилом, что PushFailures/LeaseExpiries:
// это беда стороннего сервиса (или его правила, о котором офис не знает),
// а не провал агента.
func MergeRefusals(comments []Comment, role string) int {
	// event:merge-pending — нейтрально: forge просто ещё не ответил на этой
	// попытке, а не вмешался кто-то или что-то другое. Не будь оно нейтральным,
	// чередование «отказ → pending → отказ» обрывало бы счёт на каждом pending,
	// и предел не достигался бы никогда — тот же класс бага, ради которого
	// вообще завели общий PRReturns, только с третьим событием вместо двух
	// (внешнее ревью, pr-converge round 2).
	return neutralEventStreak(comments, role, []string{EventMergeRefused}, []string{EventMergePending})
}

// MergePendingSince — когда началась текущая серия ожидания (GitHub сам ещё
// не решил, годится ли pull request к слиянию — forge.ErrNotReady), если она
// идёт прямо сейчас.
//
// Считает не тики, а время: длительность CI не привязана к частоте тика
// (`--every`), а mergePending (internal/pipeline/prpass.go) пишет одну запись
// на весь эпизод ожидания, а не одну на тик, — тик посчитать было бы нечем,
// и час в конфиге (limits.max_merge_pending_sec) остаётся часом при любом
// `--every`, а не «столько-то тиков» в зависимости от него.
//
// found — true, только если самая последняя запись роли и есть
// event:merge-pending: более поздняя запись любого другого рода (конфликт,
// отказ, слияние, ответ человека) значит, что тот эпизод ожидания уже кончился.
//
// Нулевое время записи — не «эпизод идёт с начала времён», а «времени нет»:
// адаптер трекера может тихо проглотить ошибку разбора даты (JIRA — если
// сервер вернул её в неожиданном формате) и оставить Comment.Created нулевым.
// До этой функции Created нигде не участвовал в решениях, только в выводе, —
// с ним это сошло бы с рук; здесь нулевое время означало бы гарантированно
// истёкший limits.max_merge_pending_sec и эскалацию на первом же тике,
// то есть ровно ту раннюю эскалацию, ради которой считалось время, а не тики
// (внешнее ревью, pr-converge round 3). Нулевое время поэтому — found=false:
// решать нечем, лучше завести эпизод заново, чем соврать о его возрасте.
func MergePendingSince(comments []Comment, role string) (since time.Time, found bool) {
	c, ok := lastRoleComment(comments, role)
	if !ok {
		return time.Time{}, false
	}
	m, _ := MarkerOf(c.Body)
	if m.Event != EventMergePending || c.Created.IsZero() {
		return time.Time{}, false
	}
	return c.Created, true
}

// lastRoleComment — самая последняя (по порядку в истории) запись этой роли,
// отчёт или системная, если она вообще есть.
func lastRoleComment(comments []Comment, role string) (Comment, bool) {
	for i := len(comments) - 1; i >= 0; i-- {
		if m, ok := MarkerOf(comments[i].Body); ok && m.Role == role {
			return comments[i], true
		}
	}
	return Comment{}, false
}

// PRReturns — сколько раз подряд PR-проход вернул задачу, не сдвинув её:
// база продвинулась (event:merge-conflict — оба случая, и текстовый конфликт,
// и просто уехавшая вперёд база) или forge отказал в слиянии
// (event:merge-refused).
//
// Счётчик на оба вида один — как у IdleRuns и по той же причине: следствие
// у них одно (pull request не сходится), а два раздельных счётчика чередование
// обошло бы. Отказ, конфликт, снова отказ — и ни MergeRefusals, ни счёт одних
// конфликтов не дошли бы до своего предела, пока задача крутится вечно.
//
// Обрывает серию любая другая запись **прохода** — не только открытие pull
// request (event:pr-opened) и разбор ответа человека (event:human-reply —
// unblock подписывает его ролью того, кто говорил последним), но и слияние,
// закрытие PR, оба вида эскалации: любое из них означает, что задача покинула
// очередь возвратов. Отчёты implementer'а и reviewer'а серию не трогают,
// и это существенно: возврат в работу тем и кончается, что они отчитываются, —
// обрывайся серия на их отчётах, счётчик не досчитал бы до предела никогда.
//
// event:merge-pending — тоже не обрывает, но и не считается: он нейтрален
// (см. MergeRefusals). Не будь он нейтральным, обычный круг «CI идёт → база
// подъехала → CI снова идёт» рвал бы счёт на каждом pending, и PRReturns
// не дошёл бы до предела никогда — тот самый вечный круг, ради которого сам
// PRReturns и заводили, только с третьим событием (внешнее ревью, round 2).
func PRReturns(comments []Comment, role string) int {
	return neutralEventStreak(comments, role,
		[]string{EventMergeConflict, EventMergeRefused}, []string{EventMergePending})
}

// IdleRuns — сколько прогонов роли подряд не дошли до результата.
//
// Считает записи двух видов одним счётчиком: «не начинал» (agent-unavailable)
// и «не успел» (run-truncated). Счёт общий не для краткости, а по существу:
// следствие у них одно — прогона как работы не было, — и причина одна.
// Два раздельных счётчика чередование обошло бы: прогон не начался, следующий
// оборвался, третий опять не начался, и ни один счётчик не дошёл бы до предела,
// пока задача крутится вечно.
//
// Что серию обрывает — там же, где у остальных: отчёт роли о прогоне, разбор
// ответа человека, неудачная публикация. Все они означают, что прежние пустые
// прогоны стали прошлым.
func IdleRuns(comments []Comment, role string) int {
	return eventStreak(comments, role, EventAgentUnavailable, EventRunTruncated)
}

// WithoutMarker — тело записи офиса без её первой, машинной строки.
//
// Маркер — служебная разметка для раннера, и человеку, читающему pull request,
// он не нужен: там от него остаётся строка вида `[office run:… role:…]`,
// которая ничего не объясняет и мешает читать.
func WithoutMarker(body string) string {
	line, rest, found := strings.Cut(body, "\n")
	if !found {
		if _, ok := ParseMarker(strings.TrimSpace(line)); ok {
			return ""
		}
		return body
	}
	if _, ok := ParseMarker(strings.TrimSpace(line)); !ok {
		return body
	}
	return rest
}

// prEvents — записи, по которым выводится состояние pull request.
//
// Их ровно две, и это выбор, а не недосмотр. `merged` и `merge-conflict`
// в семейство не входят: после слияния открывать нечего, а конфликт открытый PR
// не закрывает — задача уходит на доработку, и тот же PR ждёт её возвращения.
var prEvents = []string{EventPROpened, EventPRClosed}

// PRState — последняя запись семейства и адрес PR из её тела.
//
// Состояние выводится по последней записи, а не по наличию `pr-opened`
// в истории. Разница видна на живом случае: PR закрыли руками → задача ушла
// в Blocked → человек ответил → задача вернулась. По факту в истории второй PR
// не открылся бы никогда, потому что `pr-opened` там уже есть.
func PRState(comments []Comment, role string) (event, url string, found bool) {
	i := lastOfRole(comments, role, func(m Marker) bool {
		return slices.Contains(prEvents, m.Event)
	})
	if i < 0 {
		return "", "", false
	}
	m, _ := MarkerOf(comments[i].Body)
	return m.Event, firstURL(comments[i].Body), true
}

// firstURL — первый http-адрес в теле записи. Адрес PR живёт в прозе, а не
// в маркере: маркер — это набор коротких полей, и длинный URL сделал бы первую
// строку тикета нечитаемой.
func firstURL(body string) string {
	for _, field := range strings.Fields(body) {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			return field
		}
	}
	return ""
}

// eventStreak — серия однородных записей роли с конца истории.
//
// Событий может быть несколько: серия считается общей, если разные события
// значат для человека одно и то же (см. IdleRuns).
//
// «Подряд» получается само собой: любая другая запись этой роли обрывает счёт.
// Отчёт означает, что прогон дошёл до конца и опубликовался, запись о разборе
// ответа — что вмешался человек. Записи чужих ролей и проза без маркера
// не значат ни того, ни другого и серию не трогают.
func eventStreak(comments []Comment, role string, events ...string) int {
	return neutralEventStreak(comments, role, events, nil)
}

// neutralEventStreak — eventStreak с третьим вердиктом: события из neutral
// не входят в счёт, но и не обрывают его — они прошли мимо, не сказав ничего
// ни за, ни против. Обычному eventStreak это не нужно (там уже любое чужое
// событие — сигнал, что обстоятельства сменились), но событие может само по
// себе быть «ничего не решилось» — например, merge-pending: forge просто ещё
// не ответил, и это не то же самое, что implementer отчитался или human
// вмешался (те по-прежнему обрывают серию через default: stop).
func neutralEventStreak(comments []Comment, role string, events, neutral []string) int {
	return streak(comments, func(_ Comment, m Marker, office bool) verdict {
		switch {
		case !office || m.Role != role:
			return passBy
		case slices.Contains(events, m.Event):
			return countIn
		case slices.Contains(neutral, m.Event):
			return passBy
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

// EventCategories — множество уже сказанных причин для данного события:
// первая строка текста (без строки маркера — NoticeBody кладёт маркер
// первой строкой, текст дальше) каждой записи с этим событием, по всей
// переписке, а не только последней. Нужен там, где дедупликация обязана
// сравнивать причину, а не только факт события: HasEvent сказал бы «уже
// сообщено» и для тикета, который свежая, другая по сути беда постигла
// уже после первой (splitFailed).
//
// По ВСЕЙ переписке, не по последней записи (LastEventText — прежняя,
// более узкая версия этой функции — сравнивала только с ней): если ранний
// шаг падает изредка, а поздний — стабильно, их причины чередуются между
// заходами цикла раннера, и сравнение с последней всегда видело бы «новую» причину,
// хотя обе уже звучали (внешнее ревью, pr-converge раунд 3).
func EventCategories(comments []Comment, event string) map[string]bool {
	categories := make(map[string]bool)
	for _, c := range comments {
		m, ok := MarkerOf(c.Body)
		if !ok || m.Event != event {
			continue
		}
		_, rest, _ := strings.Cut(c.Body, "\n")
		category, _, _ := strings.Cut(strings.TrimSpace(rest), "\n")
		categories[category] = true
	}
	return categories
}

// SplitConfirmed решает, подтверждён ли split этой роли: считает все
// комментарии-маркеры outcome:split от role в переписке — второй такой
// маркер и есть подтверждение. Общий с DESIGN.md §2.8 приём — состояние
// выводится из переписки на лету, а не хранится отдельным флагом, — а не
// то же самое правило: §2.8 берёт последнюю запись, здесь считает счётчик.
//
// Считает по всей истории, а не суффиксом с конца (в отличие от
// eventStreak/ReturnRounds): между двумя split-маркерами роли лежит
// системная запись event:human-reply с тем же Role (unblock() подписывает
// её ролью, которой был задан вопрос) — суффиксный счёт оборвался бы на
// ней, посчитав её «другой записью этой роли». Повторное «пересмотреть»
// несколько раз подряд этот плоский счёт не отличает от подтверждения —
// принятое упрощение этой волны, не забытый случай
// (docs/notes/analyst-task-splitting.md, «открытый вопрос» волны 1).
//
// attachmentID берётся из тега **последнего** такого маркера: если человек
// просил пересмотреть несколько раз, старые вложения остаются в истории,
// актуально только последнее.
//
// Подтверждения без вложения быть не может: под старой (до задачи 10) версией
// role.md split-маркеры вложения не несли вовсе, и тикет, доживший под ней
// до второго такого маркера, отдал бы confirmed=true с пустым attachmentID —
// CompleteSplits затем звал бы GetAttachment(key, "") и падал бы на этом
// тикете каждый заход цикла раннера, бесконечно. Поэтому пустой attachmentID
// у последнего маркера — тоже «ещё не подтверждено», а не «подтверждено,
// но нечего читать».
func SplitConfirmed(comments []Comment, role string) (confirmed bool, attachmentID string) {
	count := 0
	for _, c := range comments {
		m, ok := MarkerOf(c.Body)
		if !ok || m.Role != role || m.Outcome != "split" {
			continue
		}
		count++
		attachmentID = m.Attachment
	}
	if attachmentID == "" {
		return false, ""
	}
	return count >= 2, attachmentID
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

// shortenSHA режет commit до восьми символов, сохраняя пометку -dirty: без неё
// маркер утверждал бы, что агенту достался коммит, которого агент не видел.
// Что считается commit'ом, решает runner.IsCommitIdentity — там, где личность
// и производится; версия релиза и любая другая личность пишутся целиком.
func shortenSHA(identity string) string {
	if !runner.IsCommitIdentity(identity) {
		return identity
	}
	if strings.HasSuffix(identity, runner.DirtySuffix) {
		return shorten(strings.TrimSuffix(identity, runner.DirtySuffix)) + runner.DirtySuffix
	}
	return shorten(identity)
}
