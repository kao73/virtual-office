# sbx-kits

`sbx`-специфичная обвязка машины, той же природы, что `sbx policy init deny-all`:
одноразовая настройка хоста, а не часть поставки офиса. Раннер про это не знает —
он просто зовёт `sbx create`, ожидая, что нужный образ уже есть.

## comet-cli

Вендоренные CLI `@rpamis/comet` (`.source.yaml`) и `@fission-ai/openspec`
(`.source-openspec.yaml`), запечённые в локальный образ песочницы через `sbx kit`,
а не устанавливаемые заново на каждый прогон: `npm install -g` обоих тарболов внутри
песочницы стоит ~28 секунд (тарболы локальные, но тянут собственные зависимости из
`registry.npmjs.org` — см. `network` в `spec.yaml`), против обычных 5–6 секунд
`sbx create`. Испечённый образ снимает эту наценку целиком.

Роли `analyst`/`implementer`/`reviewer` (Comet Native) без `comet` в PATH не могут
работать вовсе — их скиллы `comet`/`comet-native` без самого CLI мертвы. Подробности
исследования и живой проверки — `docs/notes/sbx.md`, раздел «`comet` CLI bootstrap».

`openspec` не нужен ни одной из трёх ролей сегодня — проверено эмпирически
(`docs/notes/sbx.md`, тот же раздел): `comet native new/status/next` не читает и не
запускает ничего, кроме собственного состояния в `.comet/`, а собственный
`continuation.skill` у `comet-native` в ответе называет только `comet-native`, никогда
соседние скиллы пакета (`comet-any`/`comet-build`/`comet-classic`/…) и не `openspec`.
Он завезён заранее, чтобы будущий переход этой роли-конвейера на Comet Classic
(если понадобится) не упёрся в тот же bootstrap-пробел заново.

### Испечь на новой машине (или после смены версии)

    ./bootstrap/sbx-kits/bake-comet-template.sh

Печёт `office-claude-comet:<версия>` в локальный образный стор `sbx` на этой машине —
образ никуда не публикуется и с git не едет. Без этого шага `sbx create` в
`internal/backends/sbx` откажет — живьём код `403 Forbidden: pull failed for image`,
неотличимый на глаз от сетевого отказа политики, а не «образ не найден»: если увидели
его на новой машине, первым делом проверьте `sbx template ls`, а не сеть. Раннер
сознательно не откатывается молча на образ без `comet` — иначе роли Comet Native
проваливались бы на первом же вызове CLI без понятной причины.

**`network.allowedDomains` в `spec.yaml` — не разовое окно на время установки.**
Кит-домены сливаются в то же per-sandbox правило, что и домены встроенного кита
`claude`, и живут всю жизнь песочницы, а не только время `commands.install`
(проверено: `sbx policy ls <sandbox> --wide` после `--kit`-создания показывает
`registry.npmjs.org` рядом с `claude.com`/`downloads.claude.ai` в одном правиле
источника `kit`). Сегодня это не дыра только потому, что продакшн создаёт песочницы
через `--template` (снимок), а не заново через `--kit`, — у снимка никаких сетевых
правил нет вовсе (проверено: в песочнице из шаблона `registry.npmjs.org` запрещён).
Добавляя домен в `spec.yaml` на будущее, считайте его открытым на всю жизнь
песочницы, если кто-то когда-то вызовет `sbx create --kit` этим китом напрямую,
а не через испечённый шаблон.

### Обновление версии `comet`

1. Освежить тарбол: `npm pack @rpamis/comet@<новая версия> --pack-destination
   bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg`, убрать старый файл.
2. Поправить путь и версию в `commands.install` файла `spec.yaml`, и в `.source.yaml`
   (тег/коммит — как для `skills/comet/.source.yaml`/`skills/comet-native/.source.yaml`,
   тот же npm-пакет).
3. Поправить тег `TAG` в `bake-comet-template.sh` и константу `Template` в
   `internal/backends/sbx/sbx.go` — они обязаны совпадать буквально.
4. Перепечь: `./bootstrap/sbx-kits/bake-comet-template.sh`.

Старый образ в сторе не подчищается сам — `sbx template rm <старый тег>`, если нужно
освободить место.

### Обновление версии `openspec`

То же самое, отдельно от `comet`: `npm pack @fission-ai/openspec@<версия>
--pack-destination bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg`, убрать старый
файл, поправить путь во втором `commands.install` файла `spec.yaml` и в
`.source-openspec.yaml`, перепечь. Версии `comet` и `openspec` друг от друга не зависят технически — `@rpamis/comet`
несёт `@fission-ai/openspec` как npm-зависимость лишь для собственных нужд (не пробрасывает
её бинарник наружу глобальной установкой), поэтому наша версия `openspec` в PATH выбирается
отдельно и может разойтись с той, что зашита внутрь `comet`.
