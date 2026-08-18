#!/usr/bin/env bash
# Очередь задач на полигоне JIRA — сырьё для нагрузочного прогона.
#
# Скрипт, а не руки, по той же причине, что и jira-setup.sh: полигон сбрасывается
# удалением каталога, а очередь нужна одна и та же — иначе два замера сравнивать
# не с чем.
#
# Задачи подобраны так, чтобы не мешать друг другу: у каждой свой модуль в textkit,
# никакая не опирается на работу соседней. Разброс по размеру намеренный — от
# двух строк до функции с граничными случаями: замер должен увидеть, во сколько
# обходится задача, а не одна и та же задача десять раз.
#
#   scripts/jira-tasks.sh [--url http://localhost:2990/jira] [--user admin]
#                         [--password admin] [--project VO]
#                         [--status Ready] [--initial Backlog]
#
# Скрипт доводит очередь до нужного вида, а не просто создаёт задачи: заголовок,
# который уже есть, не дублируется, а задача, лежащая в начальном статусе,
# переводится в целевой. Задачу, которая уже ушла дальше по конвейеру, скрипт
# не трогает и говорит об этом: дёрнуть в Ready то, над чем прямо сейчас работает
# агент, значило бы отобрать у него задачу.
#
# Ответы JIRA разбирает python3, а не sed. Это не вкус: разбор регулярками здесь
# уже соврал дважды за один запуск — сначала притащил в заголовок хвост `}}`
# от JSON, отчего проверка на дубликат пропустила всё, а потом принял `name`
# у целевого статуса за имя перехода и отправил в JIRA идентификатор статуса
# вместо идентификатора перехода. Оба раза скрипт при этом отработал молча.

set -euo pipefail

url=http://localhost:2990/jira
user=admin
password=${JIRA_PASSWORD:-admin}
project=VO
status=Ready
# initial — статус, в котором задача рождается. Свойство workflow, а не доски:
# выводить его из того, что сейчас лежит на доске, значит зависеть от порядка строк.
initial=Backlog

while [ $# -gt 0 ]; do
	case $1 in
	--url) url=$2; shift 2 ;;
	--user) user=$2; shift 2 ;;
	--password) password=$2; shift 2 ;;
	--project) project=$2; shift 2 ;;
	--status) status=$2; shift 2 ;;
	--initial) initial=$2; shift 2 ;;
	*) echo "неизвестный флаг: $1" >&2; exit 2 ;;
	esac
done

api() { curl -sS -u "$user:$password" -H 'Content-Type: application/json' "$@"; }

# Состояние очереди: заголовок, ключ и статус каждой задачи проекта.
board=$(mktemp -t office-jira-tasks)
trap 'rm -f "$board"' EXIT

api -G --data-urlencode "jql=project = $project" --data 'maxResults=200&fields=summary,status' \
	"$url/rest/api/2/search" |
	python3 -c '
import json, sys
for issue in json.load(sys.stdin)["issues"]:
    fields = issue["fields"]
    print("\t".join([fields["summary"], issue["key"], fields["status"]["name"]]))
' >"$board"

