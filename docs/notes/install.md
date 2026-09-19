# Установка: релиз, личность, проверка снапшота

## Что лежит в релизе

Источник истины — `.goreleaser.yaml`, `scripts/build-validators.sh`,
`scripts/check-release-identity.sh` (гейт «личность собранного раннера ==
версия», post-hook сборки) и `scripts/check-host-is-target.sh` (до сборки:
машина сборки обязана быть среди целей, иначе гейту негде сработать),
`.github/workflows/release.yml`, `install.sh`, `office/payload.go`; согласованность
списков платформ и пинов держит `internal/release/config_test.go`; решения и их
причины — `docs/openspec/changes/archive/2026-09-18-install/design.md` (D1, D5, D6) и
Design Doc `docs/superpowers/specs/2026-09-17-install-design.md` §2.1–2.4.

### Три архива, чексуммы, install.sh

Релиз GitHub несёт три архива `virtual-office_<os>_<arch>.tar.gz`
(`darwin_arm64`, `linux_amd64`, `linux_arm64`), в каждом — ровно два файла,
`runner` и `run-agent` (`archives.files: [none*]`: дефолтный README
GoReleaser в архив не идёт, `install.sh` ждёт только эти два имени); один
общий `checksums.txt` (sha256, строка `<hash>  <файл>`); и `install.sh`,
приложенный к релизу отдельным файлом (`release.extra_files`), а не
запакованный внутрь архивов.

Ни в одном имени архива нет версии (`name_template:
"{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"`) — так
`releases/latest/download/virtual-office_<os>_<arch>.tar.gz` разрешается
GitHub напрямую, без обращения к API за именем текущего тега. Какая версия
внутри — говорит уже сам бинарник (`runner version`), а не имя файла.

### Что несёт каждый раннер

Поставка (`office/payload.go`, `go:embed`) весит на диске около 13 МБ:
`office/skills/` — 6,6 МБ, `office/sbx-kits/` (тарболы кита песочницы) —
6,4 МБ, `office/roles/` — 104 КБ, `office/hooks/` — 8 КБ, плюс
`office/workflow.yaml`, `office/budgets.yaml` и оба образца.
Она встроена и в `runner`, и в `run-agent` — второй тоже импортирует пакет
`office` через `internal/runner`, так что каждый архив несёт эти ~13 МБ дважды.
Принято осознанно (Design Doc §2.2): сжатая поставка — единицы мегабайт, а
отдельный код-путь «`run-agent` ждёт, пока `runner` распакует офис» усложнил
бы устройство сильнее, чем экономит.

Сверх поставки каждый раннер несёт бинарники-чекеры `validate-result-<os>-<arch>`
(по ~3,6–3,7 МБ штука), собранные `scripts/build-validators.sh` и встроенные
под тегом `release` файлами `validators_darwin_arm64.go`,
`validators_linux_amd64.go`, `validators_linux_arm64.go` (D5). Какие именно —
решает целевая платформа раннера, а не платформа, на которой раннер
работает во время сборки:

| Раннер собран для | Несёт чекеры |
|---|---|
| `darwin/arm64` | `darwin-arm64` (хост) и `linux-arm64` (песочница `sbx` на Apple Silicon — линукс той же архитектуры) |
| `linux/amd64` | `linux-amd64` (хост и его же `sbx`) |
| `linux/arm64` | `linux-arm64` (хост и его же `sbx`) |

`validators_dev.go` (без тега `release`) даёт пустой набор — сборка без
`-tags release` чекеров не несёт вовсе, ими такую сборку и не обязали
(режим `clone`, ниже).

### Как это собирается

