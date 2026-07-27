# План реализации этапа 0 «Фундамент» — скелет плагина

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Репозиторий `virtual-office` становится валидным плагином Claude Code и собственным маркетплейсом; установка проверена на демо-клиенте.

**Architecture:** Два манифеста в `.claude-plugin/` (plugin.json + marketplace.json c `source: "./"`), один скилл-маркер `office-about` в `skills/`, CHANGELOG и правка README. «Тест» каждого артефакта — `claude plugin validate . --strict` (red → green), финальная приёмка — реальная установка в демо-проект.

**Tech Stack:** Claude Code plugin system (манифесты JSON, SKILL.md с YAML-frontmatter), CLI `claude plugin`.

**Спека:** `docs/specs/2026-07-27-plugin-skeleton-design.md`.

## Global Constraints

- Имя плагина и маркетплейса: `virtual-office` (kebab-case, оба совпадают).
- Версия `0.1.0` живёт **только** в `plugin.json`; в записи `plugins[]` маркетплейса версии нет.
- Скиллы — в `skills/<имя>/SKILL.md` в корне репозитория (НЕ внутри `.claude-plugin/`).
- Язык документов и текстов — русский; идентификаторы, ключи конфигов — английские.
- Секретов в репозитории нет (конституция §6); ничего вперёд дорожной карты (YAGNI): ни `adapters/`, ни `templates/`, ни скиллов ролей.
- `git add` — только явным списком файлов, никогда `-A` (закон git-гигиены офиса, §9).
- Коммиты — в `master` (конвенция репозитория), после каждой задачи; сообщение — русское, с трейлером:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- После каждой задачи `claude plugin validate . --strict` обязан быть зелёным (exit 0).
- Рабочая директория всех команд — `/Users/aleksejkolesnikov/IdeaProjects/virtual-office`, если не сказано иное.

---

### Task 1: Манифест плагина `.claude-plugin/plugin.json`

**Files:**
- Create: `.claude-plugin/plugin.json`

**Interfaces:**
- Produces: имя `virtual-office` и версия `0.1.0` — на них ссылаются Task 2 (запись маркетплейса), Task 3 (текст скилла), Task 4 (CHANGELOG) и Task 5 (адрес установки `virtual-office@virtual-office`).

- [x] **Step 1: Проверить, что валидация падает без манифеста**

Run: `claude plugin validate . --strict; echo "exit: $?"`
Expected: ошибка (манифест не найден), exit ≠ 0.

- [x] **Step 2: Создать `.claude-plugin/plugin.json`**

```json
{
  "name": "virtual-office",
  "displayName": "Виртуальный IT-офис",
  "description": "Команда AI-агентов, ведущая разработку проекта-клиента через конвейер с фиксированными гейтами контроля владельца",
  "version": "0.1.0",
  "author": {"name": "kao73"}
}
```

- [x] **Step 3: Валидация зелёная**

Run: `claude plugin validate . --strict; echo "exit: $?"`
Expected: `✔ Validation passed` для plugin manifest, exit 0.

- [x] **Step 4: Commit**

```bash
git add .claude-plugin/plugin.json
git commit -m "feat: манифест плагина virtual-office 0.1.0 (этап 0)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Маркетплейс-манифест `.claude-plugin/marketplace.json`

**Files:**
- Create: `.claude-plugin/marketplace.json`

**Interfaces:**
- Consumes: имя плагина `virtual-office` из Task 1.
- Produces: маркетплейс `virtual-office` с плагином `source: "./"` — адрес установки `virtual-office@virtual-office` для Task 5.

- [x] **Step 1: Создать `.claude-plugin/marketplace.json`**

```json
{
  "name": "virtual-office",
  "description": "Маркетплейс виртуального IT-офиса: репозиторий одновременно плагин и его дистрибуция",
  "owner": {"name": "kao73"},
  "plugins": [
    {
      "name": "virtual-office",
      "source": "./",
      "description": "Команда AI-агентов, ведущая разработку проекта-клиента через конвейер с фиксированными гейтами контроля владельца"
    }
  ]
}
```

Ключевое: поля `version` в записи плагина **нет** — версия резолвится из `plugin.json` (закон версии из спеки).

- [x] **Step 2: Валидация зелёная (теперь проверяется и маркетплейс)**

Run: `claude plugin validate . --strict; echo "exit: $?"`
Expected: `✔ Validation passed`, exit 0. В выводе — валидация marketplace manifest.

- [x] **Step 3: Commit**

```bash
git add .claude-plugin/marketplace.json
git commit -m "feat: маркетплейс-манифест — репозиторий сам себе маркетплейс (Р-08)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Скилл-маркер `skills/office-about/SKILL.md`

