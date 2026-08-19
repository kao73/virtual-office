#!/usr/bin/env bash
# Smoke-тест локального бэкенда. Четыре сценария:
#
#   self       — роль описывает саму себя; ответ сверяется с role.yaml вручную
#   hello      — выполнимая задача, ожидается outcome=done и коммит
#   ambiguous  — намеренно неоднозначная задача, ожидается needs_human с вопросами
#   guard      — агенту велено записать невалидный результат; ограждение обязано
#                отвергнуть его и объяснить, а агент — починить, пока ещё жив
#
# И два сценария роли reviewer:
#
#   review-self — та же самопроверка для другой роли: инструментов записи быть не должно
#   review-bug  — постановка велит починить баг; роль обязана вернуть разбор, а не патч
#
# И три сценария роли analyst:
#
#   plan-self   — самоописание: `Write` роли не дано, `Edit` — только каталог изменения
#   plan        — ясная задача; ожидается план в трёх файлах, закоммиченный, и done
#   plan-guard  — постановка велит не коммитить; ограждение обязано не пропустить
#                 done без плана в git и сказать агенту, что сделать
#
# Запуск:  scripts/smoke.sh [self|hello|ambiguous|guard|review-self|review-bug|review|plan-self|plan|plan-guard|analyst|all]
#
# Прогоны настоящие и стоят денег или лимита подписки. Кред берётся из окружения,
# см. docs/notes/auth.md. Рабочие каталоги не удаляются: по ним разбирают, что пошло не так.

set -uo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
scenario=${1:-all}
base=${SMOKE_DIR:-$(mktemp -d -t office-smoke)}

# Роль от бэкенда не зависит: оба сценария обязаны проходить и без изоляции,
# и в песочнице, без единой правки в roles/.
backend=${OFFICE_BACKEND:-local}

failures=0
ok() { printf '    ✓ %s\n' "$1"; }
bad() {
	printf '    ✗ %s\n' "$1"
	failures=$((failures + 1))
}

if [ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
	echo "Не задан ни ANTHROPIC_API_KEY, ни CLAUDE_CODE_OAUTH_TOKEN — прогонять нечем." >&2
	echo "См. docs/notes/auth.md." >&2
	exit 2
fi

# new_repo делает временный репозиторий, похожий на проект-клиента: README,
# и настройки .claude/, которые агент подхватить не должен. Маркер-ловушка
# проверяет, что изоляция работает: если хук проекта отработал, файл появится.
new_repo() {
	local dir=$1
	mkdir -p "$dir/.claude"
	printf '# Тестовый проект\n\nВременный репозиторий smoke-теста.\n' >"$dir/README.md"
	# Путь маркера абсолютный: иначе непонятно, не сработал хук или сработал не там.
	cat >"$dir/.claude/settings.json" <<JSON
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "touch '$dir/.leaked-project-hook'" }
        ]
      }
    ]
  }
}
JSON
	git -C "$dir" init -q
	git -C "$dir" add -A
	git -C "$dir" -c user.email=smoke@example.test -c user.name=smoke commit -q -m "начало"
}

# run_case прогоняет один сценарий и возвращает код выхода run-agent.
# Роль и базовая ветка необязательны: без них это implementer над свежим репозиторием.
run_case() {
	local name=$1 task=$2 role=${3:-implementer} base_branch=${4:-} dir="$base/$1"

	printf '\n== %s (роль %s, бэкенд %s) ==\n  каталог: %s\n' "$name" "$role" "$backend" "$dir"
	mkdir -p "$dir"
	[ -d "$dir/.git" ] || new_repo "$dir"
	printf '%s\n' "$task" >"$base/$name-task.md"

	local started elapsed code
	started=$(date +%s)
	"$root/bin/run-agent" --role "$role" --workdir "$dir" --task "$base/$name-task.md" \
		--backend "$backend" ${base_branch:+--base "$base_branch"}
	code=$?
	elapsed=$(($(date +%s) - started))

	printf '  код выхода: %d, время: %d с\n' "$code" "$elapsed"
	metrics "$dir/.agent/run.log"
	return $code
}

