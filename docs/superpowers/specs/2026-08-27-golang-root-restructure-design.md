---
status: final
---

# Go-пакеты под `internal/`, точки входа — в `cmd/` — дизайн

## Проблема

Одиннадцать Go-пакетов модуля `github.com/kao73/virtual-office` лежат прямо
в корне репозитория: `adapters`, `backends`, `budget`, `forge`, `guard`,
`ledger`, `pipeline`, `runagent`, `runner`, `tracker`, `workspace`. Вперемешку
с ними в корне лежат не-Go директории (`roles`, `skills`, `hooks`, `scripts`,
`bootstrap`, `docs`) и конфиг-файлы (`workflow.yaml`, `projects.yaml`,
`budgets.yaml`, `tracker.example.yaml`). Итого около 19 записей верхнего
уровня — структура репозитория не читается с одного взгляда.

Отдельно от общего мусора: три точки входа (`func main()`) лежат в
`runner/cmd/{runner,run-agent,validate-result}` — то есть каталог `cmd`
вложен внутрь пакета `runner`, хотя `run-agent` зависит от `ledger`,
`runagent`, `runner`, `tracker`, а `validate-result` — от `guard` и `runner`.
Ни один из них не принадлежит пакету `runner` содержательно — размещение
историческое, не архитектурное.

## Объём

Только Go-пакеты и точки входа. Не-Go директории (`roles`, `skills`, `hooks`,
`scripts`, `bootstrap`, `docs`) и конфиг-файлы верхнего уровня остаются как
есть — они не код, а контент/конфигурация офиса, и в эту работу не входят.

## Целевая структура

```
cmd/
├── runner/main.go            (было runner/cmd/runner/main.go)
├── run-agent/main.go         (было runner/cmd/run-agent/main.go)
└── validate-result/main.go   (было runner/cmd/validate-result/main.go)
internal/
├── adapters/        (+claude/)
├── backends/        (+sbx/, local/)
├── budget/
├── forge/
├── guard/
├── ledger/
├── pipeline/
├── runagent/
├── runner/          (без cmd/ — тот переехал наверх)
├── tracker/         (+mock/, jira/)
└── workspace/
```

Имена пакетов не меняются — только путь. Корень теряет 11 директорий и
получает взамен `cmd/` и `internal/`.

## Почему `internal/`, а не `pkg/`

Модуль — самостоятельное приложение, его никто не импортирует как библиотеку.
`internal/` даёт то же разделение «код vs остальное», но вдобавок запрещает
внешний импорт на уровне компилятора Go — гарантия, а не соглашение. `pkg/`
здесь ничего не выигрывает.

## Почему `cmd/` — на верхнем уровне, а не в `runner/cmd/`

Верхнеуровневый `cmd/` с несколькими подкомандами, зависящими от разных
внутренних пакетов, — стандартное Go-размещение. Оставлять `cmd/` внутри
`runner/` означало бы держать чужие точки входа внутри пакета, к которому
они не относятся по смыслу.

## Что не делаем (явно вне рамок)

- Не группируем пакеты внутри `internal/` по слоям (`internal/core/`,
  `internal/integrations/` и т.п.) — это отдельный, более спорный вопрос
  дизайна пакетов, которого не требует заявленная проблема.
- Не разбиваем сам `runner` (34 файла) на более мелкие пакеты.
- Не переименовываем ни один пакет.
- Не трогаем `go.mod` (module path не меняется).

## Механика переноса

Один коммит (или короткая серия коммитов на одной ветке) — промежуточное
состояние всё равно не соберётся, дробить незачем.

1. `git mv` каждого из 11 пакетов в `internal/<имя>` (сохраняет историю
   файлов); `runner/cmd/*` после переноса `runner` → `internal/runner`
   разъезжается дальше по `cmd/runner`, `cmd/run-agent`, `cmd/validate-result`,
   и пустой `internal/runner/cmd/` убирается.
2. Массовая правка импортов: `github.com/kao73/virtual-office/<pkg>` →
   `.../internal/<pkg>` во всех `*.go`.
3. **Обнаруженная ловушка**, которую правка импортов не поймает:
   `runner/validator.go:19` —
   `const validatorPkg = "./runner/cmd/validate-result"`. Это не импорт,
   а строковый литерал, который `EnsureValidator` передаёт в `go build` при
   сборке бинарника ограждения для песочницы (см. `docs/contracts/agent-io.md`
   про `validate-result` и хук `Stop`). Не поправить — ограждение перестанет
   собираться, хук `Stop` перестанет работать. Проверено grep'ом по репозиторию
   на другие такие литералы — этот единственный.
4. Правка build-целей в `bin/runner` и `bin/run-agent`
   (`./runner/cmd/runner` → `./cmd/runner`, аналогично для `run-agent`).
5. Правка путей в «живых» документах (ниже).

## Документация

**Правим** (текущее состояние системы, актуальность важна):
- `README.md`, раздел «Где что лежит» — основной список путей.
- `docs/DESIGN.md:55` — `adapters/claude`, `adapters/codex` →
  `internal/adapters/claude`, `internal/adapters/codex`.
- `docs/contracts/role-sandbox-permissions.md` и
  `docs/contracts/tracker-protocol.md` — упоминания вида `tracker/config.go`,
  `adapters/claude/adapter.go`, `backends/sbx/sbx.go`, `tracker/jira`,
  `tracker/mock` как файловых путей (Go-квалифицированные имена вида
  `tracker.LoadProjects` не трогаем — они не зависят от import-пути).

**Не трогаем** (датированные записи о прошлом состоянии, не текущее):
`docs/notes/`, `docs/STAGE-*.md`, `docs/comet/`, `docs/openspec/`,
`docs/superpowers/specs/` и `docs/superpowers/plans/` (кроме этого файла),
а также `docs/DESIGN.md:163` (план MVP этапа 1 — исторический список).

## Проверка

- `go build ./...`
- `go vet ./...`
- `go test ./...`
- grep на отсутствие старых импорт-путей вида `virtual-office/<pkg>"` без
  `/internal/` перед именем пакета.
- `scripts/smoke.sh` не нужен — поведение не меняется, рефакторинг чисто
  структурный.

## Ветка

Обычная git-ветка `golang-root-restructure` от `master`, без Comet Classic:
изменение небольшое, механическое и не про поведение офиса — заводить под
него Open/Design/Build/Verify/Archive было бы процессом ради процесса.
