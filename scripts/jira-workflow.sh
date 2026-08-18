#!/usr/bin/env bash
# Достройка workflow полигона под граф офиса: шаги под наши статусы, переходы
# между ними и публикация черновика.
#
# В REST API Jira Server 8.13 workflow нет вовсе, и заметки долго считали это
# приговором: «правится только мышью в редакторе диаграмм». Неправда — текстовый
# редактор workflow состоит из обычных форм, и они принимают POST. Отсюда скрипт:
# единственная ручная часть настройки полигона перестала быть ручной.
#
#   scripts/jira-workflow.sh [--url http://localhost:2990/jira] [--user admin]
#                            [--password admin] [--workflow '<имя>']
#
# Повторный запуск безопасен: существующие шаги и переходы пропускаются.
# Глобальных переходов скрипт не делает — текстовый редактор их не умеет, и они
# не нужны: маршруты задаёт workflow.yaml, а трекеру хватает тех переходов,
# которыми ходит раннер.

set -euo pipefail

url=http://localhost:2990/jira
user=admin
password=${JIRA_PASSWORD:-admin}
workflow=

while [ $# -gt 0 ]; do
	case $1 in
	--url) url=$2; shift 2 ;;
	--user) user=$2; shift 2 ;;
	--password) password=$2; shift 2 ;;
	--workflow) workflow=$2; shift 2 ;;
	*) echo "неизвестный флаг: $1" >&2; exit 2 ;;
	esac
done

work=$(mktemp -d -t office-jira-wf)
jar="$work/cookies"
trap 'rm -rf "$work"' EXIT

curl -sS -c "$jar" -X POST -H 'Content-Type: application/json' \
	-d "{\"username\":\"$user\",\"password\":\"$password\"}" "$url/rest/auth/1/session" >/dev/null

token() { awk '/atlassian.xsrf.token/ {print $7}' "$jar"; }

# Административные формы требуют защищённой сессии: без неё Jira молча покажет
# страницу подтверждения пароля, а действие не выполнит.
curl -sS -b "$jar" -c "$jar" -o /dev/null -X POST "$url/secure/admin/WebSudoAuthenticate.jspa" \
	--data-urlencode "atl_token=$(token)" --data-urlencode "webSudoPassword=$password" \
	--data-urlencode 'webSudoIsPost=false' --data-urlencode 'webSudoDestination=/secure/admin/ViewStatuses.jspa'