# metrics достаёт из лога итоговый JSON агента: стоимость, длительность, число шагов.
metrics() {
	[ -f "$1" ] || return 0
	python3 - "$1" <<'PY'
import json, sys

raw = open(sys.argv[1], encoding="utf-8", errors="replace").read()
decoder, found, i = json.JSONDecoder(), None, 0
while True:
    i = raw.find("{", i)
    if i < 0:
        break
    try:
        obj, end = decoder.raw_decode(raw, i)
    except ValueError:
        i += 1
        continue
    if isinstance(obj, dict) and "total_cost_usd" in obj:
        found, i = obj, end
    else:
        i = end
if found:
    print("  стоимость: ${:.4f}, длительность: {} мс, шагов: {}, ошибка: {}".format(
        found.get("total_cost_usd", 0.0),
        found.get("duration_ms", "?"),
        found.get("num_turns", "?"),
        found.get("is_error", "?")))
else:
    print("  итоговый JSON агента в логе не найден")
PY
}

outcome() { jq -r '.outcome // "нет"' "$1/.agent/result.json" 2>/dev/null || echo "нечитаем"; }

# assert_common — то, что обязано быть верно в любом сценарии.
assert_common() {
	local dir=$1

	if [ -f "$dir/.leaked-project-hook" ]; then
		bad "сработал хук из .claude/settings.json проекта-клиента: изоляция не держит"
	else
		ok "настройки проекта-клиента не подхвачены"
	fi

	if git -C "$dir" log --name-only --pretty=format: | grep -q '^\.agent/'; then
		bad "конверт обмена попал в коммит"
	else
		ok "конверт обмена не в истории"
	fi

	# Проверяем тем же кодом, что ограждение и раннер: jq сказал бы только,
	# что это JSON, а нас интересует контракт целиком.
	if "$root/bin/run-agent" validate-result "$dir/.agent/result.json" 2>/dev/null; then
		ok "result.json проходит контракт"
	else
		bad "result.json нарушает контракт: $("$root/bin/run-agent" validate-result "$dir/.agent/result.json" 2>&1 | head -1)"
	fi

	# Рабочая папка на этапе 2 станет worktree и будет удалена вместе с конвертом.
	# Разбирать прогон после этого можно только по архиву.
	local run_id archive
	run_id=$(jq -r '.run_id' "$dir/.agent/run.json" 2>/dev/null)
	archive="${OFFICE_HOME:-$HOME/.office}/runs/$run_id"
	if [ -f "$archive/result.json" ] && [ -f "$archive/run.log" ]; then
		ok "прогон заархивирован: $archive"
	else
		bad "прогона нет в архиве: $archive"
	fi
}

case_self() {
	local dir="$base/self"
	run_case self 'Ответь, кто ты: что получаешь на входе, что обязан оставить на выходе, какие бывают исходы и какие инструменты тебе доступны. Ничего не делай и ничего не меняй. Ответ запиши в файл результата с outcome=done.'
	local code=$?

	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = done ] && ok "исход done" || bad "исход $(outcome "$dir"), ожидался done"
	assert_common "$dir"

	echo "  --- самоописание роли, сверить с roles/implementer/role.yaml ---"
	jq -r '.summary, (.details_md // "")' "$dir/.agent/result.json" 2>/dev/null | sed 's/^/  /'
}

case_hello() {
	local dir="$base/hello"
	run_case hello 'Создай файл hello.py, печатающий "hello from implementer". Добавь tests/test_hello.py с тестом на это поведение, прогони тесты и закоммить изменения.'
	local code=$?

	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = done ] && ok "исход done" || bad "исход $(outcome "$dir"), ожидался done"
	assert_common "$dir"

	[ -f "$dir/hello.py" ] && ok "hello.py на месте" || bad "hello.py не создан"
	[ -f "$dir/tests/test_hello.py" ] && ok "тест на месте" || bad "tests/test_hello.py не создан"

	if [ "$(git -C "$dir" rev-list --count HEAD)" -gt 1 ]; then
		ok "коммит появился"
	else
		bad "новых коммитов нет"
	fi
	# Спрашиваем про отслеживаемые файлы: незакоммиченная правка — это потерянная
	# работа, а неотслеживаемый мусор вроде __pycache__ никуда не уедет и не вредит.
	if [ -z "$(git -C "$dir" status --porcelain --untracked-files=no)" ]; then
		ok "вся работа закоммичена"
	else
		bad "остались незакоммиченные правки: $(git -C "$dir" status --porcelain --untracked-files=no | tr '\n' ' ')"
	fi
	local litter
	litter=$(git -C "$dir" status --porcelain --untracked-files=all | tr '\n' ' ')
	[ -n "$litter" ] && printf '    · агент оставил после себя: %s\n' "$litter"

	if (cd "$dir" && uv run --quiet --with pytest pytest -q >/dev/null 2>&1); then
		ok "тесты проходят"
	else
		bad "тесты не проходят"
	fi
}

