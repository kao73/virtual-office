# Comet Design Handoff

- Change: sandbox-network-and-permissions
- Phase: design
- Mode: compact
- Context hash: 8f6fbbefa6dc170f668667c8ed6ce4bea3e7b95420faa1fec074863f1fe736ee

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## docs/openspec/changes/sandbox-network-and-permissions/proposal.md

- Source: docs/openspec/changes/sandbox-network-and-permissions/proposal.md
- Lines: 1-89
- SHA256: e79ad04a34311e9fd61bc1de2de062ab49247beacdc88f088c000623772af7e8

[TRUNCATED]

```md
## Why

Роль в песочнице `sbx` не может сама поднять то, что нужно её задаче — прогнать
тесты, поднять БД для миграции, поднять сайт целиком через `docker compose` для
e2e, — потому что сеть песочницы закрыта, а разрешённые хосты сейчас заводятся
вручную, задним числом, по факту `403` на живом прогоне, и дословно дублируются
в каждом `roles/*/role.yaml` без единого места, где их назвать один раз. Тот же
пробел — с `tools.allow`/`tools.deny`: `permissions.allow` не является
техническим ограничением Bash (ограничивает только `permissions.deny`), но
проектных решений, что с этим знанием делать на уровне всех ролей и всех
проектов сразу, сейчас негде записать — только в самом `role.yaml` каждой роли
по отдельности.

Прежний план решить это точечно, механизмом `companion` (предзаявленный сервис
вроде `test-db`, который раннер прокидывает роли), не удался и не возобновляется:
общая способность песочницы (роль сама вызывает docker/docker-compose внутри
своей песочницы) не требует отдельного канала для каждого сценария, только сети
и прав.

## What Changes

- Заводится слоистая модель разрешения `network.allow` / `tools.allow` /
  `tools.deny` для прогона роли — четыре уровня, итог — объединение того,
  что назвал каждый уровень (ни один уровень не может убрать то, что
  добавил менее специфичный — иначе `tools.deny` можно было бы ослабить):
  1. repo-wide умолчания — зарезервированный ключ `defaults` в `projects.yaml`
     (не отдельный новый файл);
  2. project-level — обычные ключи проектов (`EXP`, `VO`, `OFFICE`, ...) там же;
  3. machine-level — тот же принцип (`defaults` + per-project ключи)
     в `projects.local.yaml`, по аналогии с уже существующим разделением
     `repo_url`/`tracker`/`forge` (только local) и `default_branch`/`branch_prefix`
     (office);
  4. role-level — `roles/*/role.yaml`, как сейчас, точечная добавка.
- В repo-wide умолчания (уровень 1) добавляется базовый список сетевых доменов
  для типовых нужд разработки: Docker Hub (проверено эмпирически), заготовки
  для GitHub/npm/PyPI/Go modules/apt — каждую нужно перепроверить тем же
  эмпирическим способом (пустая песочница → голая попытка → добавление хостов
  по факту ошибки) прежде чем полагаться на неё в бою.
- `tools.allow` ролей расширяется до широких правил (`Bash(*)` и подобные)
  вместо перечисления подкоманд — `permissions.allow` не защита независимо от
  формы, а перечисление вдобавок хрупко к флагам-префиксам перед подкомандой
  (`git -c core.pager=cat push`, `PYTHONPATH=. uv run` — обе формы уводят
  команду из-под сверки с началом строки).
- `tools.deny` — единственная реальная граница — расширяется на опасные команды
  за пределами нынешнего набора git-операций записи (например, `rm -rf`,
  переписывание истории git, сеть в обход разрешённого), с тем же вниманием
  к хрупкости флагов-префиксов, и переносится общей частью на repo-wide
  уровень (1), чтобы не дублироваться в каждой роли, как сейчас.
- Правится неверный комментарий `adapters/claude/adapter.go:180`, утверждающий,
  что `permissions.allow` запрещает всё, чего в нём нет.
- Из `roles/*/role.yaml` убирается захардкоженное дублирование
  `pypi.org`/`files.pythonhosted.org`: PyPI — один из пунктов чернового
  repo-wide списка (уровень 1), а не специфика одного проекта, и после
  эмпирической проверки переезжает в `defaults`, а не в проектный слой
  `EXP`. Если после этого у `EXP` не останется ничего сверх repo-wide
  умолчаний, проектный слой для него может остаться пустым — это нормально,
  слой не обязателен.

## Capabilities

### New Capabilities

- `role-sandbox-permissions`: как для одного прогона роли резолвится итоговый
  `network.allow` и итоговые `tools.allow`/`tools.deny` из четырёх слоёв
  (repo-wide умолчания, проект, машина, роль) — что каждый слой вправе
  добавить, что переопределить, и что происходит при конфликте.

### Modified Capabilities

(нет существующих спек — капабилити в проекте заводится впервые)

## Impact

- `adapters/claude/adapter.go` — `networkAllow`, сборка `--tools`, комментарий
  на месте `--permission-mode dontAsk`
- `backends/sbx/sbx.go` — потребляет итоговый `NetworkAllow`, поведение самого
  применения не меняется
- `runner/role.go` — `Role`/`LoadRole`: сейчас разбирает один `role.yaml` строго
  и без слияния; получает роль в резолюции слоёв
- `tracker/config.go` — `LoadProjects`: сейчас требует строгого совпадения

```

Full source: docs/openspec/changes/sandbox-network-and-permissions/proposal.md

## docs/openspec/changes/sandbox-network-and-permissions/design.md

- Source: docs/openspec/changes/sandbox-network-and-permissions/design.md
- Lines: 1-158
- SHA256: ec9d99f47c28c9629d4ca103f2e5ad21b2b015959ce9c3cc2156d5330e62b7d5

[TRUNCATED]

```md
## Context

См. `proposal.md` — «Why» и «What Changes» за мотивацией и полным списком
изменений. Здесь — только то, что нужно объяснить выбранный подход.

Сейчас в коде: `adapters/claude/adapter.go:225,240-244` формирует
`network.allow` прогона как `[AgentAPIHost] + role.Network.Allow` — источника
шире одной роли нет. `runner/role.go` (`LoadRole`) разбирает один `role.yaml`
строго (`yaml.KnownFields(true)`), без какого-либо слияния слоёв конфига.
`tracker/config.go` (`LoadProjects`, :541-620) уже склеивает `projects.yaml`
(office-половина: `default_branch`, `branch_prefix`) и `projects.local.yaml`
(machine-половина: `repo_url`, `worktree_root`, `tracker`, `forge`) — но
делает это построчно по ключу проекта и **требует строгого совпадения**
множества ключей между двумя файлами: каждый ключ office обязан быть
в machine, и наоборот.

Во всех трёх `roles/*/role.yaml` `network.allow` и семь строк `tools.deny`
дословно продублированы (специфика клиентского проекта `EXP`, вписанная
в общий, не per-project, файл роли). Отдельная находка (`docs/notes/
stage-1-retro.md:135-142`, `docs/notes/stage-5-cleanup.md:121-131`):
и `tools.allow`, и `tools.deny` сверяются с началом строки, и флаг перед
подкомандой (`git -c core.pager=cat push`, `PYTHONPATH=. uv run`) уводит
команду из-под сверки в обоих направлениях — из этого следует, что
мелкое перечисление подкоманд в `allow` не даёт защиты, которую могло бы
казаться, что даёт.

## Goals / Non-Goals

**Goals:**
- Одна реализация слияния слоёв, одинаково применяемая к `network.allow`,
  `tools.allow` и `tools.deny` — не три разных механизма.
- Не заводить новый top-level файл: слой repo-wide умолчаний и слой
  project-override живут в уже существующих `projects.yaml`/
  `projects.local.yaml` под зарезервированным ключом.
- `role.yaml` как формат не меняется (те же поля `network.allow`,
  `tools.allow`, `tools.deny`) — роль остаётся верхним, самым специфичным
  слоем.
- `tools.deny` не может быть ослаблен слиянием: правило, добавленное на
  любом слое, обязано остаться в итоге независимо от `tools.allow`
  более специфичных слоёв (см. спеку `role-sandbox-permissions`).
- Домены чернового списка, кроме Docker Hub, явно помечены непроверенными
  и не считаются частью боевого набора до эмпирической проверки тем же
  способом (пустая песочница → голая попытка → добавление по факту ошибки).

**Non-Goals:**
- Не меняется, как сама песочница `sbx` применяет итоговый `NetworkAllow`
  (`backends/sbx/sbx.go` продолжает звать `sbx policy allow network` как
  сейчас) — меняется только то, откуда список берётся.
- Не решается класс проблемы «флаг перед подкомандой уводит из-под сверки»
  целиком — это давнее устройство сверки правил Claude Code, не предмет
  этого изменения (см. «Риски»).
- Не вводится механизм точечного «вычитания» — снять конкретный хост/паттерн,
  добавленный более общим слоем, этим изменением нельзя (см. «Решения»,
  почему выбрано объединение, а не полноценный override).

## Decisions

**Слой умолчаний — зарезервированный ключ `defaults`, а не новый файл.**
Рассмотрены варианты: отдельный `network.yaml`/`rules.yaml` в корне
репозитория (по образцу `workflow.yaml`); `roles/_base/` (по образцу
`base.md`, уже общего для всех ролей). Выбран зарезервированный ключ
`defaults:` внутри `projects.yaml` (и, симметрично, `projects.local.yaml`)
— явное решение пользователя не заводить новый файл; вдобавок он естественно
садится на уже существующий раскол office/machine, вместо того чтобы
изобретать для repo-wide слоя отдельный канал office/machine-разделения.

**Семантика слияния — объединение множеств (union), не override.**
Для всех трёх списков (`network.allow`, `tools.allow`, `tools.deny`) итог —
объединение того, что назвал каждый слой; ни один слой не может убрать то,
что добавил менее специфичный. Альтернатива — полноценный override
(слой вправе заменить/убрать унаследованное значение) — отклонена: для
`tools.deny` это прямо противоречило бы цели «deny нельзя ослабить»
(зафиксировано как отдельное требование в спеке), а разная семантика для
разных списков (deny — только union, allow/network — union-или-override)
усложнила бы реализацию и создала бы источник путаницы, не отвечающий
конкретной названной пользователем нужде. Если на практике понадобится
вычитание — это отдельное решение отдельным изменением, не расширение
текущего вслепую.

**`defaults` — не проект, освобождён от требований к реальным проектам.**

```