page() { curl -sS -b "$jar" -c "$jar" "$@"; }
urlenc() { python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$1"; }

if [ -z "$workflow" ]; then
	page -o "$work/list.html" "$url/secure/admin/workflows/ListWorkflows.jspa"
	workflow=$(python3 - "$work/list.html" <<'PY'
import html, re, sys, urllib.parse

names = re.findall(r"wfName=([^&\"']+)", open(sys.argv[1], encoding="utf-8", errors="replace").read())
names = [urllib.parse.unquote_plus(html.unescape(n)) for n in names]
# Шаблонные workflow Jira общие для инстанса, их трогать нельзя.
own = [n for n in names if n not in ("jira", "classic default workflow")]
print(own[0] if own else "")
PY
)
	[ -n "$workflow" ] || { echo "не нашёл проектного workflow — задайте --workflow" >&2; exit 2; }
fi
echo "workflow: $workflow"

# Черновик: опубликованный workflow Jira править не даёт.
page -o /dev/null "$url/secure/admin/workflows/EditWorkflowDispatcher.jspa?atl_token=$(token)&wfName=$(urlenc "$workflow")"

steps_page() {
	page -o "$work/steps.html" \
		"$url/secure/admin/workflows/ViewWorkflowSteps.jspa?workflowMode=draft&workflowName=$(urlenc "$workflow")"
}

# Идентификаторы статусов — с административной страницы, а не из /rest/api/2/status:
# тот показывает только статусы, уже участвующие в каком-нибудь workflow,
# и свежезаведённого Approved в нём нет.
page -o "$work/statuses.html" "$url/secure/admin/ViewStatuses.jspa"
python3 - "$work/statuses.html" >"$work/statuses.txt" <<'PY'
import html, re, sys

h = open(sys.argv[1], encoding="utf-8", errors="replace").read()
for name, sid in re.findall(r"<tr><td><b>([^<]+)</b>.*?id=\"edit_(\d+)\"", h, re.S):
    print(html.unescape(name), sid)
PY

id_of() { awk -v n="$1" '$0 ~ "^" n " " {print $NF}' "$work/statuses.txt"; }

echo "шаги:"
for status in Analysis Ready Review Approved Blocked; do
	steps_page
	if grep -q "id=\"step_link_[0-9]*\">$status</a>" "$work/steps.html"; then
		echo "  $status — шаг уже есть"
		continue
	fi
	sid=$(id_of "$status")
	[ -n "$sid" ] || { echo "  $status — статуса нет, сначала scripts/jira-setup.sh" >&2; exit 2; }
	page -o /dev/null -X POST "$url/secure/admin/workflows/AddWorkflowStep.jspa" \
		--data-urlencode "atl_token=$(token)" --data-urlencode "stepName=$status" \
		--data-urlencode "stepStatus=$sid" --data-urlencode "workflowName=$workflow" \
		--data-urlencode 'workflowMode=draft' --data-urlencode 'Add=Add'
	echo "  $status — шаг заведён"
done

# Переходы — ровно те, которыми ходит раннер (workflow.yaml): захват, исходы
# трёх ролей, reap, ответ человека, остановка по бюджету — плюс человеческие:
# триаж Backlog → Analysis и возврат родителя Blocked → Backlog после разбиения.
# Больше в трекере не нужно ничего.
#
# Backlog → Ready оставлен: задачу, которой план не нужен, человек кладёт
# в очередь разработчика напрямую.
transitions="Backlog|Analysis|Plan
Backlog|Ready|Triage
Analysis|Ready|Handoff
Analysis|Blocked|Block
Ready|In Progress|Claim
Ready|Blocked|Block
In Progress|Review|Submit
In Progress|Ready|Return
In Progress|Analysis|Replan
In Progress|Blocked|Block
Review|Approved|Approve
Review|Ready|Return
Review|Analysis|Replan
Review|Blocked|Block
Blocked|Analysis|Unblock to Analysis
Blocked|Ready|Unblock to Ready
Blocked|Review|Unblock to Review
Blocked|Backlog|Back to Backlog"

echo "переходы:"
while IFS='|' read -r src dst name; do
	[ -n "$src" ] || continue
	steps_page
	if python3 - "$work/steps.html" "$src" "$dst" <<'PY'
import html, re, sys

h, src, dst = open(sys.argv[1], encoding="utf-8", errors="replace").read(), sys.argv[2], sys.argv[3]
for row in re.split(r"<tr>", h):
    m = re.search(r'id="step_link_(\d+)">([^<]+)</a>', row)
    if not m or html.unescape(m.group(2)).strip() != src:
        continue
    targets = [html.unescape(t).strip() for t in re.findall(r"&gt;&gt;\s*([^<\n]+)", row)]
    sys.exit(0 if any(t.lower() == dst.lower() for t in targets) else 1)
sys.exit(1)
PY
	then
		echo "  $src → $dst — уже есть"
		continue
	fi
	ids=$(python3 - "$work/steps.html" "$src" "$dst" <<'PY'
import html, re, sys

h = open(sys.argv[1], encoding="utf-8", errors="replace").read()
steps = {html.unescape(n).strip(): i
         for i, n in re.findall(r'id="step_link_(\d+)">([^<]+)</a>', h)}
print(steps.get(sys.argv[2], ""), steps.get(sys.argv[3], ""))
PY
)
	from=${ids% *}; to=${ids#* }
	if [ -z "$from" ] || [ -z "$to" ]; then
		echo "  $src → $dst — нет шага, пропускаю" >&2
		continue
	fi
	# Экран переходу не нужен: поля аренды раннер пишет обычным PUT /issue.
	page -o /dev/null -X POST "$url/secure/admin/workflows/AddWorkflowTransition.jspa" \
		--data-urlencode "atl_token=$(token)" --data-urlencode "transitionName=$name" \
		--data-urlencode "description=офис: $src -> $dst" \
		--data-urlencode "destinationStep=$to" --data-urlencode "view=" \
		--data-urlencode "workflowStep=$from" --data-urlencode "workflowName=$workflow" \
		--data-urlencode 'workflowMode=draft'
	echo "  $src → $dst — заведён ($name)"
done <<<"$transitions"

page -o /dev/null -X POST "$url/secure/admin/workflows/PublishDraftWorkflow.jspa" \
	--data-urlencode "atl_token=$(token)" --data-urlencode 'enableBackup=false' \
	--data-urlencode 'madeDeliberateChoice=true' --data-urlencode "workflowName=$workflow" \
	--data-urlencode 'workflowMode=draft'
echo "черновик опубликован"
