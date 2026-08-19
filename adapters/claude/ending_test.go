package claude

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
)

// Итоговые события живых прогонов из ~/.office/runs.
//
// Числовые поля взяты из архива **дословно** и руками не переписывались:
// на переписывании здесь уже четырежды ловились ошибки — то выдуманная
// длительность, то перепутанные местами числа шагов. Правится этот блок
// не набором, а выемкой из архива заново.
//
// Две вольности, и обе видны: поле result укорочено до первой строки — именно
// её и берёт разбор, — и обратные кавычки в нём заменены на одинарные, потому
// что raw-строка Go их не терпит.
const (
	// Прогон дошёл до конца сам: implementer по VO-12. (fe00b3fa)
	endCompleted = `{"type":"result","subtype":"success","is_error":false,"terminal_reason":"completed","num_turns":24,"total_cost_usd":0.3709136,"duration_ms":98858,"result":"Задача VO-12 выполнена: 'escape_md' переписан согласно решению владельца (идемпотентность приоритетнее, сам обратный слэш не экранируется), тесты и CHANGELOG обновлены, изменения закоммичены ('f25b056'), '.agent/result.json' записан с исходом 'done'."}`

	// Срезан пределом шагов при max_turns: 50. Тот самый прогон,
	// который не успел написать result.json и чуть не увёл задачу с чужим
	// отчётом — docs/notes/stage-4-load-2.md. (8dfc3e3e)
	endMaxTurns = `{"type":"result","subtype":"error_max_turns","is_error":true,"terminal_reason":"max_turns","num_turns":51,"total_cost_usd":1.7751908999999997,"duration_ms":432672}`

	// Обрыв связи на втором шаге: результата прогон не оставил.
	// Здесь же ловушка — subtype говорит "success" при is_error true. (f4705aba)
	endBreakEarly = `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","num_turns":2,"total_cost_usd":0.0494975,"duration_ms":183782,"result":"API Error: Unable to connect to API (ECONNRESET)"}`

	// Тот же обрыв, но поздно. Прогон при этом был **удачным**: агент
	// дописал работу и результат, связь оборвалась уже на завершении, задача
	// ушла к ревьюеру и была одобрена. (5beb60cf)
	endBreakLate = `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","num_turns":25,"total_cost_usd":0.3671,"duration_ms":694727,"result":"API Error: Unable to connect to API (ECONNRESET)"}`

	// Обратный узор: CLI считает прогон дошедшим до конца, а результата
	// нет. Ревьюеру не дали ни одного инструмента записи, и отчитаться было
	// нечем — docs/notes/smoke.md. Таких прогонов в архиве три. (251a62ea)
	endSilent = `{"type":"result","subtype":"success","is_error":false,"terminal_reason":"completed","num_turns":6,"total_cost_usd":0.22266020000000003,"duration_ms":48797,"result":"Я не могу выполнить обязательное требование этой роли: у меня нет прав ни на один инструмент записи файлов."}`
)

// Удачный прогон объяснять нечего: итоговая строка такого прогона — его отчёт,
// и второй раз в тикете она не нужна.
func TestEndingReadsCompletedRun(t *testing.T) {
	ending := ParseEnding(strings.NewReader(endCompleted + "\n"))

	if ending.Reason != runner.EndCompleted {
		t.Errorf("конец %q, ожидался %q", ending.Reason, runner.EndCompleted)
	}
	if ending.Provider != "completed" {
		t.Errorf("слово поставщика %q, ожидалось completed", ending.Provider)
	}
	if ending.Detail != "" {
		t.Errorf("объяснение удачного прогона %q, ожидалось пустое", ending.Detail)
	}
}

// Предел шагов — усечение, а не провал: агент работал и не успел.
func TestEndingReadsMaxTurns(t *testing.T) {
	ending := ParseEnding(strings.NewReader(endMaxTurns + "\n"))

	if ending.Reason != runner.EndMaxTurns {
		t.Errorf("конец %q, ожидался %q", ending.Reason, runner.EndMaxTurns)
	}
}

// Главная ловушка живого лога: subtype равен "success" у прогона, оборванного
// сетью. Судить по нему нельзя — решает terminal_reason, а окончательно
// решает след работы, которого адаптер не видит вовсе.
func TestEndingIgnoresLyingSubtype(t *testing.T) {
	for _, log := range []string{endBreakEarly, endBreakLate} {
		ending := ParseEnding(strings.NewReader(log + "\n"))

		if ending.Reason != runner.EndError {
			t.Errorf("конец %q, ожидался %q (subtype в этом логе врёт)", ending.Reason, runner.EndError)
		}
		if ending.Detail != "API Error: Unable to connect to API (ECONNRESET)" {
			t.Errorf("объяснение %q, ожидалась строка агента про ECONNRESET", ending.Detail)
		}
	}
}

