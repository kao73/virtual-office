---
change: sandbox-network-and-permissions
design-doc: docs/superpowers/specs/2026-08-27-sandbox-network-and-permissions-design.md
base-ref: e8c3aca1906ac27d1385856cf105f5d9b4f09d5e
archived-with: 2026-08-27-sandbox-network-and-permissions
---

# Слоистое разрешение network/tools — план реализации

> **Для агентов-исполнителей:** ОБЯЗАТЕЛЬНЫЙ СКИЛЛ: используй
> superpowers:subagent-driven-development (рекомендуется) или
> superpowers:executing-plans, чтобы выполнять план по задачам одну за
> другой. Шаги отмечены чекбоксами (`- [ ]`) для отслеживания.

**Цель:** протянуть слоистую (repo-wide → проект → машина → роль) модель
разрешения `network.allow`/`tools.allow`/`tools.deny` для прогона роли —
от типов в `tracker/config.go` до реального прогона на `sbx` для `EXP`
и для проекта без собственной специфики.

**Архитектура:** три уровня (repo-wide `defaults`, проект, машина) сливаются
объединением (union) внутри `tracker.LoadProjects` в `Project.Network`/
`Project.Tools`; четвёртый уровень (роль) сливается отдельной функцией
`tracker.MergeProjectRules` в конвейере — после того, как `claim()` узнал
проект задачи, а не сразу при загрузке роли. Адаптер (`adapters/claude`)
не меняется в части логики: он и так читает уже смёрженные
`role.Network.Allow`/`role.Tools.Allow`/`role.Tools.Deny`.

**Технологии:** Go, `gopkg.in/yaml.v3` (`KnownFields(true)` + `yaml:",inline"`
для встраивания), CLI `sbx` (Docker Sandboxes) для эмпирической проверки сети.

**Спека:** `docs/superpowers/specs/2026-08-27-sandbox-network-and-permissions-design.md`
(технический дизайн); контекст и решения — `docs/openspec/changes/sandbox-network-and-permissions/{proposal,design}.md`;
разбивка по группам — `docs/openspec/changes/sandbox-network-and-permissions/tasks.md`.

## Глобальные ограничения

- Семантика слияния везде — **union, не override**: ни один уровень не может
  убрать то, что назвал менее специфичный. Это касается `tools.deny` в первую
  очередь — это единственная реальная граница (design.md, «Decisions»).
- `role.yaml` как формат не меняется — те же поля `network.allow`,
  `tools.allow`, `tools.deny`.
- Новый top-level файл не заводится: repo-wide и машинный слои живут под
  зарезервированным ключом `defaults` в уже существующих `projects.yaml`/
  `projects.local.yaml`.
- `adapters/claude/adapter.go` не меняется в части логики слияния —
  слияние происходит ДО вызова `Build`, не внутри адаптера (design doc,
  «Точки интеграции»). Единственная правка в этом файле — комментарий на
  месте `--permission-mode dontAsk` (строка 180 на момент чтения).
- **Отклонение от пседокода design-doc, найденное при чтении реального кода:**
  `tracker/config.go` уже импортирует `runner` (для `runner.Outcome*`
  констант). Функция слияния уровня роли (design doc называет её
  `runner.MergeProjectRules(role, project)`) не может лежать в пакете
  `runner` с параметром `tracker.Project` — это создало бы цикл импорта
  `runner → tracker → runner`. План кладёт её в пакет `tracker` как
  `tracker.MergeProjectRules(project Project, role runner.Role) runner.Role`
  (файл `tracker/rules.go`) — `tracker` уже безопасно видит `runner`, цикла
  нет. Все места ниже, где план ссылается на эту функцию, используют это
  имя и эту сигнатуру, а не пседокод design-doc.
- **Задачи 5, 15 и 16 требуют живого доступа к `sbx`** (`sbx login`,
  работающий демон Docker Sandboxes) и не выполняются одной правкой кода за
  столом. Если у исполнителя нет такого доступа — эти задачи следует
  остановить на этом месте и передать тому, у кого доступ есть, а не
  имитировать результат по аналогии с уже проверенным Docker Hub.
- Каждая задача, трогающая Go-код, заканчивается `go build ./...` и
  `go test ./...` (или как минимум тестами изменённого пакета) зелёными —
  это не отдельный шаг «прогнать тесты», а условие «сделано» для задачи.

---

## Фаза A — слой правил в `tracker/config.go` (группа 1 `tasks.md`)

### Task 1: Типы `Rules`/`Tools` и встраивание в `officeProject`/`machineProject`/`Project`

**Файлы:**
- Изменить: `tracker/config.go`
- Тест: `tracker/config_test.go`

**Интерфейсы:**
- Производит: тип `tracker.Rules{Network []string; Tools runner.Tools}`;
  поля `Network []string` и `Tools runner.Tools` на `tracker.Project`
  (обычные поля, не embedding — `Project` не декодируется из YAML
  напрямую, собирается вручную в `LoadProjects`); embedding `Rules` в
  `officeProject`/`machineProject` через `yaml:",inline"`.
- На этом шаге `LoadProjects` ещё НЕ читает и не сливает эти поля —
  это Task 2/3. Здесь только типы и то, что декодер не падает на новых
  полях.

Перед тем, как писать этот таск, было эмпирически проверено (не через
чтение документации, а прогоном): `gopkg.in/yaml.v3` v3.0.1 с
`dec.KnownFields(true)` корректно принимает `yaml:",inline"` на встроенной
структуре — неизвестные поля по-прежнему отвергаются, а поля инлайна
разбираются на одном уровне с остальными. Значит запасной вариант из
design-doc («явные поля без embedding») не нужен — используем embedding
как и предполагал design doc.

- [x] **Шаг 1: Написать падающий тест на новые поля**

Добавить в `tracker/config_test.go` (после `TestLoadProjectsRejectsMachineKeysInOfficeFile`):

```go
// Поля network/tools встраиваются в officeProject через yaml:",inline" —
// строгий разбор (KnownFields(true)) обязан принимать их на том же уровне
// вложенности, что и default_branch. Проверено вручную на gopkg.in/yaml.v3
// v3.0.1 перед тем, как класть embedding в прод; тест фиксирует это как
// регресс, а не как разовую проверку.
func TestOfficeProjectAcceptsInlineNetworkAndTools(t *testing.T) {
	office := validOffice + "  network: [a.test]\n  tools:\n    allow: [Read]\n    deny: [\"Bash(rm*)\"]\n"
	var m map[string]officeProject
	if err := decodeStrict(writeTemp(t, ProjectsFile, office), &m); err != nil {
		t.Fatalf("network/tools на уровне проекта не разобраны: %v", err)
	}
	off := m["OFF"]
	if !slices.Equal(off.Network, []string{"a.test"}) {
		t.Errorf("network = %v, ожидалось [a.test]", off.Network)
	}
	if !slices.Equal(off.Tools.Allow, []string{"Read"}) || !slices.Equal(off.Tools.Deny, []string{"Bash(rm*)"}) {
		t.Errorf("tools = %+v, ожидалось allow:[Read] deny:[Bash(rm*)]", off.Tools)
	}
}
```

- [x] **Шаг 2: Убедиться, что тест падает**

Запустить: `go test ./tracker/... -run TestOfficeProjectAcceptsInlineNetworkAndTools -v`
Ожидается: FAIL — `officeProject` не имеет полей `Network`/`Tools` (compile error).

- [x] **Шаг 3: Добавить тип `Rules` и embedding**

В `tracker/config.go`, сразу после блока `Project` (после строки, закрывающей
`type Project struct { ... }`, перед комментарием `// Половины проекта,
разложенные по двум файлам.`), добавить:

```go
// Rules — сетевой и инструментальный слой, который может назвать любой
// уровень слоистой модели (repo-wide умолчания, конкретный проект, машина).
// Роль (уровень 4) сюда не входит: она использует собственные Network/Tools
// из runner.Role, и сливается с этим слоем отдельным шагом —
// см. MergeProjectRules (tracker/rules.go), а не здесь.
type Rules struct {
	Network []string     `yaml:"network"`
	Tools   runner.Tools `yaml:"tools"`
}
```

Заменить блок `Project`:

```go
type Project struct {
	RepoURL       string `yaml:"repo_url"`
	DefaultBranch string `yaml:"default_branch"`
	BranchPrefix  string `yaml:"branch_prefix"`
	// WorktreeRoot необязателен: пусто — значит ${OFFICE_HOME}/worktrees/<project>.
	WorktreeRoot string `yaml:"worktree_root"`
	// Tracker — трекер проекта. Обязателен: одна и та же задача не живёт разом
	// в файловом трекере и в JIRA, и раннер, запущенный с одним трекером,
	// не должен видеть чужих проектов.
	Tracker string `yaml:"tracker"`
	// Forge — куда открывать pull request. Пусто — forge у проекта нет:
	// PR-проход вырождается, но маршрут остаётся тем же (см. workflow.yaml: pr).
	Forge string `yaml:"forge"`
	// Network — уровни 1–3 слоистой модели (repo-wide + проект + машина),
	// уже объединённые LoadProjects. Уровень 4 (роль) сюда не входит —
	// его добавляет MergeProjectRules ближе к месту запуска.
	Network []string
	// Tools — то же самое для tools.allow/tools.deny.
	Tools runner.Tools
}
```

(Поля `Network`/`Tools` на `Project` — обычные, без yaml-тега: `Project`
никогда не декодируется из YAML напрямую, он собирается вручную в
`LoadProjects`; существующие yaml-теги на остальных полях `Project` уже
инертны по той же причине — decodeStrict вызывается только с `&office`/
`&machine`, никогда с `&Project`.)

Заменить блок `officeProject`/`machineProject`:

```go
type (
	// officeProject — то, что одинаково у всех, кто поднимет этот офис.
	officeProject struct {
		DefaultBranch string `yaml:"default_branch"`
		BranchPrefix  string `yaml:"branch_prefix"`
		Rules         `yaml:",inline"`
	}

	// machineProject — то, что правят, заводя новую машину или второй инстанс.
	machineProject struct {
		RepoURL      string `yaml:"repo_url"`
		WorktreeRoot string `yaml:"worktree_root"`
		Tracker      string `yaml:"tracker"`
		Forge        string `yaml:"forge"`
		Rules        `yaml:",inline"`
	}
)
```

- [x] **Шаг 4: Прогнать тесты пакета**

Запустить: `go test ./tracker/... -v`
Ожидается: PASS весь пакет, включая новый тест и весь существующий набор
(`TestLoadProjects`, `TestLoadProjectsRejectsIncomplete`,
`TestLoadProjectsRejectsMachineKeysInOfficeFile`,
`TestLoadProjectsRequiresBothHalves`, `TestShippedConfigIsValid` и т.д.) —
добавление полей аддитивно и не меняет поведение `LoadProjects`, который
их пока не читает.

- [x] **Шаг 5: Собрать весь репозиторий**

Запустить: `go build ./...`
Ожидается: успешная сборка (embedding `runner.Tools` компилируется, других
пакетов правка не касается).

- [x] **Шаг 6: Commit**

```bash
git add tracker/config.go tracker/config_test.go
git commit -m "feat(tracker): добавить тип Rules и network/tools в Project/officeProject/machineProject"
```

---

### Task 2: Зарезервированный ключ `defaults`