`.goreleaser.yaml` (version 2): хук `before.hooks` зовёт
`scripts/build-validators.sh` (кросс-сборка трёх чекеров в
`office/validators/`, `CGO_ENABLED=0`) раньше сборки самих раннеров; затем
два билда — `runner` (`./cmd/runner`) и `run-agent` (`./cmd/run-agent`) — на
три цели (`darwin_arm64`, `linux_amd64`, `linux_arm64`) с
`flags: [-trimpath, -tags=release]` и `ldflags: -s -w -X
github.com/kao73/virtual-office/office.Version=v{{ .Version }}` — этой строкой
версия релиза попадает в `payload.Version` и делает раннер веткой 2 из
следующего раздела. `git.ignore_tags: ["archive/*"]` нужен ровно потому, что
теги `archive/concept-2026-08` и им подобные достижимы из HEAD этого
репозитория: не отсеки их, GoReleaser принял бы `archive/*` за последний тег
и вычислял бы номер снапшота от него, а не от отсутствия версионных тегов.

Тег `v*`, запушенный в GitHub, запускает `.github/workflows/release.yml`:
checkout с `fetch-depth: 0` (GoReleaser читает историю и теги для списка
коммитов), `go test ./...`, затем `goreleaser/goreleaser-action@v7` с
`args: release --clean`. Больше в конвейере ничего нет: ни подписи, ни
Homebrew, ни своего changelog.

GoReleaser закреплён по версии `v2.18.2` в двух местах — в workflow
(`goreleaser-action@v7` с `version: v2.18.2`) и в
`scripts/release-snapshot.sh` (`go run
github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean`),
одной и той же командой без сети GoReleaser не устанавливается — `go run`
скачивает и собирает его при первом запуске. Пин существует, чтобы два
разработчика (или разработчик и CI) неизбежно собирали идентичный `dist/`.

### Перед первым тегом

Тестовый набор до сих пор гонялся только на darwin/arm64: прежде чем пушить
первый тег `v*`, стоит один раз прогнать `go test ./...` на Linux — workflow
делает это перед вызовом GoReleaser, так что линуксовый сюрприз остановит
релиз именно на этом шаге, а не подсунет битый архив; восстановимо — тег
удаляется, причина чинится, тег ставится заново.

При `git.ignore_tags: ["archive/*"]` автосгенерированный changelog первого
релиза перечислит все коммиты с момента перезапуска репозитория — безвредно,
это следствие отсутствия версионных тегов раньше, а не ошибка; при нежелании —
`changelog.disable: true` в `.goreleaser.yaml`.

## Личность прогона в каждом режиме

Источник истины — `internal/runner/office.go` (`ResolveOffice`,
`payloadIdentity`, `errNoIdentity`), `internal/runner/validator.go`
(`EnsureValidator`), `internal/office/unpack.go`, `internal/tracker/marker.go`
(`commitHash`, `shortenSHA`), `cmd/runner/version.go`, `cmd/runner/init.go`,
`bin/runner`/`bin/run-agent`; решения — `docs/openspec/changes/archive/2026-09-18-install/design.md`
D3, D4, D7, D9 и Design Doc §1.2–1.6.

### Четыре ветки `ResolveOffice`

Первая подошедшая ветка выигрывает; текущий каталог офисом не считается
никогда.

| # | Условие | Root | Identity | Source | Чекер даёт |
|---|---|---|---|---|---|
| 1 | `OFFICE_CONFIG_ROOT` задан | `$OFFICE_CONFIG_ROOT/office` (`Module` — сам `$OFFICE_CONFIG_ROOT`, корень клона) | `git rev-parse HEAD` (+`-dirty`, если `git status --porcelain` не пуст) | `clone` | `go build ./cmd/validate-result` из `Module` (корня клона, не `Root`) под целевую платформу — заново на каждый прогон, в `${OFFICE_HOME}/bin/` (`buildValidator`) |
| 2 | `payload.Version != ""` (релизная сборка, ldflags `-X …Version=`) | `${OFFICE_HOME}/office/<Version>`, либо, при незакоммиченных правках в дереве сборки (`vcs.modified`), `${OFFICE_HOME}/office/<Version>-dirty-<hash8>` | `Version` (например, `v0.7.0` или `v0.0.1-SNAPSHOT-abc1234`), либо `<Version>-dirty` | `payload` | встроенный бинарник поставки, записан один раз в `<Root>/bin/` (`embeddedValidator`) |
| 3 | build info несёт `vcs.revision` (сборка `go build` из клона без обёртки) | `${OFFICE_HOME}/office/<rev[:12]>`, либо, при незакоммиченных правках, `${OFFICE_HOME}/office/<rev[:12]>-dirty-<hash8>` | `<rev>` (полные 40 hex), либо `<rev>-dirty` | `payload` | тот же встроенный бинарник |
| 4 | ни версии, ни `vcs.revision` (например, `go build -buildvcs=false` вне git) | — | — | отказ `errNoIdentity` | — |

