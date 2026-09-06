package tracker

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const runID = "488e8d8f-f441-4374-bdd9-25f4a0952596"

func TestMarkerRendersAndParses(t *testing.T) {
	cases := []struct {
		name   string
		marker Marker
		line   string
	}{
		{
			name:   "отчёт прогона",
			marker: Marker{RunID: runID, Role: "implementer", Outcome: "needs_human", ConfigSHA: "5bc6a3b0000000000000000000000000000000ab"},
			line:   "[office run:488e8d8f role:implementer outcome:needs_human config:5bc6a3b0]",
		},
		{
			name:   "системная запись",
			marker: Marker{RunID: runID, Role: "implementer", Event: EventHumanReply, ConfigSHA: "5bc6a3b0000000000000000000000000000000ab"},
			line:   "[office run:488e8d8f role:implementer event:human-reply config:5bc6a3b0]",
		},
		{
			// Пометка «конфиг был грязный» обязана пережить обрезку до восьми
			// символов: без неё маркер утверждал бы, что агенту достался коммит,
			// которого агент не видел.
			name:   "грязный конфиг",
			marker: Marker{RunID: runID, Role: "implementer", Outcome: "done", ConfigSHA: "5bc6a3b0000000000000000000000000000000ab-dirty"},
			line:   "[office run:488e8d8f role:implementer outcome:done config:5bc6a3b0-dirty]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.marker.String(); got != tc.line {
				t.Errorf("маркер %q, ожидался %q", got, tc.line)
			}
			parsed, ok := ParseMarker(tc.line)
			if !ok {
				t.Fatalf("свой же маркер не разобран: %s", tc.line)
			}
			if parsed.Role != tc.marker.Role || parsed.Outcome != tc.marker.Outcome || parsed.Event != tc.marker.Event {
				t.Errorf("разобрано %+v, ожидалось %+v", parsed, tc.marker)
			}
			// Полный run_id из маркера не восстановить — там живут первые восемь.
			if parsed.RunID != "488e8d8f" {
				t.Errorf("run_id %q, ожидался 488e8d8f", parsed.RunID)
			}
		})
	}
}

// Кому прогон передал задачу — часть машиночитаемой шапки: без этого «вернул
// на доработку» и «одобрил» в переписке неразличимы, а круги «правки → ревью»
// считать нечем. Ключ необязательный: маркеры этапа 2 разбираются как раньше.
func TestMarkerCarriesNextOwner(t *testing.T) {
	m := Marker{RunID: runID, Role: "reviewer", Outcome: "done", Next: "implementer", ConfigSHA: "5bc6a3b0"}
	line := "[office run:488e8d8f role:reviewer outcome:done next:implementer config:5bc6a3b0]"

	if got := m.String(); got != line {
		t.Errorf("маркер %q, ожидался %q", got, line)
	}
	parsed, ok := ParseMarker(line)
	if !ok {
		t.Fatalf("свой же маркер не разобран: %s", line)
	}
	if parsed.Next != "implementer" {
		t.Errorf("next разобран как %q", parsed.Next)
	}

	// Пустое значение поля маркером не является вовсе (ParseMarker строг),
	// поэтому пустой next в строку не попадает.
	without := Marker{RunID: runID, Role: "implementer", Outcome: "done", ConfigSHA: "5bc6a3b0"}
	if got := without.String(); strings.Contains(got, "next:") {
		t.Errorf("пустой next попал в маркер: %s", got)
	}
	if _, ok := ParseMarker(without.String()); !ok {
		t.Errorf("маркер без next не разобран: %s", without.String())
	}
}

// Вложение — как next: значимо только у отчётов, и пустое в строку не идёт.
func TestMarkerCarriesAttachment(t *testing.T) {
	m := Marker{RunID: runID, Role: "analyst", Outcome: "split", Attachment: "10042", ConfigSHA: "5bc6a3b0"}
	line := "[office run:488e8d8f role:analyst outcome:split attachment:10042 config:5bc6a3b0]"

	if got := m.String(); got != line {
		t.Errorf("маркер %q, ожидался %q", got, line)
	}
	parsed, ok := ParseMarker(line)
	if !ok {
		t.Fatalf("свой же маркер не разобран: %s", line)
	}
	if parsed.Attachment != "10042" {
		t.Errorf("attachment разобран как %q", parsed.Attachment)
	}

	without := Marker{RunID: runID, Role: "analyst", Outcome: "split", ConfigSHA: "5bc6a3b0"}
	if got := without.String(); strings.Contains(got, "attachment:") {
		t.Errorf("пустой attachment попал в маркер: %s", got)
	}
}

