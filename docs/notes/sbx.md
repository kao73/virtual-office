# Docker Sandboxes (sbx) — рабочие заметки

Проверено 2026-08-16 на `sbx` **v0.38.0**, macOS arm64.

## Почему sbx, а не docker

План этапа ставил докер первым, потому что его «проще отладить». К пятому шагу это
перестало быть правдой: контур уже был отлажен на бэкенде `local`, и роль известной
величины досталась ему. У докера осталась одна ценность — машины без KVM, которых
у нас пока нет.

А трудозатраты оказались обратными ожидаемым:

| | docker | sbx |
|---|---|---|
| Трансляция путей в `/work`, `/config` | нужна | не нужна, пути совпадают |
| Образ, пиннинг версии, uid, `safe.directory`, личность git | всё вручную | берёт на себя |
| Изоляция от `~/.claude` | своим `CLAUDE_CONFIG_DIR` | из коробки |
| Изоляция сети | никакой | deny-by-default с allowlist |

Докер отложен до появления железа без KVM. `DESIGN.md` §2.6 и без того называет sbx
предпочтительным бэкендом, а докер — фолбэком.

## Модель: песочница на прогон

Создаётся под задачу, сносится после неё. Замер при закэшированном образе:

| Операция | Время |
|---|---|
| `sbx create` | 5–6 с |
| первый `sbx exec` | до 1 с |
| `sbx rm --force` | 0–1 с |

При прогоне в 20–60 секунд накладные — 15–25%. Платим за свежую файловую систему
на каждой задаче и за отсутствие состояния между задачами. Если накладные вырастут,
запасной вариант — долгоживущая песочница на роль, как предполагает `DESIGN.md` §2.6.

## Что проверено эмпирически

**Пути внутри совпадают с хостовыми.** Документация обещает «all workspaces appear
inside the sandbox at their absolute host paths», и это так: `pwd` возвращает хостовый
путь. Отсюда главное следствие — **адаптеру не нужна трансляция путей вообще**.

**`:ro` — настоящий read-only.** Запись в каталог, отданный с суффиксом `:ro`,
отбивается ядром: `Read-only file system`. Материалы роли агент переписать не может.

**Коды выхода прокидываются.** `sbx exec sh -c 'exit 7'` даёт 7.

**`sbx exec`, а не `sbx run`.** `sbx run claude` подставляет свои дефолты, включая
`--dangerously-skip-permissions`, и аргументы после `--` дописываются *после* них.
`sbx exec` исполняет ровно то, что дали, — поэтому наш режим `dontAsk` с белым списком
уцелевает.

**Флаги, которые нужны бэкенду:** `-i` (пробрасывает stdin — там идёт стартовое
сообщение), `-w` (рабочая директория), `-e VAR=значение` и `-e VAR` (наследование
из окружения хоста, значение не попадает в командную строку).

**Внутри:** `uid=1000(agent)`, не root. Есть `claude`, `git`, `python3`, `uv`.
Все флаги, которые собирает адаптер, версия в образе принимает.

**Платформа внутри — `linux/arm64`** (`uname -s -m` → `Linux aarch64`, ядро 7.0.12,
Ubuntu 26.04 LTS, glibc 2.43). Архитектура совпадает с хостовой: песочница — microVM
на том же железе. Отсюда `GOOS=linux GOARCH=arm64` для всего, что раннер собирает
для исполнения внутри, — сейчас это бинарник ограждения `validate-result`
(`backends/sbx.Platform()`, на это есть тест).

Собирается он с `CGO_ENABLED=0`: `file` показывает `ELF 64-bit LSB executable,
ARM aarch64, statically linked`. Статическая сборка снимает вопрос о версии libc
в образе вовсе. Проверено запуском внутри песочницы: невалидный результат — код 2
и причина в stderr, валидный — код 0.

**git работает без плясок.** Примонтированный репозиторий читается и коммитится,
`safe.directory` настраивать не пришлось. Коммит, сделанный внутри, виден на хосте
немедленно: это bind mount, а не копия, — раннер читает `result.json` и историю прямо
у себя.

