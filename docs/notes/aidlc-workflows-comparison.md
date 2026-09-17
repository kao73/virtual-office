# Сравнение с awslabs/aidlc-workflows

Записка по прямой просьбе владельца — не относится ни к одному этапу и не входит
в план ни одного из них. Сравнивает архитектуру этого репозитория (пять
сделанных этапов, `docs/DESIGN.md`, `docs/STAGE-1…5-*.md`, ретро в
`docs/notes/stage-*-retro.md`, контракты в `docs/contracts/`, `roles/*/role.md`)
с открытым продуктом AWS Labs `aidlc-workflows` (https://github.com/awslabs/aidlc-workflows,
ветка `main`, версия движка на момент чтения — 2.9.0).

Внешняя сторона читана не по памяти и не по вторичным пересказам: полное дерево
репозитория (1448 файлов, без усечения) через `api.github.com/repos/awslabs/aidlc-workflows/git/trees/main`,
содержимое конкретных файлов — через `raw.githubusercontent.com`, плюс код-поиск
по репозиторию (`gh api search/code`) на отсутствие/наличие терминов (`jira`,
`tracker`, `pull request`, `worktree`, `sandbox`, `budget` и т.д.), чтобы не
полагаться на то, что нужный факт вообще попался на глаза при чтении файлов
по одному. Сеть при этом ни разу не отказала — все ссылки в разделе
«Источники» открывались напрямую, без обходных путей и без пробелов.

## Резюме

Направление совпадает в одной вещи, и она не мелкая: оба проекта разводят
**детерминированный код, решающий, что делать дальше** (наш `runner` по
`workflow.yaml`, DESIGN §2.1 — их «Engine», `aidlc-orchestrate.ts`), и
**LLM-агента, который делает один шаг и не решает за оркестратор** (наш
`run-agent`/роль — их «Conductor», тонкий `SKILL.md`-цикл, дозванивающийся
до движка после каждого хода). Это архитектурное решение мы принимали
независимо, и то, что широко используемый чужой проект дошёл до того же
разделения, — не подтверждение, что оно единственно верное, но и не совпадение
на пустом месте.

На этом содержательное сходство заканчивается, а не начинается. `aidlc-workflows` —
методология всего жизненного цикла разработки: пять фаз, тридцать три стадии,
от нащупывания идеи (Ideation) и архитектуры (Inception) до эксплуатации и
обратной связи в продакшене (Operation), с четырнадцатью ролями-агентами и
семью харнессами (Claude Code, Kiro CLI, Kiro IDE, Codex CLI, Cursor,
opencode, GitHub Copilot). У неё нет ни одной строки про внешний трекер задач — вся работа
живёт файлами в git-репозитории самого проекта, — и нет собственного слоя
песочницы или сетевого allowlist: она полагается целиком на то, что даёт
харнесс-хозяин. Обе эти вещи — трекер как средство координации агентов и
песочница с allowlist как граница безопасности — не факультативные детали
нашего замысла, а два из трёх пунктов, которыми `docs/DESIGN.md` §1 описывает
самую суть системы («задачи живут в трекере», «сеть роли — `network.allow`»).
Там, где `aidlc-workflows` — широкая и зрелая (по охвату инструментов, а не
обязательно по объёму боевой эксплуатации) методология для одного разработчика
или команды, ведущей *свой* проект напрямую внутри его же репозитория, наш
офис — узкий и глубоко эмпирически проверенный конвейер, ведущий *чужой*
проект-клиент через его *собственный* трекер, с собственной сетевой
изоляцией на каждый прогон. Это соседи по нише («многоэтапный конвейер AI-ролей
с явным графом» — формулировка `docs/DESIGN.md` §5), но не альтернативные
реализации одного и того же продукта: `aidlc-workflows` не пытается решить
задачу, которую решает этот репозиторий, а мы не пытаемся закрыть весь SDLC,
который покрывает он.

## Сравнение по измерениям

| # | Измерение | Вердикт |
|---|---|---|
| 1 | Архитектура ролей/фаз | частично совпадает |
| 2 | Взаимодействие с трекером задач | нет аналога (у них) |
| 3 | Участие человека | частично совпадает |
| 4 | Артефакты и их формат | совпадает |
| 5 | Разбиение крупных задач | частично совпадает |
| 6 | Review-цикл | частично совпадает |
| 7 | Безопасность/песочница | нет аналога (у них) |
| 8 | Конфигурация/деплой | расходится |
| 9 | PR-проход | расходится |
| 10 | Бюджеты/лимиты расхода | частично совпадает |
| 11 | Зрелость/масштаб | расходится |

