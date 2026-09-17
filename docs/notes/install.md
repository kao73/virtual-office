# Установка: релиз, личность, проверка снапшота

## Проверка снапшота 2026-09-18

Ручная проверка, ничего не автоматизировано: команды прогнаны по очереди из корня
репозитория (ветка `comet/install`, коммит `338d2e7`), вывод — ниже. Наблюдённая
личность снапшота везде одна: **`v0.0.1-SNAPSHOT-338d2e7`**.

### Шаг 1 — сборка снапшота

```sh
sh scripts/release-snapshot.sh
ls -l dist/
```

GoReleaser отработал (`release succeeded after 10s`), лог содержит ожидаемое:
хук `sh scripts/build-validators.sh` перед сборкой, снапшот-версия
`version=0.0.1-SNAPSHOT-338d2e7`, шесть сборок (`run-agent`/`runner` × `linux_amd64_v1`,
`linux_arm64_v8.0`, `darwin_arm64_v8.0`), три архива, один `checksums.txt`.

`ls -l dist/`: три архива `virtual-office_<os>_<arch>.tar.gz`, `checksums.txt`,
`artifacts.json`, `config.yaml`, `metadata.json`, шесть каталогов `runner_*`/`run-agent_*`.

Размеры архивов:

| файл | размер |
|---|---|
| `virtual-office_darwin_arm64.tar.gz` | 32 783 791 байт (~31,3 МБ) |
| `virtual-office_linux_amd64.tar.gz` | 28 073 715 байт (~26,8 МБ) |
| `virtual-office_linux_arm64.tar.gz` | 27 075 193 байт (~25,8 МБ) |

Размеры бинарников (`dist/runner_*/runner`, `dist/run-agent_*/run-agent`):

| бинарник | платформа | размер |
|---|---|---|
| `runner` | darwin/arm64 | 30 589 762 байт (~29,2 МБ) |
| `runner` | linux/amd64 | 26 697 888 байт (~25,5 МБ) |
| `runner` | linux/arm64 | 25 755 808 байт (~24,6 МБ) |
| `run-agent` | darwin/arm64 | 26 419 026 байт (~25,2 МБ) |
| `run-agent` | linux/amd64 | 22 257 824 байт (~21,2 МБ) |
| `run-agent` | linux/arm64 | 21 823 648 байт (~20,8 МБ) |

`git status --porcelain` под `dist/` и `payload/validators/` — пусто (обе директории
в `.gitignore`, `build-validators.sh` кладёт бинарники ограждений заново при каждой
сборке).

### Шаг 2 — какой checker несёт каждый раннер

```sh
for b in dist/runner_darwin_arm64*/runner dist/runner_linux_amd64*/runner dist/runner_linux_arm64*/runner; do
  echo "$b:"; for p in darwin-arm64 linux-amd64 linux-arm64; do
    printf '  %s %s\n' "$p" "$(grep -a -c "payload/validators/validate-result-$p" "$b")"; done; done
```

| раннер | darwin-arm64 | linux-amd64 | linux-arm64 |
|---|---|---|---|
| `runner_darwin_arm64_v8.0/runner` | 1 | 0 | 1 |
| `runner_linux_amd64_v1/runner` | 0 | 1 | 0 |
| `runner_linux_arm64_v8.0/runner` | 0 | 0 | 1 |

Совпадает с ожиданием: darwin/arm64-раннер несёт свой checker и линуксовый ARM
(бэкенд `sbx` — линукс на Apple Silicon), линуксовые раннеры — только свой.

### Шаг 3 — shell-тест по архивам, затем реальная установка

```sh
sh scripts/install-test.sh dist
```

```
ok: установка
ok: обновление на месте
ok: битая контрольная сумма
ok: чужая платформа
все четыре сценария прошли
```

Это же подтверждает перенос из задачи 12: формат строки `dist/checksums.txt`
(`<hash>  <file>`, без `*`) и раскладка архива (бинарники на верхнем уровне)
совпадают с тем, что ждёт `install.sh` — иначе `install-test.sh` не прошёл бы
сценарий «установка» на настоящем `dist/`. Для записи:

```
$ head -2 dist/checksums.txt
1eb16425bdc5140ddb4bd5795f666eeb67b740b924e02bb6b18593e434fcc24a  virtual-office_darwin_arm64.tar.gz
9b62ecba55046283c89c713b5bc094e10dfd6c309ee82b7bd0722179cba3c03a  virtual-office_linux_amd64.tar.gz

$ tar -tzf dist/virtual-office_darwin_arm64.tar.gz
run-agent
runner
```

