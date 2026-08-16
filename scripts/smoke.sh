#!/usr/bin/env bash
# Smoke-тест локального бэкенда. Три сценария:
#
#   self       — роль описывает саму себя; ответ сверяется с role.yaml вручную
#   hello      — выполнимая задача, ожидается outcome=done и коммит
#   ambiguous  — намеренно неоднозначная задача, ожидается needs_human с вопросами
#
# Запуск:  scripts/smoke.sh [self|hello|ambiguous|all]
#
# Прогоны настоящие и стоят денег или лимита подписки. Кред берётся из окружения,
# см. docs/notes/auth.md. Рабочие каталоги не удаляются: по ним разбирают, что пошло не так.

set -uo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
scenario=${1:-all}
base=${SMOKE_DIR:-$(mktemp -d -t office-smoke)}

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
run_case() {
	local name=$1 task=$2 dir="$base/$1"

	printf '\n== %s ==\n  каталог: %s\n' "$name" "$dir"
	mkdir -p "$dir"
	new_repo "$dir"
	printf '%s\n' "$task" >"$base/$name-task.md"

	local started elapsed code
	started=$(date +%s)
	"$root/bin/run-agent" --role implementer --workdir "$dir" --task "$base/$name-task.md" --backend local
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

	if jq -e . "$dir/.agent/result.json" >/dev/null 2>&1; then
		ok "result.json разбирается"
	else
		bad "result.json отсутствует или невалиден"
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

	[ $code -eq 1 ] && ok "код выхода 1" || bad "код выхода $code, ожидался 1"
	[ "$(outcome "$dir")" = needs_human ] && ok "исход needs_human" || bad "исход $(outcome "$dir"), ожидался needs_human"
	assert_common "$dir"

	local questions
	questions=$(jq -r '.questions | length' "$dir/.agent/result.json" 2>/dev/null || echo 0)
	[ "$questions" -gt 0 ] && ok "вопросов задано: $questions" || bad "вопросов нет"

	echo "  --- вопросы ---"
	jq -r '.questions[]? | "  - " + .text' "$dir/.agent/result.json" 2>/dev/null
}

case "$scenario" in
self) case_self ;;
hello) case_hello ;;
ambiguous) case_ambiguous ;;
all)
	case_self
	case_hello
	case_ambiguous
	;;
*)
	echo "неизвестный сценарий: $scenario (доступны self, hello, ambiguous, all)" >&2
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
