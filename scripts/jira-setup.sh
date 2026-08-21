#!/usr/bin/env bash
# Настройка полигона JIRA под офис: статусы, кастомные поля аренды, экраны, учётки.
#
# Повторяемость важнее краткости: H2 полигона сбрасывается удалением каталога,
# и после сброса всё это надо завести заново. Поэтому скрипт, а не память о кликах.
#
# Что скриптом НЕ делается и остаётся человеку — workflow и схема workflow:
# в REST API Jira Server 8.13 их нет вовсе. Инструкция — docs/notes/jira-setup.md.
#
#   scripts/jira-setup.sh [--url http://localhost:2990/jira] [--user admin] [--password admin]
#
# Пароль читается из JIRA_PASSWORD, если флаг не задан. В репозиторий не попадает
# ни то, ни другое.

set -euo pipefail

url=http://localhost:2990/jira
user=admin
password=${JIRA_PASSWORD:-admin}
human_user=${OFFICE_HUMAN_USER:-owner}
human_password=${OFFICE_HUMAN_PASSWORD:-owner}
reviewer_user=${OFFICE_REVIEWER_USER:-office-reviewer}
reviewer_password=${OFFICE_REVIEWER_PASSWORD:-office-reviewer}

while [ $# -gt 0 ]; do
	case $1 in
	--url) url=$2; shift 2 ;;
	--user) user=$2; shift 2 ;;
	--password) password=$2; shift 2 ;;
	*) echo "неизвестный флаг: $1" >&2; exit 2 ;;
	esac
done

jar=$(mktemp -t office-jira-jar)
trap 'rm -f "$jar"' EXIT

api() { curl -sS -u "$user:$password" -H 'Content-Type: application/json' "$@"; }

# --- сессия и websudo -------------------------------------------------------
# Административные формы требуют «защищённой сессии»: без неё Jira молча
# перекидывает на страницу подтверждения пароля, а действие не выполняется.
curl -sS -c "$jar" -X POST -H 'Content-Type: application/json' \
	-d "{\"username\":\"$user\",\"password\":\"$password\"}" "$url/rest/auth/1/session" >/dev/null

token() { awk '/atlassian.xsrf.token/ {print $7}' "$jar"; }

curl -sS -b "$jar" -c "$jar" -o /dev/null -X POST "$url/secure/admin/WebSudoAuthenticate.jspa" \
	--data-urlencode "atl_token=$(token)" --data-urlencode "webSudoPassword=$password" \
	--data-urlencode 'webSudoIsPost=false' --data-urlencode 'webSudoDestination=/secure/admin/ViewStatuses.jspa'

# --- статусы ----------------------------------------------------------------
# Категории: 2 — «к работе», 4 — «в работе», 3 — «готово».
add_status() {
	local name=$1 description=$2 category=$3 body
	body=$(curl -sS -b "$jar" -c "$jar" -X POST "$url/secure/admin/AddStatus.jspa" \
		--data-urlencode "atl_token=$(token)" --data-urlencode "name=$name" \
		--data-urlencode "description=$description" --data-urlencode "statusCategory=$category" \
		--data-urlencode 'Add=Add')
	if grep -q 'already exists' <<<"$body"; then
		echo "  статус $name уже есть"
	else
		echo "  статус $name заведён"
	fi
}

echo "статусы:"
add_status Backlog 'Куча задач до триажа: территория человека, офис сюда не смотрит' 2
add_status Analysis 'Очередь роли analyst: сюда задачу переводит человек' 2
add_status Ready 'Очередь роли implementer' 2
add_status Review 'Очередь роли reviewer; рабочего статуса у неё нет' 4
add_status Approved 'Разбор пройден, pull request открыт: ждём merge от человека' 4
add_status Done 'Работа слита: конец жизни задачи, рабочая папка убрана' 3
add_status Blocked 'Задача ждёт человека' 2