func TestParseMarkerRejectsForeignLines(t *testing.T) {
	lines := []string{
		"",
		"Обычный комментарий человека.",
		"[office]",
		"[office run:488e8d8f role:implementer config:5bc6a3b0]",                           // ни исхода, ни события
		"[office run:488e8d8f role:implementer outcome:done event:human-reply config:5bc]", // и то и другое
		"[office run:488e8d8f role:implementer outcome:done config:5bc6a3b0 лишнее:поле]",  // неизвестный ключ
		" [office run:488e8d8f role:implementer outcome:done config:5bc6a3b0]",             // не первый символ
		// Кому передана задача — свойство отчёта о прогоне. У системной записи
		// исхода нет, и передавать ей нечего.
		"[office run:488e8d8f role:implementer event:lease-expired next:human config:5bc6a3b0]",
		// Вложение — тоже свойство отчёта. У системной записи его не бывает.
		"[office run:488e8d8f role:implementer event:lease-expired attachment:10042 config:5bc6a3b0]",
	}
	for _, line := range lines {
		if _, ok := ParseMarker(line); ok {
			t.Errorf("чужая строка принята за маркер: %q", line)
		}
	}
}

// Маркер ищется только в первой строке: текст комментария человека может
// содержать что угодно, в том числе цитату чужого маркера.
func TestMarkerOfReadsFirstLineOnly(t *testing.T) {
	body := "Согласен.\n[office run:488e8d8f role:implementer outcome:done config:5bc6a3b0]"
	if _, ok := MarkerOf(body); ok {
		t.Error("маркер найден не в первой строке")
	}
}

// comment — комментарий с телом и автором; время идёт по возрастанию,
// как его отдаёт трекер.
func comment(author, body string, minute int) Comment {
	return Comment{
		ID:      author + string(rune('0'+minute)),
		Author:  author,
		Created: now.Add(time.Duration(minute) * time.Minute),
		Body:    body,
	}
}

func report(role, outcome string, minute int) Comment {
	m := Marker{RunID: runID, Role: role, Outcome: outcome, ConfigSHA: "5bc6a3b0"}
	return comment("office", m.String()+"\nОтчёт прогона.", minute)
}

// Агент не перечитывает историю тикета: в контекст идёт только хвост после
// последнего отчёта своей роли. Чужие роли на границу не влияют — иначе
// implementer терял бы часть собственной переписки.
func TestTailAfterRole(t *testing.T) {
	comments := []Comment{
		comment("человек", "Постановка уточнена.", 1),
		report("implementer", "needs_human", 2),
		comment("человек", "Отвечаю: Stripe.", 3),
		report("reviewer", "done", 4),
		comment("человек", "И ещё одно.", 5),
	}

	tail := TailAfterRole(comments, "implementer")
	if len(tail) != 3 {
		t.Fatalf("хвост из %d комментариев, ожидалось 3: %+v", len(tail), tail)
	}
	if tail[0].Body != "Отвечаю: Stripe." {
		t.Errorf("хвост начинается с %q", tail[0].Body)
	}

	// Роль ещё не отчитывалась — значит вся история её, резать нечего.
	if full := TailAfterRole(comments, "planner"); len(full) != len(comments) {
		t.Errorf("без своего маркера отдано %d комментариев, ожидалось %d", len(full), len(comments))
	}
}