Full source: docs/openspec/changes/sandbox-network-and-permissions/design.md

## docs/openspec/changes/sandbox-network-and-permissions/tasks.md

- Source: docs/openspec/changes/sandbox-network-and-permissions/tasks.md
- Lines: 1-89
- SHA256: 1f1355d592f71b5bbe493030cfd6b5100223b8af07c0bcad2df515e441577a15

[TRUNCATED]

```md
## 1. Слой правил в конфиге проектов

- [ ] 1.1 Добавить в `tracker/config.go` поля `Network []string` и
      `Tools{Allow, Deny []string}` в `Project`, `officeProject`,
      `machineProject`
- [ ] 1.2 Научить `LoadProjects` зарезервированному ключу `defaults`:
      разобрать его отдельно от `map[string]officeProject`/
      `map[string]machineProject`, исключить из обязательной парности
      ключей office/machine и из проверок `repo_url`/`tracker`/
      `default_branch`/`branch_prefix`
- [ ] 1.3 Реализовать объединение (union, без удаления унаследованного)
      `defaults` + собственных полей проекта — отдельно для office- и
      machine-половины, — в итоговые `Network`/`Tools` каждого проекта
- [ ] 1.4 Тесты `LoadProjects`: `defaults` отсутствует в одном из двух
      файлов; `defaults` задан в обоих; реальный проект без собственного
      `network`/`tools` наследует только `defaults`; специфика одного
      проекта не видна другому; проект без `defaults` вообще ведёт себя
      как сегодня (регресс не сломан)

## 2. Repo-wide базовый список доменов

- [ ] 2.1 Добавить в `projects.yaml` под `defaults.network` проверенный
      список Docker Hub: `registry-1.docker.io`, `auth.docker.io`,
      `*.docker.io`, `production.cloudfront.docker.com`,
      `*.cloudfront.docker.com`
- [ ] 2.2 Эмпирически проверить GitHub тем же способом, что и Docker Hub
      (пустая одноразовая песочница → голая попытка → добавление хостов по
      факту ошибки); при успехе добавить в `defaults.network`
- [ ] 2.3 Эмпирически проверить npm тем же способом; при успехе добавить
      в `defaults.network`
- [ ] 2.4 Эмпирически проверить PyPI тем же способом; при успехе добавить
      в `defaults.network` (это закрывает и нужду `EXP`, см. группу 3)
- [ ] 2.5 Эмпирически проверить Go modules тем же способом; при успехе
      добавить в `defaults.network`
- [ ] 2.6 Решить нужность apt/deb на этом этапе (пакеты внутрь ОС
      песочницы, а не проекта); если нужно — проверить эмпирически и
      добавить, если нет — явно зафиксировать причину отказа

## 3. tools.allow / tools.deny ролей

- [ ] 3.1 Расширить `tools.allow` во всех трёх `role.yaml`
      (`analyst`, `implementer`, `reviewer`) до широких правил
      (`Bash(*)` и подобные) вместо перечисления подкоманд
- [ ] 3.2 Вынести семь общих строк `tools.deny`
      (`push`/`remote`/`checkout`/`switch`/`branch`/`worktree`/`config`)
      в `defaults.tools.deny` в `projects.yaml`; убрать дублирование из
      всех трёх `role.yaml`, оставить в них только роль-специфичные deny
      (например, запрет `add`/`commit`/`restore` у `reviewer`)
- [ ] 3.3 Добавить в `defaults.tools.deny` опасные команды за пределами
      текущего набора git-операций (например `rm -rf`, переписывание
      истории git), с учётом хрупкости сверки к флагам-префиксам
      (design.md, «Открытые вопросы»)
- [ ] 3.4 Убрать из всех трёх `role.yaml` дублирование
      `network.allow: pypi.org, files.pythonhosted.org` — покрывается
      `defaults.network` после задачи 2.4

## 4. Резолюция слоёв в раннере и адаптере

- [ ] 4.1 Добавить точку резолюции: итоговые `network.allow`/
      `tools.allow`/`tools.deny` прогона собираются объединением
      результата `LoadProjects` (слои 1–3) с ролевым слоем
      (`Role.Network.Allow`, `Role.Tools.Allow`/`Deny`) по тому же
      правилу union
- [ ] 4.2 Передать объединённый результат в `adapters/claude/adapter.go`:
      `networkAllow` перестаёт быть единственным источником
      `NetworkAllow`; `buildSettings`/`--tools` используют объединённые
      `tools.allow`/`tools.deny`, а не только `role.Tools.*`
- [ ] 4.3 Поправить комментарий `adapters/claude/adapter.go:180`
      (`// Запрещает всё, чего нет в permissions.allow: ...`) — убрать
      утверждение, что `permissions.allow` ограничивает Bash