### 1. Архитектура ролей/фаз — частично совпадает

У нас три роли на явном графе статусов трекера: `analyst` → `implementer` →
`reviewer`, движение задаёт `workflow.yaml`, а не промпт (`docs/DESIGN.md` §2.1,
`README.md`, раздел «Три роли и путь задачи»). Роль не знает о графе вовсе —
она получает готовую задачу и возвращает `result.json` с исходом, а куда он
ведёт, решает раннер.

У `aidlc-workflows` то же самое разделение, но на другом масштабе: движок
(`core/tools/aidlc-orchestrate.ts`) решает, какая из 33 стадий следующая,
а «Conductor» — сессия `/aidlc` в конкретном харнессе — исполняет один шаг
и снова спрашивает движок
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/00-introduction.md,
раздел «How the Orchestrator Works»;
https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/03-orchestrator.md).
Стадии сгруппированы в пять фаз — Initialization, Ideation, Inception,
Construction, Operation
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/04-phases-and-stages.md) —
и закрывают весь жизненный цикл: от нащупывания идеи и рыночного
ресёрча до эксплуатации и метрик SLO. Ролей четырнадцать: 11 «доменных»
агентов (продукт, дизайн, доставка, архитектура, AWS-платформа, комплаенс,
devsecops, разработка, качество, pipeline-deploy, эксплуатация) + 2
reviewer-агента + отдельный Composer-агент
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/06-agents.md,
разделы «The 11 Domain Agents», «Reviewer Agents», «The Composer Agent»),
а не три.

Принцип «код распоряжается переходами, агент — нет» общий. Охват — нет: наш
граф покрывает один тикет от постановки до слитой ветки, их — весь SDLC,
включая архитектуру, deployment pipeline и обратную связь из продакшена,
которых у нас нет вовсе и не планировалось (`docs/DESIGN.md` §3, план MVP
ограничен одним репо и одним трекером).

Отдельно стоит развести термины: `docs/STAGE-4-analyst-questions.md`, раздел
«Ориентир на будущее», и `docs/DESIGN.md` §4 говорят про фазы **Comet Native**
(`open/design → build → verify → archive`) — это отдельный, не связанный
с AWS вендоренный скилл (`skills/comet-native/`), которым наши роли пользуются
внутри своего собственного прогона (`roles/analyst/role.md`, раздел «Comet
Native: фаза Shape»). Совпадение слова «фаза» и общей идеи «Shape → Build →
Verify» с AI-DLC — случайное: это два независимых проекта c похожей, но не
единой родословной, и в этой записке они не смешиваются нигде, кроме этой
оговорки.

### 2. Взаимодействие с трекером задач — нет аналога (у AI-DLC)

Наш контракт `docs/contracts/tracker-protocol.md` — половина всего объёма
проекта: интерфейс `Tracker` с `Claim`/`Renew`/`Release`/`Transition`/`Comment`,
две полные реализации (`internal/tracker/mock`, `internal/tracker/jira`),
лизы, протокол маркеров, `depends_on` как `issueLinkType` в настоящей JIRA.