// Два обрыва связи различаются только числом шагов, и адаптер их не различает
// намеренно: разница видна по следу работы, а не по событию. Здесь проверяется,
// что адаптер и не пытается — оба лога дают один и тот же ответ.
func TestEndingDoesNotJudgeWhetherWorkStarted(t *testing.T) {
	early := ParseEnding(strings.NewReader(endBreakEarly + "\n"))
	late := ParseEnding(strings.NewReader(endBreakLate + "\n"))

	if early != late {
		t.Errorf("адаптер развёл два обрыва связи (%+v против %+v), а это дело следа работы", early, late)
	}
}

// Прогон, убитый до итогового события, о своём конце не отчитывается.
// Это не ошибка разбора: «не знаю» — законный ответ, и выдумывать за него
// «дошёл до конца» было бы враньём в самую опасную сторону.
func TestEndingIsUnknownWithoutFinalEvent(t *testing.T) {
	log := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"assistant","message":{"content":[]}}` + "\n"

	if ending := ParseEnding(strings.NewReader(log)); ending.Reason != runner.EndUnknown {
		t.Errorf("конец %q, ожидался неизвестный", ending.Reason)
	}
}

// Словарь конца принадлежит поставщику, и пополнить его он может без нас.
// Незнакомое слово при явной беде — всё-таки беда; слово при этом сохраняется,
// чтобы человек увидел, чего раннер не понял.
func TestEndingTreatsUnknownReasonWithErrorAsError(t *testing.T) {
	log := `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"quota_exhausted","num_turns":7,"result":"нечто новое"}`

	ending := ParseEnding(strings.NewReader(log + "\n"))
	if ending.Reason != runner.EndError {
		t.Errorf("конец %q, ожидался %q", ending.Reason, runner.EndError)
	}
	if ending.Provider != "quota_exhausted" {
		t.Errorf("слово поставщика %q, ожидалось сохранённым", ending.Provider)
	}
}

// А незнакомое слово без пометки об ошибке остаётся неразобранным: гадать
// раннер не станет, но и промолчать не вправе — слово видно человеку.
func TestEndingKeepsUnknownReasonWithoutError(t *testing.T) {
	log := `{"type":"result","subtype":"success","is_error":false,"terminal_reason":"handed_off","num_turns":7}`

	ending := ParseEnding(strings.NewReader(log + "\n"))
	if ending.Reason != runner.EndUnknown {
		t.Errorf("конец %q, ожидался неизвестный", ending.Reason)
	}
	if ending.Provider != "handed_off" {
		t.Errorf("слово поставщика %q, ожидалось сохранённым", ending.Provider)
	}
}

// Объяснение едет в тикет рядом с запиской раннера, а не вместо неё: берётся
// первая строка и не длиннее предела.
func TestEndingTrimsDetail(t *testing.T) {
	long := strings.Repeat("очень длинное объяснение ", 40)
	log := `{"type":"result","is_error":true,"terminal_reason":"api_error","num_turns":3,` +
		`"result":"первая строка\nвторая строка"}`

	if ending := ParseEnding(strings.NewReader(log + "\n")); ending.Detail != "первая строка" {
		t.Errorf("объяснение %q, ожидалась только первая строка", ending.Detail)
	}

	ending := ParseEnding(strings.NewReader(
		`{"type":"result","is_error":true,"terminal_reason":"api_error","result":"` + long + `"}` + "\n"))
	if runes := []rune(ending.Detail); len(runes) > maxEndDetail+1 {
		t.Errorf("объяснение длиной %d рун, предел %d", len(runes), maxEndDetail)
	}
	if !strings.HasSuffix(ending.Detail, "…") {
		t.Errorf("обрезанное объяснение %q не помечено многоточием", ending.Detail)
	}
}

// Живой узор, обратный обрыву связи: CLI считает прогон дошедшим до конца,
// а результата нет. Адаптер обязан отдать здесь ровно то же, что у любого
// удачного прогона, — «completed», — и это не оплошность, а разделение труда:
// был ли результат, знает не лог, а раннер. Разведёт эти два случая
// runagent.terminationOf, у которого файл результата перед глазами.
//
// Случай не выдуман: в архиве таких прогонов три — прогоны ревьюера, которому
// забыли дать право на файл результата (docs/notes/smoke.md).
func TestEndingReadsSilentRunAsCompleted(t *testing.T) {
	ending := ParseEnding(strings.NewReader(endSilent + "\n"))

	if ending.Reason != runner.EndCompleted {
		t.Errorf("конец %q, ожидался %q: лог о результате ничего не знает", ending.Reason, runner.EndCompleted)
	}
}
