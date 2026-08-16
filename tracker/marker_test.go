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

func TestParseMarkerRejectsForeignLines(t *testing.T) {
	lines := []string{
		"",
		"Обычный комментарий человека.",
		"[office]",
		"[office run:488e8d8f role:implementer config:5bc6a3b0]",                           // ни исхода, ни события
		"[office run:488e8d8f role:implementer outcome:done event:human-reply config:5bc]", // и то и другое
		"[office run:488e8d8f role:implementer outcome:done config:5bc6a3b0 лишнее:поле]",  // неизвестный ключ
		" [office run:488e8d8f role:implementer outcome:done config:5bc6a3b0]",             // не первый символ
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

// Ответ человека — комментарий не-агента после маркера needs_human своей роли.
// Комментарий другой роли ответом человека не является, даже если он последний.
func TestHumanReply(t *testing.T) {
	agents := []string{"office", "office-reviewer"}

	cases := []struct {
		name     string
		comments []Comment
		want     string // тело ответа; пусто — ответа нет
	}{
		{
			name:     "человек ответил",
			comments: []Comment{report("implementer", "needs_human", 1), comment("человек", "Stripe.", 2)},
			want:     "Stripe.",
		},
		{
			name:     "вопрос задан, ответа нет",
			comments: []Comment{report("implementer", "needs_human", 1)},
		},
		{
			name:     "после вопроса писал только другой агент",
			comments: []Comment{report("implementer", "needs_human", 1), comment("office-reviewer", "Посмотрел.", 2)},
		},
		{
			name:     "последний отчёт роли — не вопрос",
			comments: []Comment{report("implementer", "needs_human", 1), comment("человек", "Stripe.", 2), report("implementer", "failed", 3)},
		},
		{
			// Идемпотентность: подтверждение уже написано, значит ответ разобран.
			// Без этого следующий tick разбирал бы тот же ответ вечно.
			name: "ответ уже разобран",
			comments: []Comment{
				report("implementer", "needs_human", 1),
				comment("человек", "Stripe.", 2),
				comment("office", Marker{RunID: runID, Role: "implementer", Event: EventHumanReply, ConfigSHA: "5bc6a3b0"}.String()+"\nВозвращаю в работу.", 3),
			},
		},
		{
			name:     "вопроса не было вовсе",
			comments: []Comment{comment("человек", "Просто мысль.", 1)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := HumanReply(tc.comments, "implementer", agents)
			switch {
			case tc.want == "" && ok:
				t.Errorf("найден ответ, которого нет: %q", got.Body)
			case tc.want != "" && !ok:
				t.Error("ответ человека не найден")
			case tc.want != "" && got.Body != tc.want:
				t.Errorf("ответ %q, ожидался %q", got.Body, tc.want)
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