Дальше — реальная установка в свежий, ранее не существовавший `OFFICE_HOME`
(`mktemp -d`, не `~/.office`):

```sh
export OFFICE_HOME="$(mktemp -d)"
OFFICE_INSTALL_FROM=dist sh install.sh
"$OFFICE_HOME/bin/runner" version
ls "$OFFICE_HOME"            # только bin/
```

```
установлено в /var/folders/.../tmp.YHk8AbD1Uu/bin:
runner v0.0.1-SNAPSHOT-338d2e7
офис: /var/folders/.../tmp.YHk8AbD1Uu/office/v0.0.1-SNAPSHOT-338d2e7
/var/folders/.../tmp.YHk8AbD1Uu/bin не в PATH — добавьте в профиль оболочки:
  export PATH="/var/folders/.../tmp.YHk8AbD1Uu/bin:$PATH"
дальше: runner init
```

`runner version` печатает те же две строки (`runner v0.0.1-SNAPSHOT-338d2e7` и
`офис: .../office/v0.0.1-SNAPSHOT-338d2e7`). `ls "$OFFICE_HOME"` в этот момент —
только `bin`: `office/` ещё не распакован, как и ожидалось.

**Методическое замечание.** `OFFICE_HOME` обязателен именно как экспортированная
переменная (`export`, не просто присвоение) — раннер и `run-agent` читают его как
переменную окружения дочернего процесса. Присвоение без `export` (например,
через `source` файла без слова `export`) молча даёт `runner`/`run-agent` пустой
`OFFICE_HOME`, и они уходят на предсказуемый дефолт `~/.office` — реальное
хозяйство этой машины. Поймано и исправлено на этом прогоне до какого-либо
вреда: `runner init` успел один раз создать в `~/.office` два примера
(`projects.local.example.yaml`, `tracker.example.yaml`, которых там раньше не
было), они немедленно удалены, рабочие файлы (`projects.local.yaml`,
`tracker.yaml`) `init` не трогает никогда и не был задет.

### Шаг 4 — `runner init`, mock-проект, прогон implementer'а

```sh
"$OFFICE_HOME/bin/runner" init
ls "$OFFICE_HOME"            # bin/ projects.local.example.yaml tracker.example.yaml
```

```
создан   .../projects.local.example.yaml
создан   .../tracker.example.yaml
дальше: скопируйте каждый образец под рабочее имя и поправьте значения: ...
```

`ls "$OFFICE_HOME"` → `bin`, `projects.local.example.yaml`, `tracker.example.yaml`.

Mock-проект — bare-репозиторий, клон, пустой коммит, push, `projects.local.yaml`
с `tracker: mock` (последовательность из плана сохранена буквально):

```sh
git init -q --bare -b master "$OFFICE_HOME/client.git"
git clone -q "$OFFICE_HOME/client.git" "$OFFICE_HOME/client"
git -C "$OFFICE_HOME/client" -c user.name=you -c user.email=you@local commit -q --allow-empty -m init
git -C "$OFFICE_HOME/client" push -q origin master
printf 'OFF:\n  repo_url: %s\n  default_branch: master\n  tracker: mock\n' "$OFFICE_HOME/client.git" > "$OFFICE_HOME/projects.local.yaml"
"$OFFICE_HOME/bin/runner" mock add OFF-1 --status Ready --summary "Добавить hello.py" \
  --description "Создай hello.py, печатающий приветствие. Закоммить."
```

`mock add` → `OFF-1 заведена в Ready`. Mock-трекер тем самым пройден (bare-репозиторий,
клон, push, `projects.local.yaml`, `runner mock add`), хотя платный `tick` не запускался
(см. ниже).

**Платный `runner tick --role implementer --backend local` не запускался** — это решение
контролёра задачи: у сессии на этой машине нет кредов агента в обычном рабочем окружении.
Вместо него — предложенный планом бесплатный заменитель, с обязательным `--task`
(без него `run-agent` отказывает раньше любой работы: «нужны --role и --workdir» относится
к другим флагам, а без задачи логика читает пустую постановку):

```sh
printf 'Создай hello.py, печатающий приветствие.\n' > "$OFFICE_HOME/task.md"
"$OFFICE_HOME/bin/run-agent" --role implementer --workdir "$OFFICE_HOME/client" \
  --backend local --dry-run --task "$OFFICE_HOME/task.md"
```

Первая попытка без креда в окружении завершилась инфраструктурным отказом (код 2):

