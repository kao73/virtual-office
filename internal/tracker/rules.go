package tracker

import "github.com/kao73/virtual-office/internal/runner"

// MergeProjectRules сливает роль (уже несущую базовый слой roles/_base/base.yaml,
// см. runner.LoadRole) с машинным и проектным слоями, уже объединёнными
// в project теми же union-правилами внутри LoadProjects.
// Живёт в tracker, а не в runner: runner.Role здесь виден (tracker уже
// импортирует runner ради runner.Outcome* в разборе графа), а обратное
// направление создало бы цикл импорта.
//
// Возвращает новую Role — копия, Role и так передаётся по значению везде
// в существующем коде. Дальше по коду ничего не отличает «роль после
// слияния» от «роль как есть»: internal/adapters/claude/adapter.go не меняется
// в части типов, только получает уже смёрженную роль.
func MergeProjectRules(project Project, role runner.Role) runner.Role {
	role.Network.Allow = runner.Union(project.Network, role.Network.Allow)
	role.Tools.Allow = runner.Union(project.Tools.Allow, role.Tools.Allow)
	role.Tools.Deny = runner.Union(project.Tools.Deny, role.Tools.Deny)
	return role
}