**Файлы:**
- Изменить: `tracker/config.go`
- Тест: `tracker/config_test.go`

**Интерфейсы:**
- Потребляет: `Rules`, `officeProject`, `machineProject` (Task 1).
- Производит: `const reservedRulesKey = "defaults"`,
  `func extractDefaultsOffice(m map[string]officeProject) (Rules, error)`,
  `func extractDefaultsMachine(m map[string]machineProject) (Rules, error)`.
  Обе вызываются из `LoadProjects`, но сами по себе не сливают `defaults`
  в проекты — это Task 3.

- [x] **Шаг 1: Написать падающие тесты**

Добавить в `tracker/config_test.go`:

```go
// defaults — не проект: отсутствие в одном из двух файлов не ошибка,
// а «на этом уровне добавок нет». Наличие в обоих — оба вклада учтены
// (это проверяет Task 3, здесь — что сам разбор ключа не падает).
func TestLoadProjectsAllowsDefaultsInEitherOrBothFiles(t *testing.T) {
	withDefaults := "defaults:\n  network: [a.test]\n"

	cases := []struct {
		name, office, machine string
	}{
		{"только в офисном файле", validOffice + withDefaults, validMachine},
		{"только в машинном файле", validOffice, validMachine + withDefaults},
		{"в обоих файлах", validOffice + withDefaults, validMachine + withDefaults},
		{"ни в одном", validOffice, validMachine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadHalves(t, tc.office, tc.machine); err != nil {
				t.Fatalf("defaults не должен быть ошибкой: %v", err)
			}
		})
	}
}

// defaults — зарезервированное имя для repo-wide/машинного слоя, а не
// проект: default_branch/branch_prefix ему не положены, и загрузчик обязан
// сказать об этом явно, а не молча принять их как проект по имени "defaults".
func TestLoadProjectsRejectsDefaultBranchUnderDefaultsKey(t *testing.T) {
	office := validOffice + "defaults:\n  default_branch: master\n"
	_, err := loadHalves(t, office, validMachine)
	if err == nil {
		t.Fatal("default_branch под defaults принят без ошибки")
	}
	if !strings.Contains(err.Error(), "defaults") || !strings.Contains(err.Error(), "default_branch") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

// Зеркально — машинные поля под defaults в machine-файле.
func TestLoadProjectsRejectsRepoURLUnderDefaultsKeyInMachineFile(t *testing.T) {
	machine := validMachine + "defaults:\n  repo_url: https://example.test/x.git\n"
	_, err := loadHalves(t, validOffice, machine)
	if err == nil {
		t.Fatal("repo_url под defaults принят без ошибки")
	}
	if !strings.Contains(err.Error(), "defaults") || !strings.Contains(err.Error(), "repo_url") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

// defaults не участвует в проверке парности ключей office/machine: он не
// проект, и требовать для него пару в другом файле значило бы обязать
// заводить пустой машинный (или офисный) слой ради синтаксиса.
func TestLoadProjectsDefaultsSkipsParityCheck(t *testing.T) {
	office := validOffice + "defaults:\n  network: [a.test]\n"
	if _, err := loadHalves(t, office, validMachine); err != nil {
		t.Fatalf("defaults только в офисном файле не должен требовать пары в машинном: %v", err)
	}
}
```

- [x] **Шаг 2: Убедиться, что тесты падают**

Запустить: `go test ./tracker/... -run TestLoadProjectsAllowsDefaultsInEitherOrBothFiles -v`
Ожидается: FAIL — `defaults` пока разбирается как обычный проект и валится
на отсутствующих `repo_url`/`tracker` (или требует пары в другом файле).

- [x] **Шаг 3: Реализовать `extractDefaultsOffice`/`extractDefaultsMachine`**

В `tracker/config.go`, перед функцией `LoadProjects`, добавить:

```go
// reservedRulesKey — имя, под которым в обоих файлах проектов живёт
// repo-wide (в ProjectsFile) или машинный (в ProjectsLocalFile) слой
// умолчаний network/tools. Не проект: не подчиняется требованиям
// к обычным записям и не участвует в проверке парности ключей office/machine.
const reservedRulesKey = "defaults"

// extractDefaultsOffice вынимает ключ "defaults" из карты офисной половины
// до того, как остальной код увидит её как список проектов. defaults — не
// проект: default_branch/branch_prefix ему не положены, а отсутствие ключа
// вовсе — не ошибка, а «repo-wide слоя добавок нет».
func extractDefaultsOffice(m map[string]officeProject) (Rules, error) {
	d, ok := m[reservedRulesKey]
	if !ok {
		return Rules{}, nil
	}
	delete(m, reservedRulesKey)
	if d.DefaultBranch != "" || d.BranchPrefix != "" {
		return Rules{}, fmt.Errorf(
			"%s: %q — зарезервированное имя для repo-wide умолчаний network/tools, "+
				"default_branch/branch_prefix ему не положены (это не проект)",
			ProjectsFile, reservedRulesKey)
	}
	return d.Rules, nil
}

// extractDefaultsMachine — зеркально extractDefaultsOffice, для машинного
// слоя в ProjectsLocalFile.
func extractDefaultsMachine(m map[string]machineProject) (Rules, error) {
	d, ok := m[reservedRulesKey]
	if !ok {
		return Rules{}, nil
	}
	delete(m, reservedRulesKey)
	if d.RepoURL != "" || d.WorktreeRoot != "" || d.Tracker != "" || d.Forge != "" {
		return Rules{}, fmt.Errorf(
			"%s: %q — зарезервированное имя для машинного слоя умолчаний network/tools, "+
				"repo_url/worktree_root/tracker/forge ему не положены (это не проект)",
			ProjectsLocalFile, reservedRulesKey)
	}
	return d.Rules, nil
}
```

- [x] **Шаг 4: Вызвать обе функции из `LoadProjects`**

В `LoadProjects`, сразу после декодирования `office` (после блока
`if err := decodeStrict(officePath, &office); err != nil { return nil, err }`)
и ДО проверки `if len(office) == 0`, вставить:

```go
	officeDefaults, err := extractDefaultsOffice(office)
	if err != nil {
		return nil, err
	}
```

Сразу после декодирования `machine` (после блока
`if err := decodeStrict(machinePath, &machine); err != nil { return nil, err }`)
и ДО цикла `for key := range machine { ... }`, вставить:

```go
	machineDefaults, err := extractDefaultsMachine(machine)
	if err != nil {
		return nil, err
	}
```

Порядок обязателен: `len(office) == 0` должен проверяться уже без ключа
`defaults` в карте (иначе офис из одного `defaults` без единого настоящего
проекта прошёл бы эту проверку), а цикл парности office/machine — уже
без `defaults` в обеих картах (иначе `defaults` в одном файле и не в другом
дал бы ложную ошибку парности). На этом шаге `officeDefaults`/
`machineDefaults` объявлены, но ещё не использованы в сборке `Project` —
компилятор потребует их использовать; это делает Task 3. Пока пометить
их `_ = officeDefaults; _ = machineDefaults` сразу после объявления —
Task 3 эту строку уберёт вместе с реальным использованием.

- [x] **Шаг 5: Прогнать тесты**

Запустить: `go test ./tracker/... -v`
Ожидается: PASS — новые тесты и весь существующий набор.

- [x] **Шаг 6: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 7: Commit**

```bash
git add tracker/config.go tracker/config_test.go
git commit -m "feat(tracker): зарезервированный ключ defaults для repo-wide/машинного слоя правил"
```

---

### Task 3: Union-слияние уровней 1–3 в `Project.Network`/`Project.Tools`

**Файлы:**
- Изменить: `tracker/config.go`
- Тест: `tracker/config_test.go`

**Интерфейсы:**
- Потребляет: `officeDefaults`, `machineDefaults` (Task 2); `Rules`,
  `Project.Network`/`Project.Tools` (Task 1).
- Производит: `func unionStrings(layers ...[]string) []string` (пакетно-
  приватная, используется и здесь, и в `tracker/rules.go`, Task 10);
  `Project.Network`/`Project.Tools` теперь населены по факту вызова
  `LoadProjects`.

- [x] **Шаг 1: Написать падающие тесты**

Добавить в `tracker/config_test.go`:

```go
// Проект без собственных network/tools наследует только defaults —
// не пусто, но и не выдумывает ничего сверх repo-wide слоя.
func TestLoadProjectsProjectInheritsOnlyDefaults(t *testing.T) {
	office := validOffice + "defaults:\n  network: [a.test]\n  tools:\n    deny: [\"Bash(git *push*)\"]\n"
	projects, err := loadHalves(t, office, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	if !slices.Equal(p.Network, []string{"a.test"}) {
		t.Errorf("network = %v, ожидалось [a.test] (только из defaults)", p.Network)
	}
	if !slices.Equal(p.Tools.Deny, []string{"Bash(git *push*)"}) {
		t.Errorf("tools.deny = %v, ожидалось [Bash(git *push*)] (только из defaults)", p.Tools.Deny)
	}
}

// Специфика одного проекта не видна другому — иначе слой перестал бы
// быть per-project и превратился в ещё один repo-wide список.
func TestLoadProjectsProjectSpecificsAreIsolated(t *testing.T) {
	office := validOffice + "defaults:\n  network: [common.test]\n" +
		"VO:\n  default_branch: main\n  branch_prefix: a/\n  network: [vo-only.test]\n"
	machine := validMachine + "VO:\n  repo_url: https://example.test/vo.git\n  tracker: mock\n"

	projects, err := loadHalves(t, office, machine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	off, _ := projects.Get("OFF")
	vo, _ := projects.Get("VO")

	if !slices.Equal(off.Network, []string{"common.test"}) {
		t.Errorf("OFF.network = %v, ожидалось [common.test] без утечки VO", off.Network)
	}
	if !slices.Equal(vo.Network, []string{"common.test", "vo-only.test"}) {
		t.Errorf("VO.network = %v, ожидалось [common.test vo-only.test]", vo.Network)
	}
}

// Проект без defaults вообще — сегодняшнее поведение не должно измениться:
// Network/Tools остаются пустыми, а не паникой на nil-слиянии.
func TestLoadProjectsWithoutDefaultsAtAllIsUnaffected(t *testing.T) {
	projects, err := loadHalves(t, validOffice, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, _ := projects.Get("OFF")
	if len(p.Network) != 0 || len(p.Tools.Allow) != 0 || len(p.Tools.Deny) != 0 {
		t.Errorf("проект без единого defaults получил правила из ниоткуда: %+v", p)
	}
}

// unionStrings — дедуп и сортировка через все слои разом, тот же приём,
// что уже применяет adapters/claude/adapter.go:networkAllow, обобщённый
// на произвольное число слоёв.
func TestUnionStringsDedupsAndSorts(t *testing.T) {
	got := unionStrings([]string{"b", "a"}, nil, []string{"a", "c"})
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("unionStrings = %v, ожидалось %v", got, want)
	}
	if got := unionStrings(); got != nil {
		t.Errorf("unionStrings() без слоёв = %v, ожидался nil", got)
	}
}
```

- [x] **Шаг 2: Убедиться, что тесты падают**

Запустить: `go test ./tracker/... -run 'TestLoadProjectsProjectInheritsOnlyDefaults|TestLoadProjectsProjectSpecificsAreIsolated|TestUnionStringsDedupsAndSorts' -v`
Ожидается: FAIL — `unionStrings` не существует, `Project.Network`/`Tools`
не населены.

