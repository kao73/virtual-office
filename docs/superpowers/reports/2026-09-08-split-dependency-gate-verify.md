# Отчёт верификации: split-dependency-gate

Comet Classic full workflow, полный режим верификации (`comet state scale`:
17 задач > 3, 2 капабилити делта-спек > 1, 25 изменённых файлов > 8 — все три
порога превышены).

## Сводка

| Измерение | Статус |
|---|---|
| Полнота | 17/17 задач `tasks.md`; 4/4 требования `pipeline-dependency-gate` покрыты; 1/1 требование `role-native-workflow` покрыто |
| Корректность | Все требования реализованы и покрыты и юнит-, и поведенческими тестами (все прогнаны заново, зелёные); два ключевых сценария (направление чтения `issuelinks`, гейт+видимость целиком) дополнительно подтверждены живыми прогонами на реальном JIRA-полигоне |
| Согласованность | Реализация совпадает со всеми 7 решениями `design.md`; финальное ревью всей ветки нашло 3 Important + 6 Minor расхождений, все закрыты одной волной фиксов, повторное ревью — чисто |

## Полнота

**Задачи.** Все 17 пунктов `tasks.md` отмечены `[x]`. 27 коммитов от
`7d182d0` (merge-base с master, `base-ref` плана) до `fc67633` (конец волны
фиксов и живой проверки Task 12), включая открытие/дизайн change (`89fc513`,
`7ef17c6`), запись плана (`559f00a`), 12 задач реализации, живую проверку
направления `issuelinks` (Tasks 4/5), финальную волну фиксов (`9d86ef3`,
`0cf31cf`, `8d574ab`, плюс прямой фикс ревьюера `7169a2c`) и живую проверку
гейта целиком (`fc67633`).

**Покрытие требований спекой.**

`specs/pipeline-dependency-gate/spec.md` (новая капабилити) — 4 ADDED
Requirements, разобраны по отдельности ниже. `specs/role-native-workflow/spec.md`
(существующая капабилити) — 1 ADDED Requirement (новое требование к уже
существующей капабилити), тоже ниже.

## Корректность

### ADDED: A candidate with an unresolved dependency is not claimed

Реализация: `internal/pipeline/pipeline.go:296-357` (`claim()`) — после
`ListReady`, при наличии хотя бы одного кандидата с непустым `DependsOn`,
строит `byKey` через `projectByKey` (`List(project, Workflow.Statuses)`),
затем `UnmetDependencies(ref, byKey, Workflow.IsTerminal)` пропускает
кандидата тем же путём, что и исчерпанные попытки.

- **Scenario «Unresolved dependency blocks claim»** —
  `TestClaimSkipsCandidateWithUnresolvedDependency` (fresh run: PASS).
- **Scenario «Dependency reaching a terminal status unblocks the candidate»**
  — `TestClaimTakesCandidateOnceDependencyIsTerminal` (fresh run: PASS).
- **Scenario «A missing dependency task blocks the candidate»** —
  `TestClaimTreatsMissingDependencyAsUnresolved` (fresh run: PASS), плюс
  `internal/pipeline/deps_test.go`, `TestUnmetDependenciesTreatsMissingKeyAsUnresolved`
  на самом хелпере.

### ADDED: The gate applies uniformly across workflow roles

Реализация: один и тот же вызов `UnmetDependencies` в `claim()`, без
`if roleName == "reviewer"` или подобного ветвления.

- **Scenario «An analyst candidate is gated the same as an implementer
  candidate»** — `TestClaimGatesAnalystCandidateTheSameWay` (fresh run:
  PASS).

### ADDED: Dependency status is read consistently across tracker backends

Реализация: `internal/tracker/jira/jira.go` — `toTask` разбирает
`fields["issuelinks"]`, отбирая записи по `type.name == cfg.DependsOnLink` и
читая `outwardIssue` (jira.go:1039-1057); `searchFields()` включает
`"issuelinks"` (jira.go:938); `List()` — `searchAllProject`, постраничный,
не обрезает проект на первой странице (jira.go:273-290, 357-403), критично
для гейта: именно новые тикеты-дети `split` рисковали выпасть за пределы
одной страницы.

- **Scenario «A JIRA-backed dependency is read back like a file-backed
  one»** — `TestGetParsesDependsOnFromIssuelinks`,
  `TestListCandidateCarriesDependsOnLikeGet`,
  `TestGetDependsOnEmptyWithoutIssuelinksField`,
  `TestListPaginatesBeyondFirstPage` (все fresh run: PASS). **Живой
  прогон на полигоне** (Tasks 4/5, `docs/notes/analyst-task-splitting.md`,
  «2026-09-07: живая верификация направления чтения issuelinks»):
  throwaway-пара, связанная эталонным типом `Blocks`, подтвердила чистую
  топологию JSON (каждая сторона видит поле, противоположное тому, как
  связь была записана); отдельная пара через реальный `LinkDependsOn`
  подтвердила, что разбор в `toTask` читает направление, которое пишет
  производственный код. Обе пары удалены после проверки.

### ADDED: A blocked candidate's wait is visible without extra tooling

Реализация: `cmd/runner/board.go` — `printBoard` строит тот же `byKey`,
`dependsColumn(unmet)` добавляет «ждёт: KEY (Status)» хвостом строки после
`summary`.