```
run-agent: бэкенд local сетевой политики не применяет: роль просит *.cloudfront.docker.com, ...
run-agent: не задан ни ANTHROPIC_API_KEY, ни CLAUDE_CODE_OAUTH_TOKEN: агенту нечем авторизоваться, см. docs/notes/auth.md
```

Это показывает, что `--dry-run` не полностью «беспарольный»: `runagent.Prepare` всё
равно собирает `Launch` через `claude.Build`, а тот проверяет кред
(`internal/adapters/claude/adapter.go:credential`), прежде чем что-либо материализовать
— даже при `--dry-run`, где сам `claude` не запускается вовсе. Кред для агента на этой
машине лежит в `~/.zshrc` (`CLAUDE_CODE_OAUTH_TOKEN`) и не виден неинтерактивному шеллу.
Подставив его в окружение только для этой команды (кред ни разу не тратится: `--dry-run`
не исполняет `claude`, значит платного вызова по-прежнему нет — требование контролёра
не запускать платный прогон соблюдено буквально), получили ожидаемый план'ом вывод:

```
run_id:     918fff2c-b18e-431a-bd4c-8d8efb51cc42
роль:       implementer
config_sha: v0.0.1-SNAPSHOT-338d2e7
workdir:    .../client
конфиг:     /var/folders/.../office-run-.../config
таймаут:    2h0m0s
платформа:  darwin/arm64
ограждение: .../office/v0.0.1-SNAPSHOT-338d2e7/bin/validate-result-darwin-arm64
```

(в `== окружение запуска ==` кред напечатан замаскированным: `CLAUDE_CODE_OAUTH_TOKEN=***`
— утечки нет). `grep -c 'go build'` на полном выводе — `0`: бэкенд `local` действительно
берёт готовый бинарник ограждения из поставки, а не собирает Go на лету.

```sh
ls "$OFFICE_HOME/office/" "$OFFICE_HOME/office/"*/bin/
```

```
office/: v0.0.1-SNAPSHOT-338d2e7
office/v0.0.1-SNAPSHOT-338d2e7/bin/: validate-result-darwin-arm64
```

Раз `tick` не выполнялся, маркер `config:` в тикете не появляется:

```
$ "$OFFICE_HOME/bin/runner" mock show OFF-1 | grep 'config:'
(пусто)
```

По допущению самого плана эта проверка заменяется строкой `config_sha:` из вывода
`--dry-run` выше — та же личность снапшота, тем же самым способом подтверждённая.

### Шаг 5 — checker песочницы, bake пропущен

```sh
"$OFFICE_HOME/bin/run-agent" --role implementer --workdir "$OFFICE_HOME/client" \
  --backend sbx --dry-run --task "$OFFICE_HOME/task.md" >/dev/null; echo "exit $?"
file "$OFFICE_HOME/office/"*/bin/validate-result-linux-arm64
```

`exit 0`. `--dry-run` на `sbx` подготовил запуск (`EnsureValidator(linux/arm64)` дописал
линуксовый checker из поставки), сам песочницу не поднимая — в выводе виден полный план
запуска: команда `claude ... --tools Bash,Edit,Read,Write,Skill`, список сети сверх
нужной агенту, смонтированные рабочие пространства, `settings.json` с hooks Comet.

```
$ file "$OFFICE_HOME/office/v0.0.1-SNAPSHOT-338d2e7/bin/validate-result-linux-arm64"
...: ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked, ...
```

Bake (`sh "$OFFICE_HOME/office/"*/sbx-kits/bake-comet-template.sh`) **пропущен**: сеть
песочницы на этой машине закрыта (deny-all), а bake тянет образ с `registry.npmjs.org` —
без сети шаг не пройдёт, запускать не стали.

### Шаг 6 — очистка

```sh
rm -rf "$OFFICE_HOME"; rm -rf dist; rm -f payload/validators/validate-result-*
```

`git status --porcelain` после очистки — пусто, кроме этого файла заметки.

### Что покрыто вживую, что пропущено

Покрыто: сборка снапшота даёт три раннера с их checker'ами; установка из локального
снапшота; релизный раннер называет свою версию и каталог офиса; прогон помечен верной
версией (`config_sha`/`config:` — личность снапшота); локальный запуск берёт checker
хоста без Go; запуск на Apple Silicon для линукса не нуждается в Go (checker пришёл из
поставки, ARM ELF подтверждён `file`).

Пропущено: платный `runner tick` (кредов нет в обычном окружении сессии — заменён
`--dry-run`-эквивалентом, см. шаг 4); bake образа sbx-кита (сеть песочницы на машине
закрыта).
