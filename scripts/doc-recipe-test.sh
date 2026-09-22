#!/bin/sh
# Проверка документированного рецепта: пройти путь, который обещают README
# и docs/guide/quickstart.md, в одноразовом ${OFFICE_HOME} — без сети и без
# платного прогона. Ловится то, что уже однажды уехало в релиз: рецепт,
# умирающий на «projects.local.yaml не заведён», потому что `runner init`
# кладёт только образцы.
#
#   sh scripts/doc-recipe-test.sh
#
# Гоняется через OFFICE_CONFIG_ROOT (клон), а не путём релизной поставки —
# это единственный режим, в котором ограждение validate-result собирается
# без -tags release. Значит, харнесс доказывает путь разработки, а не путь
# установленного релиза: office-init и распакованный бинарник им не пройдены.
#
# Агент подменён: на PATH кладётся поддельный `claude`, который пишет
# .agent/result.json и выходит нулём. Этого довольно — результат раннер читает
# из рабочей папки (internal/runner/agentio.go, ReadResult), а цену и причину
# конца прогона берёт из лога разбором адаптера, и нечитаемый лог означает
# «неизвестно», а не отказ (internal/runagent/runagent.go, fromLog).
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

home="$work/home"
export OFFICE_HOME="$home"
# Офис — из клона: ограждение validate-result в этом режиме собирается
# go build'ом, а сборка без -tags release ограждений не несёт вовсе
# и в режиме поставки отказала бы (internal/runner/validator.go,
# EnsureValidator). На сам `runner init` режим не влияет: образцы он берёт
# из поставки в бинарнике в любом. Это же ограничивает то, что харнесс
# доказывает: он не проходит путём релизной поставки (office-init,
# распакованный бинарник), только путём клона для разработки.
export OFFICE_CONFIG_ROOT="$root"
# Кред поддельный: настоящего агента здесь нет, но без переменной адаптер
# отказывается собирать запуск (internal/adapters/claude/adapter.go, credential).
export ANTHROPIC_API_KEY=поддельный-кред
unset CLAUDE_CODE_OAUTH_TOKEN || :

runner="$work/bin/runner"
( cd "$root" && go build -o "$runner" ./cmd/runner ) || fail "раннер не собрался"

# 1. Хозяйство: пять файлов и каталог заданий, код 0.
"$runner" init > "$work/out-init" 2>&1 || fail "init: $(cat "$work/out-init")"
for f in projects.local.example.yaml \
         tracker.example.yaml \
         scheduler/local.office.runner.plist \
         scheduler/office-runner.service \
         scheduler/office-runner.timer; do
  [ -f "$home/$f" ] || fail "init не положил $f"
done
[ -d "$home/scheduler" ] || fail "init не завёл scheduler/"
grep -q 'projects.local.yaml' "$work/out-init" || fail "init не сказал, куда копировать образец проектов"
grep -q 'tracker.yaml' "$work/out-init" || fail "init не сказал, куда копировать образец трекера"
echo "ok: runner init"

# 2. Репозиторий проекта-клиента — тот же рецепт, что в README: пустого мало,
# ветку задачи раннер ответвляет от origin/<default_branch>.
client="$work/client.git"
git init -q --bare -b master "$client" || fail "git init --bare не задался"
git clone -q "$client" "$work/client" || fail "git clone не задался"
git -C "$work/client" -c user.name=you -c user.email=you@local commit -q --allow-empty -m init \
  || fail "коммит в клиенте не задался"
git -C "$work/client" push -q origin master || fail "push в client.git не задался"
echo "ok: репозиторий клиента"

# 3. Образец под рабочим именем и ровно четыре правки — это и есть проверяемое
# утверждение: образец обещает, что больше править нечего.
example="$home/projects.local.example.yaml"
cfg="$home/projects.local.yaml"
cp "$example" "$cfg" || fail "образец projects.local.example.yaml не скопирован"
sed -e 's|^PROJ:|OFF:|' \
    -e "s|^  repo_url: .*|  repo_url: $client|" \
    -e 's|^  default_branch: .*|  default_branch: master|' \
    -e 's|^  tracker: .*|  tracker: mock|' \
    "$cfg" > "$cfg.new"
