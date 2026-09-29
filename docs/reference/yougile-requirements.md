# Что офис требует от вашего YouGile

Документ для того, кто настраивает компанию в YouGile под офис. Каждый раздел
ниже — требование и способ проверить, выполнено ли оно. Скрипта настройки нет:
учётка, проект и колонки заводятся руками в интерфейсе YouGile, а их id
читаются `curl`'ом из REST API. Пара к этому документу для JIRA —
[«Что офис требует от вашей JIRA»](jira-requirements.md).

Все запросы ниже берут ключ из переменной `YOUGILE_API_KEY` и в командной
строке его не показывают. Если в `api_key_env` вы назвали другую переменную,
подставьте её имя.

## base_url

Требование: в `${OFFICE_HOME}/tracker-yougile.yaml` `base_url` равен
`https://yougile.com` — корень хоста, без `/api-v2` на конце: префикс API
адаптер добавляет сам, а адрес с `/api-v2` отвергает.

`https://ru.yougile.com` раннер тоже отвергает, ещё при чтении файла. С ним не
скачались бы вложения: `/user-data/…` отвечает переадресацией на
`prod-user-data.yougile.com`, а это не поддомен `ru.yougile.com`, и файловый
клиент офиса за пределы хоста из `base_url` и его поддоменов не ходит.

Проверка — ключ принят, адрес верный:

```sh
curl -fsS -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $YOUGILE_API_KEY" \
  https://yougile.com/api-v2/users/me
```

печатает `200`.

## Учётка

