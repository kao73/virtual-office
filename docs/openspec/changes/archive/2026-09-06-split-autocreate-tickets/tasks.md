## 1. Контракт `Tracker` — новые методы (`internal/tracker/tracker.go`)

- [x] 1.1 Добавить в интерфейс `Tracker`: `AddAttachment(key string, by Actor, name string, data []byte) (id string, err error)`, `GetAttachment(key, id string) ([]byte, error)`, `CreateTask(project string, input TaskInput) (TaskRef, error)` (без `Actor` — уточнено Design Doc'ом: создавать нечего «владеть»), `FindByMarker(project, marker string) ([]TaskRef, error)`, `LinkDependsOn(key, dependsOnKey string, by Actor) error` — doc-комментарии тем же тоном, что у остальных методов интерфейса
- [x] 1.2 Определить тип `TaskInput` (заголовок, описание, метки) рядом с `Task`/`TaskRef`

## 2. Реализация `mock` (`internal/tracker/mock/mock.go`)

- [x] 2.1 `AddAttachment`/`GetAttachment` — файл рядом с задачей в файловом хранилище фикстуры
- [x] 2.2 `CreateTask` — новый файл задачи; метка (единая `split-child:<PARENT_KEY>:<id>`, уточнено Design Doc'ом взамен исходной пары `split-parent:<KEY>`+`split-child:<id>`) пишется вызывающим кодом как часть `TaskInput` — `CreateTask` сам ничего про формат метки не знает
- [x] 2.3 `FindByMarker` — фильтр по каталогу проекта, ищет задачи с данной меткой
- [x] 2.4 `LinkDependsOn` — поле связи в YAML задачи
- [x] 2.5 Юнит-тесты на все четыре метода: `CreateTask` → `FindByMarker` находит созданное, `LinkDependsOn` записывает связь, `AddAttachment`→`GetAttachment` round-trip байт-в-байт

## 3. Реализация `jira` (`internal/tracker/jira/`)

- [x] 3.1 Проверить на локальном полигоне (Server 8.13), есть ли готовый тип issuelink «depends on»/«is blocked by» (design.md, Risk 1); если нет — завести на инстансе отдельным операционным шагом, не блокирующим код (не нашёлся — заведён `Depends` через `POST /issueLinkType`, направление совпало с кодом, см. docs/notes/analyst-task-splitting.md)
- [x] 3.2 `AddAttachment`/`GetAttachment` — `POST`/`GET .../attachments`, REST v2
- [x] 3.3 `CreateTask` — `POST /issue`, `labels` из `TaskInput`
- [x] 3.4 `FindByMarker` — JQL-поиск по `labels`
- [x] 3.5 `LinkDependsOn` — `POST /issueLink` найденным типом; проверить эмпирически, идемпотентен ли повторный вызов для той же пары (design.md, Risk 2) — если нет, добавить проверку существующих `issuelinks` перед созданием (проверено на полигоне — идемпотентен как есть, защитный код не понадобился)
- [x] 3.6 Тесты на полигоне тем же поведенческим контрактом, что 2.5 у `mock` (живой прогон CreateTask→FindByMarker→AddAttachment→GetAttachment, все 5 вызовов прошли без ошибок)

## 4. Вложение в путь исхода `split` (`internal/tracker/report.go` и вызывающий код)

- [x] 4.1 На исход `split` — вызвать `AddAttachment` с сырыми `split.children[]` (JSON), получить `id`, включить `attachment:<id>` в тег комментария-маркера (реализовано в `internal/pipeline/pipeline.go`'s `finish()`, не в `report.go` — `SplitBlock` остаётся нетронутым, см. design.md)
- [x] 4.2 Тест: `outcome: split` → вложение создано, тег комментария содержит `attachment:<id>`, `GetAttachment` по этому `id` возвращает исходные `split.children[]` без потерь

## 5. Детерминированное создание (`internal/pipeline/pipeline.go`)

- [x] 5.1 Функция «это второе подряд `outcome:split` от `analyst` на этом тикете» — считает предыдущие комментарии-маркеры `outcome:split` от `analyst` в переписке (design.md, решение №4) — реализована как экспортированная `tracker.SplitConfirmed` в `internal/tracker/marker.go`, не в `pipeline.go` (уточнено Design Doc'ом: вызывается из другого пакета)
- [x] 5.2 На подтверждённое второе `split` — скачать вложение по `id` из тега последнего `split`-маркера, распарсить `split.children[]`
- [x] 5.3 Для каждого ребёнка — `FindByMarker`, создать при отсутствии (`CreateTask`), связать по `depends_on` (`LinkDependsOn`)
- [x] 5.4 На полном успехе — комментарий «разбита на ...» с перечислением созданных ключей, `Transition` родителя в `Done`
- [x] 5.5 На сбое — `NoticeBody`/маркер `event:split-create-failed`, родитель не двигается, `attempts` не тратится (все шаги `completeSplit`, включая финальный `closeSplitParent`, теперь идут через `splitFailed` — Task 13 закрыл пробел, найденный ревью Task 11)
- [x] 5.6 Поведенческие тесты (по образцу `TestTickSplitBlocksAndFlags`): fake-трекер, два тика подряд с `outcome: split` → проверка вызовов `CreateTask`/`LinkDependsOn`/`Transition`; отдельный тест на прерванный батч (`FindByMarker` уже находит часть — досоздаются только недостающие, без дублей); отдельный тест на первое предложение (не запускает создание)
- [x] 5.7 Подключить `CompleteSplits` к `Loop` (рядом с `Reap`) и завести CLI `runner complete-splits` — пробел, найденный при написании Design Doc'а (без этого шага `CompleteSplits` не вызывался бы никаким продакшн-кодом); тест на сам факт вызова из `Loop`, не только на поведение `CompleteSplits`

## 6. `roles/analyst/role.md` и живая спека

- [x] 6.1 Убрать ветки резюме «прошлый вопрос был «подзадачи уже заведены?»» и «прошлый вопрос был «что дальше с этим тикетом?»» — обе становятся недостижимы (design.md, решение №6)
- [x] 6.2 Первая ветка резюме («разбить как предложено?» → да) — убрать формулировку нового вопроса «подзадачи уже заведены?», оставить исходный вопрос без изменений
- [x] 6.3 Сверить дельту `docs/openspec/changes/split-autocreate-tickets/specs/role-native-workflow/spec.md` с правкой `role.md` до архивации — оба должны описывать одно и то же поведение (расхождений не найдено)

## 7. Полная верификация

- [x] 7.1 `go build ./...`, `go vet ./...`, `gofmt -l .` (пусто), `go test ./...` — весь модуль, зелёные (единственный `gofmt`-файл — `internal/pipeline/prpass_test.go`, предсуществующий, не этого плана)
- [x] 7.2 `./bin/eval-roles --role analyst` — существующие golden-кейсы по-прежнему зелёные (правка `role.md` не задела их косвенно) — 5/5 под `--clone`

## 8. Живая проверка (реальный трекер, реальные деньги)

- [x] 8.1 На реальном проекте (или существующем `EXP`-тикете с уже предложенным разбиением) подтвердить split дважды подряд — тикеты создались, связались, родитель ушёл в `Done`, без единого прогона агента на сам шаг создания (`EXP-3` → `EXP-7…11`, реальный прогон `analyst`, реальный клон `kao73/expense-tracker`)
- [x] 8.2 Искусственно прервать создание партии на середине, прогнать снова — досоздаются только недостающие, без дублей (`EXP-12`, порча `depends_on_link`, оба ребёнка созданы, связь не встала, повтор довязал без дублей)
- [x] 8.3 Находки записаны в отчёт верификации и в `docs/notes/analyst-task-splitting.md`