# move <ключ> — перевод задачи в целевой статус.
#
# Переход ищется по тому, куда он ведёт, а не по своему имени: в workflow офиса
# вход в Ready называется Triage, и совпадения имён ждать не от чего.
move() {
	local key=$1 transition response

	transition=$(api "$url/rest/api/2/issue/$key/transitions" |
		python3 -c '
import json, sys
target = sys.argv[1]
for t in json.load(sys.stdin)["transitions"]:
    if t["to"]["name"] == target:
        print(t["id"])
        break
' "$status")

	if [ -z "$transition" ]; then
		echo "$key — перехода в $status нет; workflow проекта настроен не под офис?" >&2
		return 1
	fi

	# Ответ проверяется, а не выбрасывается: неудачный перевод оставляет задачу
	# в статусе, которого роль не читает, и очередь тихо оказывается пустой.
	response=$(api -X POST "$url/rest/api/2/issue/$key/transitions" \
		-d "{\"transition\": {\"id\": \"$transition\"}}")
	if [ -n "$response" ]; then
		echo "$key — перевод в $status отвергнут: $response" >&2
		return 1
	fi
}

# task <заголовок> <описание>
task() {
	local summary=$1 description=$2 line key state

	line=$(awk -F'\t' -v s="$summary" '$1 == s {print; exit}' "$board")
	if [ -n "$line" ]; then
		key=$(printf '%s' "$line" | cut -f2)
		state=$(printf '%s' "$line" | cut -f3)
		case $state in
		"$status") echo "$key — уже в $status" ;;
		"$initial") move "$key" && echo "$key — переведена в $status" ;;
		*) echo "$key — в статусе $state, не трогаю: задача уже в работе" ;;
		esac
		return 0
	fi

	key=$(api -X POST "$url/rest/api/2/issue" -d "{
		\"fields\": {
			\"project\": {\"key\": \"$project\"},
			\"issuetype\": {\"name\": \"Task\"},
			\"summary\": \"$summary\",
			\"description\": \"$description\"
		}
	}" | python3 -c 'import json, sys; print(json.load(sys.stdin).get("key", ""))')

	if [ -z "$key" ]; then
		echo "$summary — задача не создана" >&2
		return 1
	fi
	move "$key" && echo "$key — заведена и переведена в $status"
}

rules='\n\nПравила проекта — в CONTRIBUTING.md, ревью проверяет их все.'

task 'textkit: снятие общего отступа' \
"Нужна функция dedent(text) в модуле textkit/dedent.py: убирает у всех строк\nобщий отступ — тот, что есть у каждой непустой строки. Пустые строки на расчёт\nотступа не влияют и остаются пустыми.$rules"

task 'textkit: выравнивание строки до ширины' \
"Нужна функция pad(text, width, align) в модуле textkit/pad.py: дополняет строку\nпробелами до заданной ширины. Значения align — left, right, center. Строку длиннее\nширины не трогает и не режет.$rules"

task 'textkit: схлопывание пробелов' \
"Нужна функция squeeze(text) в модуле textkit/squeeze.py: заменяет каждую\nпоследовательность пробельных символов одним пробелом и убирает пробелы\nпо краям.$rules"

task 'textkit: нумерация строк' \
"Нужна функция number_lines(text, start) в модуле textkit/numbering.py: приписывает\nк каждой строке её номер, начиная со start. Номера выровнены по правому краю\nпо ширине самого длинного из них.$rules"

task 'textkit: рамка вокруг текста' \
"Нужна функция frame(text) в модуле textkit/frame.py: обводит блок строк рамкой\nиз символов +, - и |, с одним пробелом отступа слева и справа. Ширина рамки —\nпо самой длинной строке.$rules"

task 'textkit: длительность по-человечески' \
"Нужна функция duration(seconds) в модуле textkit/duration.py: переводит секунды\nв строку вида 1ч 05м 30с. Нулевые старшие части опускаются, но внутри строки\nсохраняются: 65 секунд — это 1м 05с.$rules"

task 'textkit: размер по-человечески' \
"Нужна функция human_size(bytes) в модуле textkit/size.py: переводит число байт\nв строку с единицей — Б, КБ, МБ, ГБ. Множитель 1024, дробная часть — один знак,\nу байт дробной части нет.$rules"

task 'textkit: экранирование markdown' \
"Нужна функция escape_md(text) в модуле textkit/escape.py: экранирует обратным\nслэшем символы, меняющие разметку: * _ backtick [ ] и сам обратный слэш.\nПовторное применение к уже экранированному тексту должно давать тот же\nрезультат, что и первое.$rules"

task 'textkit: маскирование хвоста' \
"Нужна функция mask(text, visible) в модуле textkit/mask.py: заменяет всё, кроме\nпоследних visible символов, звёздочками. Если видимых просят больше, чем есть\nсимволов, строка возвращается как есть — маскировать нечего.$rules"