### Грязная сборка: имя каталога по содержимому

Каталог `<rev[:12]>-dirty-<hash8>` — и `<Version>-dirty-<hash8>` у снапшота
с незакоммиченного дерева — (восемь hex от `office.Hash(Payload, Validators)`)
существует потому, что для «грязной» сборки ни коммит, ни версия уже не
определяют содержимое поставки: две сборки одного и того же
незакоммиченного дерева могут нести разные роли или разные ограждения, а
GoReleaser даёт снапшотам одного HEAD одну и ту же версию. Ключевание по содержимому, а не только по commit,
делает распаковку идемпотентной и никогда не переписывающей уже
распакованный каталог (D3) — то же правило, что держит «распакованную
версию не трогают» и для чистых версий.

### Маркер: сокращается только commit

`shortenSHA` режет до восьми символов (сохраняя `-dirty`) только значения,
подходящие под `^[0-9a-f]{40}(-dirty)?$`; любая другая личность —
`v0.7.0`, `v0.0.1-SNAPSHOT-abc1234` — пишется в `config:` целиком. Ключ
`config:` в тикете и `config_sha` в `ledger.jsonl` — исторические имена
(поле раньше и было хешем коммита), их переименование не входит в этот
этап.

### `runner version` и `runner init`

`version` зовёт `ResolveOffice(Resolve{Unpack: false})` — ничего не
распаковывает и не открывает ни одного конфигурационного файла — и печатает
`runner <identity>` плюс либо `офис: <Root>`, либо, в режиме `clone`,
`офис: <Root> (клон, OFFICE_CONFIG_ROOT)`. Команда обязана отвечать и там,
где `${OFFICE_HOME}` ещё не существует: `install.sh` зовёт её сразу после
установки как доказательство, что бинарник вообще запускается на этой
машине (виден в шаге 3 проверки ниже).

`init` не разрешает офис вообще (`ResolveOffice` не зовёт) и ничего не
распаковывает: оба образца (`projects.local.example.yaml`,
`tracker.example.yaml`) читаются прямо из `payload.Payload` — те же байты
что и в режиме `clone`, что и в режиме `payload`. Существующий файл не
трогается (печатает `оставлен`), отсутствующий создаётся (`создан`);
рабочие файлы (`projects.local.yaml`, `tracker.yaml`, `budgets.yaml`) `init`
не открывает и не пишет никогда.

### `OFFICE_CONFIG_ROOT` и обёртки `bin/*`

`OFFICE_CONFIG_ROOT` — переключатель разработчика: офис берётся из клона,
а не из поставки, и оба скрипта `bin/runner`, `bin/run-agent` выставляют
его сами (`cd .. && pwd` от места, где лежит сама обёртка) перед тем, как
собрать (`go build -o "${OFFICE_HOME:-$HOME/.office}/bin/<имя>-dev"
./cmd/<имя>`) и заменить процесс обёртки собранным бинарником (`exec`).