- [x] **Шаг 3: Реализовать `unionStrings` и подключить слияние**

В `tracker/config.go`, рядом с `extractDefaultsMachine`, добавить:

```go
// unionStrings сливает несколько слоёв в один список без потерь: то, что
// назвал любой слой, остаётся в итоге. Один и тот же приём — и для
// network.allow, и для каждого из tools.allow/tools.deny по отдельности,
// вместо трёх разных механизмов. Обобщает приём, уже применённый
// в adapters/claude/adapter.go (networkAllow), на большее число слоёв.
func unionStrings(layers ...[]string) []string {
	var all []string
	for _, l := range layers {
		all = append(all, l...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}
```

В `LoadProjects`, убрать заглушку `_ = officeDefaults; _ = machineDefaults`
из Task 2 и заменить тело сборки проекта:

```go
	projects := Projects{}
	for key, half := range office {
		local, found := machine[key]
		if !found {
			errs = append(errs, fmt.Errorf("%s: проект назван в %s, но не заведён в %s — "+
				"неоткуда взять ни репозиторий, ни трекер",
				key, officePath, machinePath))
			continue
		}
		projects[key] = Project{
			RepoURL:       local.RepoURL,
			DefaultBranch: half.DefaultBranch,
			BranchPrefix:  half.BranchPrefix,
			WorktreeRoot:  local.WorktreeRoot,
			Tracker:       local.Tracker,
			Forge:         local.Forge,
			Network: unionStrings(officeDefaults.Network, half.Network, machineDefaults.Network, local.Network),
			Tools: runner.Tools{
				Allow: unionStrings(officeDefaults.Tools.Allow, half.Tools.Allow, machineDefaults.Tools.Allow, local.Tools.Allow),
				Deny:  unionStrings(officeDefaults.Tools.Deny, half.Tools.Deny, machineDefaults.Tools.Deny, local.Tools.Deny),
			},
		}
	}
```

(`half.Network`/`half.Tools` и `local.Network`/`local.Tools` — поля,
промотированные embedding'ом `Rules` из Task 1; обращение напрямую, без
`half.Rules.Network`, работает благодаря анонимному встраиванию.)

- [x] **Шаг 4: Прогнать тесты**

Запустить: `go test ./tracker/... -v`
Ожидается: PASS весь пакет.

- [x] **Шаг 5: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 6: Commit**

```bash
git add tracker/config.go tracker/config_test.go
git commit -m "feat(tracker): слить уровни repo-wide/проект/машина в Project.Network и Project.Tools"
```

---

## Фаза B — repo-wide список доменов (группа 2 `tasks.md`)

### Task 4: Docker Hub в `defaults.network` (проверено, готово к записи)

**Файлы:**
- Изменить: `projects.yaml`
- Тест: `tracker/config_test.go` (расширение `TestShippedConfigIsValid`)

Docker Hub уже проверен эмпирически 2026-08-27 (throwaway sbx-песочница,
протокол — `docs/notes/followup-network-and-permissions.md`, раздел
«Находка 1»). Живого прогона `sbx` для этой задачи не требуется — только
перенос уже подтверждённого списка в конфиг.

- [x] **Шаг 1: Добавить `defaults.network` в `projects.yaml`**

В `projects.yaml`, после существующего заголовочного комментария файла
и ПЕРЕД `OFFICE:`, вставить:

```yaml
# defaults — зарезервированное имя, не проект. Слой repo-wide умолчаний
# network/tools (уровень 1 слоистой модели, docs/contracts/role-sandbox-permissions.md):
# применяется к прогону любой роли на любом проекте объединением (union),
# а не переопределением — ни один более специфичный слой (проект, машина,
# роль) не может убрать то, что здесь названо. Оттого сюда не кладут
# default_branch/branch_prefix — это не проект, и загрузчик отдельно
# проверяет, что их здесь нет.
#
# Симметрично, ${OFFICE_HOME}/projects.local.yaml вправе иметь свой ключ
# defaults — машинный слой (уровень 3), тем же принципом.
defaults:
  network:
    # Docker Hub — проверено эмпирически 2026-08-27 (пустая одноразовая
    # песочница sbx → голая попытка → добавление хостов по факту ошибки),
    # docs/notes/followup-network-and-permissions.md, «Находка 1».
    - registry-1.docker.io
    - auth.docker.io
    - "*.docker.io"
    - production.cloudfront.docker.com   # CDN раздачи blob'ов, не cloudflare.docker.com
    - "*.cloudfront.docker.com"

OFFICE:
  default_branch: master
  branch_prefix: agent/

VO:
  default_branch: master
  branch_prefix: agent/

EXP:
  default_branch: main
  branch_prefix: agent/
```

- [x] **Шаг 2: Проверить, что реальный файл проходит строгий разбор и валиден по бизнес-правилам**

Добавить в `tracker/config_test.go` (расширяет существующий
`TestShippedConfigIsValid`, не создаёт новый тест — вписать в конец
функции, перед закрывающей `}`):

```go
	// Ключ defaults в реальном файле обязан пройти те же проверки, что
	// и синтетический: не содержать default_branch/branch_prefix,
	// не участвовать в парности office/machine.
	if _, err := extractDefaultsOffice(office); err != nil {
		t.Errorf("defaults в %s не проходит проверку: %v", ProjectsFile, err)
	}
```

(Эта строка встаёт в `TestShippedConfigIsValid` ПОСЛЕ существующего
`decodeStrict(filepath.Join(root, ProjectsFile), &office)` и ПЕРЕД
проверкой `len(office) == 0` — иначе `office["defaults"]` всё ещё в карте,
и `extractDefaultsOffice` мутирует её штатно, как и в `LoadProjects`.)

- [x] **Шаг 3: Прогнать тесты**

Запустить: `go test ./tracker/... -run TestShippedConfigIsValid -v`
Ожидается: PASS.

- [x] **Шаг 4: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 5: Commit**

```bash
git add projects.yaml tracker/config_test.go
git commit -m "feat(config): добавить проверенный Docker Hub в defaults.network"
```

---

### Task 5: Эмпирическая проверка GitHub/npm/PyPI/Go modules/apt — ТРЕБУЕТ ЖИВОГО `sbx`

**ЭТА ЗАДАЧА НЕ ВЫПОЛНЯЕТСЯ ПРАВКОЙ КОДА.** Нужен рабочий `sbx` на машине
исполнителя (`sbx login` пройден, демон Docker Sandboxes отвечает). Если
доступа нет — остановиться здесь, не имитировать результат и не копировать
черновой список из `docs/notes/followup-network-and-permissions.md` как
будто он уже проверен: это ровно то, от чего предостерегает design.md
(«Открытые вопросы», «Риски»).

**Файлы:**
- Изменить: `projects.yaml` (только строки, реально прошедшие проверку)
- Изменить (рекомендуется): `docs/notes/followup-network-and-permissions.md`
  (обновить колонку «Проверено эмпирически?» по факту)

**Метод** (тот же, что дал результат по Docker Hub, зафиксирован в
`docs/notes/followup-network-and-permissions.md` и `docs/notes/sbx.md`,
раздел «Сеть: как устроена политика»): пустая одноразовая песочница →
голая попытка без всякого сетевого правила → по факту `403`/отказа
добавить ИМЕННО тот хост, который назвала ошибка, — не хост «по аналогии» →
повторить попытку → зафиксировать результат. Один throwaway-sandbox можно
использовать для всех пяти экосистем по очереди: пока хосты экосистемы N
ещё не разрешены, попытка для неё — «голая», это не портит методику.

- [x] **Шаг 1: Поднять одну throwaway-песочницу на весь таск**

```bash
sbx create --name net-probe claude /tmp
```

(Рабочее пространство здесь не важно — песочница нужна только для сетевых
попыток, не для настоящего прогона роли.)

- [x] **Шаг 2: GitHub — голая попытка**

```bash
sbx exec net-probe -- git clone --depth=1 https://github.com/octocat/Hello-World.git /tmp/probe-github
```

Записать, на каком хосте и с каким кодом упала попытка (`sbx policy log
net-probe` покажет заблокированные хосты с правилом и причиной, если
попытка не даёт явного сообщения в stdout/stderr).

- [x] **Шаг 3: GitHub — добавить хосты по факту ошибки, повторить**

```bash
sbx policy allow network --sandbox net-probe github.com,api.github.com,codeload.github.com,objects.githubusercontent.com,raw.githubusercontent.com,*.githubusercontent.com
sbx exec net-probe -- git clone --depth=1 https://github.com/octocat/Hello-World.git /tmp/probe-github-2
```

Если клон прошёл — вычеркнуть из списка хосты, которые оказались лишними
(`sbx policy log net-probe` покажет, какие правила реально сработали);
записать только те, что понадобились по факту. Если клон не прошёл — искать
недостающий хост в новой ошибке, а не гадать, и повторять, пока не пройдёт
или пока не станет ясно, что доступ этим методом не даётся (тогда — не
записывать GitHub в `defaults.network`, зафиксировать причину в п. «Шаг 8»).

- [x] **Шаг 4: npm — тем же приёмом**

```bash
sbx exec net-probe -- sh -c 'cd /tmp && npm view left-pad'
# по факту ошибки:
sbx policy allow network --sandbox net-probe registry.npmjs.org,*.npmjs.org
sbx exec net-probe -- sh -c 'cd /tmp && npm view left-pad'
```

- [x] **Шаг 5: PyPI — тем же приёмом**

Эта экосистема закрывает и нужду `implementer`/`reviewer`/`analyst` —
именно её загрузку с PyPI сегодня открывает `network.allow` в трёх
`role.yaml` (Task 9 их уберёт, но только если PyPI здесь подтверждён).

```bash
sbx exec net-probe -- sh -c 'uv run --with pytest -- python -c "import pytest; print(pytest.__version__)"'
# по факту ошибки (ожидаются pypi.org и files.pythonhosted.org — известны
# из docs/notes/sbx.md, «Что ещё стучится», живой прогон этапа 3):
sbx policy allow network --sandbox net-probe pypi.org,files.pythonhosted.org,*.pythonhosted.org
sbx exec net-probe -- sh -c 'uv run --with pytest -- python -c "import pytest; print(pytest.__version__)"'
```

- [x] **Шаг 6: Go modules — тем же приёмом**

```bash
sbx exec net-probe -- sh -c 'cd /tmp && GOPATH=/tmp/gopath go get github.com/google/uuid@latest'
# по факту ошибки:
sbx policy allow network --sandbox net-probe proxy.golang.org,sum.golang.org,*.golang.org
sbx exec net-probe -- sh -c 'cd /tmp && GOPATH=/tmp/gopath go get github.com/google/uuid@latest'
```

- [x] **Шаг 7: apt/deb — решить нужность, затем проверить или явно отказаться**

Пакеты внутрь ОС песочницы (а не внутрь проекта) нужны реже, чем перечисленные
выше. Решить по факту существующих ролей: ни `analyst`, ни `implementer`,
ни `reviewer` сегодня не устанавливают системные пакеты (`uv`/`pytest`
ставятся как python-пакеты, не как `apt install`). Если решение —
«не нужно на этом этапе», зафиксировать это явно как отказ (Шаг 8) и не
тратить на это живую песочницу. Если позже находится роль/задача, которой
это нужно, — проверить тем же методом (`sbx exec ... apt-get install ...`
→ по факту ошибки → `deb.debian.org`/`archive.ubuntu.com`/`security.ubuntu.com`)
отдельным заходом, не в рамках этого плана.

