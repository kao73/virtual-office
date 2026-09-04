---
comet_change: split-autocreate-tickets
role: technical-design
canonical_spec: openspec
---

# split-autocreate-tickets — Technical Design

Это углублённая техническая проработка артефактов Open-фазы
(`docs/openspec/changes/split-autocreate-tickets/proposal.md`, `design.md`,
`specs/*/spec.md`), которые остаются каноническим источником целей, объёма
и требований. Этот документ идёт на уровень глубже: конкретные типы,
сигнатуры, последовательность выполнения и то, чего Open-фаза сознательно
не решала — механизм повтора после сбоя.

## Найденный пробел: чем запускается повтор прерванного батча

Open-фазный `design.md` сказал «частичный сбой чинится следующим тиком
сам», но не сказал, чем запускается этот следующий тик. Родитель после
подтверждения стоит в статусе `Blocked` — а `Blocked`-задачи ни одна роль
в обычном цикле не читает (`ListReady` их не отдаёт). Без отдельного
механизма «сам починится следующим тиком» неверно: ничто не подойдёт
к прерванной пачке снова.

**Решение:** новый системный проход `CompleteSplits`, по образцу уже
существующего `Reap` (`internal/pipeline/pipeline.go:900`). Вызывается из
`Loop` каждый цикл рядом с `Reap`, и отдельной CLI-командой
`runner complete-splits` (по образцу `runner reap`,
`cmd/runner/office.go`). Единый код-путь для первой попытки и для
повтора — синхронного вызова из `finish()` нет: `finish()` только
публикует комментарий с исходом `split`, дальнейшее — забота
`CompleteSplits` на следующем цикле. Цена — задержка в пределах одного
цикла (`every`, по умолчанию 2 минуты) до первой попытки; принято ради
одного код-пути вместо двух.

## Контракт `Tracker`

```go
// internal/tracker/tracker.go

// TaskInput — данные для создания новой задачи. Отдельный тип, а не Task
// целиком: у только что создаваемой задачи нет ни ключа, ни аренды, ни
// статуса — их назначает сам трекер.
type TaskInput struct {
    Summary     string
    Description string
    Labels      []string
}

type Tracker interface {
    // ...существующие методы без изменений...

    // CreateTask заводит новую задачу. Без Actor: создавать нечего "владеть" —
    // как у Add в mock (не из контракта) и List/ListReady в самом контракте.
    CreateTask(project string, input TaskInput) (TaskRef, error)

    // FindByMarker — задачи проекта с данной меткой. Источник идемпотентности
    // пакетного создания: спрашивает трекер, не хранимую запись о нём.
    FindByMarker(project, marker string) ([]TaskRef, error)

    // AddAttachment сохраняет сырые данные вложением к существующей, уже
    // захваченной задаче — Actor и CheckOwner нужны, как у Comment.
    AddAttachment(key string, by Actor, name string, data []byte) (id string, err error)

    // GetAttachment читает вложение обратно. Без Actor — как Get, чтение
    // не требует владения.
    GetAttachment(key, id string) ([]byte, error)

    // LinkDependsOn связывает только что созданную задачу (key) с её
    // зависимостью (dependsOnKey). by обычно BySystem() — тем же приёмом,
    // что reap и разбор ответа человека используют для мутаций вне аренды
    // какой-либо роли.
    LinkDependsOn(key, dependsOnKey string, by Actor) error
}
```

**Метка — одна строка, не две.** Open-фазный `design.md` предполагал две
метки (`split-parent:<KEY>` + `split-child:<id>`). Здесь уточнено до одной:
`split-child:<PARENT_KEY>:<id>`. Проще JQL (`labels = "<marker>"`, не два
условия через AND), проще `mock` (`slices.Contains`), той же информации
достаточно для однозначности — родитель и `id` ребёнка вместе уникальны.

## Последовательность в `internal/pipeline`