// Ответ человека — реплика не-агента после **последней записи офиса** на задаче,
// ждущей человека. Кто и чем её заблокировал, роли не играет: прежнее правило
// требовало маркер needs_human, и задачи, заблокированные самим раннером
// (попытки исчерпаны, серия смертей прогона), человек не мог вернуть в работу
// вовсе — они стояли в ожидании вечно.
//
// Вместе с ответом возвращается роль последней записи: ей задача и достанется.
func TestHumanReply(t *testing.T) {
	agents := []string{"office", "office-reviewer"}

	cases := []struct {
		name     string
		comments []Comment
		want     string // тело ответа; пусто — ответа нет
		role     string // кому вернуть задачу
	}{
		{
			name:     "ответ на вопрос роли",
			comments: []Comment{report("implementer", "needs_human", 1), comment("человек", "Stripe.", 2)},
			want:     "Stripe.",
			role:     "implementer",
		},
		{
			// Задачу заблокировал раннер: попытки исчерпаны, последняя запись —
			// отчёт о провале, а не вопрос.
			name:     "попытки исчерпаны",
			comments: []Comment{report("implementer", "failed", 1), comment("человек", "Попробуй иначе.", 2)},
			want:     "Попробуй иначе.",
			role:     "implementer",
		},
		{
			// И серия смертей прогона: последняя запись — системная.
			name:     "прогон не доживает до отчёта",
			comments: []Comment{notice("implementer", EventLeaseExpired, 1), comment("человек", "Починил машину.", 2)},
			want:     "Починил машину.",
			role:     "implementer",
		},
		{
			// Маршрут — к тому, кто говорил последним: спросил reviewer, ему
			// задача и вернётся, а не в очередь implementer'а.
			name: "спрашивал reviewer",
			comments: []Comment{
				report("implementer", "done", 1),
				report("reviewer", "needs_human", 2),
				comment("человек", "Так и надо.", 3),
			},
			want: "Так и надо.",
			role: "reviewer",
		},
		{
			name:     "вопрос задан, ответа нет",
			comments: []Comment{report("implementer", "needs_human", 1)},
		},
		{
			name:     "после записи офиса писал только другой агент",
			comments: []Comment{report("implementer", "needs_human", 1), comment("office-reviewer", "Посмотрел.", 2)},
		},
		{
			// Идемпотентность: подтверждение разбора — тоже запись офиса, и она
			// закрывает вопрос. Без этого следующий tick разбирал бы тот же ответ вечно.
			name: "ответ уже разобран",
			comments: []Comment{
				report("implementer", "needs_human", 1),
				comment("человек", "Stripe.", 2),
				notice("implementer", EventHumanReply, 3),
			},
		},
		{
			// Офис ещё не говорил — отвечать было не на что.
			name:     "записей офиса нет вовсе",
			comments: []Comment{comment("человек", "Просто мысль.", 1)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, role, ok := HumanReply(tc.comments, agents)
			switch {
			case tc.want == "" && ok:
				t.Fatalf("найден ответ, которого нет: %q", got.Body)
			case tc.want == "":
				return
			case !ok:
				t.Fatal("ответ человека не найден")
			case got.Body != tc.want:
				t.Errorf("ответ %q, ожидался %q", got.Body, tc.want)
			}
			if role != tc.role {
				t.Errorf("задача вернётся роли %q, ожидалась %q", role, tc.role)
			}
		})
	}
}

// Границу хвоста двигают только отчёты о прогонах. Системная запись, сделанная
// после ответа человека, не должна отрезать сам ответ — иначе агент не увидит
// того, ради чего его и разбудили. Поймано сквозным тестом конвейера, не этим.
func TestTailKeepsSystemNoticesAndTheAnswerBeforeThem(t *testing.T) {
	notice := func(event string, minute int) Comment {
		m := Marker{RunID: runID, Role: "implementer", Event: event, ConfigSHA: "5bc6a3b0"}
		return comment("office", m.String()+"\nСистемная запись.", minute)
	}
	comments := []Comment{
		report("implementer", "needs_human", 1),
		comment("человек", "Берём Stripe.", 2),
		notice(EventHumanReply, 3),
	}

	tail := TailAfterRole(comments, "implementer")
	if len(tail) != 2 {
		t.Fatalf("в хвосте %d комментариев, ожидалось 2: %+v", len(tail), tail)
	}
	if tail[0].Body != "Берём Stripe." {
		t.Errorf("ответ человека отрезан: %+v", tail)
	}

	// Отчёт о следующем прогоне границу двигает, и хвост снова пуст.
	if got := TailAfterRole(append(comments, report("implementer", "failed", 4)), "implementer"); len(got) != 0 {
		t.Errorf("после нового отчёта в хвосте осталось %d комментариев", len(got))
	}
}