- [x] **Шаг 8: Снести throwaway-песочницу**

```bash
sbx rm --force net-probe
```

- [x] **Шаг 9: Записать результат в `projects.yaml`**

В `defaults.network` (блок, заведённый Task 4) добавить только те строки,
которые реально прошли Шаги 2–6 (и, если решено проверять, Шаг 7), каждую —
с комментарием, аналогичным Docker Hub:

```yaml
defaults:
  network:
    # Docker Hub — проверено эмпирически 2026-08-27, ...
    - registry-1.docker.io
    - auth.docker.io
    - "*.docker.io"
    - production.cloudfront.docker.com
    - "*.cloudfront.docker.com"
    # GitHub — проверено эмпирически <дата этого прогона>, тем же способом
    # (throwaway sbx-песочница + голая попытка + добавление хостов по факту
    # ошибки).
    - github.com
    - api.github.com
    - codeload.github.com
    - objects.githubusercontent.com
    - raw.githubusercontent.com
    - "*.githubusercontent.com"
    # npm — проверено эмпирически <дата>.
    - registry.npmjs.org
    - "*.npmjs.org"
    # PyPI — проверено эмпирически <дата>; закрывает и нужду ролей
    # analyst/implementer/reviewer (их собственный network.allow с этими
    # же хостами убирается из role.yaml задачей 9).
    - pypi.org
    - files.pythonhosted.org
    - "*.pythonhosted.org"
    # Go modules — проверено эмпирически <дата>.
    - proxy.golang.org
    - sum.golang.org
    - "*.golang.org"
```

Точный список хостов внутри каждой группы — по факту того, что реально
понадобилось на Шагах 2–6 (может отличаться от черновика в
`docs/notes/followup-network-and-permissions.md`, если что-то из чернового
списка оказалось лишним или недостаточным). Экосистему, не прошедшую
проверку, в `defaults.network` не добавлять вовсе — оставить её строкой
в примечании (см. Шаг 10) с пометкой «не проверено/не подтверждено», а не
молчаливым пропуском.

- [x] **Шаг 10: Обновить журнал проверки**

В `docs/notes/followup-network-and-permissions.md`, в таблице «Черновой
базовый список доменов», проставить дату проверки и фактический список
хостов в колонке «Проверено эмпирически?» для каждой проверенной
экосистемы (по образцу уже стоящей строки Docker Hub); для apt/deb —
записать решение Шага 7, даже если это «нет, не нужно на этом этапе,
причина: …».

- [x] **Шаг 11: Прогнать регресс**

Запустить: `go test ./tracker/... -v`
Ожидается: PASS (правка `projects.yaml` — только новые строки в списке,
структура не менялась, `TestShippedConfigIsValid` не спрашивает конкретный
состав `defaults.network`).

- [x] **Шаг 12: Commit**

```bash
git add projects.yaml docs/notes/followup-network-and-permissions.md
git commit -m "feat(config): добавить в defaults.network проверенные эмпирически GitHub/npm/PyPI/Go modules"
```

---

## Фаза C — `tools.allow`/`tools.deny` ролей (группа 3 `tasks.md`)

### Task 6: Расширить `tools.allow` до широких правил во всех трёх `role.yaml`

**Файлы:**
- Изменить: `roles/analyst/role.yaml`, `roles/implementer/role.yaml`,
  `roles/reviewer/role.yaml`
- Изменить (регресс, см. ниже): `runner/role_test.go`

**Обоснование, почему это безопасно менять без узкого allow:** измерено
2026-08-27 (`docs/notes/followup-network-and-permissions.md`, «Находка 2»,
throwaway git-репозиторий + `claude -p --permission-mode dontAsk`):
`permissions.allow` не является техническим ограничением Bash — команду,
не описанную ни в `allow`, ни в `deny`, модель выполняет или нет по
собственному суждению. Перечисление git-подкоманд в `allow` — не защита,
а хрупкая документация намерения (флаг перед подкомандой и так уводил
команду из-под сверки в обе стороны, `docs/notes/stage-1-retro.md`,
`docs/notes/stage-5-cleanup.md`). Реальная граница — только `tools.deny`
(Task 7/8).

- [x] **Шаг 1: Обнаружить тест, который сломается, и понять, почему**

Запустить: `go test ./runner/... -run TestReviewerRoleCannotWrite -v`

Тест `TestReviewerRoleCannotWrite` (`runner/role_test.go`) сегодня
проверяет: (а) ни одно правило в `role.Tools.Allow` не начинается с
`"Edit"`, `"Write"`, `"NotebookEdit"`, `"Bash(git add"`, `"Bash(git
commit"`; (б) `role.Tools.Allow` содержит буквально `"Bash(git diff*)"`.
После расширения allow до `Bash(*)` пункт (б) перестанет выполняться
(широкое правило не равно узкому), а пункт (в) для git add/commit по сути
теряет смысл — allow никогда не было реальной защитой от них, просто до
сих пор строка `"Bash(git add*)"` не встречалась в allow ревьюера буквально.
Этот шаг — не код, а понимание: следующий шаг переписывает тест под новую
семантику, а не удаляет проверку молча.

- [x] **Шаг 2: Переписать `TestReviewerRoleCannotWrite` под новую семантику**

В `runner/role_test.go` заменить тело функции:

```go
// Право на запись — единственное, что отличает reviewer'а от implementer'а,
// и держится оно составом --tools (adapters/claude/adapter.go: toolNames
// собирает --tools из имён allow-правил), а не запретом Bash(git add/commit) —
// tools.allow не технически ограничивает Bash (измерено 2026-08-27,
// docs/notes/followup-network-and-permissions.md), поэтому его отсутствие
// в allow ничего не доказывает. Реальная защита от add/commit/restore —
// в tools.deny, и её проверяет отдельный тест после задачи 7 плана
// (роль-специфичные deny остаются в role.yaml).
func TestReviewerRoleCannotWrite(t *testing.T) {
	role, err := LoadRole("..", "reviewer")
	if err != nil {
		t.Fatalf("roles/reviewer не прочитана: %v", err)
	}

	for _, writing := range []string{"Edit", "Write", "NotebookEdit"} {
		if slices.Contains(role.Tools.Allow, writing) {
			t.Errorf("reviewer разрешает править: %q", writing)
		}
	}
	for _, want := range []string{"Read", "Bash(*)"} {
		if !slices.Contains(role.Tools.Allow, want) {
			t.Errorf("reviewer лишён %q — ему нечем читать и запускать проверки", want)
		}
	}
}
```

(Эта версия ещё не проверяет `Tools.Deny` — эта часть добавляется в
Task 7, Шаг 6, когда роль-специфичные deny приведены к финальной форме.)

- [x] **Шаг 3: Убедиться, что переписанный тест падает на текущем `role.yaml`**

Запустить: `go test ./runner/... -run TestReviewerRoleCannotWrite -v`
Ожидается: FAIL — `role.Tools.Allow` пока не содержит `"Bash(*)"`.

- [x] **Шаг 4: Расширить `tools.allow` в `roles/analyst/role.yaml`**

Заменить блок `tools:` (комментарий и `allow:`, до `deny:` включительно —
`deny:` в этом шаге не трогается, правится в Task 7):

```yaml
tools:
  # tools.allow не защищает: команда, не описанная ни в allow, ни в deny,
  # модель выполняет или нет по собственному суждению, а не по проверке
  # рантайма (измерено 2026-08-27, docs/notes/followup-network-and-permissions.md,
  # «Находка 2»; подробнее — docs/contracts/role-sandbox-permissions.md).
  # Перечисление git-подкоманд здесь раньше не давало защиты, которую
  # могло бы казаться, что даёт, — и вдобавок было хрупким к флагу перед
  # подкомандой. Единственная реальная граница — tools.deny ниже
  # и defaults.tools.deny в projects.yaml.
  allow:
    - Read
    - Grep
    - Glob
    - "Bash(*)"

  deny:
    - "Bash(git reset*)"
    - "Bash(git push*)"
    - "Bash(git remote*)"
    - "Bash(git checkout*)"
    - "Bash(git switch*)"
    - "Bash(git branch*)"
    - "Bash(git worktree*)"
    - "Bash(git config*)"
```

- [x] **Шаг 5: Расширить `tools.allow` в `roles/implementer/role.yaml`**

Заменить блок `tools:` (`allow:` до `deny:`, `deny:` не трогается):

```yaml
tools:
  # tools.allow не защищает: команда, не описанная ни в allow, ни в deny,
  # модель выполняет или нет по собственному суждению, а не по проверке
  # рантайма (измерено 2026-08-27, docs/notes/followup-network-and-permissions.md,
  # «Находка 2»). Перечисление git-подкоманд, которое здесь стояло раньше,
  # защиты не давало и вдобавок было хрупким к флагу перед подкомандой
  # (`git -c core.pager=cat push`, `PYTHONPATH=. uv run` — обе формы уводили
  # команду из-под сверки с началом строки в обе стороны). Единственная
  # реальная граница — tools.deny ниже и defaults.tools.deny в projects.yaml.
  allow:
    - Read
    - Edit
    - Write
    - "Bash(*)"

  deny:
    - "Bash(git push*)"
    - "Bash(git remote*)"
    - "Bash(git checkout*)"
    - "Bash(git switch*)"
    - "Bash(git branch*)"
    - "Bash(git worktree*)"
    - "Bash(git rebase*)"
    - "Bash(git reset*)"
    - "Bash(git config*)"
```

- [x] **Шаг 6: Расширить `tools.allow` в `roles/reviewer/role.yaml`**

Заменить блок `tools:` (`allow:` до `deny:`, `deny:` не трогается):

```yaml
tools:
  # tools.allow не защищает (измерено 2026-08-27, «Находка 2»,
  # docs/notes/followup-network-and-permissions.md) — единственная реальная
  # граница ниже, в tools.deny, и в defaults.tools.deny (projects.yaml).
  # Инструментов записи здесь по-прежнему нет: Write/Edit/NotebookEdit
  # отсутствуют в allow, а это не «пожелание промпта» — состав --tools
  # адаптер собирает из имён allow-правил (adapters/claude/adapter.go:
  # toolNames), и без имени в allow инструмента у ревьюера нет физически.
  allow:
    - Read
    - Grep
    - Glob
    - "Bash(*)"

  deny:
    - "Bash(git add*)"
    - "Bash(git commit*)"
    - "Bash(git restore*)"
    - "Bash(git push*)"
    - "Bash(git remote*)"
    - "Bash(git checkout*)"
    - "Bash(git switch*)"
    - "Bash(git branch*)"
    - "Bash(git worktree*)"
    - "Bash(git config*)"
```

- [x] **Шаг 7: Прогнать тесты**

Запустить: `go test ./runner/... -v` и `go test ./adapters/... -v`
Ожидается: PASS. `TestShippedRolesAreValid` проходит (проверяет только
`LoadRole`/`SystemPrompt`, не конкретный состав allow — не задет).
`TestReviewerRoleCannotWrite` проходит с новым телом.

- [x] **Шаг 8: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 9: Commit**

