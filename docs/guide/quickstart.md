# Быстрый старт

Этот документ отвечает на вопрос «как поставить офис и провести первую задачу
через всех трёх ролей» — на файловом трекере `mock`, без настройки JIRA. Он для
того, кто видит офис впервые и хочет понять механику, а не для внедрения на
боевой проект: для этого есть **`docs/ONBOARDING.md`**, чек-лист от чистой машины
и пустой JIRA до задачи с открытым pull request. Быстрый старт его не заменяет:
он показывает, как офис работает, а внедрение проводит через то, что для этого
нужно завести.

## Установка

**Тегов релиза пока нет** — команда с `releases/latest` ниже вернёт 404.
До первого тега ставится локальный снапшот:

```sh
sh scripts/release-snapshot.sh
OFFICE_INSTALL_FROM=dist sh install.sh
runner init        # завести ${OFFICE_HOME} и положить образцы
runner version     # что установлено и где лежит офис
```

Снапшоту нужны клон репозитория и Go: `scripts/release-snapshot.sh` собирает
дистрибутив через `go run` (goreleaser). С первым тегом релиза офис ставится
без клона и без Go, одной командой:

```sh
curl -fsSL https://github.com/kao73/virtual-office/releases/latest/download/install.sh | sh
runner init        # завести ${OFFICE_HOME} и положить образцы
runner version     # что установлено и где лежит офис
```

`install.sh` кладёт `runner` и `run-agent` в `${OFFICE_HOME:-~/.office}/bin`,
проверив контрольную сумму архива, и говорит, лежит ли этот каталог в `PATH`.
Обновление — та же команда ещё раз и перезапуск цикла.

Что где оказывается после установки и что чем перекрывается —
[«Конфигурация»](../reference/configuration.md).

Агенту нужен кредит: `ANTHROPIC_API_KEY` или `CLAUDE_CODE_OAUTH_TOKEN`
в окружении, оба пути описаны в `docs/notes/auth.md`. Без него дальше первого
настоящего прогона не уйти.

## Первый проект

Проектов в самом офисе нет — они свойство инстанса и живут только
в `${OFFICE_HOME}/projects.local.yaml`, который `runner init` кладёт как образец
`projects.local.example.yaml` прямо в корень `${OFFICE_HOME}` (рабочий файл —
его копия под рабочим именем).

Дальше нужен репозиторий проекта-клиента. Годится любой; для пробы — локальный.
Пустого репозитория мало: ветку задачи раннер ответвляет от
`origin/<default_branch>`, и без единого коммита такой ссылки нет.

```sh
git init --bare -b master /tmp/client.git
git clone -q /tmp/client.git /tmp/client
git -C /tmp/client -c user.name=you -c user.email=you@local commit -q --allow-empty -m init
git -C /tmp/client push -q origin master
```

Сказать офису, где проект на этой машине, — значит дописать запись
в `${OFFICE_HOME}/projects.local.yaml`:

```yaml
OFF:
  repo_url: /tmp/client.git
  default_branch: master
  tracker: mock          # чей проект: mock или jira
  # branch_prefix: agent/ — умолчание, можно не писать
  # forge не задан: локальный репозиторий, открывать PR негде
```

Обязательны `repo_url`, `default_branch`, `tracker`; `branch_prefix`,
`worktree_root`, `forge`, `auto_merge`, `network`, `tools` — опциональны, у каждого
своя строка-подсказка в образце (`internal/tracker/config.go` в репозитории). Списка проектов
в репозитории офиса нет и не будет: чистая установка не тащит за собой чужие
проекты.

## Первая задача

```sh
# Завести задачу и отдать её офису — в очередь аналитика
runner mock add OFF-1 --status Analysis \
  --summary "Добавить hello.py" \
  --description "Создай hello.py, печатающий приветствие, и тест к нему. Закоммить."

# Цикл по всем ролям: аналитик напишет план, разработчик сделает,
# ревьюер разберёт. По задаче за роль за заход — значит трижды,
# и ещё раз, чтобы системный проход закрыл задачу.
runner tick
runner tick
runner tick
runner tick
```

Что именно делает один `tick` — [«Роли и путь задачи»](roles-and-flow.md#что-делает-один-тик).

**Дальше — настоящие прогоны агента, а они тратят подписку или деньги.** Если
нужна только механика без расходов, остановитесь перед первым `runner tick`
выше; продолжение — на свой счёт.

```sh
# Посмотреть, что вышло
runner mock show OFF-1
git -C /tmp/client.git log --oneline agent/OFF-1
git -C /tmp/client.git ls-tree -r --name-only agent/OFF-1
```

## Что происходит за цикл

Задача, на которой агент упрётся в неоднозначность, уедет в `Blocked` с вопросами
и атрибутом ожидания. Ответить можно тем же CLI:

```sh
runner mock comment OFF-1 "Берём Stripe, точка входа — payments/stripe.py"
runner tick --role implementer      # ответ попадёт в контекст следующего прогона
```

Вопросы приходят с метками, и отвечать на них можно одним словом — так короче
и так они точно попадут туда, куда нужно:

```
## Вопросы

Q1: Идемпотентность или скорость?
  a) идемпотентность
  b) скорость

Q2: Какой формат даты в экспорте?

Ответьте комментарием: `Q1: a`, `Q2: <текст>`; можно и прозой.
```

```sh
runner mock comment OFF-1 "Q1: b
Q2: ISO-8601"
```

Как раннер отличает голос человека от своего и что происходит с задачами,
которые он заблокировал сам, —
[«Роли и путь задачи»](roles-and-flow.md#как-офис-отличает-голос-человека).

## То же самое против JIRA

```sh
export JIRA_USER=admin JIRA_PASSWORD='...'
export JIRA_REVIEWER_USER=office-reviewer JIRA_REVIEWER_PASSWORD='...'
runner tick --role implementer
```

Что инстанс обязан уметь и чем это проверить — [«Что офис требует от вашей
JIRA»](../reference/jira-requirements.md); в каком порядке это проходят —
[`docs/ONBOARDING.md`](../ONBOARDING.md); почему именно так —
`docs/notes/jira-setup.md`. Сам инстанс, если своего нет, поднимается из
`bootstrap/jira/`.