**`finish()` не меняется в части исхода `split`** — вторая (подтверждающая)
`outcome: split` от `analyst` публикует комментарий и уходит в `Blocked`
ровно как первая, тем же путём, что и сегодня. Различение «первое
предложение» / «подтверждено» и всё дальнейшее действие переезжают
целиком в новый проход.

```go
// CompleteSplits — системный проход: досоздаёт и связывает детей
// подтверждённых split-предложений, по образцу Reap.
func (o *Office) CompleteSplits(ctx context.Context) error {
    flow, err := o.Workflow.Role("analyst")
    if err != nil {
        return nil // графа без analyst не бывает в реальном workflow.yaml,
                    // но синтетические тестовые графы могут его не иметь
    }
    for _, project := range o.projects() {
        refs, err := o.Tracker.List(project, []string{flow.Blocked()})
        if o.skipProject(project, err) {
            continue
        }
        if err != nil {
            return err
        }
        for _, ref := range refs {
            task, err := o.Tracker.Get(ref.Key)
            if err != nil {
                return err
            }
            if confirmed, attachmentID := splitConfirmed(task.Comments); confirmed {
                if err := o.completeSplit(task, attachmentID); err != nil {
                    return err
                }
            }
        }
    }
    return nil
}
```

`splitConfirmed` — новая функция в `internal/tracker/marker.go`, по
образцу уже существующего `lastOfRole`/`ReturnRounds`: считает маркеры
`outcome:split` от `analyst` в переписке; ≥2 и родитель ещё не переведён
(проверяется тем же List, отдающим только `Blocked`) — подтверждено.
Возвращает `id` вложения из тега последнего такого маркера
(`attachment:<id>`, поле уже существует в марке́ре с волны 1).

`completeSplit(task, attachmentID)`:
1. `GetAttachment(task.Key, attachmentID)` → распарсить `split.children[]`.
2. Для каждого ребёнка — `FindByMarker(task.Project, "split-child:"+task.Key+":"+child.ID)`.
   Найден — пропустить. Не найден — `CreateTask` с `TaskInput{Summary:
   child.Title, Description: child.Description, Labels: []string{marker}}`.
3. После того как **все** дети существуют (найдены или только что
   созданы) — для каждого с непустым `DependsOn` — `LinkDependsOn` на
   соответствующего родственника (по тому же маркеру находим его
   `TaskRef.Key`).
4. `Comment(task.Key, tracker.BySystem(), "разбита на: "+ключи)`,
   `Transition(task.Key, tracker.BySystem(), doneStatus)`.
5. Любая ошибка на любом шаге — `notice` с `event:split-create-failed`
   (тем же приёмом, что `Reap` пишет `EventLeaseExpired`), возврат ошибки
   не прерывает обход остальных задач `CompleteSplits` (та же защита, что
   у `Reap` — беда на одной задаче не должна ронять весь проход).

Шаг 3 (связывание) идёт отдельным подпроходом после шага 2 (создание всех
детей) намеренно: `depends_on` может ссылаться на ребёнка, который в
списке `split.children[]` идёт позже — связывать раньше, чем существуют
оба конца, нечем.

## `mock` (`internal/tracker/mock/mock.go`)

- Вложения — новый подкаталог `attachments/` рядом с `comments/`, тем же
  приёмом эксклюзивного создания номера, что уже использует `AddComment`.
- `CreateTask` генерирует ключ вида `<project>-N`, сканируя существующие
  ключи этого проекта (`Keys()` + фильтр по префиксу) и беря `max+1`;
  создаёт директорию `os.Mkdir` (не `MkdirAll`) — коллизия двух
  параллельных `CreateTask` предпочтительнее упадёт с ошибкой, чем молча
  перезапишет; `mock` — инструмент отладки конвейера, не имитация
  конкурентного трекера под нагрузкой (тот же принцип уже сформулирован в
  доккомментарии пакета).
- `LinkDependsOn` — новое поле `DependsOn []string` в `taskFile`/`Task`,
  по аналогии с уже существующим `Labels`; не читается никаким кодом
  Change 1 (только пишется) — Change 2 добавит чтение.
