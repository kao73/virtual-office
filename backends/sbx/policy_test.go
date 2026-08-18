package sbx

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// answers подделывает sbx: запоминает вызовы и отвечает заготовленным.
type answers struct {
	calls [][]string
	out   string
	err   error
}

func (a *answers) run(args ...string) (string, error) {
	a.calls = append(a.calls, args)
	return a.out, a.err
}

// Закрытая машина — то, на что раннер рассчитывает, обещая роли сеть по списку.
// Тогда сказать нечего.
func TestBasePolicySilentWhenNetworkClosed(t *testing.T) {
	a := &answers{out: `{"allowed": false, "context": "global",
		"deny_kind": "implicit", "reason": "No matching allow rule (default deny)"}`}

	notice, err := (BasePolicy{run: a.run}).Notice()
	if err != nil {
		t.Fatalf("политика не прочитана: %v", err)
	}
	if notice != "" {
		t.Errorf("сказано лишнее: %s", notice)
	}
}

// Отказ приходит вместе с ненулевым кодом выхода: `sbx policy check` отвечает
// «не пустят» и завершается единицей. Измерено на живом CLI — подделка такого
// не подсказала бы. Считать это неудачей проверки нельзя: ответ есть, и он самый
// частый из возможных.
func TestBasePolicyReadsAnswerDespiteExitCode(t *testing.T) {
	a := &answers{
		out: `{"allowed": false, "context": "global", "reason": "No matching allow rule (default deny)"}`,
		err: errors.New("sbx policy check network example.com --json: exit status 1"),
	}

	notice, err := (BasePolicy{run: a.run}).Notice()
	if err != nil {
		t.Fatalf("отказ политики принят за неудачу проверки: %v", err)
	}
	if notice != "" {
		t.Errorf("сказано лишнее: %s", notice)
	}
}

// Открытая машина — тот случай, ради которого проверка и заводилась: роль без
// единого домена получит всю сеть, и сказать об этом больше некому.
//
// Ответ взят с живого CLI дословно: разрешающий ответ не называет ни причины,
// ни правила — только «пустят». Поэтому предупреждение и отсылает к `policy ls`,
// а не пересказывает несуществующее поле.
func TestBasePolicyWarnsWhenNetworkOpen(t *testing.T) {
	a := &answers{out: `{"action": "net:connect:tcp", "allowed": true, "context": "global",
		"resource_value": "example.com:443", "type": "network"}`}

	notice, err := (BasePolicy{run: a.run}).Notice()
	if err != nil {
		t.Fatalf("политика не прочитана: %v", err)
	}
	if notice == "" {
		t.Fatal("об открытой сети не сказано ни слова")
	}
	// Человеку нужны три вещи: чем проверяли, чем смотреть и чем закрыть.
	for _, want := range []string{CanaryHost, "sbx policy ls", "sbx policy rm network"} {
		if !strings.Contains(notice, want) {
			t.Errorf("в предупреждении нет %q: %s", want, notice)
		}
	}
}

// Спрашивается глобальный контекст, а не песочница: базовую политику ставит
// человек на машину, и знать о ней надо до того, как песочница создана.
func TestBasePolicyAsksGlobalContext(t *testing.T) {
	a := &answers{out: `{"allowed": false}`}

	if _, err := (BasePolicy{run: a.run}).Notice(); err != nil {
		t.Fatalf("политика не прочитана: %v", err)
	}
	if len(a.calls) != 1 {
		t.Fatalf("вызовов sbx %d, ожидался один: %q", len(a.calls), a.calls)
	}
	want := []string{"policy", "check", "network", CanaryHost, "--json"}
	if !slices.Equal(a.calls[0], want) {
		t.Errorf("команда проверки\nполучена:  %q\nожидалась: %q", a.calls[0], want)
	}
}

// Неудача проверки — не молчание: раннер не узнал, закрыта ли сеть, и делать
// вид, что узнал, нельзя. Ответ «сеть закрыта» здесь был бы враньём.
func TestBasePolicyReportsUnreadableAnswer(t *testing.T) {
	cases := map[string]answers{
		"sbx не ответил":  {err: errors.New("401 Unauthorized: user is not authenticated to Docker")},
		"ответ не разбор": {out: "policy check: unknown flag --json"},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			notice, err := (BasePolicy{run: a.run}).Notice()
			if err == nil {
				t.Fatalf("неудача проверки выдана за ответ: %q", notice)
			}
		})
	}
}