Требование: в компании заведён отдельный пользователь для офиса, не
человек, и ключ API выпущен от его имени. Вы сами входите в YouGile под другой
учёткой. Ответом человека офис считает только сообщение в чате задачи не от
учётки офиса, поэтому ваш ответ, написанный под учёткой офиса, он не услышит
([контракт «раннер ↔ трекер», «Кто человек»](../contracts/tracker-protocol.md#кто-человек)).

Учётка одна на все роли: роли раннер различает по маркеру в первой строке
комментария, а не по автору. Её email вписывать никуда не нужно — раннер
спрашивает его у `/users/me` при запуске.

Ключ выпускается двумя запросами под логином и паролем учётки офиса. Логин и
пароль читаются из переменных, чтобы не попасть в историю оболочки и в `ps`:

```sh
# id компании, к которой у учётки есть доступ
jq -n --arg login "$YG_LOGIN" --arg password "$YG_PASSWORD" \
    '{login: $login, password: $password}' |
  curl -fsS -X POST -H 'Content-Type: application/json' -d @- \
    https://yougile.com/api-v2/auth/companies |
  jq '.content[] | {id, name}'

# ключ API — сразу в переменную, не на экран
YOUGILE_API_KEY=$(jq -n --arg login "$YG_LOGIN" --arg password "$YG_PASSWORD" \
    --arg companyId '<id компании>' \
    '{login: $login, password: $password, companyId: $companyId}' |
  curl -fsS -X POST -H 'Content-Type: application/json' -d @- \
    https://yougile.com/api-v2/auth/keys |
  jq -r .key)
```

Куда ключ кладётся на машине раннера — в переменную, которую называет
`api_key_env` (в образце — `YOUGILE_API_KEY`):
[«Подготовка машины», «Креды»](../guide/machine-setup.md#креды). В сам файл
`tracker-yougile.yaml` ключ не пишется никогда.

`also_agents` в `tracker-yougile.yaml` — email'ы чужой автоматизации: её
сообщения тоже не считаются словами человека. Учётку офиса туда не пишут.
Email'ы сравниваются с авторами комментариев как строки, точно и с учётом
регистра, поэтому пишите их ровно так, как их отдаёт YouGile:

```sh
curl -fsS -H "Authorization: Bearer $YOUGILE_API_KEY" \
  'https://yougile.com/api-v2/users?limit=100' |
  jq '.content[] | {id, email}'
```

Проверка: запрос к `/users/me` отвечает email'ом учётки офиса, а не вашим:

```sh
curl -fsS -H "Authorization: Bearer $YOUGILE_API_KEY" \
  https://yougile.com/api-v2/users/me | jq -r .email
```

## Проект и колонки

Требование: раннер обслуживает ровно один проект YouGile, и учётка офиса —
его участник. На доске этого проекта заведена своя колонка на каждый статус
графа: `Backlog`, `Analysis`, `Ready`, `InProgress`, `Review`, `Approved`,
`Done`, `Blocked` (полный список — `office/workflow.yaml`, ключ `statuses`).
Статус задачи в YouGile — это её колонка, поэтому колонка нужна и статусам,
куда задачи кладёт только человек (`Backlog`, `Done`). Одна колонка на два
статуса не годится: раннер откажется открыть такой проект.

Названия колонок — любые: раннер сопоставляет их по id из раздела `columns`
в `tracker-yougile.yaml`. Колонки, которых в этом разделе нет, разрешены — это
колонки вне графа, офис задачи в них не читает. Колонки заводит человек:
раннер их только сверяет и сам не создаёт.

id читаются тремя запросами, каждый следующий — по id из предыдущего:

```sh
# project_id
curl -fsS -H "Authorization: Bearer $YOUGILE_API_KEY" \
  'https://yougile.com/api-v2/projects?limit=100' | jq '.content[] | {id, title}'

# id доски проекта
curl -fsS -H "Authorization: Bearer $YOUGILE_API_KEY" \
  'https://yougile.com/api-v2/boards?projectId=<project_id>' | jq '.content[] | {id, title}'

# id колонок доски — восемь из них идут в columns
curl -fsS -H "Authorization: Bearer $YOUGILE_API_KEY" \
  'https://yougile.com/api-v2/columns?boardId=<id доски>' | jq '.content[] | {id, title}'
```

`jq` здесь только для читаемости: без него ответ придёт тем же JSON'ом одной
строкой.

Проверка: `runner doctor` печатает `ok` у `config:tracker-yougile.yaml`
(у каждого статуса графа есть колонка в файле) и у `yougile:open` (проект и
все восемь колонок нашлись на сервере).

## Как снять задачу из Blocked

Задачу, которую офис больше не должен трогать, перетащите в колонку вне графа.
Архивировать её не надо: если это родитель разбиения, `runner complete-splits`
достроит ему детей и из архива. Это известный остаток адаптера, и он принят
как есть.

## runner ls и архивные карточки

`runner ls` показывает и архивные карточки — в статусе их колонки. Так устроен
`List` адаптера: по нему раннер решает, закрыта ли зависимость, и архивная
зависимость в терминальной колонке иначе держала бы зависимую задачу вечно.
Удалённые карточки `runner ls` не показывает.

## Лимит запросов

YouGile пропускает не больше 50 запросов в минуту на компанию. Ответ `429`
раннер не повторяет сам: он становится ошибкой этого захода с текстом «превышен
rate limit YouGile», а следующий заход пробует заново. Период `runner loop`
держите на умолчании (`--every 2m`) или длиннее.

## Проверка целиком

`runner doctor` проверяет YouGile, только если хоть один проект в
`projects.local.yaml` назвал `tracker: yougile`. В YouGile доктор только читает.
Находки идут по порядку, и отказ каждой пропускает следующие:

| check-id | Что проверяет | Если `fail` |
|---|---|---|
| `config:tracker-yougile.yaml` | файл читается и проходит проверку ключей; ключ проекта совпадает с `projects.local.yaml`; у каждого статуса графа есть колонка | дальше `warn skip:yougile`, остальные три не проверяются |
| `cred:<api_key_env>` | переменная из `api_key_env` задана (значение не печатается) | дальше `warn skip:yougile-checks`, `yougile:open` и `yougile:account` не проверяются |
| `yougile:open` | проект с `project_id` отвечает ключу, все колонки из `columns` есть на досках проекта | дальше `warn skip:yougile-checks`, `yougile:account` не проверяется |
| `yougile:account` | `/users/me` называет email учётки; при `ok` он напечатан в находке | — |

Email в находке `yougile:account` — учётка офиса. Если там ваш, ключ выпущен не
от той учётки: см. [«Учётка»](#учётка).