**Worktree внутри песочницы требует двух монтирований и канонических путей.**
Проверено на шаге 3 этапа 2, вживую.

Рабочая папка задачи — git worktree, и `.git` в ней не каталог, а файл со строкой
`gitdir: <bare>/worktrees/<KEY>`. Если отдать песочнице только рабочую папку, git внутри
отвечает `fatal: not a git repository: …` — указатель висит в пустоту. Объекты коммитов
тоже пишутся в общий каталог, поэтому bare-репозиторий монтируется **на запись**:

    sbx create --name … claude <worktree> <bare-репозиторий>

Второе условие — **канонические пути**. `git worktree add` записывает в `.git` разрешённый
путь, а на macOS `/tmp` — симлинк на `/private/tmp`. Смонтировав `/tmp/...`, получаем внутри
тот же отказ, хотя формально отдано всё нужное. Поэтому `workspace` разрешает симлинки
(`filepath.EvalSymlinks`) на всех своих путях, и на это есть тест.

С обоими условиями связка работает целиком: коммит, сделанный агентом внутри песочницы,
виден на хосте немедленно (bind mount, не копия), и раннер тут же публикует ветку
своим `Push` — уже вне песочницы и вне агента.

## Грабли

**Пустой элемент команды отвергается.** `sbx exec` отвечает
`400 Bad Request: cmd element N is empty`. Мы передавали `--setting-sources ""` двумя
токенами, и прогон падал на старте за восемь секунд. Лечится формой с равенством —
`--setting-sources=` одним токеном; она принимается разбором и сохраняет смысл
(проверено отдельно: настройки проекта-клиента по-прежнему не подхватываются).

Правило на будущее: в командной строке агента не должно быть пустых аргументов.
На это есть тест.

**`sbx rm` требует `--force`** в неинтерактивном режиме, иначе
`stdin is not a terminal; use --force to skip confirmation`.

**`sbx rm` несуществующей песочницы — отказ, а не тишина:** код 1 и
`Error: sandbox 'X' not found`. Отсутствие песочницы — обычное дело (reap мог быть
запущен не на той машине, где шёл прогон), поэтому уборщик сначала спрашивает
список. `sbx ls --quiet` для этого удобнее `--json`: голые имена по одному
в строке, без шапки, пустой вывод при пустом списке.

## Уборка песочниц мёртвых прогонов

Обычный прогон сносит свою песочницу сам, но раннера могли убить — тогда microVM
остаётся работать, и убрать её некому. Это делает `reap`: имя собирается из `run_id`
мёртвой аренды тем же правилом, что и при создании, и сносится ровно оно. Метём
точечно, а не по списку: соседняя песочница может принадлежать живому прогону
в другом процессе или на другой машине.

**Известный предел.** Если задачу перезахватит соседний `tick` ровно между выборкой
истёкших аренд и действием `reap`, задача достанется новому прогону, а песочница
старого останется: reap эту задачу не чинил, значит и не убирает. Дыра требует двух
раннеров на одной машине и попадания в окно в доли секунды; под `loop` она
недостижима вовсе — reap и tick идут там по очереди в одном процессе. Чинится
сравнением `run_id` до и после отказа по владению; не делаем, пока не встретим
вживую.

**Чего в песочницах пока не убирается вовсе** — ничего: и брошенные прогоном,
и оставшиеся от смерти раннера теперь снимаются. А вот архив прогонов
в `${OFFICE_HOME}/runs/` копится по-прежнему.

**Версия агента в образе своя.** В песочнице был claude **2.1.221** при хостовом
2.1.233. Версию образа задаёт sbx, а не мы. ~~Для воспроизводимости это открытый
вопрос: у sbx есть `kit` и `template`, но мы их не трогали.~~ **Закрыто, задача 17** —
`kit` и `template` теперь трогаем, и как именно: см. «Открытые вопросы» ниже
и раздел «`comet` CLI bootstrap».

## Чего sbx не даёт