```bash
git add roles/analyst/role.yaml roles/implementer/role.yaml roles/reviewer/role.yaml runner/role_test.go
git commit -m "feat(roles): расширить tools.allow до Bash(*) во всех трёх role.yaml"
```

---

### Task 7: Перенести семь общих `tools.deny` в `defaults.tools.deny`, исправить форму со звёздочкой

**Файлы:**
- Изменить: `projects.yaml`, `roles/analyst/role.yaml`,
  `roles/implementer/role.yaml`, `roles/reviewer/role.yaml`
- Изменить: `runner/role_test.go`

**Проверенная форма** (design doc, «Формулировки `defaults.tools.deny`»;
эмпирически подтверждено 2026-08-27, throwaway git-репозиторий + локальный
bare remote): `Bash(git push*)` не ловит `git -c core.pager=cat push`;
`Bash(*git push*)` тоже не ловит (во флаговой форме подряд идущей
подстроки «git push» физически нет); `Bash(git *push*)` — звёздочка МЕЖДУ
«git» и «push» — ловит и обычную, и флаговую форму. Эта задача, помимо
переноса семи общих строк, приводит к этой же форме и оставшиеся
роль-специфичные `deny` — они несли ту же уязвимость (`Bash(git reset*)`,
`Bash(git rebase*)`, `Bash(git add*)`, `Bash(git commit*)`, `Bash(git
restore*)`), и оставлять уже диагностированную дыру только потому, что
`tasks.md` 3.2 буквально называет лишь семь общих строк, значило бы
сознательно оставить известную брешь в единственной реальной границе.
Это расширение сферы относительно буквального текста `tasks.md`, явно
отмечено здесь для ревью.

- [x] **Шаг 1: Добавить `defaults.tools.deny` в `projects.yaml`**

В `projects.yaml`, в блок `defaults:` (заведён Task 4/5), добавить `tools:`
рядом с `network:`:

```yaml
defaults:
  network:
    # ... (без изменений, из Task 4/5)
  tools:
    deny:
      # Общие для всех ролей: работу с origin, переключение веток и
      # подмену личности коммитов ведёт раннер, а не агент. Форма со
      # звёздочкой МЕЖДУ "git" и подкомандой (не по краям) — единственная,
      # что ловит и флаговую форму: проверено эмпирически 2026-08-27,
      # docs/contracts/role-sandbox-permissions.md.
      - "Bash(git *push*)"
      - "Bash(git *remote*)"
      - "Bash(git *checkout*)"
      - "Bash(git *switch*)"
      - "Bash(git *branch*)"
      - "Bash(git *worktree*)"
      - "Bash(git *config*)"
```

- [x] **Шаг 2: Убрать дублирующиеся семь строк из `roles/analyst/role.yaml`, исправить оставшуюся**

Заменить блок `deny:` (`tools.allow` от Task 6 не трогается):

```yaml
  # Общие для всех ролей (запись в origin, переключение веток, личность
  # коммитов) — в defaults.tools.deny (projects.yaml), не здесь: это
  # решение раннера для всех ролей и всех проектов разом, а не аналитика.
  # Здесь остаётся то, что специфично аналитику: `reset` откатывает чужие
  # коммиты (круг implementer → analyst оставляет их в ветке), а аналитику
  # откатывать чужое нечем и незачем — для своей ошибки есть `revert`.
  deny:
    - "Bash(git *reset*)"
```

- [x] **Шаг 3: Убрать дублирующиеся семь строк из `roles/implementer/role.yaml`, исправить оставшиеся**

Заменить блок `deny:`:

```yaml
  # Общие для всех ролей — в defaults.tools.deny (projects.yaml). Здесь —
  # то, что специфично implementer'у: история ветки общая (её видели
  # ревьюер и человек в pull request), поэтому переписывать её нельзя.
  deny:
    - "Bash(git *rebase*)"
    - "Bash(git *reset*)"
```

- [x] **Шаг 4: Убрать дублирующиеся семь строк из `roles/reviewer/role.yaml`, исправить оставшиеся**

Заменить блок `deny:`:

```yaml
  # Общие для всех ролей — в defaults.tools.deny (projects.yaml). Здесь —
  # то, что специфично ревьюеру: он читает и запускает, но не правит.
  # Write ему и так не даётся составом --tools (см. allow выше), но
  # add/commit/restore — команды Bash, не файловый инструмент, и состав
  # --tools их не остановит.
  deny:
    - "Bash(git *add*)"
    - "Bash(git *commit*)"
    - "Bash(git *restore*)"
```

- [x] **Шаг 5: Написать тест на смёрженный набор для реального `projects.yaml`**

Добавить в `tracker/config_test.go`:

```go
// Реальный projects.yaml после переноса общих deny обязан отдавать их
// каждому проекту через defaults, даже когда у проекта нет собственной
// специфики: EXP/VO/OFFICE не описывают tools вовсе.
func TestShippedDefaultsCarrySevenCommonDenyRules(t *testing.T) {
	root := filepath.Join("..")
	// Синтетическая машинная половина: у реального projects.local.yaml нет
	// коммита в репозитории (он инстанс-специфичен), поэтому тест собирает
	// минимальную сам — она нужна только чтобы LoadProjects прошёл парность.
	machine := "OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n" +
		"VO:\n  repo_url: https://example.test/v.git\n  tracker: mock\n" +
		"EXP:\n  repo_url: https://example.test/e.git\n  tracker: mock\n"

	projects, err := LoadProjects(filepath.Join(root, ProjectsFile), writeTemp(t, ProjectsLocalFile, machine))
	if err != nil {
		t.Fatalf("реальные проекты не загружены: %v", err)
	}
	want := []string{
		"Bash(git *branch*)", "Bash(git *checkout*)", "Bash(git *config*)",
		"Bash(git *push*)", "Bash(git *remote*)", "Bash(git *switch*)", "Bash(git *worktree*)",
	}
	for _, key := range []string{"OFFICE", "VO", "EXP"} {
		p, err := projects.Get(key)
		if err != nil {
			t.Fatalf("%s не найден: %v", key, err)
		}
		for _, rule := range want {
			if !slices.Contains(p.Tools.Deny, rule) {
				t.Errorf("%s.tools.deny не содержит %q (defaults не доехал)", key, rule)
			}
		}
	}
}
```

- [x] **Шаг 6: Дополнить `TestReviewerRoleCannotWrite` проверкой роль-специфичного deny**

В `runner/role_test.go`, в теле `TestReviewerRoleCannotWrite` (переписано
в Task 6), добавить после существующих проверок:

```go
	// Роль-специфичный deny (add/commit/restore) остаётся в role.yaml
	// и после переноса общих семи строк в defaults.tools.deny — это то,
	// что защищает reviewer'а от правки, раз tools.allow не защищает
	// ничего технически.
	for _, want := range []string{"Bash(git *add*)", "Bash(git *commit*)", "Bash(git *restore*)"} {
		if !slices.Contains(role.Tools.Deny, want) {
			t.Errorf("reviewer лишён роль-специфичного deny %q", want)
		}
	}
```

- [x] **Шаг 7: Прогнать тесты**

Запустить: `go test ./tracker/... ./runner/... -v`
Ожидается: PASS.

- [x] **Шаг 8: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 9: Commit**

```bash
git add projects.yaml roles/analyst/role.yaml roles/implementer/role.yaml roles/reviewer/role.yaml tracker/config_test.go runner/role_test.go
git commit -m "feat(roles): перенести общие tools.deny в defaults.tools.deny, исправить форму со звёздочкой"
```

---

### Task 8: Новые опасные команды в `defaults.tools.deny`

**Файлы:**
- Изменить: `projects.yaml`
- Изменить: `tracker/config_test.go`

Design doc и `proposal.md` называют класс («опасные команды за пределами
git-операций записи», «rm -rf и т.п., переписывание истории git»), но не
финальные строки — это явно оставлено на реализацию (`tasks.md`, 3.3;
`design.md`, «Open Questions»). Ниже — конкретные формулировки тем же
приёмом, что и в Task 7: звёздочка вокруг опасной части, а не только по
краям, чтобы не зависеть от места и формы флага.

- [x] **Шаг 1: Добавить строки в `defaults.tools.deny`**

В `projects.yaml`, в блок `defaults.tools.deny` (заведён Task 7), дописать:

```yaml
      # Переписывание истории — та же природа, что у общих семи строк
      # выше: чужая, уже опубликованная работа не должна переписываться
      # ни одной ролью.
      - "Bash(git *filter-branch*)"
      - "Bash(git *filter-repo*)"
      # Рекурсивное/принудительное удаление — тем же приёмом: звёздочка
      # вокруг флага, а не по краям, чтобы не зависеть от его формы
      # (короткая -rf/-fr или длинная --recursive/--force) и места
      # (до или после пути).
      - "Bash(rm *-r*)"
      - "Bash(rm *-f*)"
      - "Bash(rm *--recursive*)"
      - "Bash(rm *--force*)"
```

Эти строки не закрывают класс проблемы целиком (design.md, «Риски»:
хрупкость сверки по началу строки остаётся общим устройством Claude Code)
и не претендуют на исчерпывающий список — `tools.deny` остаётся живым,
расширяемым по находкам списком (design.md, «Риски», второй пункт), не
закрывается раз и навсегда этой задачей.

- [x] **Шаг 2: Написать тест на смёрженный набор**

Добавить в `tracker/config_test.go`:

```go
// Новые опасные команды (переписывание истории, rm -rf) доезжают до
// каждого проекта тем же способом, что и семь общих строк.
func TestShippedDefaultsCarryDangerousCommandDenyRules(t *testing.T) {
	root := filepath.Join("..")
	machine := "OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n" +
		"VO:\n  repo_url: https://example.test/v.git\n  tracker: mock\n" +
		"EXP:\n  repo_url: https://example.test/e.git\n  tracker: mock\n"

	projects, err := LoadProjects(filepath.Join(root, ProjectsFile), writeTemp(t, ProjectsLocalFile, machine))
	if err != nil {
		t.Fatalf("реальные проекты не загружены: %v", err)
	}
	p, err := projects.Get("EXP")
	if err != nil {
		t.Fatalf("EXP не найден: %v", err)
	}
	for _, rule := range []string{"Bash(git *filter-branch*)", "Bash(rm *-r*)", "Bash(rm *-f*)"} {
		if !slices.Contains(p.Tools.Deny, rule) {
			t.Errorf("EXP.tools.deny не содержит %q", rule)
		}
	}
}
```

- [x] **Шаг 3: Прогнать тесты**

Запустить: `go test ./tracker/... -v`
Ожидается: PASS.

- [x] **Шаг 4: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 5: Commit**

```bash
git add projects.yaml tracker/config_test.go
git commit -m "feat(config): добавить в defaults.tools.deny переписывание истории и rm -rf"
```

---

### Task 9: Убрать `network.allow` (PyPI) из всех трёх `role.yaml`

**Файлы:**
- Изменить: `roles/analyst/role.yaml`, `roles/implementer/role.yaml`,
  `roles/reviewer/role.yaml`

**Зависимость:** требует, чтобы Task 5 подтвердил PyPI эмпирически и
записал его в `defaults.network`. Если Task 5 решил, что PyPI не проходит
проверку тем же методом (маловероятно — известный работающий случай,
`docs/notes/sbx.md`, «Что ещё стучится», но формально возможно), эта
задача откладывается до тех пор, пока PyPI не окажется в `defaults.network`
по факту, а не по ожиданию.