- **Scenario «ls names what a blocked task is waiting on»** —
  `TestBoardShowsBlockedDependency` (fresh run: PASS, включая проверку
  позиции столбца).

### ADDED (role-native-workflow): `depends_on` reflects a real merge-order
dependency, not a preferred ordering

Реализация: `roles/analyst/role.md:212-219` — явный критерий: `depends_on`
только когда подзадача не может начаться без смерженного кода другой
(схема/миграция/модель/интерфейс), не «так логичнее по порядку».

- Обе сценарные ветки (реальная зависимость / удобный порядок) — текстовый
  критерий роли, не код; проверено чтением файла, отдельного теста нет по
  природе требования (то же, чем волна 1 покрывала аналогичные критерии
  `role.md`).

## Живая проверка гейта и видимости целиком (Task 12)

`docs/notes/analyst-task-splitting.md`, «2026-09-08: живая проверка гейта и
видимости целиком»: реальный бинарник `runner` (`--tracker jira`) на живой
цепочке `EXP-25→…→30` — `tick --role implementer` корректно пропустил
зависимую задачу с логом `EXP-27: ждёт EXP-26 (Backlog), пропускаю`, `ls`
показал ту же зависимость тем же текстом; после перевода `EXP-26` в `Done`
зависимость у всех детей корректно снялась и в `ls`, и (по коду, общему с
`claim()`) в гейте. Разблокировку до реального захвата агентом сознательно
не гоняли — риск бесконтрольного прогона на живом GitHub-репозитории не
оправдан, когда код захвата и гейта общий и уже покрыт юнит-тестами.

## Согласованность

Все 7 решений `design.md` подтверждены реализацией:

1. Источник чтения — только `issuelinks`, без параллельной метки —
   `jira.go:toTask`, нет `depends-on:<KEY>` нигде в диффе.
2. Направление проверено эталонным типом `Blocks`, отдельно от рабочего
   типа `Depends` — Tasks 4/5, живой прогон выше.
3. Один `List()` на проект, вызываемый лениво (кандидаты есть **и** хотя бы
   у одного из них непустой `DependsOn`) — `pipeline.go:311-333`, тесты
   `TestClaimSkipsListWhenNoCandidateHasDependency`/
   `TestClaimCallsListWhenCandidateHasDependency` (fresh run: PASS).
4. Гейт без исключения `reviewer` — `TestClaimGatesAnalystCandidateTheSameWay`
   выше плюс отсутствие ветвления по `roleName` в `claim()`.
5. «Терминальный» — через `Workflow.IsTerminal`, не хардкод строки —
   сигнатура `UnmetDependencies(ref, byKey, terminal func(string) bool)`.
6. Отсутствующая в срезе зависимость — не разрешена, а незакрыта —
   `UnmetDependencies`, ветка `!found` (deps.go:44-49).
7. Критерий `depends_on` — правка `role.md`, не `docs/contracts/` —
   подтверждено выше.

**Находки финального ревью всей ветки** (после всех 12 задач, шире рамок
отдельных задачных ревью): критическая находка задачи 7
(`jira.List()` был однострадничным — гейт молча и навсегда сломался бы на
проектах ≥50 тикетов, именно на новых детях `split`, ради которых гейт
существует) закрыта отдельным раундом фикса ещё в ходе исполнения плана.
Финальное ревью (opus) поверх готовой ветки нашло дополнительно
3 Important + 6 Minor:

- Обязательный вызов `projectByKey` при любом непустом `refs` (не только
  когда у кандидата есть `DependsOn`) — после полной пагинации означал
  полный скан проекта на каждом тике каждой роли без необходимости.
- Ошибка `projectByKey` не проходила через `skipProject`, как у
  `ListReady` — жёстко возвращала ошибку вместо единообразного пропуска
  проекта.
- `dependsColumn` вставлялась в середину строки, ломая выравнивание всех
  последующих столбцов — перенесена хвостом после `summary`.
- Плюс 6 Minor: формулировка «не найдена» вместо намёка на удаление
  (deps.go), доккомент `LinkDependsOn` (устарел, отражал уже
  зафиксированную живой проверкой ненадёжность), обоснование лени
  `linkChildren` (не экономия — надёжность, раз `POST` дешевле, чем
  предваряющий `Get`), стек `docs/contracts/tracker-protocol.md`
  (называл гейт «будущей работой»), пример `ls` в `README.md`, опечатка
  «притворится»→«притвориться».

Все 9 закрыты одной волной фиксов (`9d86ef3`, `0cf31cf`, `8d574ab` плюс
прямой фикс ревьюера `7169a2c`), подтверждённой отдельным scoped-ревью
(чисто, без новых Critical/Important; каждый новый тест проверен как
реальный регрессионный, а не тавтологичный). Полная хронология —
`docs/notes/analyst-task-splitting.md`.

## Итог

Критических проблем нет. Все 17 задач, все 5 требований (4+1) двух
делта-спек реализованы, покрыты тестами (весь набор прогнан заново перед
этим отчётом — `go build`/`go vet`/`go test ./...` зелёные, `gofmt -l .`
чист за исключением не относящегося к этому change файла
`internal/pipeline/prpass_test.go`) и дополнительно подтверждены двумя
живыми прогонами на реальном JIRA-полигоне. Готово к архивации.
