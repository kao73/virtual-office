package claude

import (
	"strings"
	"testing"
)

// Настоящий хвост лога прогона: имена полей и порядок взяты из ~/.office/runs.
const finalEvent = `{"is_error":false,"duration_api_ms":19027,"num_turns":3,"session_id":"36934d66","total_cost_usd":0.0921689,"subtype":"success","type":"result","duration_ms":18258,"uuid":"cd26"}`

// Стоимость прогона знает только сам агент, и говорит он её на своём языке.
// Разбирает этот язык адаптер: выше по стеку формат Claude Code никому не известен.
func TestUsageReadsFinalEvent(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"36934d66"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"работаю"}]}}`,
		finalEvent,
	}, "\n") + "\n"

	usage := ParseUsage(strings.NewReader(log))
	if !usage.Known() {
		t.Fatalf("итог прогона не разобран: %+v", usage)
	}
	if usage.CostUSD != 0.0921689 {
		t.Errorf("стоимость %v, ожидалось 0.0921689", usage.CostUSD)
	}
	if usage.DurationMS != 18258 {
		t.Errorf("длительность %d, ожидалось 18258", usage.DurationMS)
	}
	if usage.Turns != 3 {
		t.Errorf("шагов %d, ожидалось 3", usage.Turns)
	}
}

// Прогон, убитый на середине, итогового события не оставляет. Это не ошибка
// разбора: учёт просто не знает, во что обошёлся такой прогон, и говорит об этом
// пустым значением, а не выдуманным нулём стоимости.
func TestUsageIsEmptyWithoutFinalEvent(t *testing.T) {
	log := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"assistant","message":{"content":[]}}` + "\n"

	if usage := ParseUsage(strings.NewReader(log)); usage.Known() {
		t.Errorf("у прогона без итога нашлась стоимость: %+v", usage)
	}
}

// Лог пишется потоком и обрывается там, где агента убили: последняя строка
// бывает половиной JSON. Спотыкаться на ней разбор не вправе — итог, если он
// был, уже записан выше.
func TestUsageSurvivesBrokenLines(t *testing.T) {
	log := "не-JSON вовсе\n" +
		finalEvent + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tex`

	usage := ParseUsage(strings.NewReader(log))
	if !usage.Known() || usage.Turns != 3 {
		t.Errorf("обрывок строки испортил разбор: %+v", usage)
	}
}

// Событие с телом файла весит куда больше буфера строки по умолчанию (64 КиБ),
// а лежит оно перед итогом. Разбор, читающий лог построчно наивно, на таком
// логе молча терял бы стоимость всех больших прогонов — то есть ровно тех,
// ради которых бюджеты и заводятся.
func TestUsageSurvivesHugeLines(t *testing.T) {
	huge := `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		strings.Repeat("текст файла ", 40000) + `"}]}}`

	log := huge + "\n" + finalEvent + "\n"

	if usage := ParseUsage(strings.NewReader(log)); !usage.Known() {
		t.Errorf("длинная строка перед итогом сорвала разбор: %+v", usage)
	}
}

// Слово result встречается и в тексте самого агента. Итогом считается событие,
// а не строка, в которой попалось нужное слово.
func TestUsageIgnoresTalkAboutResult(t *testing.T) {
	log := `{"type":"assistant","message":{"content":[{"type":"text","text":"пишу \"type\":\"result\" в отчёт"}]}}` + "\n"

	if usage := ParseUsage(strings.NewReader(log)); usage.Known() {
		t.Errorf("реплика агента принята за итог прогона: %+v", usage)
	}
}