- [x] **Шаг 1: Убрать блок `network:` из `roles/analyst/role.yaml`**

Удалить целиком (комментарий и `allow:`):

```yaml
# Тот же список, что у остальных ролей: аналитику велено понять, что в репозитории
# зелёное, а тесты поднимаются через `uv run --with pytest` — то есть загрузкой
# с PyPI. Без сети роль повторила бы 403 ревьюера этапа 3.
network:
  allow:
    - pypi.org
    - files.pythonhosted.org
```

Ничего не добавлять взамен — PyPI теперь в `defaults.network`
(Task 4/5), а собственного network-слоя у `analyst` после этого не
остаётся вовсе, и это нормально: слой необязателен (design.md,
«Migration Plan», п. 3).

- [x] **Шаг 2: Убрать блок `network:` из `roles/implementer/role.yaml`**

Удалить целиком:

```yaml
# Куда роли разрешено ходить сверх того, что нужно самому агенту. Пусто — никуда.
# Применяет список песочница; бэкенд local его не применяет и говорит об этом.
#
# Здесь он непустой не «на всякий случай»: живые прогоны показали, что агент сам
# поднимает окружение тестов через `uv run --with pytest`, а это загрузка пакетов
# с PyPI. Проекту, чьи тесты ничего не ставят, эти строки не нужны — уберите их.
network:
  allow:
    - pypi.org
    - files.pythonhosted.org
```

- [x] **Шаг 3: Убрать блок `network:` из `roles/reviewer/role.yaml`**

Удалить целиком:

```yaml
# Куда роли разрешено ходить сверх того, что нужно самому агенту. Список тот же,
# что у implementer'а, и по той же причине: ревьюер обязан прогнать тесты сам,
# а поднимаются они через `uv run --with pytest`, то есть загрузкой с PyPI.
#
# Забыть это здесь оказалось легко: сеть роли заводилась ради того, кто пишет код.
# Первый же живой прогон ревьюера при закрытой сети показал цену — 403 от PyPI,
# «тесты запустить не удалось» в отчёте и разбор, опирающийся на чтение вместо
# проверки. Роль, которой велено проверять, обязана иметь чем.
network:
  allow:
    - pypi.org
    - files.pythonhosted.org
```

- [x] **Шаг 4: Прогнать тесты**

Запустить: `go test ./runner/... ./adapters/... -v`
Ожидается: PASS — `runner.Role.Network` для этих ролей теперь пустой,
`Network` — необязательное поле (`runner/role.go`, комментарий: «пусто
означает „ничего“»), проверок содержимого сети шипованных ролей на конкретных
доменах нет (только на структуру, см. разведку перед Task 6).

- [x] **Шаг 5: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 6: Commit**

```bash
git add roles/analyst/role.yaml roles/implementer/role.yaml roles/reviewer/role.yaml
git commit -m "feat(roles): убрать дублирующийся network.allow (PyPI) — теперь в defaults.network"
```

---

## Фаза D — резолюция слоёв в раннере и адаптере (группа 4 `tasks.md`)

### Task 10: `tracker.MergeProjectRules` — слияние уровня роли

**Файлы:**
- Создать: `tracker/rules.go`
- Тест: `tracker/rules_test.go`

**Интерфейсы:**
- Потребляет: `Project` (Task 1/3), `runner.Role`, `runner.Tools`
  (уже существуют), `unionStrings` (Task 3, тот же пакет).
- Производит: `func MergeProjectRules(project Project, role runner.Role) runner.Role` —
  используется Task 11 (`pipeline.go`) и Task 12 (`run-agent/main.go`).

См. «Глобальные ограничения» выше про отклонение от пседокода design-doc:
функция лежит в пакете `tracker`, не `runner`, во избежание цикла импорта.

- [x] **Шаг 1: Написать падающие тесты**

Создать `tracker/rules_test.go`:

```go
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
```

- [x] **Шаг 2: Убедиться, что тесты падают**

Запустить: `go test ./tracker/... -run TestMergeProjectRules -v`
Ожидается: FAIL — `MergeProjectRules` не существует (ошибка компиляции).

- [x] **Шаг 3: Реализовать `MergeProjectRules`**

Создать `tracker/rules.go`:

```go
// Слияние уровня роли (4-й, самый специфичный уровень слоистой модели
// разрешений) поверх первых трёх, уже объединённых LoadProjects.
package tracker

import "github.com/kao73/virtual-office/runner"

// MergeProjectRules сливает роль с уровнями 1–3 (repo-wide, проект,
// машина), уже объединёнными в project теми же union-правилами внутри
// LoadProjects. Живёт в tracker, а не в runner: runner.Role здесь виден
// (tracker уже импортирует runner ради runner.Outcome* в разборе графа),
// а обратное направление создало бы цикл импорта.
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
```

- [x] **Шаг 4: Прогнать тесты**

Запустить: `go test ./tracker/... -v`
Ожидается: PASS весь пакет.

- [x] **Шаг 5: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 6: Commit**

```bash
git add tracker/rules.go tracker/rules_test.go
git commit -m "feat(tracker): MergeProjectRules — слияние ролевого уровня с уровнями 1-3"
```

---

### Task 11: Подключить `MergeProjectRules` в `pipeline.go` — после `claim()`

**Файлы:**
- Изменить: `pipeline/pipeline.go`
- Тест: `pipeline/pipeline_test.go`

**Интерфейсы:**
- Потребляет: `tracker.MergeProjectRules` (Task 10).
- Место: `Office.tickRole` — `LoadRole` (строка ~176) вызывается ДО
  `claim()`, когда проект задачи ещё не известен; слияние встаёт ПОСЛЕ
  успешного `claim()`, когда `task.project` (поле `claimed.project`,
  заполняется в `take()`) уже есть.

- [x] **Шаг 1: Написать падающий тест**

Добавить в `pipeline/pipeline_test.go` (рядом с `TestTickFeedsAgentTaskAndContext`):

```go
// Роль, дошедшая до агента, обязана нести уже смёрженные с проектом
// network/tools — слияние происходит после claim(), не сразу при загрузке
// роли, потому что до захвата задачи проект не известен.
func TestTickMergesProjectRulesBeforeAgentRun(t *testing.T) {
	o := newOffice(t)
	proj := o.Office.Projects["OFF"]
	proj.Network = []string{"project.test"}
	proj.Tools = runner.Tools{
		Allow: []string{"Bash(project-tool)"},
		Deny:  []string{"Bash(git *dangerous*)"},
	}
	o.Office.Projects["OFF"] = proj

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	got := o.agent.seen.Role
	if !slices.Contains(got.Network.Allow, "project.test") {
		t.Errorf("network.allow агента %v не содержит project.test", got.Network.Allow)
	}
	if !slices.Contains(got.Tools.Allow, "Bash(project-tool)") {
		t.Errorf("tools.allow агента %v не содержит Bash(project-tool)", got.Tools.Allow)
	}
	if !slices.Contains(got.Tools.Deny, "Bash(git *dangerous*)") {
		t.Errorf("tools.deny агента %v не содержит Bash(git *dangerous*)", got.Tools.Deny)
	}
}
```

- [x] **Шаг 2: Убедиться, что тест падает**

Запустить: `go test ./pipeline/... -run TestTickMergesProjectRulesBeforeAgentRun -v`
Ожидается: FAIL — агент видит роль без слияния (`Network.Allow`/`Tools.*`
из `role.yaml` как есть).

- [x] **Шаг 3: Вставить слияние в `tickRole`**

В `pipeline/pipeline.go`, в функции `tickRole`, заменить:

```go
	task, err := o.claim(roleName, flow, role)
	if err != nil || task.ref.Key == "" {
		return false, err
	}
	// Захват только что положил задачу в рабочий статус — есть на чём спросить
	// трекер о его workflow. Без работы этот вопрос не задаётся вовсе.
	o.checkWorkflow(task.ref.Project, flow)
```

на:

```go
	task, err := o.claim(roleName, flow, role)
	if err != nil || task.ref.Key == "" {
		return false, err
	}
	// Слои repo-wide/проектных/машинных правил сливаются в роль здесь, а не
	// сразу после LoadRole: до claim() проект задачи не известен — LoadRole
	// не знает, чей это прогон.
	role = tracker.MergeProjectRules(task.project, role)

	// Захват только что положил задачу в рабочий статус — есть на чём спросить
	// трекер о его workflow. Без работы этот вопрос не задаётся вовсе.
	o.checkWorkflow(task.ref.Project, flow)
```

(`pipeline/pipeline.go` уже импортирует `"github.com/kao73/virtual-office/tracker"` —
новый импорт не нужен. Переменная `role` уже объявлена через `:=` парой
строк выше — здесь простое переприсваивание `=`, тип не меняется.)

- [x] **Шаг 4: Прогнать тест**

Запустить: `go test ./pipeline/... -run TestTickMergesProjectRulesBeforeAgentRun -v`
Ожидается: PASS.

- [x] **Шаг 5: Прогнать весь пакет `pipeline`**

Запустить: `go test ./pipeline/... -v`
Ожидается: PASS — вставка происходит после успешного `claim()` и не
меняет поведение при отсутствии работы (`task.ref.Key == ""` уже вернул
раньше), так что весь остальной набор (порядка 40 тестов) не должен
измениться.

- [x] **Шаг 6: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 7: Commit**

```bash
git add pipeline/pipeline.go pipeline/pipeline_test.go
git commit -m "feat(pipeline): сливать project-правила в роль после claim(), перед прогоном агента"
```

---

### Task 12: Флаг `-project` в `run-agent/main.go`, правка комментария `adapter.go:180`

**Файлы:**
- Изменить: `runner/cmd/run-agent/main.go`
- Изменить: `adapters/claude/adapter.go`
- Тест: `runner/cmd/run-agent/main_test.go`

**Интерфейсы:**
- Потребляет: `tracker.LoadProjects`, `tracker.MergeProjectRules` (Task 10),
  `runner.Home()` (уже существует, `runner/archive.go`).