У `aidlc-workflows` внешнего трекера нет вовсе. Код-поиск по всему
репозиторию на «jira» дал ноль совпадений; «tracker» встречается четыре
раза, и все — про баг-трекер самого репозитория `aidlc-workflows` на GitHub,
не про трекер проекта-клиента. Единица работы называется «Intent» — запись
в локальном `intents.json` плюс каталог артефактов в самом git-репозитории
проекта (`aidlc/spaces/<space>/intents/<YYMMDD>-<label>/`); никакой
синхронизации с Jira/GitHub Issues не описано нигде
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/glossary.md,
запись «Intent»;
https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/14-artifacts-reference.md).
Единственное появление «pull request»/«GitHub issue» в терминах разработки —
это контрибьютинг в сам `aidlc-workflows`
(https://github.com/awslabs/aidlc-workflows/blob/main/CONTRIBUTING.md), не
фича для пользовательских проектов.

Ближайший аналог их процесса в нашем репозитории — файловый `mock`, а не
`jira`. Разница принципиальная: `mock` у нас — вторая, равноправная
реализация одного интерфейса рядом с настоящей JIRA (`docs/contracts/tracker-protocol.md`,
преамбула: «Реализаций две… всё, что здесь описано, у них обязано совпадать
до мелочей»); у AI-DLC файловый процесс — единственный, интеграции с внешней
системой учёта нет и, судя по документации, не планируется. Наша заявка на
нишу («независимость от трекера», `docs/DESIGN.md` §5) означает абстракцию
*над* трекером, которую можно подключить к любому; у AI-DLC трекера просто
нет как понятия.

### 3. Участие человека — частично совпадает

У нас человек участвует в фиксированных точках: типизированный вопрос
(`Q1: b`, `docs/contracts/agent-io.md`, раздел «Вопросы человеку»), двойное
подтверждение разбиения задачи (`roles/analyst/role.md`, «Comet Native: фаза
Shape»), слияние pull request (по умолчанию — человек, `docs/DESIGN.md` §2.8).
Вне этих точек конвейер идёт без него.

У AI-DLC участие человека гораздо чаще: обязательный «approval gate» **после
каждой стадии**, кроме трёх стадий Initialization
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/07-interaction-modes.md).
Форма схожа с нашей — файл `{stage}-questions.md` с вариантами `A`–`E` и
обязательным `X. Other (please specify)`, ответ размечен `[Answer]:`
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/14-artifacts-reference.md,
раздел «Question Files») — это тот же принцип, что наш `questions[]` с `id`
и `options`. Есть и прямой аналог «спорную постановку — в типизированный
вопрос»: механика «Assumption Confirmation» (`A. Accept assumptions` /
`B. Convert to follow-up questions`,
https://github.com/awslabs/aidlc-workflows/blob/main/core/memory/org.md,
строка 116) конвертирует найденное предположение в такой же вопрос.
У них есть и то, чего нет у нас, — «3-strike escape hatch»: после трёх
подряд циклов правок на approval gate появляется третий вариант
«Accept as-is», отдельно от «Approve»/«Request Changes» (там же,
`07-interaction-modes.md`).

Форма («буква + текст + `Other`», «предположение → вопрос») совпадает с нашей
почти дословно. Частота — нет: approval gate на каждой из ~30 стадий
плотнее любого разбора, который делает `reviewer`, а наш замысел с самого
начала обратный — «человек участвует в фиксированных точках, а не в каждом
шаге» (`README.md`, первая строка). Это не недоработка ни с одной стороны:
AI-DLC ведёт разработку *своего* проекта, где плотный контроль естественен;
мы ведём *чужой* проект через *его* трекер, где плотность контроля — то,
от чего явно отказывались ради экономии времени человека.

### 4. Артефакты и их формат — совпадает

У нас — `docs/changes/<KEY>/{brief,design,tasks.md}` (или, после перехода на
Comet Native, `docs/comet/changes/<name>/`) в ветке задачи, а не текст
в отчёте: «следующая роль видит ветку, а не чужую рабочую папку»
(`docs/DESIGN.md` §2.4, `README.md`, раздел «Каталог изменения»).

У AI-DLC — тот же принцип на большем числе файлов: markdown-артефакты
коммитятся в тот же git-репозиторий проекта под
`aidlc/spaces/<space>/intents/<YYMMDD>-<label>/`, со строгой структурой
по фазам и стадиям, явным разделением «что коммитить / что в `.gitignore`»
(служебное состояние движка — `.aidlc-engine/`, `runtime-graph.json`) и
отдельным журналом аудита по клонам
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/14-artifacts-reference.md).

Общее — не деталь, а решение верхнего уровня в обоих проектах: состояние
замысла и хода работы живёт в git как обычные файлы, а не в памяти агента
или в переписке чат-инструмента. Разница — только в масштабе таксономии:
у нас три файла на один тикет, у них — дерево на пять фаз и тридцать три
стадии, потому что и объём работы, который они описывают, шире на порядок.

### 5. Разбиение крупных задач — частично совпадает

У нас `analyst` эмитит исход `split` со структурированными детьми
(`id`/`title`/`description`/`depends_on`), человек подтверждает дважды подряд,
дальше раннер сам заводит и связывает тикеты в трекере
(`docs/contracts/agent-io.md`, раздел «Предложение разбивки»;
`docs/notes/analyst-task-splitting.md`), а `claim()` не пускает `implementer`
и `analyst` на зависимую задачу, пока предшественник не смёржен
(`docs/DESIGN.md` §4, пункт про блокеры; `docs/notes/analyst-task-splitting.md`,
раздел «Change 2»). Каждый ребёнок — независимый тикет со своей арендой,
бюджетом и жизненным циклом, который может достаться другому раннер-хосту
в другое время.