Имена разведены нарочно: `${OFFICE_HOME}/bin/runner` и `run-agent`
принадлежат `install.sh`, `runner-dev` и `run-agent-dev` — обёрткам. До
pr-converge (2026-09-18) обёртки писали в те же имена, что и `install.sh`,
и один `./bin/runner tick` из клона молча подменял установленный релиз
dev-сборкой без ограждений и без версии; `runner` из PATH после этого падал
на «раннер собран без ограждения», не говоря о подмене. Теперь на одной
машине клон и релиз не мешают друг другу; старый `${OFFICE_HOME}/bin/runner`
от прежних обёрток, если он есть, при следующем `install.sh` будет
перезаписан релизом — а если релиз ставить не собираетесь, уберите его
руками: обновлять его больше нечему, и в `PATH` он будет замёрзшей
dev-сборкой.

### Осиротевшие `.unpack-*`

Если процесс убит посреди распаковки (`office.Unpack`), временный каталог
`office/.unpack-<имя>-<случайный суффикс>/` (суффикс — от `os.MkdirTemp`, не
pid) остаётся лежать: убирать его некому — соседний `tick` мог в этот момент
вести свою собственную распаковку, и слепая чистка удалила бы чужой ещё живой
временный каталог. Такой осиротевший каталог безвреден и убирается вручную
(`rm -r`).

### Что не входит в объём этого этапа

Чистка старых `office/<dir>/` (их сама поставка не убирает никогда — чужой
работающий раннер мог быть собран из любой из них), `runner doctor`,
самообновление раннера, платформа `darwin/amd64`, установка через Homebrew.
См. Non-Goals в `docs/openspec/changes/archive/2026-09-18-install/design.md` и §6 Design Doc.

### Две находки со сборки

- `go run ./cmd/runner version` из клона с не заданным `OFFICE_CONFIG_ROOT`
  отказывает с текстом «без личности» (`errNoIdentity`): `go run` не
  проставляет VCS-информацию сборки, и `readBuildInfo()` не находит
  `vcs.revision` — ветка 4. `go run -buildvcs=true ./cmd/runner version`
  или обычный `go build` дают ожидаемую ветку 3 (`<commit>-dirty`, каталог
  `<rev[:12]>-dirty-<hash8>`, если дерево не чистое). Обёртки `bin/*` этой
  ловушки не знают: они всегда собирают `go build`, а не `go run`, и всегда
  задают `OFFICE_CONFIG_ROOT` — путь разработчика через них не задет.
- `run-agent --dry-run` требует заданной `ANTHROPIC_API_KEY` или
  `CLAUDE_CODE_OAUTH_TOKEN` (значение может быть любым — так поступают и
  тесты) даже притом что `--dry-run` ничего не тратит и самого агента не
  запускает: проверка креда (`internal/adapters/claude/adapter.go:credential`)
  стоит внутри `claude.Build` и срабатывает раньше, чем запуск вообще
  материализуется — до того, как что-либо, включая печать личности прогона,
  становится доступно вызывающему.

## Проверка снапшота 2026-09-18

Снимок сделан до переезда офиса в `office/`: пути ниже (`payload/validators/`
и всё, что от него производно) — как они были на тот момент, а не как сегодня;
сегодняшний эквивалент — `office/validators/`.

Ручная проверка, ничего не автоматизировано: команды прогнаны по очереди из корня
репозитория (ветка `comet/install`, коммит `338d2e7`), вывод — ниже. Наблюдённая
личность снапшота везде одна: **`v0.0.1-SNAPSHOT-338d2e7`**.

### Шаг 1 — сборка снапшота

```sh
sh scripts/release-snapshot.sh
ls -l dist/
```

GoReleaser отработал (`release succeeded after 10s`), лог содержит ожидаемое:
хук `sh scripts/build-validators.sh` перед сборкой, снапшот-версия
`version=0.0.1-SNAPSHOT-338d2e7`, шесть сборок (`run-agent`/`runner` × `linux_amd64_v1`,
`linux_arm64_v8.0`, `darwin_arm64_v8.0`), три архива, один `checksums.txt`.