- Без флага `-project` поведение CLI не меняется: роль остаётся «в
  изоляции» — только то, что названо в её собственном `role.yaml», явный
  debug-режим, а не тихий пробел (design doc, «Точки интеграции»).

- [x] **Шаг 1: Написать падающий тест на флаг**

Добавить в `runner/cmd/run-agent/main_test.go` (рядом с
`TestRunAgentWarnsThatLocalIgnoresNetworkPolicy`):

```go
// Без --project роль остаётся в изоляции: implementer лишился собственного
// network.allow (задача 9 плана) и без флага не просит сети вовсе. С флагом
// --project OFFICE (реальный projects.yaml с defaults.network из задач 4/5)
// сеть и tools.deny роли пополняются repo-wide слоем — сравнение двух
// прогонов и есть тест механизма.
func TestDryRunProjectFlagMergesRepoWideRules(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	home := t.TempDir()
	// Машинная половина обязана назвать все три проекта офиса (парность
	// office/machine) — значения репозиториев здесь не важны, --dry-run
	// ничего не клонирует.
	machine := "OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n" +
		"VO:\n  repo_url: https://example.test/v.git\n  tracker: mock\n" +
		"EXP:\n  repo_url: https://example.test/e.git\n  tracker: mock\n"
	if err := os.WriteFile(filepath.Join(home, "projects.local.yaml"), []byte(machine), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	env := []string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	_, withoutFlag := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--dry-run")
	if strings.Contains(withoutFlag, "registry-1.docker.io") {
		t.Errorf("без --project роль уже видит repo-wide сеть:\n%s", withoutFlag)
	}

	code, withFlag := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--project", "OFFICE", "--dry-run")
	if code != 0 {
		t.Fatalf("код %d, ожидался 0; вывод: %s", code, withFlag)
	}
	if !strings.Contains(withFlag, "registry-1.docker.io") {
		t.Errorf("с --project OFFICE в сети нет repo-wide Docker Hub:\n%s", withFlag)
	}
	if !strings.Contains(withFlag, "Bash(git *push*)") {
		t.Errorf("с --project OFFICE в settings.json нет repo-wide deny:\n%s", withFlag)
	}
}

// Опечатка в имени проекта не должна тихо проигнорироваться — Projects.Get
// уже даёт содержательную ошибку, используется как есть.
func TestDryRunProjectFlagRejectsUnknownProject(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "projects.local.yaml"), []byte(
		"OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n"+
			"VO:\n  repo_url: https://example.test/v.git\n  tracker: mock\n"+
			"EXP:\n  repo_url: https://example.test/e.git\n  tracker: mock\n"), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	env := []string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	code, out := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--project", "НЕТ-ТАКОГО", "--dry-run")
	if code != 2 {
		t.Errorf("код %d, ожидался 2 (инфраструктурная беда); вывод: %s", code, out)
	}
}
```

(Тест `TestDryRunProjectFlagMergesRepoWideRules` предполагает, что к этому
моменту плана Task 4/5 уже добавили `registry-1.docker.io` в
`defaults.network`, а Task 7 — `Bash(git *push*)` в `defaults.tools.deny`;
план исполняется по порядку, так что на Task 12 это уже так.)

- [x] **Шаг 2: Убедиться, что тесты падают**

Запустить: `go test ./runner/cmd/run-agent/... -run TestDryRunProjectFlag -v`
Ожидается: FAIL (compile error — флага `--project` не существует) или,
после добавления флага без реализации, обе проверки не находят ожидаемых
строк.

- [x] **Шаг 3: Добавить флаг и слияние в `execute()`**

В `runner/cmd/run-agent/main.go`, добавить импорт:

```go
	"github.com/kao73/virtual-office/tracker"
```

В блок объявления флагов, после `taskFlag`, добавить:

```go
	projectFlag := flag.String("project", "", "проект из projects.yaml/projects.local.yaml: подмешивает "+
		"repo-wide, project- и machine-слои network/tools поверх роли, как это делает конвейер; "+
		"без флага роль остаётся в изоляции — только то, что названо в её собственном role.yaml")
```

После блока

```go
	role, err := runner.LoadRole(configRoot, *roleName)
	if err != nil {
		return 0, err
	}
```

вставить:

```go
	// Ручное воспроизведение прогона не должно расходиться с тем, что видит
	// реальный конвейер (pipeline.tickRole сливает те же слои после claim()) —
	// иначе повторится ситуация исходной находки: 403 в живом прогоне
	// не воспроизводился одинаково без понимания, откуда на самом деле
	// берётся сеть (docs/notes/followup-network-and-permissions.md).
	if *projectFlag != "" {
		home, err := runner.Home()
		if err != nil {
			return 0, err
		}
		projects, err := tracker.LoadProjects(
			filepath.Join(configRoot, tracker.ProjectsFile),
			filepath.Join(home, tracker.ProjectsLocalFile),
		)
		if err != nil {
			return 0, err
		}
		project, err := projects.Get(*projectFlag)
		if err != nil {
			return 0, err
		}
		role = tracker.MergeProjectRules(project, role)
	}
```

(Вставка — ДО блока `NetworkNotice`/`NetworkAudit`, который уже стоит
следом: диагностика обязана видеть уже смёрженную роль, не роль-как-есть.)

- [x] **Шаг 4: Поправить комментарий `adapters/claude/adapter.go:180`**

Заменить:

```go
		// Запрещает всё, чего нет в permissions.allow: диалогов в headless всё равно никто не увидит.
		"--permission-mode", "dontAsk",
```

на:

```go
		// headless-режим: диалогов подтверждения всё равно никто не увидит.
		// Не путать с ограничением состава команд — permissions.allow не
		// запрещает ничего технически (агент решает сам, что не описано
		// ни в allow, ни в deny); реальная граница — только
		// permissions.deny. Измерено 2026-08-27,
		// docs/notes/followup-network-and-permissions.md,
		// docs/contracts/role-sandbox-permissions.md.
		"--permission-mode", "dontAsk",
