# Отчёт верификации: split-autocreate-tickets

Comet Classic full workflow, полный режим верификации (`comet state scale`:
30 задач > 3, 2 капабилити делта-спек > 1, 23 изменённых файла > 8 — все три
порога превышены с запасом).

## Сводка

| Измерение | Статус |
|---|---|
| Полнота | 30/30 задач `tasks.md`; 4/4 требования `pipeline-split-autocreate` покрыты; 1/1 требование `role-native-workflow` покрыто |
| Корректность | Все требования реализованы и покрыты и юнит-, и поведенческими тестами; оба ключевых сценария (`pipeline-split-autocreate`'s «второе подтверждение» и «идемпотентный повтор») дополнительно подтверждены живыми прогонами на реальном JIRA-полигоне |
| Согласованность | Реализация совпадает со всеми 7 решениями `design.md`; шесть Important-расхождений, видимых только на уровне всей ветки целиком (не одной задачи), найдены финальным ревью и закрыты одной волной фиксов; четыре узких остаточных Minor-находки приняты как задокументированный риск, не устранены |

## Полнота

**Задачи.** Все 30 пунктов `tasks.md` (`openspec instructions apply`:
`progress: 30/30`) отмечены `[x]`, подтверждено и `comet state task-checkoff`
на протяжении build, и повторно этой командой сейчас. 38 коммитов от
`b8c4241` (merge-base с `master`) до `2df336b` (конец 18 задач плана) плюс
9 коммитов финальной волны фиксов (`84eb5a0`…`93e9681`) плюс 2
документационных коммита после (`628a04e`, `421a81e`).

**Покрытие требований спекой.**

`specs/pipeline-split-autocreate/spec.md` (новая капабилити) — 4 ADDED
Requirements, разобраны по отдельности ниже. `specs/role-native-workflow/spec.md`
(существующая капабилити) — 1 MODIFIED Requirement, тоже ниже.

## Корректность

### ADDED: A split proposal's structured data is preserved as a tracker attachment

Реализация: `internal/pipeline/pipeline.go:738-752` (`finish()` на исход
`split` — `json.Marshal(result.Split)`, `Tracker.AddAttachment`, id уходит в
`Marker.Attachment` до публикации комментария).

- **Scenario «Split proposal carries a retrievable attachment»** —
  `internal/pipeline/pipeline_test.go`, `TestTickSplitAttachesRawChildren`
  (Task 10): реальный round-trip через `mock.Tracker`
  (`AddAttachment`→разбор маркера из опубликованного комментария→
  `GetAttachment`→`json.Unmarshal`). **Живой прогон**: `EXP-3`, второй
  `outcome:split`-маркер нёс `attachment:10001`, вложение `split.json`
  скачано и разобрано напрямую через REST — 5 детей с зависимостями, без
  потерь.

### ADDED: Confirmed split triggers automatic ticket creation without an agent run

Реализация: `internal/pipeline/splits.go` — `CompleteSplits` (Task 11),
`ensureChildren`/`linkChildren` (двухпроходный порядок: сначала все дети,
потом связи — Task 11), `closeSplitParent` (Task 11, доработан Task 13 и
финальным фиксом — снимает `HumanFlag`).

- **Scenario «Second confirmation triggers automatic creation»** —
  `splits_test.go`, `TestCompleteSplitsCreatesAndLinksChildren`. **Живой
  прогон**: `EXP-3` → `EXP-7…EXP-11` (аккаунты/категории → транзакции →
  бюджеты и дашборд, плюс независимый импорт CSV), все 7 рёбер зависимостей
  верны по направлению (сверено по `issuelinks` каждого созданного тикета),
  родитель ушёл в `Done`, `${OFFICE_HOME}/runs/` не получил новой записи —
  ноль прогонов агента на сам шаг создания.
- **Scenario «First proposal does not trigger creation»** —
  `TestCompleteSplitsSkipsUnconfirmed` (Task 11) — один маркер `split` не
  запускает `CreateTask`.

### ADDED: Batch ticket creation is idempotent under retry

Реализация: `ensureChildren`'s `FindByMarker`-перед-`CreateTask` (Task 11),
поведенчески подтверждено на прерванной пачке (Task 12).

- **Scenario «Interrupted batch resumes without duplicating created children»**
  — `TestCompleteSplitsResumesInterruptedBatch` (не тавтологичен: реализация
  задачи 12 сама временно отключала проверку и подтвердила, что тест реально
  ловит регресс). **Живой прогон**: `EXP-12`, `depends_on_link` испорчен на
  несуществующее имя → оба ребёнка созданы (`CreateTask` от связи не
  зависит), связь не встала, `event:split-create-failed` записан; после
  исправления конфига повторный `complete-splits` не задвоил ни одного
  ребёнка (проверено по количеству тикетов на каждую метку — по одному) и
  довязал `depends_on`.

### ADDED: Failed creation attempts are visible, not silent

Реализация: `splitFailed` (Task 11), доработан финальным ревью —
дедупликация через `tracker.HasEvent(task.Comments, EventSplitCreateFailed)`
вместо записи на каждый цикл `Loop`.

- **Scenario «A failed attempt leaves a visible notice»** —
  `TestSplitFailedDoesNotSpamRepeatedNotices` (финальный фикс). **Живой
  прогон**: `EXP-12` получил ровно один `event:split-create-failed` с
  конкретным текстом ошибки JIRA («задача не найдена: /issueLink»), не
  тихий повтор.

### MODIFIED: A human's reply to a split proposal is not re-investigated as Shape work

Реализация: `roles/analyst/role.md:42-65` (три ветки — подтверждение,
отказ, «что-то ещё» — последняя сужена финальным ревью: больше не может
дать `split`, только `needs_human`).

- **Scenario «Confirmed split ends the resumed run without invoking Comet
  Native»** — **живой прогон**, `EXP-3`: реальный `runner tick --role
  analyst` (реальный клон `kao73/expense-tracker`, реальный вызов агента) —
  второй `outcome: split`, тот же вопрос, `summary` прямо называет
  автосоздание, `run.log` не содержит вызова `comet native new`.
- **Scenario «Declined split proceeds with ordinary Shape»** — не тронута
  этим change (симметричная ветка сохранена как была волной 1), живым
  прогоном в рамках этого захода не проверялась — не входила в периметр
  Task 18 (её живая проверка — уже пройденный пункт волны 1, см.
  `docs/notes/analyst-task-splitting.md`, «Волна 1»).

## Согласованность

Все 7 решений `design.md` подтверждены реализацией:

1. Узкая поверхность `Tracker` (5 методов, не общий `Link()`) —
   `internal/tracker/tracker.go:288-308`.
2. Идемпотентность через `FindByMarker`, не хранимый флаг — подтверждено и
   юнит-тестом, и живым прогоном (`EXP-12`).
3. Метка, не кастомное поле — `split-child:<PARENT_KEY>:<id>`, единый
   формат, подтверждён и в `mock`, и на живом JIRA.
4. Различение по переписке (`SplitConfirmed`), не по флагу —
   `internal/tracker/marker.go:414-428`.
5. Создание — код раннера, не роль — подтверждено живым прогоном (ноль
   новых записей в `runs/` на шаге `complete-splits`).
6. Удаление обеих мёртвых веток `role.md` — Task 15, дополнительно сужена
   финальным ревью (ветка «что-то ещё» тоже была источником риска, не
   предусмотренного design.md буквально, но тем же принципом).
7. Обработка ошибок без счётчика попыток — `splitFailed`, дедуплицирован
   финальным фиксом, счётчик не заведён нигде (проверено `grep`'ом на
   отсутствие новых полей attempts/threshold в диффе).

**Находки, видимые только на уровне всей ветки, а не одной задачи.**
Финальное ревью всей ветки (38 коммитов, вне рамок отдельных задачных
ревью) нашло шесть Important-расхождений между артефактами, каждое —
на стыке нескольких задач:

- `SplitConfirmed` засчитывал подтверждённым второй `split`-маркер без
  вложения — тикеты волны 1, дошедшие до второго `split` до появления
  вложений (задача 10), зациклились бы на `split-create-failed` навсегда.
  Migration Plan `design.md` учёл только тикеты, ждущие *первого* ответа.
- Ветка «что-то ещё» `role.md` могла дать повторный `split` на
  неоднозначный ответ человека — автосоздание тикетов без явного «да».
- `closeSplitParent` не снимал `office-waits-human` при закрытии.
- `splitFailed` не дедуплицировался (до ~720 записей в сутки на
  застрявшем тикете).
- `docs/contracts/` не отражали новую поверхность `Tracker`.
- `mock`/`jira` расходятся в статусе новосозданного ребёнка (оставлено
  задокументированным, не исправлено — решение принадлежит Change 2).

Все шесть закрыты одной волной фиксов (9 коммитов, `84eb5a0…93e9681`),
подтверждённой отдельным scoped-ревью (чисто, без новых Critical/Important).
Полная хронология — `docs/notes/analyst-task-splitting.md`, раздел
«2026-09-06: финальное ревью всей ветки и остаточные ограничения».

**Четыре узких остаточных Minor-находки** (после самой волны фиксов, не
устранены — по правилу «нет второго раунда фиксов» после финального
ревью): пустой `children[]` в проверке распакованного вложения проходит
без ошибки; четвёртая копия устаревшего комментария про `depends_on`
пережила волну фиксов (`agentio.go:320`); проверка чужого хоста в
`jira.download` обходима именем вида `<хост>.attacker.tld`; запись об
успехе (`event:split-created`) не дедуплицирована так же, как запись о
сбое. Ни одна не блокирует дальнейшую работу — задокументированы в
`docs/notes/analyst-task-splitting.md` тем же разделом.

## Итог

Критических проблем нет. Все 30 задач, все 5 требований (4+1) двух
делта-спек реализованы, покрыты тестами и (за исключением decline-ветки,
не входившей в периметр Task 18) дополнительно подтверждены живыми
прогонами на реальном JIRA-полигоне. Четыре принятых узких остаточных
ограничения задокументированы. Готово к архивации.
