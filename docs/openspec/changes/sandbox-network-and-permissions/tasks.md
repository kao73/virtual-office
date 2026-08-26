## 1. Слой правил в конфиге проектов

- [x] 1.1 Добавить в `tracker/config.go` поля `Network []string` и
      `Tools{Allow, Deny []string}` в `Project`, `officeProject`,
      `machineProject`
- [x] 1.2 Научить `LoadProjects` зарезервированному ключу `defaults`:
      разобрать его отдельно от `map[string]officeProject`/
      `map[string]machineProject`, исключить из обязательной парности
      ключей office/machine и из проверок `repo_url`/`tracker`/
      `default_branch`/`branch_prefix`
- [x] 1.3 Реализовать объединение (union, без удаления унаследованного)
      `defaults` + собственных полей проекта — отдельно для office- и
      machine-половины, — в итоговые `Network`/`Tools` каждого проекта
- [x] 1.4 Тесты `LoadProjects`: `defaults` отсутствует в одном из двух
      файлов; `defaults` задан в обоих; реальный проект без собственного
      `network`/`tools` наследует только `defaults`; специфика одного
      проекта не видна другому; проект без `defaults` вообще ведёт себя
      как сегодня (регресс не сломан)

## 2. Repo-wide базовый список доменов

- [x] 2.1 Добавить в `projects.yaml` под `defaults.network` проверенный
      список Docker Hub: `registry-1.docker.io`, `auth.docker.io`,
      `*.docker.io`, `production.cloudfront.docker.com`,
      `*.cloudfront.docker.com`
- [x] 2.2 Эмпирически проверить GitHub тем же способом, что и Docker Hub
      (пустая одноразовая песочница → голая попытка → добавление хостов по
      факту ошибки); при успехе добавить в `defaults.network`
- [x] 2.3 Эмпирически проверить npm тем же способом; при успехе добавить
      в `defaults.network`
- [x] 2.4 Эмпирически проверить PyPI тем же способом; при успехе добавить
      в `defaults.network` (это закрывает и нужду `EXP`, см. группу 3)
- [x] 2.5 Эмпирически проверить Go modules тем же способом; при успехе
      добавить в `defaults.network`
- [x] 2.6 Решить нужность apt/deb на этом этапе (пакеты внутрь ОС
      песочницы, а не проекта); если нужно — проверить эмпирически и
      добавить, если нет — явно зафиксировать причину отказа

## 3. tools.allow / tools.deny ролей

- [x] 3.1 Расширить `tools.allow` во всех трёх `role.yaml`
      (`analyst`, `implementer`, `reviewer`) до широких правил
      (`Bash(*)` и подобные) вместо перечисления подкоманд
- [x] 3.2 Вынести семь общих строк `tools.deny`
      (`push`/`remote`/`checkout`/`switch`/`branch`/`worktree`/`config`)
      в `defaults.tools.deny` в `projects.yaml`; убрать дублирование из
      всех трёх `role.yaml`, оставить в них только роль-специфичные deny
      (например, запрет `add`/`commit`/`restore` у `reviewer`)
- [x] 3.3 Добавить в `defaults.tools.deny` опасные команды за пределами
      текущего набора git-операций (например `rm -rf`, переписывание
      истории git), с учётом хрупкости сверки к флагам-префиксам
      (design.md, «Открытые вопросы»)
- [x] 3.4 Убрать из всех трёх `role.yaml` дублирование
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

## 6. Проверка

- [ ] 6.1 Прогнать `go test ./...` — регресс не сломан
- [ ] 6.2 Живой прогон роли (например `implementer`) на `sbx` для `EXP`:
      unit-тесты, поднятие БД через `docker`, и, если применимо,
      `docker compose` для e2e — проходят с новыми правилами сети/tools
- [ ] 6.3 Живой прогон роли для проекта без собственной специфики
      (например `VO`/`OFFICE`): получает только repo-wide умолчания, без
      утечки специфики `EXP`