# --- кастомные поля аренды --------------------------------------------------
# Имена с префиксом office_, чтобы не столкнуться с полями проекта-клиента.
add_field() {
	local name=$1 type=$2 searcher=$3 id
	id=$(api "$url/rest/api/2/field" |
		python3 -c "import json,sys; print(next((f['id'] for f in json.load(sys.stdin) if f['name']=='$name'), ''))")
	if [ -n "$id" ]; then
		echo "  поле $name уже есть: $id"
	else
		id=$(api -X POST -d "{\"name\":\"$name\",\"description\":\"поле офиса\",\"type\":\"com.atlassian.jira.plugin.system.customfieldtypes:$type\",\"searcherKey\":\"com.atlassian.jira.plugin.system.customfieldtypes:$searcher\"}" \
			"$url/rest/api/2/field" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
		echo "  поле $name заведено: $id"
	fi
	echo "$id"
}

echo "поля аренды:"
owner_id=$(add_field office_owner textfield textsearcher | tail -1)
run_id=$(add_field office_run_id textfield textsearcher | tail -1)
lease_id=$(add_field office_lease_until datetime datetimerange | tail -1)
attempts_id=$(add_field office_attempts float exactnumber | tail -1)

# --- экраны -----------------------------------------------------------------
# Поле, которого нет на экране, Jira отказывается записывать: «Field cannot be set.
# It is not on the appropriate screen». Кладём на все экраны — полигон, не прод.
echo "экраны:"
for screen in $(api "$url/rest/api/2/screens" | python3 -c "import json,sys; print(' '.join(str(s['id']) for s in json.load(sys.stdin)))"); do
	tab=$(api "$url/rest/api/2/screens/$screen/tabs" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d[0]['id'] if d else '')")
	[ -n "$tab" ] || continue
	for field in "$owner_id" "$run_id" "$lease_id" "$attempts_id"; do
		api -X POST -d "{\"fieldId\":\"$field\"}" "$url/rest/api/2/screens/$screen/tabs/$tab/fields" >/dev/null 2>&1 || true
	done
	echo "  экран $screen: поля добавлены"
done

# --- учётки -----------------------------------------------------------------
# Учётка человека — отдельная от раннера: ответом человека считается комментарий
# не от учётки офиса, и с одним аккаунтом различать было бы нечем.
#
# Учётка reviewer'а — опция: по умолчанию все роли ходят под одной, и это штатный
# режим (роли различаются маркером в комментарии, а не автором). Отдельная нужна
# затем, чтобы проверить механизм и чтобы история тикета читалась людьми.
add_user() {
	local name=$1 secret=$2 display=$3
	if api "$url/rest/api/2/user?username=$name" | grep -q '"name"'; then
		echo "  $name уже есть"
		return
	fi
	api -X POST -d "{\"name\":\"$name\",\"password\":\"$secret\",\"emailAddress\":\"$name@office.local\",\"displayName\":\"$display\"}" \
		"$url/rest/api/2/user" >/dev/null
	echo "  $name заведён"
}

echo "учётки:"
add_user "$human_user" "$human_password" 'Владелец'
add_user "$reviewer_user" "$reviewer_password" 'Агент-ревьюер'

# Права: учётке роли нужно писать поля аренды и делать переходы, иначе первый же
# захват под ней даст 403 в рантайме, а не при настройке. Группа jira-software-users
# входит в роль Developers схемы прав по умолчанию.
if api -X POST -d "{\"name\":\"$reviewer_user\"}" "$url/rest/api/2/group/user?groupname=jira-software-users" >/dev/null 2>&1; then
	echo "  $reviewer_user добавлен в jira-software-users"
else
	echo "  $reviewer_user уже в jira-software-users (или группа называется иначе — проверь права руками)"
fi

echo
echo "готово. Осталось руками — workflow и схема: docs/notes/jira-setup.md"
echo "поля для \${OFFICE_HOME}/tracker.yaml (образец — tracker.example.yaml в репозитории):"
echo "  agent_owner: $owner_id"
echo "  run_id:      $run_id"
echo "  lease_until: $lease_id"
echo "  attempts:    $attempts_id"