mv "$cfg.new" "$cfg" || fail "правки projects.local.yaml не сохранены"
# diff внутри пайпа под set -eu: пайп берёт код возврата у grep, а не у diff,
# и «0 совпадений» (не найден правленый паттерн) выглядела бы неотличимо от
# «diff сам отказал» (код 2 — не тот файл, нет доступа). Разводим коды разных
# бед: diff_status — только для diff, edits — только для содержимого diff.
diff "$example" "$cfg" > "$work/diff.out" 2>&1 && diff_status=0 || diff_status=$?
[ "$diff_status" -le 1 ] || fail "diff между $example и $cfg отказал (код $diff_status): $(cat "$work/diff.out")"
edits=$(grep -c '^<' "$work/diff.out" || :)
[ "$edits" = 4 ] || fail "правок в projects.local.yaml $edits, документация обещает четыре"
echo "ok: образец проектов правится четырьмя значениями"

# 4. Задача заведена и видна: раскладка конфигурации называет рабочий файл,
# а не образец, и доска показывает задачу.
"$runner" mock add OFF-1 --status Analysis \
  --summary "Добавить hello.py" \
  --description "Создай hello.py, печатающий приветствие, и тест к нему. Закоммить." \
  > "$work/out-add" 2>&1 || fail "mock add: $(cat "$work/out-add")"
"$runner" ls > "$work/out-ls" 2>&1 || fail "ls: $(cat "$work/out-ls")"
grep -qF "$cfg" "$work/out-ls" || fail "раскладка не назвала $cfg: $(cat "$work/out-ls")"
grep -q 'OFF-1' "$work/out-ls" || fail "доска не показала OFF-1: $(cat "$work/out-ls")"
echo "ok: задача заведена и видна на доске"

# 5. Один тик с поддельным агентом: задача уезжает из Analysis в Ready.
fakebin="$work/fakebin"
mkdir -p "$fakebin"
cat > "$fakebin/claude" <<'FAKE'
#!/bin/sh
# Поддельный агент. Раннер запускает его в рабочей папке задачи
# (internal/backends/local/local.go: cmd.Dir = l.Workdir), поэтому файл
# результата пишется относительно текущего каталога. Форма — runner.Result
# (internal/runner/agentio.go): обязательны outcome, summary и next_owner;
# questions только у needs_human и split, blocker только у blocked.
# next_owner именно implementer: у analyst'а карта by_next_owner названа явно,
# и значение вне карты уехало бы к человеку, а не в Ready (office/workflow.yaml).
#
# Аргументы и stdin не подделка ради подделки, а проверка контракта: реальный
# раннер зовёт claude с десятком флагов и шлёт промпт в stdin, а не позиционным
# аргументом (internal/adapters/claude/adapter.go). Пустой вызов — регресс
# в сборке argv, а не рабочий сценарий, и молчать о нём harness не должен.
[ "$#" -gt 0 ] || { echo "поддельный claude: вызван без аргументов" >&2; exit 1; }
prompt="$(cat)"
[ -n "$prompt" ] || { echo "поддельный claude: пустой stdin (промпт не пришёл)" >&2; exit 1; }
mkdir -p .agent
cat > .agent/result.json <<'JSON'
{
  "outcome": "done",
  "summary": "поддельный прогон: план записан",
  "next_owner": "implementer"
}
JSON
exit 0
FAKE
chmod 0755 "$fakebin/claude"
[ -x "$fakebin/claude" ] || fail "поддельный claude не создан или не исполняем: $fakebin/claude"

PATH="$fakebin:$PATH" "$runner" tick --backend local > "$work/out-tick" 2>&1 \
  || fail "tick: $(cat "$work/out-tick")"
"$runner" mock show OFF-1 > "$work/out-show" 2>&1 || fail "mock show: $(cat "$work/out-show")"
head -1 "$work/out-show" | grep -q 'Ready' \
  || fail "задача не сдвинулась в Ready: $(head -1 "$work/out-show")"
echo "ok: тик провёл задачу из Analysis в Ready"

echo "все пять шагов рецепта прошли"