**Живой ленты действий в `sbx tui`.** Дашборд показывает, какие песочницы живы,
в каком они статусе и над какими каталогами работают, а клавиша `x` открывает шелл
внутрь. Но прикрепиться к агенту нельзя: мы запускаем его через `sbx exec` в режиме
`--print`, интерактивной сессии у него нет вовсе.

Живая картина берётся из лога. Адаптер запрашивает `--output-format stream-json`,
поэтому `run.log` наполняется построчно по ходу работы:

    tail -f <workdir>/.agent/run.log

Каждая строка — событие: вызов инструмента с аргументами, ответ, отказ в разрешении.
Разобрать по-человечески можно так:

    jq -r 'select(.type=="assistant") | .message.content[]?
           | select(.type=="tool_use") | "\(.name): \(.input.command // .input.file_path // "")"' run.log

## Сеть: как устроена политика (этап 3, шаг 6)

Проверено вживую 2026-08-18 на закрытой сети. Все команды политики требуют входа
в Docker (`sbx login`): без него отвечают `401 Unauthorized: user is not authenticated
to Docker`, и это относится даже к `sbx ls` — без входа не работает ничего.

**Три уровня, и главный из них — глобальный.**

| Уровень | Команда | Область |
|---|---|---|
| базовая политика машины | `sbx policy init <allow-all\|balanced\|deny-all>` | все песочницы, один раз |
| глобальное правило | `sbx policy allow network HOSTS` | все песочницы |
| правило на песочницу | `sbx policy allow network --sandbox NAME HOSTS` | одна песочница |
| запрет при создании | `sbx create --deny-network HOST` | одна песочница |
| правила кита | встроенные киты агентов | одна песочница |

`init` — одноразовая настройка: «must be run before adding custom allow/deny rules or
starting a sandbox for the first time», переиграть можно только через `sbx policy reset`.
`balanced` пускает «typical development traffic … such as AI services and package
registries», `deny-all` не пускает никуда.

**Запрет сильнее разрешения.** Сказано прямо в справке обеих команд: «If a resource
matches both an allow and a deny rule, the request is blocked».

**Отсюда следствие, определившее устройство раннера.** Собрать «закрыто всё, открыто
названное» **на уровне песочницы нельзя**. Локальный запрет `**` перекрыл бы и то, что
нужно самому агенту, — API и OAuth Anthropic, — а перекрыть его разрешением невозможно:
запрет сильнее. Не случайно у `sbx create` есть `--deny-network` и нет `--allow-network`:
в справке к нему так и написано — «a local deny can only narrow, never widen, egress».

Значит закрытое умолчание — свойство **машины**, а не роли, и ставит его человек один раз:

    sbx policy init deny-all

Раннер добавляет к этому только разрешающие правила на свою песочницу, из
`role.yaml: network.allow`. Порядок обязателен: `create` → `policy allow --sandbox` →
`exec`. Раньше создания правилу не к чему прицепиться, позже — агент уже работает
и часть запросов сделает вслепую.

Обещать «пусто в role.yaml = deny-all» раннер поэтому не вправе: на машине с
`allow-all` роль без единого домена всё равно получит открытую сеть.

**Как закрыть сеть на этой машине.** `sbx policy init` тут не при чём: политика уже
была инициализирована, и в ней лежало одно правило `default-allow-all → **`. Закрытие —
это его снятие, и оно обратимо:

    sbx policy rm network --id default-allow-all     # закрыть
    sbx policy allow network "**"                    # вернуть как было

После снятия проступает `default-deny-all`, и `sbx policy check network example.com --json`
отвечает `"allowed": false`, `"deny_kind": "implicit"`, `"reason": "No matching allow rule
(default deny)"`. Формат ответа удобный: одно булево поле и человекочитаемая причина.

### Кит агента не открывает его собственный API

Главная находка шага и единственная, из-за которой пришлось менять замысел.

Встроенный кит `claude` заводит песочнице ровно три правила:

    allow  claude.com:443
    allow  downloads.claude.ai:443
    allow  mcp-proxy.anthropic.com:443