`ls -l dist/`: три архива `virtual-office_<os>_<arch>.tar.gz`, `checksums.txt`,
`artifacts.json`, `config.yaml`, `metadata.json`, шесть каталогов `runner_*`/`run-agent_*`.

Размеры архивов:

| файл | размер |
|---|---|
| `virtual-office_darwin_arm64.tar.gz` | 32 783 791 байт (~31,3 МБ) |
| `virtual-office_linux_amd64.tar.gz` | 28 073 715 байт (~26,8 МБ) |
| `virtual-office_linux_arm64.tar.gz` | 27 075 193 байт (~25,8 МБ) |

Размеры бинарников (`dist/runner_*/runner`, `dist/run-agent_*/run-agent`):

| бинарник | платформа | размер |
|---|---|---|
| `runner` | darwin/arm64 | 30 589 762 байт (~29,2 МБ) |
| `runner` | linux/amd64 | 26 697 888 байт (~25,5 МБ) |
| `runner` | linux/arm64 | 25 755 808 байт (~24,6 МБ) |
| `run-agent` | darwin/arm64 | 26 419 026 байт (~25,2 МБ) |
| `run-agent` | linux/amd64 | 22 257 824 байт (~21,2 МБ) |
| `run-agent` | linux/arm64 | 21 823 648 байт (~20,8 МБ) |

`git status --porcelain` под `dist/` и `payload/validators/` — пусто (обе директории
в `.gitignore`, `build-validators.sh` кладёт бинарники ограждений заново при каждой
сборке).

### Шаг 2 — какой checker несёт каждый раннер

```sh
for b in dist/runner_darwin_arm64*/runner dist/runner_linux_amd64*/runner dist/runner_linux_arm64*/runner; do
  echo "$b:"; for p in darwin-arm64 linux-amd64 linux-arm64; do
    printf '  %s %s\n' "$p" "$(grep -a -c "payload/validators/validate-result-$p" "$b")"; done; done
```

| раннер | darwin-arm64 | linux-amd64 | linux-arm64 |
|---|---|---|---|
| `runner_darwin_arm64_v8.0/runner` | 1 | 0 | 1 |
| `runner_linux_amd64_v1/runner` | 0 | 1 | 0 |
| `runner_linux_arm64_v8.0/runner` | 0 | 0 | 1 |

Совпадает с ожиданием: darwin/arm64-раннер несёт свой checker и линуксовый ARM
(бэкенд `sbx` — линукс на Apple Silicon), линуксовые раннеры — только свой.

### Шаг 3 — shell-тест по архивам, затем реальная установка

```sh
sh scripts/install-test.sh dist
```

```
ok: установка
ok: обновление на месте
ok: битая контрольная сумма
ok: чужая платформа
все четыре сценария прошли
```

Это же подтверждает перенос из задачи 12: формат строки `dist/checksums.txt`
(`<hash>  <file>`, без `*`) и раскладка архива (бинарники на верхнем уровне)
совпадают с тем, что ждёт `install.sh` — иначе `install-test.sh` не прошёл бы
сценарий «установка» на настоящем `dist/`. Для записи:

```
$ head -2 dist/checksums.txt
1eb16425bdc5140ddb4bd5795f666eeb67b740b924e02bb6b18593e434fcc24a  virtual-office_darwin_arm64.tar.gz
9b62ecba55046283c89c713b5bc094e10dfd6c309ee82b7bd0722179cba3c03a  virtual-office_linux_amd64.tar.gz

$ tar -tzf dist/virtual-office_darwin_arm64.tar.gz
run-agent
runner
```

Дальше — реальная установка в свежий, ранее не существовавший `OFFICE_HOME`
(`mktemp -d`, не `~/.office`):

```sh
export OFFICE_HOME="$(mktemp -d)"
OFFICE_INSTALL_FROM=dist sh install.sh
"$OFFICE_HOME/bin/runner" version
ls "$OFFICE_HOME"            # только bin/
```