// Обрезка идентификатора идёт по символам: разрубленный посреди многобайтового
// символа run_id испортил бы маркер. Поймано на живом прогоне с подделанным id.
func TestMarkerShortensByRunes(t *testing.T) {
	m := Marker{RunID: "мертвец-1234", Role: "implementer", Event: EventLeaseExpired, ConfigSHA: "5bc6a3b0"}
	line := m.String()
	if !strings.Contains(line, "run:мертвец-") {
		t.Errorf("идентификатор обрезан не по символам: %s", line)
	}
	if !utf8.ValidString(line) {
		t.Errorf("маркер перестал быть корректным UTF-8: %q", line)
	}
}

// notice — системная запись раннера: не отчёт прогона, а событие вокруг него.
func notice(role, event string, minute int) Comment {
	m := Marker{RunID: runID, Role: role, Event: event, ConfigSHA: "5bc6a3b0"}
	return comment("office", m.String()+"\nСистемная запись.", minute)
}

// Смерть раннера — не провал агента: машину перезагрузили, кончилось место,
// процесс уронили. Считать её наравне с провалом значило бы звать человека туда,
// где он ничего не сделает, да ещё и объяснив ему неправду. Но и не считать нельзя:
// задача, на которой раннер умирает каждый раз, крутилась бы вечно. Счёт ведётся
// по маркерам в самом тикете — отдельного поля для этого не завели.
func TestLeaseExpiriesCountsStreakFromTheEnd(t *testing.T) {
	comments := []Comment{
		notice("implementer", EventLeaseExpired, 1),
		report("implementer", "failed", 2),
		notice("implementer", EventLeaseExpired, 3),
		notice("implementer", EventLeaseExpired, 4),
	}

	if got := LeaseExpiries(comments, "implementer"); got != 2 {
		t.Errorf("серия %d, ожидалась 2: отчёт роли обязан обрывать счёт", got)
	}
}

// Пустые прогоны считаются **одним** счётчиком на два события, и это главное
// в нём: два раздельных счётчика чередование обошло бы — прогон не начался,
// следующий оборвался, третий опять не начался, и ни один не дошёл бы до предела,
// пока задача крутится вечно.
func TestIdleRunsCountsBothKindsAsOneStreak(t *testing.T) {
	cases := []struct {
		name     string
		comments []Comment
		want     int
	}{
		{"чередование считается одной серией", []Comment{
			notice("implementer", EventAgentUnavailable, 1),
			notice("implementer", EventRunTruncated, 2),
			notice("implementer", EventAgentUnavailable, 3),
		}, 3},
		{"отчёт роли обрывает счёт", []Comment{
			notice("implementer", EventRunTruncated, 1),
			notice("implementer", EventAgentUnavailable, 2),
			report("implementer", "done", 3),
		}, 0},
		{"считается с конца, а не за всю жизнь", []Comment{
			notice("implementer", EventAgentUnavailable, 1),
			report("implementer", "failed", 2),
			notice("implementer", EventRunTruncated, 3),
			notice("implementer", EventRunTruncated, 4),
		}, 2},
		{"разбор ответа человека обрывает счёт", []Comment{
			notice("implementer", EventAgentUnavailable, 1),
			notice("implementer", EventHumanReply, 2),
		}, 0},
		{"записи чужой роли серию не трогают", []Comment{
			notice("implementer", EventRunTruncated, 1),
			notice("reviewer", EventPushFailed, 2),
			notice("implementer", EventRunTruncated, 3),
		}, 2},
		{"чужая серия своей не считается", []Comment{
			notice("implementer", EventLeaseExpired, 1),
			notice("implementer", EventPushFailed, 2),
		}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IdleRuns(tc.comments, "implementer"); got != tc.want {
				t.Errorf("серия %d, ожидалась %d", got, tc.want)
			}
		})
	}
}

// Серии считают разное, поэтому и обрываются по-разному: пустой прогон не имеет
// отношения ни к смерти аренды, ни к неудачной публикации, и смешать их значило бы
// звать человека не с тем разговором.
func TestIdleRunsDoesNotDisturbOtherSeries(t *testing.T) {
	comments := []Comment{
		notice("implementer", EventLeaseExpired, 1),
		notice("implementer", EventAgentUnavailable, 2),
	}

	if got := LeaseExpiries(comments, "implementer"); got != 0 {
		t.Errorf("серия смертей аренды %d: пустой прогон обязан её оборвать", got)
	}
	if got := IdleRuns(comments, "implementer"); got != 1 {
		t.Errorf("серия пустых прогонов %d, ожидалась 1", got)
	}
}