## 5. Документация

- [ ] 5.1 Описать слоистую модель разрешений в `docs/DESIGN.md` и/или
      новом контракте под `docs/contracts/` — сейчас она нигде не
      задокументирована
- [ ] 5.2 Прокомментировать ключ `defaults` в `projects.yaml`/
      `projects.local.yaml` по образцу уже существующих комментариев
      про `machineKeys`


```

Full source: docs/openspec/changes/sandbox-network-and-permissions/tasks.md

## docs/openspec/changes/sandbox-network-and-permissions/specs/role-sandbox-permissions/spec.md

- Source: docs/openspec/changes/sandbox-network-and-permissions/specs/role-sandbox-permissions/spec.md
- Lines: 1-74
- SHA256: 8f6104603bb28e214d46058bfbf998eca1925e12fc47643a1855c9590d8c50b6

```md
## Purpose

Определяет, как для одного прогона роли резолвится итоговый разрешённый список
сети (`network.allow`) и итоговые правила инструментов (`tools.allow`/
`tools.deny`) из четырёх слоёв настроек — общих для всего офиса, проектных,
машинных и ролевых — вместо ручного дублирования одних и тех же значений
в каждом `role.yaml`.

## ADDED Requirements

### Requirement: Слоистое разрешение правил
Система SHALL резолвить итоговые `network.allow`, `tools.allow` и `tools.deny`
прогона роли как объединение четырёх слоёв — repo-wide умолчания, проектный
слой, машинный слой, ролевой слой, — где каждый слой может только добавлять
к унаследованному, а не убирать из него.