**Files:**
- Create: `skills/office-about/SKILL.md`

**Interfaces:**
- Consumes: имя/версия из Task 1.
- Produces: скилл `virtual-office:office-about` — его наличие в инвентаре проверяет Task 5.

- [x] **Step 1: Создать `skills/office-about/SKILL.md`**

```markdown
---
name: office-about
description: Справка о плагине virtual-office — что такое виртуальный IT-офис, текущий этап дорожной карты, что уже работает и чего офис пока не умеет. Используй, когда спрашивают про установленный плагин virtual-office, «что за офис», его версию, статус или возможности.
---

# О виртуальном IT-офисе

Ты отвечаешь как справка установленного плагина `virtual-office`. Отвечай честно
и коротко, по фактам ниже; не выдумывай возможностей, которых нет.

## Что это

Виртуальный IT-офис — команда AI-агентов (Аналитик, Разработчик, AI-QA,
Делопроизводитель), ведущая разработку проекта-клиента по конвейеру
«идея → постановка → дизайн → код → приёмка → мерж» с фиксированными гейтами
контроля владельца. Субстрат офиса — реальный трекер проекта-клиента;
подключение к проекту — профиль `.office/profile.yaml`.

## Текущий статус

- Версия плагина: **0.1.0** (этап 0 «Фундамент» дорожной карты).
- Реализовано: скелет плагина — манифесты, этот справочный скилл.
- **Роли офиса ещё не реализованы.** Ни одна стадия конвейера не работает:
  нет ни визарда подключения (`office-init`), ни адаптеров трекеров, ни ролей.
  Плагин пока ничего не делает с проектом, в который установлен.
- Следующий этап — 1 «Делопроизводитель»: первая живая роль, jira-адаптер,
  профиль клиента, визард `office-init`.

## Где источник истины

Конституция офиса (архитектура, законы, журнал решений, дорожная карта) —
`docs/concept.md` в репозитории офиса; история версий — `CHANGELOG.md` там же.
Если вопрос выходит за рамки фактов выше — отвечай, что это ещё не определено
или не реализовано, и отсылай к конституции.
```

- [x] **Step 2: Валидация зелёная (frontmatter скилла проверяется)**

Run: `claude plugin validate . --strict; echo "exit: $?"`
Expected: `✔ Validation passed`, exit 0.

- [x] **Step 3: Инвентарь плагина видит скилл**

Run: `claude plugin details . 2>&1 | head -20`
Expected: в инвентаре компонентов — skill `office-about` (если `details` не принимает путь — пропустить шаг, проверка уйдёт в Task 5 после установки).

- [x] **Step 4: Commit**