// Неудачный пуш считается так же и по той же причине: это беда обвязки, а не
// провал агента, и человека по ней зовут с другим разговором.
func TestPushFailuresCountStreakFromTheEnd(t *testing.T) {
	cases := []struct {
		name     string
		comments []Comment
		want     int
	}{
		{"серия с конца", []Comment{
			notice("implementer", EventPushFailed, 1),
			report("implementer", "done", 2),
			notice("implementer", EventPushFailed, 3),
			notice("implementer", EventPushFailed, 4),
		}, 2},
		{"удачный пуш обрывает", []Comment{
			notice("implementer", EventPushFailed, 1),
			report("implementer", "done", 2),
		}, 0},
		{"чужая роль не в счёт", []Comment{
			notice("implementer", EventPushFailed, 1),
			notice("reviewer", EventPushFailed, 2),
			notice("implementer", EventPushFailed, 3),
		}, 2},
		{"вмешался человек", []Comment{
			notice("implementer", EventPushFailed, 1),
			notice("implementer", EventHumanReply, 2),
		}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PushFailures(tc.comments, "implementer"); got != tc.want {
				t.Errorf("серия %d, ожидалась %d", got, tc.want)
			}
		})
	}
}

// Любой отчёт роли означает, что прогон дошёл до конца: серия начинается заново.
func TestLeaseExpiriesResetsAfterReport(t *testing.T) {
	comments := []Comment{
		notice("implementer", EventLeaseExpired, 1),
		notice("implementer", EventLeaseExpired, 2),
		report("implementer", "done", 3),
	}

	if got := LeaseExpiries(comments, "implementer"); got != 0 {
		t.Errorf("серия %d, ожидался ноль: последним был отчёт", got)
	}
}

// Вмешательство человека — тоже смена обстоятельств, а не продолжение серии.
func TestLeaseExpiriesResetsAfterHumanReply(t *testing.T) {
	comments := []Comment{
		notice("implementer", EventLeaseExpired, 1),
		notice("implementer", EventHumanReply, 2),
	}

	if got := LeaseExpiries(comments, "implementer"); got != 0 {
		t.Errorf("серия %d, ожидался ноль: после смерти вмешался человек", got)
	}
}

// У каждой роли своя нить: чужие записи в счёт не идут и серию не обрывают.
func TestLeaseExpiriesIgnoresOtherRoles(t *testing.T) {
	comments := []Comment{
		notice("implementer", EventLeaseExpired, 1),
		report("reviewer", "done", 2),
		notice("reviewer", EventLeaseExpired, 3),
		notice("implementer", EventLeaseExpired, 4),
	}

	if got := LeaseExpiries(comments, "implementer"); got != 2 {
		t.Errorf("серия %d, ожидалась 2: записи чужой роли не в счёт", got)
	}
}

// Проза человека счёта не ведёт и не обрывает: она ничего не говорит о том,
// перестал ли раннер умирать.
func TestLeaseExpiriesIgnoresPlainComments(t *testing.T) {
	comments := []Comment{
		notice("implementer", EventLeaseExpired, 1),
		comment("owner", "смотрю, что происходит", 2),
		notice("implementer", EventLeaseExpired, 3),
	}

	if got := LeaseExpiries(comments, "implementer"); got != 2 {
		t.Errorf("серия %d, ожидалась 2: комментарий без маркера ничего не значит", got)
	}
}

// handover — отчёт прогона, передающий задачу дальше.
func handover(role, outcome, next string, minute int) Comment {
	m := Marker{RunID: runID, Role: role, Outcome: outcome, Next: next, ConfigSHA: "5bc6a3b0"}
	return comment("office", m.String()+"\nОтчёт прогона.", minute)
}

// Круг «правки → ревью» — отчёт роли, вернувшей задачу другой. Считаются они
// с конца и подряд: работа между теми же двумя ролями, в которую никто больше
// не вмешивался.
//
// Серия обрывается иначе, чем серия смертей прогона, и это не небрежность:
// смерть говорит об одном (прогон не дожил), круг — о другом (роли не сошлись).
// Поэтому у каждой серии свои правила обрыва, а общий обход только считает.
func TestReturnRoundsCountsStreakFromTheEnd(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		report("implementer", "done", 1),
		handover("reviewer", "done", "implementer", 2),
		report("implementer", "done", 3),
		handover("reviewer", "done", "implementer", 4),
	}

	if got := ReturnRounds(comments, "reviewer", "implementer", agents); got != 2 {
		t.Errorf("кругов %d, ожидалось 2: отчёты implementer'а серию не обрывают", got)
	}
}