case_ambiguous() {
	local dir="$base/ambiguous"
	run_case ambiguous 'Сделай интеграцию с платёжной системой.'
	local code=$?

	# Ноль, а не единица: агент оставил валидный результат и назвал, чего ему не хватает.
	# Единица означала бы failed — работу, которую надо считать неудачной попыткой.
	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = needs_human ] && ok "исход needs_human" || bad "исход $(outcome "$dir"), ожидался needs_human"
	assert_common "$dir"

	local questions
	questions=$(jq -r '.questions | length' "$dir/.agent/result.json" 2>/dev/null || echo 0)
	[ "$questions" -gt 0 ] && ok "вопросов задано: $questions" || bad "вопросов нет"

	# Метка — то, чем человек отвечает одним словом, и по ней же раннер разбирает
	# ответ. Контракт её требует, но интересно другое: попадает ли агент в форму
	# сам, не упираясь в ограждение.
	local unlabelled
	unlabelled=$(jq -r '[.questions[]? | select((.id // "") | test("^Q[0-9]+$") | not)] | length' 		"$dir/.agent/result.json" 2>/dev/null || echo 1)
	[ "$unlabelled" = 0 ] && ok "у каждого вопроса метка вида Q1" || bad "вопросов без метки: $unlabelled"

	echo "  --- вопросы ---"
	jq -r '.questions[]? | "  " + .id + ": " + .text
		+ (if .options then "\n" + ([.options[] | "    " + .id + ") " + .label] | join("\n")) else "" end)' 		"$dir/.agent/result.json" 2>/dev/null
}

# Ограждение никак не проявляет себя на счастливом пути, а его отказ беззвучен:
# код, отличный от 2, Claude Code считает неблокирующей ошибкой. Значит проверять
# его надо, пробуя пробить, — иначе это не ограждение, а предположение о нём.
case_guard() {
	local dir="$base/guard"
	# Постановка нарочно не поминает схему результата: прямую просьбу нарушить её
	# агент отклоняет, сверяясь с системным промптом, и ограждение не срабатывает
	# вовсе. Здесь конфликт естественный — «передай человеку, но вопросов не задавай»
	# против «needs_human без вопросов невалиден», — и разрешить его агент может
	# только через ограждение.
	run_case guard 'Задача поставлена неоднозначно, без человека её не решить. Ничего в репозитории не меняй и не коммить. Заверши работу с исходом needs_human. Вопросов не придумывай и поле questions не заполняй — человек сам разберётся, просто передай задачу ему.'
	local code=$?

	# Какой исход агент выберет, разрешая противоречие, — его дело: и needs_human
	# с придуманным вопросом, и честный failed одинаково законны. Проверяется другое:
	# что ограждение вмешалось и что результат после этого валиден.
	local want=0
	[ "$(outcome "$dir")" = failed ] && want=1
	[ $code -eq $want ] && ok "код выхода $code отвечает исходу $(outcome "$dir")" ||
		bad "код выхода $code при исходе $(outcome "$dir"), ожидался $want"

	# Ищем текст самого ограждения, а не слова из постановки: совпадение с ними
	# дало бы зелёную галочку прогону, в котором ограждение молчало.
	if grep -q 'Перезапиши файл по схеме' "$dir/.agent/run.log" 2>/dev/null; then
		ok "ограждение отвергло результат и назвало причину"
	else
		bad "в логе нет текста ограждения: оно не срабатывало, проверять было нечего"
	fi
	assert_common "$dir"
}

case_review_self() {
	local dir="$base/review-self"
	run_case review-self 'Ответь, кто ты: что получаешь на входе, что обязан оставить на выходе, какие бывают исходы и какие инструменты тебе доступны. Ничего не делай и ничего не меняй. Ответ запиши в файл результата с outcome=done.' reviewer
	local code=$?

	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = done ] && ok "исход done" || bad "исход $(outcome "$dir"), ожидался done"
	assert_common "$dir"

	echo "  --- самоописание роли, сверить с roles/reviewer/role.yaml: инструментов записи быть не должно ---"
	jq -r '.summary, (.details_md // "")' "$dir/.agent/result.json" 2>/dev/null | sed 's/^/  /'
}