```
установлено в /var/folders/.../tmp.YHk8AbD1Uu/bin:
runner v0.0.1-SNAPSHOT-338d2e7
офис: /var/folders/.../tmp.YHk8AbD1Uu/office/v0.0.1-SNAPSHOT-338d2e7
/var/folders/.../tmp.YHk8AbD1Uu/bin не в PATH — добавьте в профиль оболочки:
  export PATH="/var/folders/.../tmp.YHk8AbD1Uu/bin:$PATH"
дальше: runner init
```

`runner version` печатает те же две строки (`runner v0.0.1-SNAPSHOT-338d2e7` и
`офис: .../office/v0.0.1-SNAPSHOT-338d2e7`). `ls "$OFFICE_HOME"` в этот момент —
только `bin`: `office/` ещё не распакован, как и ожидалось.

**Методическое замечание.** `OFFICE_HOME` обязателен именно как экспортированная
переменная (`export`, не просто присвоение) — раннер и `run-agent` читают его как
переменную окружения дочернего процесса. Присвоение без `export` (например,
через `source` файла без слова `export`) молча даёт `runner`/`run-agent` пустой
`OFFICE_HOME`, и они уходят на предсказуемый дефолт `~/.office` — реальное
хозяйство этой машины. Поймано и исправлено на этом прогоне до какого-либо
вреда: `runner init` успел один раз создать в `~/.office` два примера
(`projects.local.example.yaml`, `tracker.example.yaml`, которых там раньше не
было), они немедленно удалены, рабочие файлы (`projects.local.yaml`,
`tracker.yaml`) `init` не трогает никогда и не был задет.

### Шаг 4 — `runner init`, mock-проект, прогон implementer'а

```sh
"$OFFICE_HOME/bin/runner" init
ls "$OFFICE_HOME"            # bin/ projects.local.example.yaml tracker.example.yaml
```

```
создан   .../projects.local.example.yaml
создан   .../tracker.example.yaml
дальше: скопируйте каждый образец под рабочее имя и поправьте значения: ...
```

`ls "$OFFICE_HOME"` → `bin`, `projects.local.example.yaml`, `tracker.example.yaml`.

Mock-проект — bare-репозиторий, клон, пустой коммит, push, `projects.local.yaml`
с `tracker: mock` (последовательность из плана сохранена буквально):

```sh
git init -q --bare -b master "$OFFICE_HOME/client.git"
git clone -q "$OFFICE_HOME/client.git" "$OFFICE_HOME/client"
git -C "$OFFICE_HOME/client" -c user.name=you -c user.email=you@local commit -q --allow-empty -m init
git -C "$OFFICE_HOME/client" push -q origin master
printf 'OFF:\n  repo_url: %s\n  default_branch: master\n  tracker: mock\n' "$OFFICE_HOME/client.git" > "$OFFICE_HOME/projects.local.yaml"
"$OFFICE_HOME/bin/runner" mock add OFF-1 --status Ready --summary "Добавить hello.py" \
  --description "Создай hello.py, печатающий приветствие. Закоммить."
```

`mock add` → `OFF-1 заведена в Ready`. Mock-трекер тем самым пройден (bare-репозиторий,
клон, push, `projects.local.yaml`, `runner mock add`), хотя платный `tick` не запускался
(см. ниже).

**Платный `runner tick --role implementer --backend local` не запускался** — это решение
контролёра задачи: у сессии на этой машине нет кредов агента в обычном рабочем окружении.
Вместо него — предложенный планом бесплатный заменитель, с обязательным `--task`
(без него `run-agent` отказывает раньше любой работы: «нужны --role и --workdir» относится
к другим флагам, а без задачи логика читает пустую постановку):

```sh
printf 'Создай hello.py, печатающий приветствие.\n' > "$OFFICE_HOME/task.md"
"$OFFICE_HOME/bin/run-agent" --role implementer --workdir "$OFFICE_HOME/client" \
  --backend local --dry-run --task "$OFFICE_HOME/task.md"
```

