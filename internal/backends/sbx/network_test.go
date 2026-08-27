package sbx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// steps записывает, что и в каком порядке звали у sbx.
type steps struct {
	calls  [][]string
	fail   error
	failOn string // первое слово команды, на которой падать
}

func (s *steps) run(_ context.Context, args ...string) error {
	s.calls = append(s.calls, args)
	if s.fail != nil && (s.failOn == "" || (len(args) > 1 && args[1] == s.failOn)) {
		return s.fail
	}
	return nil
}

// Сеть роли задаётся правилом на её собственную песочницу, и задаётся она
// **после** создания: раньше правилу не к чему прицепиться, позже — агент уже
// работает и половину запросов сделает вслепую.
func TestPrepareAllowsRoleHostsAfterCreate(t *testing.T) {
	l := fixtureLaunch()
	l.NetworkAllow = []string{"pypi.org", "*.pythonhosted.org"}

	s := &steps{}
	if err := prepare(context.Background(), "office-550e8400", l, s.run); err != nil {
		t.Fatalf("песочница не подготовлена: %v", err)
	}

	if len(s.calls) != 2 {
		t.Fatalf("вызовов sbx %d, ожидалось два (create и policy): %q", len(s.calls), s.calls)
	}
	if got := s.calls[0][0]; got != "create" {
		t.Errorf("первой командой шла %q, ожидалось create", got)
	}
	want := []string{"policy", "allow", "network", "--sandbox", "office-550e8400", "pypi.org,*.pythonhosted.org"}
	if !slices.Equal(s.calls[1], want) {
		t.Errorf("команда политики\nполучена:  %q\nожидалась: %q", s.calls[1], want)
	}
}

// Роль, которой сеть не нужна, политику не трогает вовсе: лишнее правило —
// это расширение доступа, пусть и пустое.
func TestPrepareLeavesPolicyAloneWithoutHosts(t *testing.T) {
	s := &steps{}
	if err := prepare(context.Background(), "office-550e8400", fixtureLaunch(), s.run); err != nil {
		t.Fatalf("песочница не подготовлена: %v", err)
	}

	if len(s.calls) != 1 {
		t.Errorf("политику трогали без нужды: %q", s.calls)
	}
}

// Не применившееся правило — не мелочь: роль пойдёт работать без сети, которую
// просила, и провалится непонятно почему. Лучше не начинать.
func TestPrepareFailsWhenPolicyRejected(t *testing.T) {
	l := fixtureLaunch()
	l.NetworkAllow = []string{"pypi.org"}

	s := &steps{fail: errors.New("policy: 401 Unauthorized"), failOn: "allow"}
	err := prepare(context.Background(), "office-550e8400", l, s.run)
	if err == nil {
		t.Fatal("прогон начат без запрошенной сети")
	}
	if !strings.Contains(err.Error(), "pypi.org") {
		t.Errorf("ошибка не называет, чего роль не получила: %v", err)
	}
}

// Хосты уезжают в sbx как их написала роль: разбор подстановок и портов —
// его дело, а не наше.
func TestPolicyArgsKeepHostsAsWritten(t *testing.T) {
	got := policyArgs("office-1", []string{"pypi.org", "*.pythonhosted.org", "registry.npmjs.org:443"})
	if last := got[len(got)-1]; last != "pypi.org,*.pythonhosted.org,registry.npmjs.org:443" {
		t.Errorf("список хостов искажён: %q", last)
	}
}
