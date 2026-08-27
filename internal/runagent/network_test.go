package runagent

import (
	"strings"
	"testing"
)

// Роль называет домены, а закрывает сеть песочница. Бэкенд local песочницы
// не заводит вовсе — значит и политики не применяет, и молчать об этом нельзя:
// человек, вписавший network.allow, вправе считать, что список работает.
func TestNetworkNoticeWarnsOnLocalBackend(t *testing.T) {
	notice := NetworkNotice(BackendLocal, []string{"pypi.org", "*.pythonhosted.org"})

	if notice == "" {
		t.Fatal("о неприменённой политике не сказано ни слова")
	}
	for _, want := range []string{BackendLocal, "pypi.org", "*.pythonhosted.org"} {
		if !strings.Contains(notice, want) {
			t.Errorf("в предупреждении нет %q: %s", want, notice)
		}
	}
}

// Там, где политика применяется, говорить нечего: строка на каждый прогон
// быстро перестанет читаться, а вместе с ней перестанет читаться и та,
// которая важна.
func TestNetworkNoticeSilentWhereNothingIsLost(t *testing.T) {
	cases := map[string]struct {
		backend string
		allow   []string
	}{
		"песочница применяет":       {DefaultBackend, []string{"pypi.org"}},
		"бэкенд по умолчанию":       {"", []string{"pypi.org"}},
		"роль не просит сети":       {BackendLocal, nil},
		"роль не просит, песочница": {DefaultBackend, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if notice := NetworkNotice(tc.backend, tc.allow); notice != "" {
				t.Errorf("сказано лишнее: %s", notice)
			}
		})
	}
}

// Базовую политику проверяют у той машины, где есть песочницы. У local их нет
// вовсе: там сеть агента — сеть хоста, и сказано об этом в NetworkNotice.
// Спрашивать sbx о машине, на которой он не участвует, — лишний вызов и лишняя
// строка.
func TestNetworkAuditSkipsLocalBackend(t *testing.T) {
	if notice := NetworkAudit(BackendLocal); notice != "" {
		t.Errorf("сказано лишнее: %s", notice)
	}
}