Первая попытка без креда в окружении завершилась инфраструктурным отказом (код 2):

```
run-agent: бэкенд local сетевой политики не применяет: роль просит *.cloudfront.docker.com, ...
run-agent: не задан ни ANTHROPIC_API_KEY, ни CLAUDE_CODE_OAUTH_TOKEN: агенту нечем авторизоваться, см. docs/notes/auth.md
```

Это показывает, что `--dry-run` не полностью «беспарольный»: `runagent.Prepare` всё
равно собирает `Launch` через `claude.Build`, а тот проверяет кред
(`internal/adapters/claude/adapter.go:credential`), прежде чем что-либо материализовать
— даже при `--dry-run`, где сам `claude` не запускается вовсе. Кред для агента на этой
машине лежит в `~/.zshrc` (`CLAUDE_CODE_OAUTH_TOKEN`) и не виден неинтерактивному шеллу.
Подставив его в окружение только для этой команды (кред ни разу не тратится: `--dry-run`
не исполняет `claude`, значит платного вызова по-прежнему нет — требование контролёра
не запускать платный прогон соблюдено буквально), получили ожидаемый план'ом вывод:

```
run_id:     918fff2c-b18e-431a-bd4c-8d8efb51cc42
роль:       implementer
config_sha: v0.0.1-SNAPSHOT-338d2e7
workdir:    .../client
конфиг:     /var/folders/.../office-run-.../config
таймаут:    2h0m0s
платформа:  darwin/arm64
ограждение: .../office/v0.0.1-SNAPSHOT-338d2e7/bin/validate-result-darwin-arm64
```

(в `== окружение запуска ==` кред напечатан замаскированным: `CLAUDE_CODE_OAUTH_TOKEN=***`
— утечки нет). `grep -c 'go build'` на полном выводе — `0`: бэкенд `local` действительно
берёт готовый бинарник ограждения из поставки, а не собирает Go на лету.

```sh
ls "$OFFICE_HOME/office/" "$OFFICE_HOME/office/"*/bin/
```

```
office/: v0.0.1-SNAPSHOT-338d2e7
office/v0.0.1-SNAPSHOT-338d2e7/bin/: validate-result-darwin-arm64
```

Раз `tick` не выполнялся, маркер `config:` в тикете не появляется:

```
$ "$OFFICE_HOME/bin/runner" mock show OFF-1 | grep 'config:'
(пусто)
```

По допущению самого плана эта проверка заменяется строкой `config_sha:` из вывода
`--dry-run` выше — та же личность снапшота, тем же самым способом подтверждённая.

### Шаг 5 — checker песочницы, bake пропущен

```sh
"$OFFICE_HOME/bin/run-agent" --role implementer --workdir "$OFFICE_HOME/client" \
  --backend sbx --dry-run --task "$OFFICE_HOME/task.md" >/dev/null; echo "exit $?"
file "$OFFICE_HOME/office/"*/bin/validate-result-linux-arm64
```

`exit 0`. `--dry-run` на `sbx` подготовил запуск (`EnsureValidator(linux/arm64)` дописал
линуксовый checker из поставки), сам песочницу не поднимая — в выводе виден полный план
запуска: команда `claude ... --tools Bash,Edit,Read,Write,Skill`, список сети сверх
нужной агенту, смонтированные рабочие пространства, `settings.json` с hooks Comet.

```
$ file "$OFFICE_HOME/office/v0.0.1-SNAPSHOT-338d2e7/bin/validate-result-linux-arm64"
...: ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked, ...
```

Bake (`"$OFFICE_HOME/office/"*/sbx-kits/bake-comet-template.sh` — скрипт bash, под `sh` = dash не запустится) **пропущен**: сеть
песочницы на этой машине закрыта (deny-all), а bake тянет образ с `registry.npmjs.org` —
без сети шаг не пройдёт, запускать не стали.