// Круг считается по паре (роль, владелец), а не по одной роли. На двух ролях
// разницы не было: у ревьюера всё, что с `next`, было возвратом. С тремя
// счёт по роли принял бы работающий конвейер за спор — обычные передачи
// implementer'а ревьюеру идут подряд десятками.
func TestReturnRoundsCountsPairNotRole(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		handover("implementer", "done", "reviewer", 1),
		handover("reviewer", "done", "implementer", 2),
		handover("implementer", "done", "reviewer", 3),
		handover("reviewer", "done", "implementer", 4),
		handover("implementer", "done", "analyst", 5),
	}

	if got := ReturnRounds(comments, "implementer", "analyst", agents); got != 1 {
		t.Errorf("кругов %d, ожидался один: к аналитику задача ушла впервые", got)
	}
	if got := ReturnRounds(comments, "implementer", "reviewer", agents); got != 0 {
		t.Errorf("кругов %d, ожидался ноль: последним implementer отдал задачу аналитику", got)
	}
}

// Системная запись той же роли обрывает серию — так же, как обрывала прежний
// счётчик. Между двумя отчётами что-то случилось с самим прогоном, и считать
// их идущими подряд значило бы приписать роли упорство, которого не было.
func TestReturnRoundsStopAtSystemRecord(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		handover("reviewer", "done", "implementer", 1),
		notice("reviewer", EventPushFailed, 2),
		handover("reviewer", "done", "implementer", 3),
	}

	if got := ReturnRounds(comments, "reviewer", "implementer", agents); got != 1 {
		t.Errorf("кругов %d, ожидался один: серию оборвала системная запись", got)
	}
}

// Одобрение закрывает разговор: задача уходит к человеку, а прежние круги
// к следующему заходу не относятся. Обрывает серию тут пара, а не исход:
// отчёт той же роли с другим владельцем — это уже другая нить.
func TestReturnRoundsResetAfterApproval(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		handover("reviewer", "done", "implementer", 1),
		handover("reviewer", "done", "human", 2),
	}

	if got := ReturnRounds(comments, "reviewer", "implementer", agents); got != 0 {
		t.Errorf("кругов %d, ожидался ноль: последним было одобрение", got)
	}
}

// Отчёт того же рода, но без next — маркер этапа 2. Кому ушла задача, он
// не говорит, и считать его кругом значило бы гадать.
func TestReturnRoundsStopAtMarkerWithoutNext(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		handover("reviewer", "done", "implementer", 1),
		report("reviewer", "done", 2),
	}

	if got := ReturnRounds(comments, "reviewer", "implementer", agents); got != 0 {
		t.Errorf("кругов %d, ожидался ноль: старый маркер кругом не считается", got)
	}
}

// Вмешательство человека обнуляет счёт, чьей бы роли ни касался разбор ответа:
// человек говорил о задаче целиком, а не о нити одной роли. Иначе задачу,
// которую он только что разблокировал, тут же вернули бы ему обратно.
func TestReturnRoundsResetAfterHumanReply(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		handover("reviewer", "done", "implementer", 1),
		handover("reviewer", "done", "implementer", 2),
		notice("implementer", EventHumanReply, 3),
	}

	if got := ReturnRounds(comments, "reviewer", "implementer", agents); got != 0 {
		t.Errorf("кругов %d, ожидался ноль: в задачу вмешался человек", got)
	}
}

// Реплика человека — тоже вмешательство, даже если раннер ещё не успел
// её разобрать.
func TestReturnRoundsResetAfterHumanComment(t *testing.T) {
	agents := []string{"office"}
	comments := []Comment{
		handover("reviewer", "done", "implementer", 1),
		handover("reviewer", "done", "implementer", 2),
		comment("owner", "Так и задумано, не трогайте.", 3),
	}

	if got := ReturnRounds(comments, "reviewer", "implementer", agents); got != 0 {
		t.Errorf("кругов %d, ожидался ноль: человек сказал своё слово", got)
	}
}