**`api.anthropic.com` среди них нет.** При закрытой сети агент из-за этого не проходит
авторизацию вовсе:

    Failed to authenticate. API Error: 403 Blocked by network policy: domain api.anthropic.com:443
      detail: no matching allow rule — blocked by default deny policy

С одним этим доменом — работает («Привет» за один шаг). Значит открывать его обязан
раннер, и делает это **адаптер**: куда ходит Claude Code — знание о конкретном агенте,
а роль про него не знает ничего (`claude.AgentAPIHost`).

### Что ещё стучится и чего не хватает

По `sbx policy log` живого прогона при закрытой сети:

| Хост | Кто | Итог |
|---|---|---|
| `api.anthropic.com:443` | агент | разрешён адаптером, 14 запросов |
| `pypi.org:443`, `files.pythonhosted.org:443` | `uv run --with pytest` | разрешены ролью |
| `http-intake.logs.us5.datadoghq.com:443` | телеметрия | заблокирован ×4, прогон не пострадал |
| `ports.ubuntu.com:80`, `download.docker.com:443` | загрузка песочницы | заблокированы, прогон не пострадал |

Две попытки достучаться до `api.anthropic.com` заблокированы **до** нашего правила —
в момент `sbx create`, когда песочница поднимается, а правило ещё не выдано. Прогон
это пережил: `exec` идёт уже с правилом, и дальше все 14 запросов прошли. Порядок
`create → policy allow → exec` иначе не собирается: до создания правилу не к чему
прицепиться.

### Проверка «а закрыта ли сеть» (этап 4, шаг 0)

Раннер закрыть сеть не может, но обязан знать, закрыта ли она: на машине
с открытой политикой роль без единого домена получит всю сеть, и сказать
об этом больше некому. Спрашивается перед каждым прогоном:

    sbx policy check network example.com --json

`example.com` — канарейка: домен зарезервирован IANA под документацию, роли
он не понадобится никогда, и ответ «пустят» означает ровно одно — пустят куда
угодно. Контекст глобальный, без `--sandbox`: спрашиваем о базовой политике
машины, а песочницы прогона в этот момент ещё нет.

Три вещи, измеренные на живом CLI, — все три подделка не подсказала бы:

**Отказ приходит с кодом 1.** `policy check` завершается единицей всякий раз,
когда «не пустят», хотя JSON при этом полный и правильный. Считать ненулевой код
неудачей проверки значит предупреждать об открытой сети именно там, где она
закрыта. Ответ разбирается раньше кода выхода и важнее его.

**Разрешающий ответ ничего не объясняет.** При `allowed: false` в JSON есть
`deny_kind` и `reason` («No matching allow rule (default deny)»), при
`allowed: true` — ни того, ни другого: какое правило пустило, команда не говорит.
Поэтому предупреждение раннера отсылает к `sbx policy ls`, а не пересказывает
несуществующее поле.

**`policy ls` — не картина мира.** Пока сеть закрыта, в списке виден
`default-deny-all`. Стоит завести хоть одно разрешающее сетевое правило — строка
`default-deny-all` из списка **пропадает**, хотя запрет по-прежнему действует:
`policy check` на постороннем хосте отвечает `deny_kind: implicit`. Отсюда и выбор
канарейки вместо разбора списка правил: авторизатор демона знает правду, а список —
только то, что в нём лежит.

Ответ «сеть открыта» прогон не отменяет: гарантию даёт машина, раннер о ней
рассказывает. Неудача самой проверки (например, `401` без `sbx login`) говорится
отдельной строкой и за закрытую сеть не выдаётся.

### Три проверки, ради которых всё затевалось

| Проверка | Результат |
|---|---|
| агент работает при закрытой сети | да, при открытом `api.anthropic.com`; без него — 403 и работа не начинается |
| запрос наружу падает и это видно | `curl https://github.com` → 403, запись в `sbx policy log` с правилом и причиной |
| роль с `network.allow` ставит пакет | `uv run --with pytest` без правила → «index URL returned a 403 Forbidden», с правилом → `Installed 5 packages`, `pytest 9.1.1` |