#### Scenario: Роль без собственных правил наследует итог предыдущих слоёв
- **WHEN** `role.yaml` роли не задаёт `network.allow`/`tools.allow`/`tools.deny`
  явно
- **THEN** итоговые правила прогона равны результату слияния repo-wide,
  проектного и машинного слоёв без изменений

#### Scenario: Ролевой слой добавляет к унаследованному, не стирая его
- **WHEN** `role.yaml` роли задаёт собственный `network.allow` с одним
  дополнительным хостом сверх унаследованного от предыдущих слоёв
- **THEN** итоговый `network.allow` прогона содержит и унаследованные хосты,
  и добавленный ролью

### Requirement: Repo-wide умолчания применяются ко всем проектам
Система SHALL применять зарезервированный ключ `defaults` в `projects.yaml`
(и, если задан, в `projects.local.yaml`) ко всем проектам, для которых
конкретное значение не переопределено на более специфичном слое.

#### Scenario: Новый проект без собственных сетевых правил получает базовый список
- **WHEN** в `projects.yaml` заведён проект без собственного поля `network`
- **THEN** итоговый `network.allow` роли, запущенной для этого проекта,
  содержит хосты из `defaults`

### Requirement: Проектный слой не утекает в другие проекты
Система SHALL ограничивать действие проектных добавок (уровень 2) только тем
проектом, для которого они заданы.