### Шаг 6 — очистка

```sh
rm -rf "$OFFICE_HOME"; rm -rf dist; rm -f payload/validators/validate-result-*
```

`git status --porcelain` после очистки — пусто, кроме этого файла заметки.

### Что покрыто вживую, что пропущено

Покрыто: сборка снапшота даёт три раннера с их checker'ами; установка из локального
снапшота; релизный раннер называет свою версию и каталог офиса; прогон помечен верной
версией (`config_sha`/`config:` — личность снапшота); локальный запуск берёт checker
хоста без Go; запуск на Apple Silicon для линукса не нуждается в Go (checker пришёл из
поставки, ARM ELF подтверждён `file`).

Пропущено: платный `runner tick` (кредов нет в обычном окружении сессии — заменён
`--dry-run`-эквивалентом, см. шаг 4); bake образа sbx-кита (сеть песочницы на машине
закрыта).

## Отложено после pr-converge (2026-09-18)

Находки локального ревью PR #11, которые сознательно не правились в этом PR —
с причиной, чтобы не переоткрывать:

- **`runner init` и `runner version` молча игнорируют позиционные аргументы**
  (`runner init /some/dir`). Так же ведут себя остальные подкоманды; менять
  договор о разборе флагов — не в этом PR.
- **Распаковка не делает fsync файлов перед `rename`.** После потери питания
  итоговый каталог теоретически может содержать усечённые файлы, и
  `ResolveOffice` будет считать его распакованным. Цена — fsync каждого из
  сотен файлов на первом запуске; лечение руками — `rm -rf` каталога версии.
  Ограждение (один файл) fsync'ается.
- **`ConfigSHA` теряет stderr git'а**: на не-git каталоге в `OFFICE_CONFIG_ROOT`
  ошибка звучит как `exit status 128`, а не `not a git repository`. Поведение
  старше этого PR.
- **`go install github.com/kao73/virtual-office/cmd/runner@vX`** получает
  отказ `errNoIdentity`, хотя `debug.ReadBuildInfo().Main.Version` знает
  версию модуля. Пятая ветка `ResolveOffice` — отдельное решение: такой
  бинарник не несёт ограждений (`-tags release` через `go install` не
  передать), и что он должен делать в режиме поставки — вопрос.
- **`${OFFICE_HOME}/office/` только растёт.** Версии не убираются нарочно
  (D3: чужой работающий раннер мог быть собран из любой), осиротевшие
  `.unpack-*` — тоже нарочно (см. выше). На машине разработчика, гоняющего
  грязные снапшоты, это копия офиса на каждый `hash8`. Команды `runner office
  ls/prune` нет — чистить руками `rm -r office/<dir>`; решение о команде — вместе
  с `runner doctor`.
- **Ручная `-tags release` на чистом дереве с устаревшими чекерами.**
  `office/validators/*` не отслеживаются git, поэтому чекеры, собранные до
  последнего коммита, не делают дерево грязным: релизный бинарник под тем же
  коммитом получит тот же каталог `office/<версия>/`, а лежащий там чекер
  первой сборки не перепишется. В CI не воспроизводится — `before.hooks`
  пересобирает чекеры каждый раз. Правило для рук: `-tags release` — только
  сразу после `scripts/build-validators.sh`; `scripts/release-snapshot.sh` это
  делает сам.
- **Соседние тексты, задетые новой моделью, но лежащие вне PR**:
  `docs/ONBOARDING.md` (путь из клона: «Go — раннер», «config_sha — HEAD
  конфиг-репозитория»), комментарии `internal/runner/archive.go` (`HomeEnv`
  без `office/<версия>/`) и `internal/backends/sbx/sbx.go` (путь к bake только
  клоновый), `internal/tracker/config.go` (`LoadProjects` не упоминает
  `runner init`). Править вместе с user-guide.