Сквозной прогон `scripts/smoke.sh hello` на бэкенде `sbx` при закрытой сети прошёл
целиком: $0.2316, 54 с, 17 шагов, все проверки сценария зелёные.

### Грабли

Первая песочница, у которой первый же `exec` пришёлся на закрытую сеть, застряла
намертво: `500 Internal Server Error: docker daemon failed to start inside the sandbox`
на любую команду, включая `echo`. Заблокированных запросов в логе при этом не прибавилось —
то есть дело не в правилах. Лечится пересозданием: свежая песочница при той же закрытой
сети поднимается и работает. Воспроизводить не пробовали.

**Синтаксис хостов** (справка `policy allow network`): точное имя `example.com`,
поддомены `*.example.com`, порт `example.com:443`, всё сразу `**`. Список — через запятую.
Раннер их не разбирает и не достраивает: отвергает только заведомо мёртвые записи —
со схемой, с путём, с пробелом внутри.

**Чем смотреть, что вышло:** `sbx policy log [SANDBOX]` показывает, какие хосты
разрешены и заблокированы, с правилом и числом запросов; `sbx policy check network`
отвечает на вопрос «пустило бы сюда» не запуская ничего.

## Открытые вопросы

- ~~Версия claude в образе задаётся sbx; как её закрепить — не разбирались.~~
  **Закрыто попутно, role-comet-native-workflow, задача 17.** Оператор печёт и
  закрепляет образ вручную, один раз на хосте (`office-claude-comet:<версия>`,
  скрипт `office/sbx-kits/bake-comet-template.sh`, см. раздел ниже) — раннер
  об этом не знает, он просто зовёт `sbx create --template`, ожидая, что образ уже
  есть. Версия `claude` внутри него теперь тоже зафиксирована моментом печи, не
  «что сегодня отдаёт `sbx`». Открытый вопрос был про `comet`, а решение закрыло
  и этот заодно.
- ~~Сеть в песочнице пока обычная.~~ **Разобрано, этап 3, шаг 6** — см. раздел выше.
  Роль называет домены в `network.allow`, раннер выдаёт их своей песочнице правилом
  `sbx policy allow network --sandbox`. Закрытое умолчание ставится на машине один раз
  (`sbx policy init deny-all`) и раннеру не подчиняется.
- ~~Раннер не проверяет, закрыта ли сеть на самом деле.~~ **Закрыто, этап 4, шаг 0.**
  Перед каждым прогоном раннер спрашивает `sbx policy check network example.com --json`
  в глобальном контексте и говорит вслух, если пустят. См. раздел «Проверка „а закрыта
  ли сеть“» выше.
- Заблокированная телеметрия (`http-intake.logs.us5.datadoghq.com`) прогону не мешает,
  но и не спрашивает разрешения. Разбираться, чья она — агента или самой песочницы, —
  не стали.
- ~~Коммиты внутри песочницы подписываются личностью владельца машины.~~ **Закрыто.**
  Адаптер задаёт `GIT_AUTHOR_*` и `GIT_COMMITTER_*` со значениями `agent-<роль>` и
  `<роль>@office.local`; переменные перебивают любой унаследованный `user.name`.
  Проверено живым прогоном в песочнице: коммит подписан `agent-implementer`.
- ~~Значение креда попадает в командную строку `sbx exec` и видно в `ps`.~~ **Закрыто**
  формой `--env ИМЯ` без значения: sbx берёт его из своего окружения, куда раннер
  кладёт кред явно. См. `docs/notes/auth.md`.
- Секретами sbx умеет управлять сам (`sbx secret`, подстановка через хостовый прокси,
  агент не видит сырого значения). Мы по-прежнему передаём кред переменной окружения —
  просто больше не через аргументы. Перейти на их механизм — кандидат на улучшение.

## `comet` CLI bootstrap (role-comet-native-workflow)

