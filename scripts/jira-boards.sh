#!/usr/bin/env bash
# Доски полигона: инженерная и человеческая.
#
# Доска — не граф. Раннер оперирует статусами и о досках не знает ничего;
# колонка — представление, и представлений одному проекту не жалко. Здесь их
# два, и делают они разное: инженерная показывает, где именно стоит задача,
# человеческая — ждут ли чего-то от человека.
#
# Скрипт нужен по той же причине, что jira-workflow.sh: раскладка досок —
# часть настройки полигона, и после `docker compose down -v` её приходится
# заводить заново. Заодно он закрывает грабли, о которых легко забыть: **статус,
# не попавший ни в одну колонку, исчезает с доски вместе с задачами** — новый
# Analysis на старой доске виден не был.
#
#   scripts/jira-boards.sh [--url http://localhost:2990/jira] [--user admin]
#                          [--password admin] [--project VO]
#
# Колонки правятся внутренним API GreenHopper — тем же, которым пользуется
# редактор доски: в публичном REST 8.13 раскладки нет. Повторный запуск
# безопасен: раскладка задаётся целиком, а не дописывается.

set -euo pipefail

url=http://localhost:2990/jira
user=admin
password=${JIRA_PASSWORD:-admin}
project=VO

while [ $# -gt 0 ]; do
	case $1 in
	--url) url=$2; shift 2 ;;
	--user) user=$2; shift 2 ;;
	--password) password=$2; shift 2 ;;
	--project) project=$2; shift 2 ;;
	*) echo "неизвестный флаг: $1" >&2; exit 2 ;;
	esac
done

# Раскладки. Формат — «Колонка:Статус,Статус|Колонка:Статус».
#
# Инженерная — колонка на статус, один к одному: по ней отлаживают конвейер.
# `Selected for Development` приезжает из шаблона Kanban и лежит рядом с Ready,
# чтобы не мозолить глаза отдельной колонкой.
engineering='Backlog:Backlog|Analysis:Analysis|Ready:Ready,Selected for Development|In Progress:In Progress|Review:Review|Blocked:Blocked|Approved:Approved|Done:Done'

# Человеческая — та, на которую смотрит владелец. Ей неинтересно, какая роль
# держит задачу сейчас; ей важно, ждут ли чего-то от него.
# `Selected for Development` — статус из шаблона Kanban, офис им не пользуется;
# на человеческой доске он лежит в Backlog, потому что для владельца это то же
# самое «ещё не в работе». Не назвать его вовсе значило бы спрятать задачу с доски.
human='Backlog:Backlog,Selected for Development|У агентов:Analysis,Ready,In Progress,Review|Ждёт меня:Blocked|К слиянию:Approved|Done:Done'
human_board="$project — человеческая"

api() {
	curl -sS -u "$user:$password" -H 'Content-Type: application/json' \
		-H 'X-Atlassian-Token: no-check' "$@"
}

work=$(mktemp -d -t office-jira-boards)
trap 'rm -rf "$work"' EXIT

# board_id — идентификатор доски по имени; пусто, если такой нет.
board_id() {
	api "$url/rest/agile/1.0/board?maxResults=100" |
		python3 -c "import json,sys; print(next((str(b['id']) for b in json.load(sys.stdin)['values'] if b['name']==sys.argv[1]), ''))" "$1"
}

# set_columns задаёт раскладку доски целиком.
#
# Колонка Kanban-бэклога (первая, без статусов) сохраняется как есть: она
# не про статусы, а про режим доски, и убрав её, мы выключили бы бэклог.
set_columns() {
	local board=$1 layout=$2

	api "$url/rest/greenhopper/1.0/rapidviewconfig/editmodel?rapidViewId=$board" >"$work/model.json"
	api "$url/rest/api/2/status" >"$work/statuses.json"

	python3 - "$work/model.json" "$work/statuses.json" "$board" "$layout" >"$work/body.json" <<'PY'
import json, sys

model, statuses, board, layout = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
config = json.load(open(model, encoding="utf-8"))["rapidListConfig"]
by_name = {s["name"]: s["id"] for s in json.load(open(statuses, encoding="utf-8"))}

columns = [c for c in config["mappedColumns"][:1] if c.get("isKanPlanColumn")]
columns = [{"name": c["name"], "min": c["min"], "max": c["max"],
            "isKanPlanColumn": True, "mappedStatuses": []} for c in columns]

for part in layout.split("|"):
    name, _, names = part.partition(":")
    mapped = []
    for status in filter(None, (n.strip() for n in names.split(","))):
        if status not in by_name:
            sys.exit(f"статуса {status!r} на инстансе нет: раскладка ссылается в пустоту")
        mapped.append({"id": by_name[status]})
    columns.append({"name": name, "min": "", "max": "", "isKanPlanColumn": False,
                    "mappedStatuses": mapped})

print(json.dumps({"rapidViewId": board,
                  "currentStatisticsField": {"id": config["currentStatisticsField"]["id"]},
                  "mappedColumns": columns}, ensure_ascii=False))
PY

	api -X PUT -d @"$work/body.json" "$url/rest/greenhopper/1.0/rapidviewconfig/columns" >"$work/result.json"

	# Статус вне колонок с доски исчезает вместе с задачами, и молчать об этом
	# нельзя: задача не «уехала», её просто не видно.
	python3 - "$work/result.json" <<'PY'
import json, sys

config = json.load(open(sys.argv[1], encoding="utf-8"))
for column in config["mappedColumns"]:
    print("    {} -> {}".format(column["name"], ", ".join(s["name"] for s in column["mappedStatuses"]) or "—"))
left = [s["name"] for s in config.get("unmappedStatuses", [])]
if left:
    print("    ВНЕ КОЛОНОК: {} — этих задач на доске не видно".format(", ".join(left)))
PY
}

echo "инженерная доска:"
board=$(board_id "$project board")
if [ -z "$board" ]; then
	echo "  доски «${project} board» нет — создайте проект по шаблону Kanban, см. docs/ONBOARDING.md (шаг Б2)" >&2
	exit 2
fi
set_columns "$board" "$engineering"

echo "человеческая доска:"
board=$(board_id "$human_board")
if [ -z "$board" ]; then
	# Доска Kanban заводится от фильтра, и фильтр обязан быть с кем-то разделён:
	# личный Jira на доску не пускает.
	filter=$(api -X POST -d "{\"name\":\"$human_board\",\"jql\":\"project = $project ORDER BY Rank ASC\",\"sharePermissions\":[{\"type\":\"global\"}]}" \
		"$url/rest/api/2/filter" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")
	[ -n "$filter" ] || { echo "  фильтр не создан" >&2; exit 2; }
	board=$(api -X POST -d "{\"name\":\"$human_board\",\"type\":\"kanban\",\"filterId\":$filter}" \
		"$url/rest/agile/1.0/board" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")
	[ -n "$board" ] || { echo "  доска не создана" >&2; exit 2; }
	echo "  доска «${human_board}» заведена (id $board)"
else
	echo "  доска «${human_board}» уже есть (id $board)"
fi
set_columns "$board" "$human"