```

- [x] **Шаг 5: Прогнать тесты**

Запустить: `go test ./runner/cmd/run-agent/... -v`
Ожидается: PASS весь пакет, включая новые тесты.

- [x] **Шаг 6: Прогнать пакет адаптера**

Запустить: `go test ./adapters/... -v`
Ожидается: PASS — комментарий не влияет на поведение.

- [x] **Шаг 7: Собрать весь репозиторий**

Запустить: `go build ./...`

- [x] **Шаг 8: Commit**

```bash
git add runner/cmd/run-agent/main.go runner/cmd/run-agent/main_test.go adapters/claude/adapter.go
git commit -m "feat(run-agent): флаг -project подмешивает слои projects.yaml; поправить неверный комментарий adapter.go"
```

---

## Фаза E — документация (группа 5 `tasks.md`)

### Task 13: `docs/DESIGN.md` §2.6 и новый контракт `docs/contracts/role-sandbox-permissions.md`

**Файлы:**
- Изменить: `docs/DESIGN.md`
- Создать: `docs/contracts/role-sandbox-permissions.md`
- Изменить: `projects.yaml`, `roles/*/role.yaml` (только комментарии,
  добавить перекрёстные ссылки на новый контракт — по месту, где план
  выше уже писал `docs/contracts/role-sandbox-permissions.md` в
  комментариях Task 4–9: убедиться, что путь совпадает с реальным именем
  файла, созданным этим таском)

- [x] **Шаг 1: Добавить пункт в `docs/DESIGN.md` §2.6**

В `docs/DESIGN.md`, в разделе `### 2.6 Изоляция выполнения`, после
пункта, начинающегося с «**Сеть роли — `network.allow` в `role.yaml`**,
добавлено на этапе 3.», вставить новый пункт:

```markdown
- **Слоистая модель разрешений сети и инструментов**, добавлена этапом
  sandbox-network-and-permissions. `network.allow`/`tools.allow`/
  `tools.deny` прогона — объединение (union) четырёх уровней: repo-wide
  умолчания (зарезервированный ключ `defaults` в `projects.yaml`), проект,
  машина (тот же принцип в `projects.local.yaml`) и роль (`role.yaml`, как
  и было). Ни один уровень не может убрать то, что назвал менее
  специфичный — это касается и `tools.deny`, единственной реальной
  границы: `permissions.allow` не ограничивает Bash технически (агент
  решает сам, что не описано ни в allow, ни в deny), измерено 2026-08-27.
  Резолюция уровней 1–3 — внутри `tracker.LoadProjects`, уровня 4 —
  `tracker.MergeProjectRules`, вызывается конвейером после того, как
  `claim()` узнал проект задачи, а не сразу при загрузке роли. Подробности
  типов, алгоритма слияния и проверенных формулировок —
  `docs/contracts/role-sandbox-permissions.md`.
```

- [x] **Шаг 2: Создать `docs/contracts/role-sandbox-permissions.md`**

```markdown
# Контракт «слоистые network/tools прогона роли»

Как для одного прогона роли резолвится итоговый `network.allow` и итоговые
`tools.allow`/`tools.deny` из четырёх уровней. Контекст и решения —
`docs/openspec/changes/sandbox-network-and-permissions/{proposal,design}.md`;
типы и точки интеграции — `docs/superpowers/specs/2026-08-27-sandbox-network-and-permissions-design.md`.

## Зачем

Роль в песочнице `sbx` не может сама поднять то, что нужно её задаче —
прогнать тесты, поднять БД, поднять сайт через `docker compose`, — потому
что сеть песочницы закрыта по умолчанию, а разрешённые хосты заводились
вручную, задним числом, по факту `403` на живом прогоне, дословно
дублируясь в каждом `roles/*/role.yaml`. `tools.allow`/`tools.deny` несли
похожий пробел: `permissions.allow` не является техническим ограничением
Bash (измерено 2026-08-27, `docs/notes/followup-network-and-permissions.md`,
«Находка 2») — команду, не описанную ни в `allow`, ни в `deny`, модель
выполняет или нет по собственному суждению, а не по детерминированной
проверке рантайма. Реальная граница — только `permissions.deny`.

## Четыре уровня

1. **Repo-wide умолчания** — зарезервированный ключ `defaults` в
   `projects.yaml`. Типовые нужды разработки, общие почти для любого
   клиентского проекта (Docker Hub, GitHub, пакетные реестры).
2. **Project-level** — обычные ключи проектов (`EXP`, `VO`, `OFFICE`, ...)
   там же. Специфика конкретного клиентского проекта.
3. **Machine-level** — тот же принцип (`defaults` + per-project ключи)
   в `projects.local.yaml`, по аналогии с уже существующим разделением
   office/machine (`repo_url`/`tracker`/`forge` — только local,
   `default_branch`/`branch_prefix` — office).
4. **Role-level** — `roles/*/role.yaml`, как и раньше, точечная добавка
   поверх остального.

Уровни 1–3 сливаются внутри `tracker.LoadProjects` в `Project.Network`/
`Project.Tools`. Уровень 4 сливается отдельно, ближе к месту запуска роли:
`tracker.MergeProjectRules(project, role) runner.Role` (пакет `tracker`,
не `runner` — `tracker` уже импортирует `runner` для разбора графа
переходов, обратное направление создало бы цикл импорта).

## Ключ `defaults`

Не проект: не обязан существовать в обоих файлах одновременно (отсутствие
в одном означает «на этом уровне добавок нет», а не ошибку), не
подпадает под проверки `repo_url`/`tracker`/`default_branch`/
`branch_prefix` и не участвует в проверке парности ключей office/machine.
Загрузчик (`tracker.extractDefaultsOffice`/`extractDefaultsMachine`)
явно отказывает, если под `defaults` встречены поля обычного проекта
(`default_branch`/`branch_prefix` в `projects.yaml`,
`repo_url`/`worktree_root`/`tracker`/`forge` в `projects.local.yaml`) —
сигнал, что кто-то перепутал `defaults` с реальным проектом.

## Алгоритм слияния — union, не override

Одна функция на все три списка (`network.allow`, каждый из
`tools.allow`/`tools.deny` отдельно): объединение (`unionStrings`,
сортировка + дедуп), а не переопределение. Ни один уровень не может убрать
то, что добавил менее специфичный. Для `tools.deny` это прямое требование:
разрешение более специфичного уровня не ослабляет запрет менее
специфичного — иначе `tools.deny` можно было бы обойти project-level
allow. Точечного «вычитания» (снять конкретный хост/паттерн, добавленный
более общим слоем) этот контракт не предоставляет — см. `design.md`
изменения, «Decisions», почему выбрано так.

## Формулировки `tools.deny`, устойчивые к флагу перед подкомандой

Эмпирически проверено 2026-08-27 (throwaway git-репозиторий + локальный
bare remote, `claude -p --permission-mode dontAsk`):

- `Bash(git push*)` — не ловит `git -c core.pager=cat push` (известно
  ещё с `docs/notes/stage-1-retro.md`).
- `Bash(*git push*)` — тоже не ловит: во флаговой форме подряд идущей
  подстроки «git push» физически нет.
- `Bash(git *push*)` — звёздочка МЕЖДУ «git» и «push» — ловит и обычную,
  и флаговую форму. Это форма, применённая во всех `tools.deny` этого
  проекта, ролевых и repo-wide.

Тот же приём применён к неограническим командам (`rm -rf` и вариантам):
звёздочка вокруг опасной части, а не только по краям, чтобы не зависеть
от формы флага (короткая/длинная) и его места (до/после аргумента). Класс
проблемы «флаг уводит команду из-под сверки с началом строки» этим не
закрывается целиком — это давнее устройство сверки правил Claude Code,
не предмет данного контракта; `tools.deny` остаётся живым, расширяемым
по находкам списком.

## Точки интеграции

- `tracker.LoadProjects` (`tracker/config.go`) — сливает уровни 1–3 в
  `Project.Network`/`Project.Tools` на этапе загрузки конфигурации.
- `tracker.MergeProjectRules` (`tracker/rules.go`) — сливает уровень 4
  (роль) поверх уже смёрженного `Project`. В конвейере
  (`pipeline.Office.tickRole`) вызывается после успешного `claim()`, когда
  проект задачи уже известен — `LoadRole` вызывается раньше и своего
  проекта ещё не знает. В `run-agent` (ручной/debug CLI) — по флагу
  `-project`; без флага роль остаётся в изоляции, что является явным
  debug-режимом, а не тихим пробелом.
- `adapters/claude/adapter.go` не меняется в части логики: `Build` и
  `networkAllow` читают `role.Network.Allow`/`role.Tools.Allow`/
  `role.Tools.Deny` как есть — к моменту вызова эти поля уже несут
  объединённый результат всех четырёх уровней.
- `backends/sbx/sbx.go` не меняется вовсе: он применяет `l.NetworkAllow`
  так же, как и раньше (`sbx policy allow network --sandbox`), не зная
  о слоях, из которых список собран.

## Эмпирическая проверка `defaults.network`

Каждая строка `defaults.network`, кроме Docker Hub (закрыт 2026-08-27),
обязана быть проверена тем же методом, прежде чем считаться боевой:
пустая одноразовая песочница `sbx` → голая попытка операции без единого
сетевого правила → по факту `403`/отказа добавить именно названный хост →
повторить попытку → снести песочницу. Протокол и таблица со статусом
проверки каждой экосистемы — `docs/notes/followup-network-and-permissions.md`;
устройство самой сетевой политики `sbx` — `docs/notes/sbx.md`.

## Границы и известные риски

- Класс проблемы «флаг перед подкомандой уводит команду из-под сверки»
  не закрыт целиком (см. выше) — только конкретные, уже найденные формы.
- Черновой список доменов, не прошедший эмпирическую проверку, не
  считается частью боевого набора и не должен появляться в
  `defaults.network` по аналогии, без собственного прогона.
- Union-семантика не даёт способа вычесть хост/паттерн, добавленный более
  общим слоем — если такая нужда появится, это отдельное решение, не
  расширение этого контракта задним числом.
```

- [x] **Шаг 3: Прогнать регресс**

Запустить: `go test ./...`
Ожидается: PASS — документация не влияет на код.

- [x] **Шаг 4: Commit**

```bash
git add docs/DESIGN.md docs/contracts/role-sandbox-permissions.md
git commit -m "docs: описать слоистую модель network/tools в DESIGN.md и новом контракте"
```

---

## Фаза F — проверка (группа 6 `tasks.md`)

### Task 14: Полный регресс `go test ./...`

**Файлы:** нет изменений — только проверка.

- [x] **Шаг 1: Прогнать весь набор тестов**

```bash
go build ./... && go vet ./... && go test ./... -count=1
```

Ожидается: всё зелёное. Это финальный гейт группы 1–5 перед живой
проверкой на `sbx` — если что-то красное, чинить здесь, не переносить
в Task 15/16.

- [x] **Шаг 2: Проверить, что все правки закоммичены**

```bash
git status --short
```

Ожидается: пусто (или только ожидаемые артефакты вроде `.comet/`,
если они не игнорируются — сверить с `.gitignore`).

---

### Task 15: Живой прогон на `sbx` для `EXP` — ТРЕБУЕТ ЖИВОГО `sbx`

**ЭТА ЗАДАЧА НЕ ВЫПОЛНЯЕТСЯ ПРАВКОЙ КОДА.** Нужен: рабочий `sbx`
(`sbx login` пройден), настроенный `${OFFICE_HOME}` с `projects.local.yaml`
(проект `EXP` → `tracker: jira`, `repo_url` на `kao73/expense-tracker`),
доступный полигон Jira 8.13 с реальной задачей `EXP` в статусе `Ready`,
которой для выполнения нужны тесты и/или поднятие БД (кандидат — задача,
упомянутая в `docs/notes/followup-network-and-permissions.md`, «План на
возврат в master», п. 4, или любая аналогичная из бэклога `EXP`),
действующий кред агента в окружении.

**Файлы:** нет изменений кода — это runbook.

- [x] **Шаг 1: Проверить, что базовая сетевая политика машины закрыта**

```bash
sbx policy check network example.com --json
```

Ожидается: `"allowed": false` (закрытое умолчание, `docs/notes/sbx.md`,
«Проверка „а закрыта ли сеть“»). Если открыто — сначала закрыть
(`sbx policy init deny-all` на новой машине, либо `sbx policy rm network
--id default-allow-all`, если политика уже была `allow-all` и её нужно
закрыть — см. `docs/notes/sbx.md`), иначе прогон ничего не докажет:
роль получит сеть независимо от `defaults.network`.

- [x] **Шаг 2: Запустить реальный конвейер на роли `implementer`**

```bash
bin/runner tick --role implementer --tracker jira --backend sbx
```

(Из каталога с `OFFICE_CONFIG_ROOT`/`OFFICE_HOME`, настроенными на
полигон EXP.)

- [x] **Шаг 3: Разобрать результат по шагам, не только по коду выхода**

Открыть `run.log` прогона (`tail -f <workdir>/.agent/run.log` во время
прогона или сам файл после) и убедиться, что:

- никакой `403 Forbidden`/`Blocked by network policy` не связан с
  Docker Hub, PyPI, npm, GitHub или Go modules (то, что теперь в
  `defaults.network`) — если такой отказ есть, значит либо хост не
  попал в проверенный список (вернуться к Task 5), либо слияние не
  доехало до прогона (вернуться к Task 11);
- если задача требует `docker compose`/тестовую БД — `docker pull`,
  `docker compose up`, healthcheck и `docker compose down` проходят
  без сетевых отказов (тот же сценарий, что дал результат в
  `docs/notes/followup-network-and-permissions.md`, «Находка 1»);
- задача доходит до отчёта агента (не синтетический `failed` из-за
  инфраструктуры) — сам исход (`done`/`needs_human`/`blocked`) решает
  агент по существу задачи, это не предмет проверки этой задачи плана.

- [x] **Шаг 4: Записать итог**

Независимо от исхода — зафиксировать находки (что подтвердилось, что
нет) в `docs/notes/` новым файлом или дополнением к существующему
(по аналогии с `docs/notes/followup-network-and-permissions.md`), не в
этом плане: план не читает роль отчёта о живом прогоне.

---

### Task 16: Живой прогон на `sbx` для проекта без собственной специфики — ТРЕБУЕТ ЖИВОГО `sbx`

**ЭТА ЗАДАЧА НЕ ВЫПОЛНЯЕТСЯ ПРАВКОЙ КОДА.** Нужен рабочий `sbx` и реальная
задача в `Ready` на `VO` или `OFFICE` — на том из двух, для которого на
машине исполнителя настроен трекер и есть готовая к взятию задача.

**Цель:** живьём подтвердить то, что Task 3/8 уже проверили юнит-тестом —
специфика `EXP` не течёт в проект, у которого нет собственного слоя, а
repo-wide `defaults` при этом достаточен для типовой работы.

**Файлы:** нет изменений кода — это runbook.

- [x] **Шаг 1: Убедиться, что у выбранного проекта нет собственного `network`/`tools` в `projects.yaml`**

```bash
grep -A5 '^VO:' projects.yaml   # или OFFICE:, смотря какой проект выбран
```

Ожидается: только `default_branch`/`branch_prefix`, никакого `network:`
или `tools:` на уровне самого проекта (если план исполнялся по порядку,
Task 4–9 не добавляли ничего проектно-специфичного ни `VO`, ни `OFFICE` —
только `defaults`).

- [x] **Шаг 2: Запустить реальный конвейер**

```bash
bin/runner tick --role implementer --tracker <трекер этого проекта> --backend sbx
```

- [x] **Шаг 3: Проверить по `sbx policy log`, что разрешённая сеть — ровно repo-wide список**

```bash
sbx policy log <имя-песочницы-прогона>
```

(Имя песочницы — `office-<первые 8 символов run_id>`, `backends/sbx/sbx.go:sandboxName`;
run_id виден в логе конвейера.) Ожидается: разрешённые хосты — подмножество
`defaults.network` (плюс `api.anthropic.com` от адаптера) и НИ ОДНОГО
хоста, специфичного только `EXP` (если такой в проектном слое `EXP`
появится позже — на момент этого плана его нет: PyPI переехал в
`defaults`, собственный слой `EXP` пуст, см. Task 9).

- [x] **Шаг 4: Разобрать результат по существу**

Так же, как в Task 15, Шаг 3 — задача должна дойти до отчёта без
инфраструктурных сетевых отказов, используя только repo-wide умолчания.

- [x] **Шаг 5: Записать итог**

Дополнить тот же файл заметок, что и Task 15, Шаг 4, отдельным разделом
про изоляцию проектов на живом прогоне.

---

## Самопроверка плана (для исполнителя, не чекбокс)

- **Покрытие `tasks.md`:** группа 1 → Task 1–3; группа 2 → Task 4–5;
  группа 3 → Task 6–9; группа 4 → Task 10–12; группа 5 → Task 13;
  группа 6 → Task 14–16. Все 20 пунктов `tasks.md` покрыты; расширения
  сверх буквального текста (роль-специфичные deny в Task 7, тест на
  реальный `projects.yaml` в Task 4/7/8) отмечены явно в тексте задач.
- **Плейсхолдеров нет:** каждый код-шаг несёт готовый diff/файл, не
  описание того, что сделать.
- **Типы согласованы сквозь задачи:** `tracker.Rules{Network, Tools
  runner.Tools}` (Task 1) → `officeProject`/`machineProject` embedding
  (Task 1) → `extractDefaultsOffice`/`extractDefaultsMachine` возвращают
  `Rules` (Task 2) → `unionStrings(...[]string) []string` (Task 3),
  используется и в `LoadProjects` (Task 3), и в `tracker.MergeProjectRules`
  (Task 10, тот же пакет, без повторного объявления) →
  `MergeProjectRules(project Project, role runner.Role) runner.Role`
  вызывается одинаково в `pipeline.go` (Task 11) и `run-agent/main.go`
  (Task 12). Ни одно имя не меняется между задачами.