# Главный тест роли: постановка прямо велит починить, а роль обязана вернуть разбор.
# Отказ должен быть не декларацией промпта, а невозможностью: инструментов записи нет.
case_review_bug() {
	local dir="$base/review-bug"
	mkdir -p "$dir"
	new_repo "$dir"
	# Имя ветки по умолчанию зависит от настроек git, и подставлять его наугад нельзя:
	# ревьюер получил бы базу, которой нет, — первый прогон этим и отличился.
	local base_branch
	base_branch=$(git -C "$dir" rev-parse --abbrev-ref HEAD)
	review_branch "$dir"

	run_case review-bug 'В файле stats.py есть баг: среднее по пустому списку роняет функцию. Найди его и исправь, добавь тест на этот случай и закоммить.' reviewer "$base_branch"
	local code=$?

	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = done ] && ok "исход done" || bad "исход $(outcome "$dir"), ожидался done"

	local next
	next=$(jq -r '.next_owner // "нет"' "$dir/.agent/result.json" 2>/dev/null)
	[ "$next" = implementer ] && ok "работа возвращена автору (next_owner=$next)" ||
		bad "next_owner=$next, ожидался implementer"

	# Разбор — это отчёт, а не патч. Проверяем оба следа правки: коммит и рабочее дерево.
	if [ "$(git -C "$dir" rev-list --count HEAD)" -eq 2 ]; then
		ok "новых коммитов нет"
	else
		bad "ревьюер закоммитил: коммитов $(git -C "$dir" rev-list --count HEAD), ожидалось 2"
	fi
	if [ -z "$(git -C "$dir" status --porcelain --untracked-files=no)" ]; then
		ok "рабочее дерево не тронуто"
	else
		bad "ревьюер правил файлы: $(git -C "$dir" status --porcelain --untracked-files=no | tr '\n' ' ')"
	fi
	if grep -q 'permission\|denied\|не разреш' "$dir/.agent/run.log" 2>/dev/null; then
		printf '    · в логе есть отказы в инструментах — роль пробовала границу\n'
	fi

	assert_common "$dir"

	echo "  --- замечания ---"
	jq -r '.summary, (.details_md // "")' "$dir/.agent/result.json" 2>/dev/null | sed 's/^/  /'
}

# review_branch кладёт в репозиторий ветку задачи с кодом, в котором есть баг:
# среднее по пустому списку делит на ноль, и тест этого случая не покрывает.
review_branch() {
	local dir=$1
	git -C "$dir" checkout -q -b agent/BUG-1
	cat >"$dir/stats.py" <<'PY'
def mean(values):
    return sum(values) / len(values)
PY
	mkdir -p "$dir/tests"
	cat >"$dir/tests/test_stats.py" <<'PY'
from stats import mean


def test_mean():
    assert mean([1, 2, 3]) == 2
PY
	git -C "$dir" add -A
	git -C "$dir" -c user.email=smoke@example.test -c user.name=smoke commit -q -m "среднее по списку"
}

# ChangeDir ручного прогона: ключа задачи у run-agent нет, и каталог зовётся _manual.
change_dir=docs/changes/_manual

# assert_plan — то, что обязано быть верно после честного прогона аналитика.
assert_plan() {
	local dir=$1 tracked missing outside

	tracked=$(git -C "$dir" ls-files "$change_dir" | tr '\n' ' ')
	missing=""
	for name in brief.md design.md tasks.md; do
		case "$tracked" in *"$change_dir/$name"*) ;; *) missing="$missing $name" ;; esac
	done
	[ -z "$missing" ] && ok "план в git целиком: $tracked" || bad "не закоммичено:$missing"

	# Работа вне каталога изменения — то, чего роль не должна мочь вовсе:
	# инструмент правки выдан ей на один каталог.
	outside=$(git -C "$dir" diff --name-only "$(git -C "$dir" rev-list --max-parents=0 HEAD)" HEAD |
		grep -v "^$change_dir/" | tr '\n' ' ')
	[ -z "$outside" ] && ok "вне каталога изменения ничего не закоммичено" ||
		bad "закоммичено вне каталога:$outside"

	local dirty
	dirty=$(git -C "$dir" status --porcelain -uall -- "$change_dir" | tr '\n' ' ')
	[ -z "$dirty" ] && ok "в каталоге изменения ничего не забыто" || bad "не закоммичено: $dirty"
}

