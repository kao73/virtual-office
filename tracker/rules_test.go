package tracker

import (
	"slices"
	"testing"

	"github.com/kao73/virtual-office/runner"
)

// Слияние без потерь: deny роли не может исчезнуть при слиянии с deny
// проекта — tools.deny остаётся единственной реальной границей и не
// сужается ни одним уровнем.
func TestMergeProjectRulesUnionsWithoutLoss(t *testing.T) {
	project := Project{
		Network: []string{"registry-1.docker.io"},
		Tools: runner.Tools{
			Allow: []string{"Bash(project-tool)"},
			Deny:  []string{"Bash(git *push*)"},
		},
	}
	role := runner.Role{
		Network: runner.Network{Allow: []string{"pypi.org"}},
		Tools: runner.Tools{
			Allow: []string{"Read"},
			Deny:  []string{"Bash(git *rebase*)"},
		},
	}

	merged := MergeProjectRules(project, role)

	if want := []string{"pypi.org", "registry-1.docker.io"}; !slices.Equal(merged.Network.Allow, want) {
		t.Errorf("network.allow = %v, ожидалось %v", merged.Network.Allow, want)
	}
	if want := []string{"Bash(project-tool)", "Read"}; !slices.Equal(merged.Tools.Allow, want) {
		t.Errorf("tools.allow = %v, ожидалось %v", merged.Tools.Allow, want)
	}
	if want := []string{"Bash(git *push*)", "Bash(git *rebase*)"}; !slices.Equal(merged.Tools.Deny, want) {
		t.Errorf("tools.deny = %v, ожидалось %v", merged.Tools.Deny, want)
	}
}

// Дедуп повторов между слоями: одна и та же строка на двух уровнях не
// должна размножаться в итоговом списке.
func TestMergeProjectRulesDedupsOverlap(t *testing.T) {
	project := Project{Tools: runner.Tools{Deny: []string{"Bash(git *push*)"}}}
	role := runner.Role{Tools: runner.Tools{Deny: []string{"Bash(git *push*)"}}}

	merged := MergeProjectRules(project, role)

	if want := []string{"Bash(git *push*)"}; !slices.Equal(merged.Tools.Deny, want) {
		t.Errorf("tools.deny = %v, ожидался один элемент без повтора: %v", merged.Tools.Deny, want)
	}
}

// Исходные срезы роли не мутируются: MergeProjectRules возвращает новое
// значение Role (Role и так передаётся по значению везде в коде), а не
// правит слайсы на месте.
func TestMergeProjectRulesDoesNotMutateInputs(t *testing.T) {
	project := Project{Network: []string{"a.test"}}
	roleAllow := []string{"b.test"}
	role := runner.Role{Network: runner.Network{Allow: roleAllow}}

	_ = MergeProjectRules(project, role)

	if !slices.Equal(roleAllow, []string{"b.test"}) {
		t.Errorf("исходный срез роли изменён: %v", roleAllow)
	}
}