// Маркер — разметка для раннера, и в тело pull request он не идёт: читают его
// люди. Поймано живой проверкой на GitHub.
func TestWithoutMarker(t *testing.T) {
	body := NoticeBody(Marker{RunID: "abcdef01", Role: "reviewer", Outcome: "done", ConfigSHA: "5bc6a3b0"},
		"Разбор пройден, замечаний нет.")
	got := WithoutMarker(body)
	if strings.Contains(got, Prefix) {
		t.Errorf("маркер остался: %q", got)
	}
	if !strings.Contains(got, "Разбор пройден") {
		t.Errorf("проза потерялась: %q", got)
	}

	// Прозу без маркера трогать нечего: первая строка человека — это его слова.
	human := "Открывай заново.\nPR закрыли по ошибке."
	if got := WithoutMarker(human); got != human {
		t.Errorf("слова человека обрезаны: %q", got)
	}
}

// splitReport — комментарий-маркер outcome:split с данным вложением:
// так выглядит и первое предложение, и подтверждение — разница только
// в том, какой по счёту в переписке.
func splitReport(role, attachment string, minute int) Comment {
	m := Marker{RunID: runID, Role: role, Outcome: "split", Attachment: attachment, ConfigSHA: "5bc6a3b0"}
	return comment("office", m.String()+"\nПредложение разбивки.", minute)
}

func TestSplitConfirmedNeedsSecondMarker(t *testing.T) {
	comments := []Comment{splitReport("analyst", "10001", 1)}
	if confirmed, _ := SplitConfirmed(comments, "analyst"); confirmed {
		t.Error("одно предложение не должно считаться подтверждением")
	}
}

// Между двумя split-маркерами лежит системная запись event:human-reply,
// подписанная той же ролью (unblock() ставит Role роли, которой был задан
// вопрос) — она не должна обрывать счёт: SplitConfirmed считает по всей
// истории, а не серией с конца.
func TestSplitConfirmedSurvivesInterveningHumanReply(t *testing.T) {
	comments := []Comment{
		splitReport("analyst", "10001", 1),
		comment("human", "Да, разбивай.", 2),
		notice("analyst", EventHumanReply, 3),
		splitReport("analyst", "10002", 4),
	}
	confirmed, attachment := SplitConfirmed(comments, "analyst")
	if !confirmed {
		t.Fatal("второе подряд предложение должно быть подтверждением")
	}
	if attachment != "10002" {
		t.Errorf("вложение %q, ожидалось последнее — 10002", attachment)
	}
}

func TestSplitConfirmedIgnoresOtherRole(t *testing.T) {
	comments := []Comment{
		splitReport("implementer", "10001", 1),
		splitReport("implementer", "10002", 2),
	}
	if confirmed, _ := SplitConfirmed(comments, "analyst"); confirmed {
		t.Error("split чужой роли не считается")
	}
}

// Пустая история и история вовсе без split-маркеров этой роли — не
// подтверждение: нулевой счёт от единичного отличать нечем, обе не
// подтверждение, но регрессия проверяет вырожденный случай отдельно.
func TestSplitConfirmedNoMarkers(t *testing.T) {
	cases := []struct {
		name     string
		comments []Comment
	}{
		{"пустая история", nil},
		{"есть переписка, но не split этой роли", []Comment{
			report("analyst", "done", 1),
			comment("human", "Ок.", 2),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			confirmed, attachment := SplitConfirmed(tc.comments, "analyst")
			if confirmed {
				t.Error("подтверждения быть не должно")
			}
			if attachment != "" {
				t.Errorf("вложение %q, ожидалось пустое", attachment)
			}
		})
	}
}

// Маркер без attachment — не подтверждение. Под старой (до задачи 10) версией
// role.md split-маркеры вообще не несли вложения: тикет, доживший под ней до
// двух таких маркеров, не должен считаться подтверждённым — иначе
// CompleteSplits вызовет GetAttachment(key, "") и будет проваливаться на
// каждом цикле Loop, вечно.
func TestSplitConfirmedWithoutAttachmentTag(t *testing.T) {
	comments := []Comment{
		splitReport("analyst", "", 1),
		splitReport("analyst", "", 2),
	}
	confirmed, attachment := SplitConfirmed(comments, "analyst")
	if confirmed {
		t.Error("второй split-маркер без attachment не должен подтверждать")
	}
	if attachment != "" {
		t.Errorf("вложение %q, ожидалось пустое", attachment)
	}
}