#### Scenario: Специфика одного проекта не видна другому
- **WHEN** проект `EXP` объявляет в своём слое хост `pypi.org`, а проект `VO`
  этот хост нигде не объявляет
- **THEN** итоговый `network.allow` роли, запущенной для `VO`, не содержит
  `pypi.org`

### Requirement: tools.deny — граница, которую нельзя ослабить слиянием
Система SHALL объединять `tools.deny` всех слоёв как объединение множеств:
правило запрета, заданное на любом слое, обязано остаться в итоговом
`tools.deny`, независимо от значений `tools.allow` на более специфичных
слоях.

#### Scenario: Ролевой allow не снимает repo-wide deny
- **WHEN** repo-wide слой запрещает `Bash(git push*)`, а `role.yaml` той же
  роли включает `Bash(*)` в `tools.allow`
- **THEN** итоговый `tools.deny` прогона по-прежнему запрещает
  `Bash(git push*)`

### Requirement: Машинный слой дополняет проектный без правки репозитория
Система SHALL позволять `projects.local.yaml` дополнять правила,
унаследованные от `projects.yaml` (уровни 1 и 2), для конкретной машины,
не требуя изменений в самом репозитории офиса. Как и на остальных уровнях,
машинный слой может только добавлять — убрать унаследованное правило им
нельзя (см. требование «tools.deny — граница, которую нельзя ослабить
слиянием»).

#### Scenario: Машина добавляет внутренний хост поверх проектных правил
- **WHEN** `projects.local.yaml` этой машины задаёт для проекта
  дополнительный хост в своём слое `network`
- **THEN** итоговый `network.allow` прогона на этой машине содержит и
  проектные хосты, и добавленный машиной, а на другой машине без этой записи
  — только проектные

```