- `FindByMarker` — фильтр `slices.Contains(task.Labels, marker)` по
  задачам проекта, тем же приёмом, что уже есть `t.list`.

## `jira` (`internal/tracker/jira/jira.go`)

- `AddAttachment`/`GetAttachment` не могут переиспользовать `t.call` —
  тот всегда работает с JSON (`Content-Type: application/json`,
  `json.Unmarshal` в `out`). Вложения — `multipart/form-data` на запись
  и произвольные байты на чтение. Оба нуждаются в собственном низкоуровневом
  HTTP-вызове рядом с `call`, использующем тот же `req.SetBasicAuth`/
  `X-Atlassian-Token: no-check` (последний уже стоит на каждом запросе —
  без него JIRA как раз вложения не примет).
- `CreateTask` — `POST /issue`. JIRA требует `issuetype` в теле запроса,
  которого сегодня нет в `Config`. Добавляется `Config.IssueType`
  (`yaml:"issue_type"`, дефолт `"Task"`, если поле пусто) — тем же
  местом, что `HumanFlagLabel`: инстанс-специфичная настройка живёт в
  `${OFFICE_HOME}/tracker.yaml`, не коммитится в репозиторий (§2.5
  DESIGN.md).
- `FindByMarker` — переиспользует существующий `t.search`, JQL
  `project = "<project>" AND labels = "<marker>"`.
- `LinkDependsOn` — `POST /issueLink` с типом связи из конфига (детали
  типа — открытый риск Open-фазы, проверяется на полигоне при
  реализации).

## Новая CLI-команда

`runner complete-splits`, по образцу `runner reap` (`cmd/runner/office.go`,
рядом с `reapCommand`) — ручной или cron-запуск прохода `CompleteSplits`
отдельно от `loop`. `Loop` (`internal/pipeline/pipeline.go:1026`) вызывает
`CompleteSplits` рядом с `Reap`, тем же порядком (housekeeping перед
`tickOnce`).

## Testing Strategy

- Юнит-тесты новых методов `Tracker`: `mock` и `jira` проверяются одним
  общим поведенческим набором (создание → `FindByMarker` находит,
  вложение round-trip, `LinkDependsOn` записывает связь) — тем же
  приёмом, что уже используют существующие тесты обеих реализаций.
- `internal/tracker/marker_test.go`: `splitConfirmed` — 0/1/2+ маркеров,
  маркер не от `analyst` не считается, маркер без `attachment:` в теге —
  не подтверждён (защита от кривого источника).
- `internal/pipeline/pipeline_test.go`: `TestCompleteSplitsCreatesAndLinksChildren`
  (fake-трекер, вложение с тремя детьми и одной зависимостью, два
  предшествующих `outcome:split` маркера) → `CreateTask`×3,
  `LinkDependsOn`×1, `Transition` в `Done`;
  `TestCompleteSplitsResumesInterruptedBatch` (`FindByMarker` уже находит
  часть детей) → создаются только недостающие; `TestCompleteSplitsSkipsUnconfirmed`
  (1 маркер) → `CreateTask` не вызывается вовсе.

## Risks / Trade-offs

- **[Trade-off]** Периодический, не синхронный триггер — до одного цикла
  задержки перед первой попыткой создания. Принято: один код-путь вместо
  двух перевешивает задержку в несколько минут на офисном, не
  пользовательском, масштабе времени.
- **[Risk]** (унаследован из Open-фазы) Тип issuelink «зависит от» может
  отсутствовать на локальном JIRA Server 8.13 — проверить в первой
  Build-задаче, трогающей `LinkDependsOn`.
- **[Risk]** (унаследован из Open-фазы) Идемпотентность повторного
  `issueLink` для той же пары на стороне JIRA не подтверждена — проверить
  эмпирически, при необходимости — предварительная проверка существующих
  `issuelinks`.

## Migration Plan

Без изменений относительно Open-фазного `design.md` — аддитивные правки,
без миграции данных, откат — реверт коммитов.