У AI-DLC для параллельной работы над зависимыми частями одного intent есть
DAG «Units» (`unit-of-work-dependency.md`) и параллельные «Bolt»-батчи в
Construction: независимые Units запускаются одновременно, каждый — в своём
`git worktree` на ветке `bolt-<slug>`
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/04-phases-and-stages.md,
раздел «Parallel Unit batches»;
https://github.com/awslabs/aidlc-workflows/blob/main/core/knowledge/aidlc-shared/worktree-info-schema.md).
Отдельно есть «Composer agent», решающий по каждой стадии EXECUTE/SKIP —
это управление глубиной прохождения одной и той же задачи по эвристике
«implementation entropy», а не разбиение на независимые единицы работы с
собственным жизненным циклом
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/06-agents.md,
раздел «The Composer Agent»).

Идея «DAG зависимостей + параллельный git worktree на узел» — общая. Разница
там же, где и в пункте 2: их Units существуют только внутри одного intent
и одной сессии оркестратора — координация идёт кодом самого движка в
процессе одного прогона; наши дети разбиения — полноценные тикеты внешнего
трекера с собственной арендой и бюджетом, которые может забрать другая
машина в другое время. Наш гейт зависимостей физически проверяется через
трекер (`docs/notes/analyst-task-splitting.md`, «Change 2: гейт очерёдности»);
их DAG не пересекает границу процесса вовсе.

### 6. Review-цикл — частично совпадает

У нас `reviewer` — один агент: точечные замечания (типографика, локальная
явная ошибка) чинит и коммитит сам, всё остальное возвращает автору без
правки на месте (`roles/reviewer/role.md`, раздел «Точечный фикс вместо
возврата»). Круги считаются по паре (роль, next_owner) с пределом
`max_return_rounds` (`docs/contracts/tracker-protocol.md`, раздел «Круги
возврата»).

У AI-DLC ревью строже разведено: два выделенных ревьюер-агента
(`aidlc-product-lead-agent`, `aidlc-architecture-reviewer-agent`), которые
**никогда не создают и не правят артефакты** — только пишут вердикт
READY/NOT-READY в отдельный файл ревью, не видя чужого плана исполнителя
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/06-agents.md,
раздел «Reviewer Agents»). Часть стадий («adversarial», в основном
Construction) гоняет цикл исполнитель→ревьюер до `reviewer_max_iterations`
(по умолчанию 2); часть («advisory», Ideation/Inception) — ревью один раз,
находки просто показываются человеку на approval gate, без автоматического
цикла (там же). У ревьюера свой бюджет ходов — `maxTurns: 60` — с явным
уходом в NOT-READY при исчерпании.

Общее — не доверять формулировке «готово» на слово и отдельная точка
принятия решения с ограниченным числом кругов; это у нас звучит явно
(`roles/reviewer/role.md`: «Отчёту не верь на слово: сказанное «сделано» и
лежащее в ветке — разные вещи»). Разница — в границе полномочий: наш
`reviewer` целенаправленно смешивает роли «читатель» и «мелкий исполнитель»
ради экономии круга на тривиальных находках; у AI-DLC ревьюер — чистый
читатель без единого byte записи в артефакт. Оба решения обоснованы, но
разные: у нас цена лишнего круга посчитана и признана заметной
(`docs/notes/stage-3-retro.md`: «разбор дороже работы»), поэтому точечный
фикс на месте оправдан; AI-DLC явно жертвует этой экономией ради более
чистого разделения ответственности.

### 7. Безопасность/песочница — нет аналога (у AI-DLC)

У нас — целый контракт, `docs/contracts/role-sandbox-permissions.md`:
четырёхслойное объединение `network.allow`/`tools.allow`/`tools.deny`
(repo-wide → проект → машина → роль), исполнение в microVM (`sbx`) с
deny-by-default сетью, канареечная проверка при старте, что базовая
политика машины действительно закрыта (`docs/DESIGN.md` §2.6).