Investigated 2026-08-30 for `role-comet-native-workflow`: `analyst`/`implementer`/
`reviewer` need Node 22+ and the `@rpamis/comet` npm CLI available inside the role's
own sandbox at runtime (their mounted `comet`/`comet-native` skills are inert
markdown+script bundles without it), and the runner host itself separately needs
the same CLI for the deterministic archive step (`internal/pipeline/archive.go`).

**No local image-customization hook existed at the time of this note's first draft.**
`bootstrap/` had only the runner-service unit files and the unrelated Jira polygon
Dockerfile — nothing that built or extended the `sbx` image, and this repository had
never had a documented way to add a package to it. **Resolved, task 17: `sbx kit` is
that hook** — see below. The paragraph that used to sit here concluded this was "a
separate prerequisite change outside this repository's control"; that conclusion was
wrong, or at least incomplete — `sbx kit`, while marked experimental, is already
shipped in `sbx` v0.38.0 and does exactly this.

**Node is confirmed present**: `sbx exec node --version` → `v22.22.1` (satisfies
`comet`'s `>=22`), `npm --version` → `9.2.0`. `npm config get prefix` inside the
sandbox is `/usr/local/share/npm-global` (not the usual `/usr/local`), owned by
`agent:agent` in the clean base image, with no `bin/` yet — npm creates it
agent-owned on first `install -g`. A root-run install can still write there too
(root bypasses Unix permission checks regardless of ownership) and the files it
creates are individually readable/executable by `agent` at runtime without any
extra `chmod` — but that observation is a trap, not a green light: root creates
`lib/`/`bin/` themselves root-*owned*, which is the actual problem the kit hit and
the point 2 fix below is about (directory ownership riding into the snapshot, not
file permissions on what's already inside it). `/usr/local/share/npm-global/bin` is
already on `PATH` for both `root` and `agent` — `claude` itself resolves via a
different mechanism (`/home/agent/.local/bin/claude`, a symlink into
`.local/share/claude/versions/…`), so the two don't collide.

### Resolved: `sbx kit` bakes `comet` (and `openspec`) into a pinned template

Recipe, validated live end-to-end (`sbx` v0.38.0) — implemented as
`office/sbx-kits/comet-cli/` + `office/sbx-kits/bake-comet-template.sh`
(`office/sbx-kits/README.md` has the operational how-to; this note keeps the why):

1. `comet` is pure JS (`npm view @rpamis/comet@0.4.0-beta.18 os cpu` — empty; its own
   dependencies are all pure-JS packages too) — a tarball built on macOS runs fine
   under the sandbox's Linux/aarch64 Node. Same for `@fission-ai/openspec`.
2. `sbx create --kit DIR` composes a declarative "mixin" kit (`spec.yaml` +
   `files/`) into the sandbox at creation time, before the agent starts.
   `commands.install` entries run synchronously, before startup commands, before
   the agent is reachable. Default `user` is `"0"` (root) — **but our own kit
   overrides it to `"agent"` explicitly, and that override matters, not just style**:
   a clean `docker/sandbox-templates:claude-code-docker` image has
   `/usr/local/share/npm-global`'s `lib` owned by `agent:agent` with no `bin/` yet
   (npm creates it agent-owned on first `install -g`); installing as root instead
   creates both **root-owned**, and that ownership rides into the `sbx template
   save` snapshot verbatim. Every sandbox created from that snapshot afterward
   would have an `agent` user unable to `npm install -g` anything else there
   (`EACCES`) — confirmed live both ways (root install → later agent install
   fails; agent install throughout → later agent install succeeds, ~50 ms). Root
   buys nothing here anyway: `agent` is already in the `sudo` group inside this
   sandbox, so it isn't a privilege boundary. `sbx kit validate DIR` checks the
   spec without creating anything.
3. **A local-tarball `npm install -g <path>.tgz` still hits the network** — the
   tarball alone doesn't carry transitive dependencies, so npm still resolves
   `comet`'s (and `openspec`'s) own deps from `registry.npmjs.org`. Under this
   machine's `deny-all` base policy that's a `403 Forbidden`, exactly like the
   `pypi.org` case documented above for `uv`. The kit's own
   `network.allowedDomains: ["registry.npmjs.org"]` opens it — the same mechanism
   the built-in `claude` kit itself relies on to `curl` its own installer under a
   deny-all base policy (see "Кит агента не открывает его собственный API" above,
   which was about the *running agent's* own API traffic — a different traffic
   source, not a different time window). **This is not a separate, earlier window
   that closes once install finishes** — checked directly: `sbx policy ls
   <sandbox> --wide` after a `--kit`-created sandbox shows `registry.npmjs.org`
   merged into the *same* per-sandbox allow rule as `claude.com`/
   `downloads.claude.ai`/`mcp-proxy.anthropic.com`, and it stays there for the
   sandbox's whole life — the same "правила кита" level the network-policy table
   above already names. It's a non-issue in practice only because production
   never calls `sbx create --kit` directly — `internal/backends/sbx.Template`
   points at a *baked snapshot* instead, and a sandbox created from that snapshot
   carries no kit network rule at all (checked: `registry.npmjs.org` denied
   there). Anyone adding a domain to this `spec.yaml` for a future package should
   assume it stays open for the sandbox's whole life if that kit is ever applied
   via `--kit` directly rather than through the baked template. `sbx kit validate`
   warns this field name is deprecated in favor of `caps.network.allow` (kit-spec
   v2) but still honors it under `schemaVersion: "1"`.
4. With `--kit`, the two `npm install -g` steps add ~24 s + ~4 s to `sbx create`
   (measured), against the usual 5–6 s — real overhead, and *avoidable*:
5. `sbx template save <sandbox> <tag>` (after `sbx stop <sandbox>` — it refuses to
   snapshot a running container, and in a non-interactive shell there's no TTY to
   answer its confirmation prompt) snapshots the container as a reusable local
   image. `sbx create --template <tag>` then creates from it with **no** kit-apply
   step at all — back to 5–6 s, `comet`/`openspec` already present. The template
   lives only in this host's local Docker/`sbx` image store; it does not travel
   with the git repo. Each runner host bakes its own copy once
   (`office/sbx-kits/bake-comet-template.sh`), the same one-time-per-host
   category `sbx policy init deny-all` already is.
6. **`comet native` does not need the other 9 skills the npm package ships**
   (`comet-any`, `comet-archive`, `comet-build`, `comet-classic`, `comet-design`,
   `comet-hotfix`, `comet-open`, `comet-tweak`, `comet-verify`) **or `openspec`** —
   checked two ways, not assumed: `skills/comet-native/SKILL.md` and its bundled
   runtime (`comet-native-runtime.mjs`/`comet-native-doctor.mjs`) contain zero
   references to any sibling skill name or to OpenSpec (the one string match,
   `comet-archive-cas-…`, is Native's own internal backup-file naming, unrelated to
   the `comet-archive` *skill*); and a live `comet native new`/`comet native status`
   run in a freshly baked sandbox succeeded cleanly (exit 0), with the JSON
   response's own `continuation.skill` field naming only `"comet-native"`. `comet`
   is a pure CLI/state-machine tool here — running `comet native …` from a shell
   doesn't consult any project-mounted skill directory at all; skills are Claude
   Code instructions *for the agent*, orthogonal to what the CLI binary itself
   does when invoked directly. `openspec` is vendored into the same kit anyway,
   at the user's explicit request, purely as insurance for a possible future
   switch of this role pipeline to Comet Classic — not because Native needs it.
7. Both CLIs are ordinary top-level global npm installs — `@fission-ai/openspec`
   being a *dependency* of `@rpamis/comet` does not put its `bin/openspec.js` on
   `PATH` automatically; a global install exposes only a package's own declared
   `bin`, not a nested dependency's. It has to be installed as its own top-level
   package to get `openspec` on `PATH`, which is exactly what the kit's second
   `commands.install` entry does.
