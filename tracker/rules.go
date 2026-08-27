package tracker

import "github.com/kao73/virtual-office/runner"

// MergeProjectRules сливает роль (уровень 4 — самый специфичный уровень
// слоистой модели разрешений) с уровнями 1–3 (repo-wide, проект, машина),
// уже объединёнными в project теми же union-правилами внутри LoadProjects.
// Живёт в tracker, а не в runner: runner.Role здесь виден (tracker уже
// импортирует runner ради runner.Outcome* в разборе графа), а обратное
// направление создало бы цикл импорта.
//
// Возвращает новую Role — копия, Role и так передаётся по значению везде
// в существующем коде. Дальше по коду ничего не отличает «роль после
// слияния» от «роль как есть»: adapters/claude/adapter.go не меняется
// в части типов, только получает уже смёрженную роль.
func MergeProjectRules(project Project, role runner.Role) runner.Role {
	role.Network.Allow = unionStrings(project.Network, role.Network.Allow)
	role.Tools.Allow = unionStrings(project.Tools.Allow, role.Tools.Allow)
	role.Tools.Deny = unionStrings(project.Tools.Deny, role.Tools.Deny)
	return role
}