Целенаправленный поиск по `aidlc-workflows` на «sandbox»/«network
allowlist»/«permission» аналога не нашёл. Единственные близкие по словам
находки — это (а) сетевые флаги (`--offline`, `--ca-bundle`) собственного
CLI-установщика `aidlc`, не имеющие отношения к запуску ролей
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/18-install-and-lifecycle.md);
(б) сеть в «workshop mode» ограничена git-операциями с удалённым
репозиторием (claim/publish/status), а не общим allowlist для агента
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/workshop-mode.md);
(в) права инструментов агента на Claude Code — стандартный
`disallowedTools`/`tools:`-allowlist самого харнесса, не собственный слой
AI-DLC (https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/06-agents.md,
раздел «Agent Tool Access»); (г) упоминание встроенной песочницы Codex CLI
(`workspace-write`, `.git` read-only) как ограничения, с которым AI-DLC
вынужден считаться, а не которое сам определяет
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/harnesses/codex-cli.md).

Вывод по этому измерению однозначный: у AI-DLC нет собственного слоя
изоляции выполнения или сетевого allowlist — весь вопрос безопасности
делегирован нативным правам харнесса-хозяина. У нас это — раздел §2.6
`docs/DESIGN.md`, отдельный контракт и отдельный этап проверок
(`docs/notes/sbx.md`, `docs/notes/followup-network-and-permissions.md`).
Различие тем более заметно, что AI-DLC оперирует MCP-серверами AWS
(`aws-mcp`, `aws-pricing`, `aws-iac`, `aws-serverless`,
https://github.com/awslabs/aidlc-workflows/blob/main/harness/claude/.mcp.json) —
то есть агент там штатно имеет доступ к облачным ресурсам без описанной
собственной сетевой границы поверх этого доступа.

### 8. Конфигурация/деплой — расходится

У нас — явная граница «репозиторий описывает офис, `${OFFICE_HOME}` —
инстанс» (`docs/DESIGN.md` §2.5, `docs/notes/stage-5-config.md`): один офис
обслуживает несколько *чужих* проектов-клиентов, каждый описан половиной
в `projects.yaml` (репо) и половиной в `projects.local.yaml`/`tracker.yaml`
(машина).

У AI-DLC подобного разделения «инструмент — отдельно, инстанс — отдельно»
в прочитанных источниках не нашлось: инструмент устанавливается как
нативный бинарник с собственным жизненным циклом (`aidlc doctor`, версии,
https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/18-install-and-lifecycle.md),
а состояние и конфигурация конкретной методологии (`aidlc/spaces/…`) живут
**внутри репозитория того самого проекта**, который методология ведёт —
то есть AI-DLC работает на *своём* проекте, встраиваясь в его репозиторий,
а не управляет парком *чужих* проектов из отдельного «офиса», как у нас.

Это архитектурно другая модель эксплуатации, а не то же решение в других
терминах: у нас офис — отдельный артефакт, который можно направить на
любой проект-клиент, не трогая его репозиторий (кроме ветки задачи); у
AI-DLC методология и её состояние — часть репозитория того проекта, который
она обслуживает.

### 9. PR-проход — расходится

У нас — отдельный, безролевой системный проход (`docs/DESIGN.md` §2.8):
`forge.OpenPR`/`PRState`/`Merge`, конфликт слияния считается локально
(`git merge-tree`) до и после открытия PR, опциональное авто-слияние по
явному `auto_merge.enabled`, иначе сливает человек.

У AI-DLC для собственного проекта пользователя автоматизации pull request
не найдено: слияние делает инструмент `aidlc-worktree merge
--strategy squash|merge` напрямую в целевую ветку, без обращения к GitHub
API — код-поиск по `core/tools/aidlc-worktree.ts` и `aidlc-bolt.ts` не дал
совпадений на «pull request»/«gh pr»/«octokit». Дефолтная стратегия — trunk-based
со squash-merge в `main`
(https://github.com/awslabs/aidlc-workflows/blob/main/core/knowledge/aidlc-pipeline-deploy-agent/branching-strategies.md;
https://github.com/awslabs/aidlc-workflows/blob/main/core/memory/org.md).
«GitHub Flow» упомянут лишь как одна из пяти опций стратегии веток, где
pull request остаётся делом человека вне инструмента. PR-автоматизация в
репозитории всё же есть, но обслуживает только сам `aidlc-workflows`
(бот-ревью входящих PR в open source проект,
https://github.com/awslabs/aidlc-workflows/blob/main/.github/workflows/ai-pr-review.yml) —
не то, что предоставляется пользователю методологии.

Расхождение прямое: у нас PR — отдельный, спроектированный проход графа
с гейтом слияемости и записанным маршрутом (`workflow.yaml`, блок `pr`);
у AI-DLC для целевого проекта пользователя PR как шаг конвейера отсутствует
вовсе, слияние — локальный git-merge инструментом.

### 10. Бюджеты/лимиты расхода — частично совпадает

У нас — развитая система: реестр `${OFFICE_HOME}/ledger.jsonl` на каждый
прогон, три предела (`per_task`/`per_role_daily`/`per_run`) с режимом
`warn`/`stop`, `limits.max_turns` на роль, отдельные счётчики серий
(`max_lease_expiries`, `max_push_failures`, `max_idle_runs`,
`max_return_rounds`, `max_pr_returns`, `max_merge_refusals`,
`max_merge_pending_sec`) — `docs/contracts/tracker-protocol.md`,
`docs/DESIGN.md` §2.7.

У AI-DLC найдены только локальные пределы одного механизма — ревью:
`maxTurns: 60` на диспетч ревьюера,
`reviewer_max_iterations` по умолчанию 2, «3-strike» эскейп-хэтч на
approval gate (см. пункт 3)
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/06-agents.md).
Показательнее другое: их собственный скилл подсчёта стоимости сессии
(`aidlc-session-cost`) **намеренно не считает токены и деньги** — документация
прямо называет это «guesswork dressed as data» и ограничивается длительностью,
числом стадий/сенсоров/записей памяти
(https://github.com/awslabs/aidlc-workflows/blob/main/core/skills/aidlc-session-cost/SKILL.md,
строки 14–28, 124–127). Обобщённого предела «стоимость на задачу» или
«стоимость роли в сутки», аналогичного нашим `per_task`/`per_role_daily`,
не найдено.

Частичное совпадение — только в самой идее «ограничить число ходов/итераций
одного механизма» (наш `max_turns` роли ↔ их `maxTurns` ревьюера). В остальном
это осознанно разные позиции, а не недосмотр одной из сторон: AI-DLC явно
заявляет отказ считать деньги как решение («guesswork»), а у нас учёт расхода —
отдельный раздел архитектуры с прицелом именно на подписочные окна и
многомашинный парк (`docs/DESIGN.md` §2.7, `docs/notes/budgets.md`).

### 11. Зрелость/масштаб — расходится

Наш проект: пять этапов примерно за месяц, единственный Go-раннер, узкий
охват (один тикет: постановка → план → код → разбор → PR), проверен живьём
дважды на одном небольшом реальном проекте с неполным прохождением бэклога
(`docs/notes/stage-5-retro.md`, «Закрытие этапа») и с явно перечисленными
открытыми долгами на каждом шаге.

`aidlc-workflows`: лицензия MIT-0
(https://github.com/awslabs/aidlc-workflows/blob/main/LICENSE), версия
движка 2.9.0, 1448 файлов в дереве репозитория, семь харнессов, механизм
плагинов, режим для нескольких команд («workshop mode»), публичный roadmap
с открытыми issue
(https://github.com/awslabs/aidlc-workflows/blob/main/docs/roadmap.md),
отдельная PDF-спецификация методологии, заявленная как источник для этой
реализации
(https://github.com/awslabs/aidlc-workflows/blob/main/README.md, строка 109).
Единственная оговорка о незрелости — стандартный дисклеймер AWS про
генеративный ИИ, не пометка «experimental»/«beta».

К вопросу о позиционировании («референсная имплементация методологии» или
«готовый продукт»): README сам разводит методологию и её реализацию.
Раздел «References» ссылается на `AWS AI-DLC blog post`
(https://aws.amazon.com/blogs/devops/ai-driven-development-life-cycle/) и на
отдельный `AI-DLC Method Definition Paper`, живущий на своём собственном
хостинге, а не в этом репозитории
(https://github.com/awslabs/aidlc-workflows/blob/main/README.md, раздел
«References»). Методология старше и шире, чем `aidlc-workflows`: у AWS есть
более ранний, теперь заархивированный репозиторий `aws-samples/sample-aidlc-workflows`
(archived: true по `https://api.github.com/repos/aws-samples/sample-aidlc-workflows`),
построенный вокруг правил Amazon Q Developer, — то есть `awslabs/aidlc-workflows`
сам себя подаёт как более позднюю, харнесс-нейтральную инженерную реализацию
внешней, отдельно опубликованной методологии, а не как её единственное или
изначальное воплощение. Ответ на вопрос из задания — «и то, и другое»:
методология описана снаружи (блог, paper, PDF-спецификация), а сам
репозиторий — не тонкий набор промптов поверх неё, а полноценный
инженерный продукт (TypeScript-инструментарий, хуки, версионирование,
CI на сам проект).

Разница — не «у кого код лучше», а в порядке величины охватываемой
методологии и в природе доказательства: у AI-DLC зрелость — это широта
инструментальной обвязки (харнессы, плагины, документация, тесты),
у нас — глубина эмпирической проверки узкого участка (`docs/notes/stage-*-retro.md`
фиксируют десятки живых прогонов с точной ценой и находками вида «механизм
оказался не тем, чем выглядел»). Прямых данных о том, насколько
`aidlc-workflows` проверен на реальных боевых проектах, в прочитанных
источниках нет — только охват инструментария, а не глубина применения.

## Что стоит перенять или иметь в виду

- **«3-strike» эскейп-хэтч на approval gate**
  (https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/07-interaction-modes.md)
  — после N кругов правок дать человеку явный третий вариант «принять как
  есть», а не только «одобрить»/«вернуть». У нас `max_return_rounds` молча
  эскалирует к человеку записью `event:return-rounds-exhausted`
  (`docs/contracts/tracker-protocol.md`, «Круги возврата») — работающее
  решение, но без явного «прими как есть» в самом вопросе. Не обязательно
  что-то менять — стоит иметь в виду при следующей правке протокола
  вопросов человеку.
- **Разведение «advisory» и «adversarial» стадий ревью** (см. пункт 6) —
  прямое подтверждение того, что уже стоит в нашем собственном отложенном
  списке: «развилки графа под пресеты (hotfix, tweak)», названные ещё в
  `docs/notes/stage-4-retro.md` («Что нужно этапу 5») и оставшиеся
  нетронутыми к концу этапа 5 (`docs/notes/stage-5-retro.md`, таблица
  «Закрытие этапа»). AI-DLC показывает работающий пример того же деления
  на другом проекте — не довод сделать это немедленно, но довод не
  списывать идею как излишнюю сложность.
- **Явный отказ AI-DLC считать токены/деньги** («guesswork dressed as
  data», см. пункт 10) стоит держать в уме не как рецепт, а как контрточку:
  у нас учёт расхода вырос из конкретной боли (`docs/notes/budgets.md`,
  окна подписки, многомашинный парк) — то, что зрелый чужой проект решил
  не мерить эту величину вовсе, не отменяет нашей причины её мерить, но
  стоит перепроверять, не тянем ли мы точность учёта туда, где она уже
  не окупается.
- **Совпадение выбора «Engine vs Conductor» / «Runner vs агент»**
  (пункт 1) — не рецепт, а независимое подтверждение того, что разделение
  «код решает переходы, LLM решает содержание одного шага» — не
  идиосинкразия этого репозитория, а решение, до которого доходят
  независимо. Полезно как аргумент при обсуждении архитектуры с кем-то извне.

## Чего не стоит перенимать / принципиальные расхождения, которые стоит просто держать в уме

- Отсутствие у AI-DLC внешнего трекера и собственного слоя песочницы — не
  недоработка, а следствие другой ниши (методология для *своего* проекта,
  не конвейер для *чужого*). Копировать эту простоту значило бы отказаться
  от двух вещей, ради которых этот репозиторий вообще существует
  (`docs/DESIGN.md` §1: «задачи живут в трекере», «сеть роли —
  `network.allow`»).
- Модель «инструмент встраивается в репозиторий обслуживаемого проекта»
  (пункт 8) прямо противоречит нашей границе «офис отдельно, проекты-клиенты
  отдельно» (`docs/DESIGN.md` §2.5) — она стоит того, чтобы её понимать, но
  не того, чтобы сближать с ней архитектуру офиса: смысл офиса именно в том,
  что он не живёт внутри проекта-клиента.
- `docs/DESIGN.md` §5 («Существующие решения») перечисляет соседей по нише,
  но не содержит `aidlc-workflows` — стоит рассмотреть добавление отдельной
  строкой при следующей правке этого раздела: из перечисленных там аналогов
  он ближе всех по идее ролей-агентов на графе фаз и дальше всех по охвату
  методологии.

## Источники

### Локальные (этот репозиторий)

- `docs/DESIGN.md` — §1, §2.1, §2.4–§2.8, §3, §4, §5
- `README.md`
- `CLAUDE.md`
- `docs/STAGE-1-agent-runtime.md`
- `docs/STAGE-2-runner-tracker.md`
- `docs/STAGE-3-reviewer-budgets.md`
- `docs/STAGE-4-analyst-questions.md`
- `docs/STAGE-5-first-live.md`
- `docs/notes/stage-1-retro.md`
- `docs/notes/stage-2-retro.md`
- `docs/notes/stage-3-retro.md`
- `docs/notes/stage-4-retro.md`
- `docs/notes/stage-5-retro.md`
- `docs/notes/analyst-task-splitting.md`
- `docs/notes/stage-5-config.md`
- `docs/notes/budgets.md`
- `docs/notes/sbx.md`
- `docs/notes/followup-network-and-permissions.md`
- `docs/contracts/agent-io.md`
- `docs/contracts/tracker-protocol.md`
- `docs/contracts/role-sandbox-permissions.md`
- `roles/analyst/role.md`, `roles/analyst/role.yaml`
- `roles/implementer/role.md`, `roles/implementer/role.yaml`
- `roles/reviewer/role.md`

### Внешние (awslabs/aidlc-workflows, ветка `main`)

- https://github.com/awslabs/aidlc-workflows/blob/main/README.md
- https://github.com/awslabs/aidlc-workflows/blob/main/LICENSE
- https://github.com/awslabs/aidlc-workflows/blob/main/AGENTS.md
- https://github.com/awslabs/aidlc-workflows/blob/main/CONTRIBUTING.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/roadmap.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/00-introduction.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/03-spaces-and-intents.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/04-phases-and-stages.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/05-scopes-and-depth.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/06-agents.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/07-interaction-modes.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/09-rules-and-the-learning-loop.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/10-state-and-audit.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/14-artifacts-reference.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/18-install-and-lifecycle.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/glossary.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/workflow-profiles.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/workshop-mode.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/guide/harnesses/codex-cli.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/01-architecture.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/02-plane-architecture.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/03-orchestrator.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/04-stage-protocol.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/04-stages/construction.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/05-agent-system.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/06-hooks-and-tools.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/11-contributing.md
- https://github.com/awslabs/aidlc-workflows/blob/main/docs/reference/13-runtime-graph.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/aidlc-common/conductor.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/aidlc-common/protocols/stage-protocol.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/aidlc-common/protocols/stage-protocol-governance.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/aidlc-common/protocols/stage-protocol-recovery.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/aidlc-common/protocols/stage-protocol-reviewer.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/agents/aidlc-pipeline-deploy-agent.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/knowledge/aidlc-shared/ai-dlc-principles.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/knowledge/aidlc-shared/rules-reading.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/knowledge/aidlc-shared/worktree-info-schema.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/knowledge/aidlc-pipeline-deploy-agent/branching-strategies.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/memory/org.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/skills/aidlc-session-cost/SKILL.md
- https://github.com/awslabs/aidlc-workflows/blob/main/core/tools/aidlc-worktree.ts
- https://github.com/awslabs/aidlc-workflows/blob/main/core/tools/aidlc-bolt.ts
- https://github.com/awslabs/aidlc-workflows/blob/main/harness/claude/skills/aidlc/SKILL.md
- https://github.com/awslabs/aidlc-workflows/blob/main/harness/claude/.mcp.json
- https://github.com/awslabs/aidlc-workflows/blob/main/.github/prompts/ai-pr-review-aidlc.md
- https://github.com/awslabs/aidlc-workflows/blob/main/.github/workflows/ai-pr-review.yml
- https://aws.amazon.com/blogs/devops/ai-driven-development-life-cycle/ — исходный блог-пост AWS про методологию AI-DLC
- https://prod.d13rzhkk8cj2z0.amplifyapp.com/ — AI-DLC Method Definition Paper (ссылка из README, раздел «References»)
- `https://api.github.com/repos/aws-samples/sample-aidlc-workflows` — метаданные заархивированного репозитория-предшественника (Amazon Q Developer rules)
- `https://api.github.com/repos/awslabs/aidlc-workflows/git/trees/main?recursive=1` — полное дерево репозитория (1448 файлов, `truncated: false`)
- код-поиск по репозиторию (`gh api search/code`) на термины `jira`, `tracker`, `pull request`, `github issue`, `worktree`, `sandbox`, `network allowlist`, `budget`, `turn limit`