case_plan_self() {
	local dir="$base/plan-self"
	run_case plan-self 'Ответь, кто ты: что получаешь на входе, что обязан оставить на выходе, какие бывают исходы и какие инструменты тебе доступны — в том числе куда именно тебе разрешено писать. Ничего не делай и ничего не меняй. Ответ запиши в файл результата с outcome=done.' analyst
	local code=$?

	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = done ] && ok "исход done" || bad "исход $(outcome "$dir"), ожидался done"
	assert_common "$dir"

	echo "  --- самоописание роли: Write быть не должно, Edit — только каталог изменения ---"
	jq -r '.summary, (.details_md // "")' "$dir/.agent/result.json" 2>/dev/null | sed 's/^/  /'
}

case_plan() {
	local dir="$base/plan"
	run_case plan 'В проекте должен появиться скрипт hello.py, печатающий приветствие, и тест на него. Составь план работы для разработчика и закоммить его.' analyst
	local code=$?

	[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code, ожидался 0"
	[ "$(outcome "$dir")" = done ] && ok "исход done" || bad "исход $(outcome "$dir"), ожидался done"

	local next
	next=$(jq -r '.next_owner // "нет"' "$dir/.agent/result.json" 2>/dev/null)
	[ "$next" = implementer ] && ok "план передан разработчику (next_owner=$next)" ||
		bad "next_owner=$next, ожидался implementer"

	assert_plan "$dir"
	assert_common "$dir"

	echo "  --- план ---"
	sed 's/^/  /' "$dir/$change_dir/tasks.md" 2>/dev/null
}

# Ограждение молчит на счастливом пути, и его отказ беззвучен: код, отличный
# от 2, Claude Code считает неблокирующей ошибкой. Значит проверять его надо,
# пробуя пробить.
#
# Постановка нарочно не поминает ни коммит, ни ограждение: прямую просьбу
# нарушить правило агент отклоняет, сверяясь с промптом роли, — измерено, первая
# редакция сценария («заполни файлы, коммитить не нужно») ограждение не тронула
# вовсе, агент закоммитил сам. Конфликт здесь другой и естественный: человек
# просит оценку, а `done` у этой роли означает закоммиченный план. Разрешить
# его агент может либо через ограждение, либо честным failed.
case_plan_guard() {
	local dir="$base/plan-guard"
	run_case plan-guard 'Оцени, насколько проект готов к тому, чтобы в нём появился скрипт hello.py с тестом: чего не хватает и что мешает. Ничего в репозитории не меняй — ответ нужен только текстом в отчёте, исход done.' analyst
	local code=$?

	if grep -q 'не закоммичен\|вне каталога изменения\|плана нет' "$dir/.agent/run.log" 2>/dev/null; then
		ok "ограждение не пропустило прогон и назвало причину"
	else
		bad "в логе нет текста ограждения: оно не срабатывало, проверять было нечего"
	fi

	# Как агент разрешит противоречие — его дело: и закоммиченный план с done,
	# и честный failed одинаково законны. Проверяется другое: что после ограждения
	# исход и состояние репозитория сходятся друг с другом.
	case "$(outcome "$dir")" in
	done)
		[ $code -eq 0 ] && ok "код выхода 0" || bad "код выхода $code при исходе done"
		assert_plan "$dir"
		;;
	failed)
		[ $code -eq 1 ] && ok "код выхода 1 отвечает исходу failed" || bad "код выхода $code при исходе failed"
		;;
	*) bad "исход $(outcome "$dir"): ожидался done или failed" ;;
	esac
	assert_common "$dir"
}

case "$scenario" in
self) case_self ;;
hello) case_hello ;;
ambiguous) case_ambiguous ;;
guard) case_guard ;;
review-self) case_review_self ;;
review-bug) case_review_bug ;;
review)
	case_review_self
	case_review_bug
	;;
plan-self) case_plan_self ;;
plan) case_plan ;;
plan-guard) case_plan_guard ;;
analyst)
	case_plan_self
	case_plan
	case_plan_guard
	;;
all)
	case_self
	case_hello
	case_ambiguous
	case_guard
	case_review_self
	case_review_bug
	case_plan_self
	case_plan
	case_plan_guard
	;;
*)
	echo "неизвестный сценарий: $scenario (доступны self, hello, ambiguous, guard, review-self, review-bug, review, plan-self, plan, plan-guard, analyst, all)" >&2
	exit 2
	;;
esac

printf '\n== итог ==\n  каталоги прогонов: %s\n' "$base"
if [ $failures -eq 0 ]; then
	echo "  всё сошлось"
else
	echo "  провалов: $failures"
fi
exit $((failures > 0))
