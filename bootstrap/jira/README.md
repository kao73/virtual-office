# Локальная JIRA в контейнере

Jira Software 8.13.19 на Atlassian Plugin SDK: инстанс, на котором офис отлаживают
и на котором ведут первый проект. Поднимается на любой машине одинаково,
сбрасывается удалением тома.

Это обвязка машины, а не часть офиса: раннер про контейнер не знает ничего
и разговаривает с любым инстансом по адресу из `${OFFICE_HOME}/tracker.yaml`.

Здесь сам контейнер. Что офис требует от любого инстанса JIRA, полигонного или
чужого, — `docs/reference/jira-requirements.md`; порядок, в котором это
проходят, — `docs/ONBOARDING.md`.

## Сначала SDK

В git он не едет — 88 МБ распакованных артефактов. Качается один раз:

```sh
cd bootstrap/jira
curl -fsSLO https://maven.artifacts.atlassian.com/com/atlassian/amps/atlassian-plugin-sdk/8.2.10/atlassian-plugin-sdk-8.2.10.tar.gz
mkdir -p sdk && tar xzf atlassian-plugin-sdk-8.2.10.tar.gz -C sdk
ls sdk/atlassian-plugin-sdk-8.2.10       # bin repository apache-maven-3.9.5
cd -                                     # обратно в корень офиса
```

Файл — 76 758 884 байта, `sha256` начинается на `9cb3a00a`; проверено 2026-08-21.
Распакованный каталог занимает 88 МиБ, архив рядом — ещё 73; `.gitignore`
закрывает и то, и другое.

Версия важна: `8.2.10` знает Jira 8.13. Ссылка «последний SDK» с marketplace
отдаёт девятую ветку, и она не подойдёт.

Без него сборка образа падает на `COPY sdk/…` на первом же шаге — команда `ls`
выше это и проверяет (из корня офиса — `ls bootstrap/jira/sdk/atlassian-plugin-sdk-8.2.10`).

## Поднять

```sh
docker compose up -d --build     # первый запуск: качается Jira, замер — 887 секунд
docker compose logs -f           # ждать «jira started successfully»
curl -fsS http://localhost:2990/jira/rest/api/2/serverInfo
```

Готовность видно и по `docker compose ps` (из корня офиса — `docker compose
-p office-jira ps`: имя проекта задано в самом `compose.yaml`, и по нему
инстанс находится из любого каталога): `healthy` значит, что `serverInfo`
отвечает и возвращает JSON с версией `8.13.19`. Пока он молчит, `ps` держит
статус `starting` — healthcheck терпит его двадцать минут, а измеренный первый
запуск занял около пятнадцати. Адрес — `http://localhost:2990/jira`, учётка
`admin`/`admin` — это учётка именно этого локального инстанса, и в примерах
вида `curl -su admin:admin` она названа открытым текстом ровно поэтому;
с настоящим паролем так не делайте.

## Завести проект

Порядок «сначала проект» обязателен, и почему —
`docs/reference/jira-requirements.md`, раздел «Проект». Команды для этого
инстанса:

```sh
curl -su admin:admin -H 'Content-Type: application/json' -X POST \
  -d '{"key":"OFF","name":"Мой проект","lead":"admin",
       "projectTypeKey":"software",
       "projectTemplateKey":"com.pyxis.greenhopper.jira:gh-kanban-template"}' \
  http://localhost:2990/jira/rest/api/2/project
```

Проверка: `curl -fsS -u admin:admin
'http://localhost:2990/jira/rest/api/2/project/OFF'` отвечает JSON, а не `404`.

Затем три скрипта подряд, в этом порядке:

```sh
export JIRA_PASSWORD=admin           # скрипты читают его сами; в ps он не светится
scripts/jira-setup.sh    --url http://localhost:2990/jira --user admin
scripts/jira-workflow.sh --url http://localhost:2990/jira --user admin
scripts/jira-boards.sh   --url http://localhost:2990/jira --user admin --project OFF
```

Что каждый заводит и как проверить результат — `docs/reference/jira-requirements.md`:
статусы, переходы, четыре поля аренды, тип связи «зависит от», доски и учётки —
всё описано там же, по разделам с теми же названиями. Четвёртый скрипт того же
семейства, `scripts/jira-tasks.sh`, в заходе не участвует — он заводит очередь
нагрузочного прогона; флаги у него те же: `--url`, `--user`, `--password`,
`--project`.