```bash
git add skills/office-about/SKILL.md
git commit -m "feat: скилл-маркер office-about — справка офиса и проверка загрузки скиллов

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: CHANGELOG и статус в README

**Files:**
- Create: `CHANGELOG.md`
- Modify: `README.md:11-17` (секция «Статус»)

**Interfaces:**
- Consumes: версия `0.1.0` из Task 1.

- [x] **Step 1: Создать `CHANGELOG.md`**

```markdown
# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/);
версионирование — [semver](https://semver.org/lang/ru/): `0.x` до боевой
обкатки этапа 1, минор — на этап дорожной карты, патч — на исправления.

## [0.1.0] — 2026-07-27

### Added

- Скелет плагина Claude Code: манифест `.claude-plugin/plugin.json`
  (этап 0 дорожной карты, спека `docs/specs/2026-07-27-plugin-skeleton-design.md`).
- Маркетплейс-манифест `.claude-plugin/marketplace.json` — репозиторий
  одновременно плагин и его маркетплейс (Р-08).
- Скилл-маркер `office-about`: справка об офисе, его версии и текущем этапе;
  проверяет путь «установка → загрузка скилла» для этапа 1.
```

- [x] **Step 2: Обновить секцию «Статус» в README.md**

Заменить строки 11–17 (от `## Статус` до строки `(скелет плагина).` включительно) на:

```markdown
## Статус

**Концепт зафиксирован 2026-07-27** (журнал решений Р-01…Р-27), источник истины —
[docs/concept.md](docs/concept.md) (конституция), порядок работ — §12 «Дорожная карта».

**Этап 0 «Фундамент» реализован**: репозиторий — устанавливаемый плагин Claude Code
и собственный маркетплейс (версии — в [CHANGELOG.md](CHANGELOG.md), дизайн —
[docs/specs/](docs/specs/)). Роли офиса ещё не реализованы; следующий шаг —
этап 1 «Делопроизводитель».
```

Остальное содержимое README не трогать.

- [x] **Step 3: Валидация всё ещё зелёная**

Run: `claude plugin validate . --strict; echo "exit: $?"`
Expected: `✔ Validation passed`, exit 0.

- [x] **Step 4: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "docs: CHANGELOG 0.1.0 и статус этапа 0 в README

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Приёмка — установка в демо-клиента

**Files:**
- Create (вне репозитория): `~/IdeaProjects/office-demo/README.md` + git-init

**Interfaces:**
- Consumes: адрес `virtual-office@virtual-office` (Task 1–2), скилл `office-about` (Task 3).

- [x] **Step 1: Создать демо-клиента**

```bash
mkdir -p ~/IdeaProjects/office-demo
cd ~/IdeaProjects/office-demo
git init
printf '# office-demo\n\nГипотетический проект-клиент для проверки установки плагина virtual-office.\n' > README.md
git add README.md
git commit -m "init: демо-клиент для проверки плагина virtual-office"
```

Expected: пустой (кроме README) git-репозиторий — «клиент, пусть пустой» из §12.

- [x] **Step 2: Подключить маркетплейс с локального пути**

Run (из `~/IdeaProjects/office-demo`):
`claude plugin marketplace add /Users/aleksejkolesnikov/IdeaProjects/virtual-office`
Expected: маркетплейс `virtual-office` добавлен; `claude plugin marketplace list` его показывает.

- [x] **Step 3: Установить плагин в scope local**

Run (из `~/IdeaProjects/office-demo`):
`claude plugin install virtual-office@virtual-office --scope local`
Expected: установка успешна, версия 0.1.0.

- [x] **Step 4: Плагин и скилл видны**

Run: `claude plugin list` и `claude plugin details virtual-office`
Expected: плагин `virtual-office 0.1.0` установлен; в инвентаре — skill `office-about`.

- [x] **Step 5: Git демо-клиента чист**

Run (из `~/IdeaProjects/office-demo`): `git status --porcelain`
Expected: пусто, либо только неотслеживаемые файлы Claude-конфигурации, не предлагаемые к коммиту (scope `local` не наследил в отслеживаемых файлах).

- [x] **Step 6: Скилл работает в сессии демо-клиента**

Ручная проверка владельцем ИЛИ headless-прогон:
`cd ~/IdeaProjects/office-demo && claude -p "Что такое установленный плагин virtual-office и что он умеет?"`
Expected: ответ по фактам `office-about` — версия 0.1.0, этап 0, роли не реализованы.

- [x] **Step 7: Зафиксировать готовность этапа**

Отметить чекбоксы плана, доложить владельцу результат приёмки (вывод команд шагов 2–6).

---

## Self-Review (выполнено при написании)

- **Покрытие спеки:** артефакты 1–5 спеки → Task 1–4; «Проверка готовности» п.1–6 → Task 5 и шаги валидации в каждой задаче. Разделы «Вне скоупа» и «Риски» задач не требуют.
- **Плейсхолдеров нет:** весь контент файлов приведён дословно.
- **Согласованность имён:** `virtual-office`, `office-about`, `0.1.0`, `virtual-office@virtual-office` — единообразны во всех задачах.

## Отчёт о выполнении (2026-07-28)

Все задачи выполнены, приёмка на демо-клиенте пройдена. Отступления от плана:

1. **`CLAUDE.md` перенесён в `.claude/CLAUDE.md`** (коммит Task 1): strict-валидация
   плагина предупреждает о CLAUDE.md в корне плагина (он не поставляется клиентам).
   `./.claude/CLAUDE.md` — документированное равнозначное расположение проектной
   памяти (docs/en/memory.md); dev-инструкции сохранены, плагин чист.
2. **Валидация усилена**: при наличии обоих манифестов `validate .` проверяет только
   маркетплейс, поэтому с Task 2 гоняются оба пути:
   `claude plugin validate . --strict` и `claude plugin validate .claude-plugin/plugin.json --strict`.
3. **Дата релиза в CHANGELOG — 2026-07-28** (фактическая), не 2026-07-27 из текста плана.
4. Task 3 Step 3 (`plugin details` по пути) пропущен по предусмотренной планом ветке —
   CLI не принимает путь; скилл подтверждён в инвентаре после установки (Task 5).

Приёмка (Task 5): маркетплейс добавлен с локального пути; плагин 0.1.0 установлен
в `~/IdeaProjects/office-demo` со scope local; `plugin list`/`details` показывают
плагин и скилл `office-about`; `git status --porcelain` демо-клиента пуст;
headless-сессия в демо-клиенте ответила по фактам скилла (версия, этап 0,
«роли не реализованы»).