## Пробная задача

Она проверяет workflow и права учётки на этом же проекте: заведите
одну задачу — в интерфейсе или тем же REST — и переведите её в `Ready`.

Проверка workflow: у задачи в `Ready` в списке переходов обязан быть `In Progress`.

```sh
curl -su admin:admin 'http://localhost:2990/jira/rest/api/2/issue/OFF-1/transitions' |
  python3 -c "import json,sys; print([t['to']['name'] for t in json.load(sys.stdin)['transitions']])"
```

Проверка досок: откройте обе — инженерную и человеческую — и убедитесь, что
задача на них видна. Права учётки
роли проверяются тем же тикетом — `docs/reference/jira-requirements.md`, раздел
«Учётки»; уберите задачу из `Ready` только после этой проверки, переходами
`Ready → Blocked → Backlog`, чтобы её не подобрал холостой прогон раннера:
`Ready` — это очередь разработчика, а `Backlog` офис не читает вовсе.

Проверка: `curl -fsS -u admin:admin
'http://localhost:2990/jira/rest/api/2/issue/OFF-1?fields=status'` показывает
`Backlog`.

Инстанс на этом можно остановить или снести:

```sh
docker compose stop              # остановить, данные сохранить
docker compose down -v           # снести вместе с данными
```

## Второй инстанс рядом

Порт задаётся переменной, имя проекта compose — флагом:

```sh
JIRA_PORT=2991 JIRA_NAME=office-jira-live docker compose -p office-jira-live up -d
```

Три вещи, которые обязаны разойтись, задаются окружением и флагом: порт, имя
контейнера и имя проекта compose. Файл править не надо — он отслеживаемый,
и правка под себя пометила бы каждый ваш прогон как `config:…-dirty`.

Тома у другого проекта compose свои, поэтому второй инстанс поднимается пустым —
это и нужно, когда полигон отлажен, а вести надо чистый проект.

## Второй проект на одном инстансе

Это другой случай: контейнер и
инстанс те же, а `jira-workflow.sh` без указанной цели возьмёт первый
непошаблонный workflow и уведёт правки не в тот проект. Назовите его явно:
`scripts/jira-workflow.sh --url http://localhost:2990/jira --user admin --workflow '<имя>'`.

## Три вещи, о которых лучше знать заранее

**Лицензия живёт трое суток.** Девелоперская лицензия приезжает внутри артефакта
`jira-plugin-test-resources-8.13.19` уже в базе H2, и срок считается не от даты
артефакта, а от того, когда ключ попал в инстанс. Перезапуск контейнера его
не переставляет, поэтому `restart` ничего не продлевает. Продление — `down -v`
и настройка заново, то есть все скрипты из `docs/reference/jira-requirements.md`
ещё раз. Стоит это
дороже, чем кажется: `down -v` сносит оба тома, включая `maven-repo`, — значит,
повторится и первый запуск с перекачкой артефактов. Идентификаторы четырёх
полей аренды при этом тоже заведутся новые — значит, и
`${OFFICE_HOME}/tracker.yaml` придётся поправить.

**Памяти нужно ~6 ГиБ свободных, и предел контейнера этого не гарантирует.**
`mem_limit` — потолок, а не бронь: инстанс убивало по памяти и с четырьмя
гигабайтами, и с шестью, когда рядом в той же виртуалке Docker работал чужой стек.
Смотреть надо на свободную память виртуалки, а не на предел в compose.

В покое инстанс занимает около трёх гигабайт (измерено на поднятом), но при
пределе в четыре его убивало — на старте и переиндексации он берёт больше. Шесть
взяты как запас на пик.

**Смерть по памяти выглядит как живой контейнер.** PID 1 её переживает, статус
остаётся `running`, в логах Jira ни строчки. Проверять — так:

```sh
docker inspect office-jira-software --format '{{.State.OOMKilled}}'
docker compose ps                # unhealthy через 45 секунд после смерти порта
```

## Почему так, а не иначе

Почему SDK, а не образ `atlassian/jira-software`; почему проект AMPS ради одной
строчки `<applications>`; почему в `CMD` стоит `tail -f /dev/null` — в комментариях
`Dockerfile` и `compose.yaml`, у каждого решения на своём месте.

Эмпирика REST и разбор страниц workflow — `docs/notes/jira-api.md`
и `docs/notes/jira-setup.md`.
