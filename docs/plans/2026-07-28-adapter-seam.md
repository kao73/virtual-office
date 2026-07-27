# План реализации 1а «Шов» — профиль, адаптер трекера, конформанс-сьют

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Спека:** [docs/specs/2026-07-28-adapter-seam-design.md](../specs/2026-07-28-adapter-seam-design.md)

**Цель:** исполняемый шов офис↔клиент — Python CLI `office-adapter` с провайдерами
`yougile` и `jira`, схемой профиля клиента и конформанс-сьютом, зелёным против
YouGile-полигона (и против песочницы CRM3 — ручным смоуком).

**Архитектура:** «сэндвич» роль → CLI → `base.py` (фенс, маркировка Р-25, read-back)
→ провайдер → REST. Безусловные законы конституции живут в `base.py`; провайдеры
тонкие, знают только REST своего трекера. Конформанс-сьют параметризован провайдером
и гоняется против живого трекера.

**Стек:** Python ≥3.12 под uv; зависимости: `pyyaml`, `requests`, `jsonschema`; dev: `pytest`.

## Global Constraints

- Python `>=3.12`, проект управляется uv; все команды из `adapters/`: `uv run ...`.
- Зависимости ровно: `pyyaml>=6.0`, `requests>=2.32`, `jsonschema>=4.21`; dev: `pytest>=8.0`. Новых не добавлять.
- Маркер офисного контента — первая строка `[ai-office:<role>]`, роль по `^[a-z][a-z0-9_-]*$` (спека, Р-25).
- Коды выхода CLI: `0` успех; `1` usage/профиль; `2` трекер/сеть; `3` read-back разошёлся; `4` фенс; `5` способность отсутствует.
- Креды только из env: `YOUGILE_API_KEY`, `JIRA_API_TOKEN` + `JIRA_LOGIN`. Значения токенов не печатать и не логировать никогда.
- Сетевые ошибки и 5xx — ровно один ретрай, затем `TrackerError`.
- Абстрактные состояния (спека): `idea analysis design_gate ready_for_dev in_dev ai_qa merge_gate bookkeeping external_qa done needs_input`.
- Язык: документы и комментарии — русский; идентификаторы, ключи, сообщения ошибок — английский.
- Коммиты — русские, с префиксами `feat:`/`test:`/`docs:` как в истории репозитория.
- Секретов в репозитории нет; `polygons.yaml` содержит только id досок/колонок и имена статусов (не секреты).

---

### Task 1: Каркас uv-проекта и модель ошибок

**Files:**
- Create: `adapters/pyproject.toml`
- Create: `adapters/office_adapter/__init__.py`
- Create: `adapters/office_adapter/errors.py`
- Test: `adapters/tests/unit/test_errors.py`

**Interfaces:**
- Produces: иерархия `AdapterError(message, **details)` с атрибутами `code: str`,
  `exit_code: int`, методом `to_json() -> dict`; подклассы `UsageError(1)`,
  `ProfileError(1)`, `TrackerError(2)`, `VerificationFailed(3)`, `FenceViolation(4)`,
  `CapabilityMissing(5)`. Все последующие задачи бросают только их.

- [ ] **Step 1: pyproject и пакет**

`adapters/pyproject.toml`:

```toml
[project]
name = "office-adapter"
version = "0.1.0"
description = "Адаптер трекера виртуального IT-офиса: единственный шов офис-клиент"
requires-python = ">=3.12"
dependencies = [
  "pyyaml>=6.0",
  "requests>=2.32",
  "jsonschema>=4.21",
]

[project.scripts]
office-adapter = "office_adapter.cli:run"

[dependency-groups]
dev = ["pytest>=8.0"]

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["office_adapter"]

[tool.pytest.ini_options]
testpaths = ["tests"]
```

`adapters/office_adapter/__init__.py` — пустой файл.

- [ ] **Step 2: Failing-тест ошибок**

`adapters/tests/unit/test_errors.py`:

```python
from office_adapter.errors import (
    AdapterError, UsageError, ProfileError, TrackerError,
    VerificationFailed, FenceViolation, CapabilityMissing,
)


def test_exit_codes_match_spec():
    assert UsageError("x").exit_code == 1
    assert ProfileError("x").exit_code == 1
    assert TrackerError("x").exit_code == 2
    assert VerificationFailed("x").exit_code == 3
    assert FenceViolation("x").exit_code == 4
    assert CapabilityMissing("x").exit_code == 5


def test_to_json_shape():
    err = FenceViolation("card outside fence", card_id="X-1")
    assert err.to_json() == {
        "error": "fence_violation",
        "message": "card outside fence",
        "details": {"card_id": "X-1"},
    }


def test_all_are_adapter_errors():
    for cls in (UsageError, ProfileError, TrackerError,
                VerificationFailed, FenceViolation, CapabilityMissing):
        assert issubclass(cls, AdapterError)
```

- [ ] **Step 3: Убедиться, что тест падает**

Из `adapters/`: `uv run pytest tests/unit/test_errors.py -v`
Ожидание: FAIL (`ModuleNotFoundError: office_adapter.errors`).

- [ ] **Step 4: Реализация errors.py**

`adapters/office_adapter/errors.py`:

```python
"""Ошибки адаптера: стабильные коды и коды выхода процесса (спека, «Обработка ошибок»)."""
from __future__ import annotations


class AdapterError(Exception):
    code = "tracker_error"
    exit_code = 2

    def __init__(self, message: str, **details: object) -> None:
        super().__init__(message)
        self.message = message
        self.details = details

    def to_json(self) -> dict:
        return {"error": self.code, "message": self.message, "details": self.details}


class UsageError(AdapterError):
    code = "usage_error"
    exit_code = 1


class ProfileError(AdapterError):
    code = "profile_error"
    exit_code = 1


class TrackerError(AdapterError):
    code = "tracker_error"
    exit_code = 2


class VerificationFailed(AdapterError):
    code = "verification_failed"
    exit_code = 3


class FenceViolation(AdapterError):
    code = "fence_violation"
    exit_code = 4


class CapabilityMissing(AdapterError):
    code = "capability_missing"
    exit_code = 5
```

- [ ] **Step 5: Тест зелёный, коммит**

`uv run pytest tests/unit/test_errors.py -v` → PASS.

```bash
git add adapters/
git commit -m "feat: каркас office-adapter — uv-проект и модель ошибок (1а)"
```

---

### Task 2: Маркировка офисного контента (Р-25)

**Files:**
- Create: `adapters/office_adapter/marking.py`
- Test: `adapters/tests/unit/test_marking.py`

**Interfaces:**
- Consumes: `UsageError` из Task 1.
- Produces: `mark(role: str, body: str) -> str` (маркер первой строкой);
  `detect(body: str) -> str | None` (роль из маркера либо `None`).

- [ ] **Step 1: Failing-тесты**

`adapters/tests/unit/test_marking.py`:

```python
import pytest

from office_adapter.errors import UsageError
from office_adapter.marking import detect, mark


def test_mark_prepends_marker_line():
    assert mark("clerk", "hello\nworld") == "[ai-office:clerk]\nhello\nworld"


def test_roundtrip():
    assert detect(mark("clerk", "any text")) == "clerk"


def test_detect_none_for_plain_text():
    assert detect("Обычный коммент владельца") is None
    assert detect("") is None


def test_detect_marker_must_be_first_line():
    assert detect("text\n[ai-office:clerk]") is None


def test_invalid_role_rejected():
    with pytest.raises(UsageError):
        mark("Clerk!", "body")
```

- [ ] **Step 2: Падают** — `uv run pytest tests/unit/test_marking.py -v` → FAIL.

- [ ] **Step 3: Реализация**

`adapters/office_adapter/marking.py`:

```python
"""Маркировка офисного контента (Р-25): вшита в адаптер, роль не может её обойти."""
from __future__ import annotations

import re

from office_adapter.errors import UsageError

_ROLE_RE = re.compile(r"^[a-z][a-z0-9_-]*$")
_MARKER_RE = re.compile(r"^\[ai-office:([a-z][a-z0-9_-]*)\]$")


def mark(role: str, body: str) -> str:
    if not _ROLE_RE.match(role):
        raise UsageError(f"invalid role for office marker: {role!r}")
    return f"[ai-office:{role}]\n{body}"


def detect(body: str) -> str | None:
    if not body:
        return None
    first_line = body.splitlines()[0].strip()
    m = _MARKER_RE.match(first_line)
    return m.group(1) if m else None
```

- [ ] **Step 4: Зелёные** — `uv run pytest tests/unit/test_marking.py -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/office_adapter/marking.py adapters/tests/unit/test_marking.py
git commit -m "feat: маркировка офисного контента [ai-office:role] (Р-25)"
```

---

### Task 3: Интерфейс адаптера и модель данных

**Files:**
- Create: `adapters/office_adapter/interface.py`
- Test: `adapters/tests/unit/test_interface.py`

**Interfaces:**
- Produces (используется всеми дальнейшими задачами):
  - `ABSTRACT_STATES: tuple[str, ...]` — 11 состояний из Global Constraints;
  - `@dataclass CardComment(author: str, created: str, body: str, office_marker: str | None)`;
  - `@dataclass Card(id: str, key: str, title: str, description: str, state: str | None,
    raw_state: str, url: str, labels: list[str], comments: list[CardComment],
    links: list[str], attachments: list[str])`;
  - `@dataclass Capabilities(attach: bool, link: bool)`;
  - `class Provider(Protocol)` с методами: `capabilities() -> Capabilities`,
    `list_cards(state: str) -> list[Card]`, `read_card(card_id: str) -> Card`,
    `move(card_id: str, state: str) -> None`, `comment(card_id: str, body: str) -> None`,
    `create_card(title: str, body: str, state: str) -> str` (возвращает id),
    `link(card_id: str, other_id: str) -> None`, `attach(card_id: str, file_path: str) -> None`,
    `in_fence(card: Card) -> bool`; атрибут `name: str`.

- [ ] **Step 1: Failing-тест**

`adapters/tests/unit/test_interface.py`:

```python
import dataclasses
import json

from office_adapter.interface import ABSTRACT_STATES, Card, CardComment, Capabilities


def test_abstract_states_exactly_as_spec():
    assert ABSTRACT_STATES == (
        "idea", "analysis", "design_gate", "ready_for_dev", "in_dev",
        "ai_qa", "merge_gate", "bookkeeping", "external_qa", "done", "needs_input",
    )


def test_card_serializes_to_json():
    card = Card(
        id="1", key="X-1", title="t", description="d", state="idea",
        raw_state="Backlog", url="", labels=["ai-office"],
        comments=[CardComment(author="kao", created="2026-07-28T00:00:00",
                              body="hi", office_marker=None)],
        links=[], attachments=[],
    )
    blob = json.dumps(dataclasses.asdict(card), ensure_ascii=False)
    assert '"key": "X-1"' in blob


def test_capabilities_fields():
    caps = Capabilities(attach=True, link=False)
    assert caps.attach and not caps.link
```

- [ ] **Step 2: Падает** — `uv run pytest tests/unit/test_interface.py -v` → FAIL.

- [ ] **Step 3: Реализация**

`adapters/office_adapter/interface.py`:

```python
"""Абстракция трекера (§6): модель карточки и контракт провайдера."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Protocol

ABSTRACT_STATES: tuple[str, ...] = (
    "idea", "analysis", "design_gate", "ready_for_dev", "in_dev",
    "ai_qa", "merge_gate", "bookkeeping", "external_qa", "done", "needs_input",
)


@dataclass
class CardComment:
    author: str
    created: str  # ISO 8601
    body: str
    office_marker: str | None  # роль из маркера Р-25 либо None


@dataclass
class Card:
    id: str
    key: str
    title: str
    description: str
    state: str | None  # абстрактное состояние; None, если представление не отображается назад
    raw_state: str
    url: str
    labels: list[str] = field(default_factory=list)
    comments: list[CardComment] = field(default_factory=list)
    links: list[str] = field(default_factory=list)
    attachments: list[str] = field(default_factory=list)


@dataclass
class Capabilities:
    attach: bool
    link: bool


class Provider(Protocol):
    name: str

    def capabilities(self) -> Capabilities: ...
    def list_cards(self, state: str) -> list[Card]: ...
    def read_card(self, card_id: str) -> Card: ...
    def move(self, card_id: str, state: str) -> None: ...
    def comment(self, card_id: str, body: str) -> None: ...
    def create_card(self, title: str, body: str, state: str) -> str: ...
    def link(self, card_id: str, other_id: str) -> None: ...
    def attach(self, card_id: str, file_path: str) -> None: ...
    def in_fence(self, card: Card) -> bool: ...
```

- [ ] **Step 4: Зелёный** — `uv run pytest tests/unit/test_interface.py -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/office_adapter/interface.py adapters/tests/unit/test_interface.py
git commit -m "feat: интерфейс провайдера и модель карточки (§6)"
```

---

### Task 4: Схема профиля, пример, загрузчик и валидатор

**Files:**
- Create: `templates/profile.schema.json`
- Create: `templates/profile.example.yaml`
- Create: `adapters/office_adapter/profile.py`
- Modify: `docs/specs/2026-07-28-adapter-seam-design.md` (секции «Профиль» и «jira»:
  дописать провайдер-специфичные ключи `tracker.url`, `tracker.project`,
  `tracker.issue_type` — обнаружено при планировании: Jira без них не умеет
  ни искать, ни создавать карточки; профиль владеет данными — им там и место)
- Test: `adapters/tests/unit/test_profile.py`

**Interfaces:**
- Consumes: `ProfileError`, `UsageError` (Task 1), `ABSTRACT_STATES` (Task 3).
- Produces:
  - `@dataclass Profile(raw: dict, path: Path | None)` со свойствами
    `tracker -> dict`, `provider_name -> str`, `fence -> tuple[str, str]`
    (пара `(kind, value)`, например `("label", "ai-office")`), методом
    `state_repr(state: str) -> dict` (`ProfileError`, если состояние не отображено);
  - `find_profile_path(start: Path) -> Path` — поиск `.office/profile.yaml` вверх;
  - `load_profile(path: Path) -> Profile` — YAML + JSON Schema + провайдер-проверки;
  - `load_profile_data(data: dict, path: Path | None = None) -> Profile` — тот же
    валидационный путь для словаря (нужен конформанс-сьюту и `office-init` в 1б).

- [ ] **Step 1: JSON-схема**

`templates/profile.schema.json` (полная форма §6; `additionalProperties: false`
на верхнем уровне и в `tracker` — опечатки не тонут):

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://github.com/kao73/virtual-office/templates/profile.schema.json",
  "title": "Virtual office client profile (.office/profile.yaml)",
  "type": "object",
  "additionalProperties": false,
  "required": ["tracker"],
  "properties": {
    "tracker": {
      "type": "object",
      "additionalProperties": false,
      "required": ["provider", "fence", "states"],
      "properties": {
        "provider": {"enum": ["jira", "yougile"]},
        "fence": {"type": "string", "pattern": "^(label|board):.+$"},
        "office_account": {"type": ["string", "null"]},
        "url": {"type": "string", "format": "uri"},
        "project": {"type": "string"},
        "issue_type": {"type": "string"},
        "states": {
          "type": "object",
          "additionalProperties": false,
          "minProperties": 1,
          "propertyNames": {
            "enum": ["idea", "analysis", "design_gate", "ready_for_dev",
                     "in_dev", "ai_qa", "merge_gate", "bookkeeping",
                     "external_qa", "done", "needs_input"]
          },
          "patternProperties": {".*": {"type": "object"}}
        }
      }
    },
    "repo": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "base_branch": {"type": "string"},
        "branch_naming": {"type": "string"},
        "merge_policy": {"type": "string"}
      }
    },
    "gates": {"type": "object"},
    "limits": {"type": "object"},
    "artifacts": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "design_docs": {"type": "string"},
        "language": {"type": "string"}
      }
    },
    "mockups": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "mode": {"enum": ["real-components", "standalone"]},
        "command": {"type": "string"}
      }
    },
    "rituals": {"type": "object"},
    "extensions": {"type": "object"}
  }
}
```

- [ ] **Step 2: Пример профиля**

`templates/profile.example.yaml`:

```yaml
# Профиль проекта-клиента (§6 конституции). Живёт у клиента: .office/profile.yaml
# Секретов здесь нет никогда: креды берутся из окружения машины.
tracker:
  provider: jira                  # jira | yougile
  fence: "label:ai-office"        # огораживание: jira — label:<имя>, yougile — board:<id>
  office_account: null            # техаккаунт офиса; null → правило маркировки (Р-20)
  url: "https://jira.example.com" # jira: базовый URL сервера
  project: "PROJ"                 # jira: ключ проекта для поиска и создания карточек
  issue_type: "Story"             # jira: тип создаваемых офисом карточек
  states:                         # абстрактное состояние → представление в трекере
    idea:        {status: "Backlog"}
    analysis:    {status: "To Do"}
    bookkeeping: {status: "Ready for prod"}
    done:        {status: "Closed"}
repo:
  base_branch: master
  branch_naming: "{type}/{key}-{slug}"
  merge_policy: merge-commit
artifacts:
  design_docs: "docs/specs/"
  language: ru
```

- [ ] **Step 3: Failing-тесты**

`adapters/tests/unit/test_profile.py`:

```python
from pathlib import Path

import pytest
import yaml

from office_adapter.errors import ProfileError
from office_adapter.profile import find_profile_path, load_profile, load_profile_data

REPO_ROOT = Path(__file__).resolve().parents[3]

YOUGILE_OK = {
    "tracker": {
        "provider": "yougile",
        "fence": "board:abc123",
        "states": {"idea": {"column": "col-1"}, "done": {"column": "col-2"}},
    }
}

JIRA_OK = {
    "tracker": {
        "provider": "jira",
        "fence": "label:ai-office",
        "url": "https://jira.example.com",
        "project": "PROJ",
        "issue_type": "Story",
        "states": {"idea": {"status": "Backlog"}},
    }
}


def test_example_profile_is_valid():
    example = yaml.safe_load(
        (REPO_ROOT / "templates" / "profile.example.yaml").read_text())
    profile = load_profile_data(example)
    assert profile.provider_name == "jira"
    assert profile.fence == ("label", "ai-office")


def test_unknown_top_level_key_rejected():
    with pytest.raises(ProfileError):
        load_profile_data({**YOUGILE_OK, "trakcer": {}})


def test_unknown_state_name_rejected():
    bad = {"tracker": {**YOUGILE_OK["tracker"],
                       "states": {"ideea": {"column": "c"}}}}
    with pytest.raises(ProfileError):
        load_profile_data(bad)


def test_yougile_requires_board_fence():
    bad = {"tracker": {**YOUGILE_OK["tracker"], "fence": "label:ai-office"}}
    with pytest.raises(ProfileError):
        load_profile_data(bad)


def test_yougile_state_repr_needs_column():
    bad = {"tracker": {**YOUGILE_OK["tracker"],
                       "states": {"idea": {"status": "Backlog"}}}}
    with pytest.raises(ProfileError):
        load_profile_data(bad)


def test_jira_requires_url_project_issue_type():
    tracker = dict(JIRA_OK["tracker"])
    del tracker["project"]
    with pytest.raises(ProfileError):
        load_profile_data({"tracker": tracker})


def test_jira_requires_label_fence():
    bad = {"tracker": {**JIRA_OK["tracker"], "fence": "board:xyz"}}
    with pytest.raises(ProfileError):
        load_profile_data(bad)


def test_state_repr_unmapped_state_raises():
    profile = load_profile_data(YOUGILE_OK)
    assert profile.state_repr("idea") == {"column": "col-1"}
    with pytest.raises(ProfileError):
        profile.state_repr("in_dev")


def test_find_profile_walks_up(tmp_path):
    office = tmp_path / ".office"
    office.mkdir()
    (office / "profile.yaml").write_text(yaml.safe_dump(YOUGILE_OK))
    nested = tmp_path / "a" / "b"
    nested.mkdir(parents=True)
    assert find_profile_path(nested) == office / "profile.yaml"
    profile = load_profile(office / "profile.yaml")
    assert profile.provider_name == "yougile"


def test_find_profile_missing_raises(tmp_path):
    with pytest.raises(ProfileError):
        find_profile_path(tmp_path)
```

- [ ] **Step 4: Падают** — `uv run pytest tests/unit/test_profile.py -v` → FAIL.

- [ ] **Step 5: Реализация**

`adapters/office_adapter/profile.py`:

```python
"""Профиль клиента (.office/profile.yaml): поиск, загрузка, валидация (§6)."""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import jsonschema
import yaml

from office_adapter.errors import ProfileError
from office_adapter.interface import ABSTRACT_STATES

SCHEMA_PATH = Path(__file__).resolve().parents[2] / "templates" / "profile.schema.json"

# Провайдер-специфика, которую JSON Schema не выражает без if/then-лапши:
# форма фенса, форма представления состояния, обязательные ключи tracker.
_FENCE_KIND = {"jira": "label", "yougile": "board"}
_REQUIRED_TRACKER_KEYS = {"jira": ("url", "project", "issue_type"), "yougile": ()}
_STATE_REPR_KEYS = {"jira": ({"status"}, {"status", "label"}),
                    "yougile": ({"column"}, {"column"})}


@dataclass
class Profile:
    raw: dict
    path: Path | None = None

    @property
    def tracker(self) -> dict:
        return self.raw["tracker"]

    @property
    def provider_name(self) -> str:
        return self.tracker["provider"]

    @property
    def fence(self) -> tuple[str, str]:
        kind, _, value = self.tracker["fence"].partition(":")
        return kind, value

    def state_repr(self, state: str) -> dict:
        if state not in ABSTRACT_STATES:
            raise ProfileError(f"unknown abstract state: {state!r}",
                               known=list(ABSTRACT_STATES))
        try:
            return self.tracker["states"][state]
        except KeyError:
            raise ProfileError(
                f"state '{state}' is not mapped for this client",
                mapped=sorted(self.tracker["states"])) from None


def find_profile_path(start: Path) -> Path:
    for directory in [start, *start.resolve().parents]:
        candidate = directory / ".office" / "profile.yaml"
        if candidate.is_file():
            return candidate
    raise ProfileError(f"no .office/profile.yaml found from {start} upward")


def load_profile(path: Path) -> Profile:
    try:
        data = yaml.safe_load(path.read_text())
    except (OSError, yaml.YAMLError) as exc:
        raise ProfileError(f"cannot read profile: {exc}") from exc
    return load_profile_data(data, path=path)


def load_profile_data(data: dict, path: Path | None = None) -> Profile:
    schema = yaml.safe_load(SCHEMA_PATH.read_text())
    try:
        jsonschema.validate(data, schema)
    except jsonschema.ValidationError as exc:
        raise ProfileError(f"profile schema violation: {exc.message}",
                           json_path=exc.json_path) from exc
    profile = Profile(raw=data, path=path)
    _check_provider_specifics(profile)
    return profile


def _check_provider_specifics(profile: Profile) -> None:
    provider = profile.provider_name
    kind, value = profile.fence
    if kind != _FENCE_KIND[provider]:
        raise ProfileError(
            f"provider '{provider}' requires fence '{_FENCE_KIND[provider]}:<...>', "
            f"got '{kind}:{value}'")
    missing = [k for k in _REQUIRED_TRACKER_KEYS[provider]
               if k not in profile.tracker]
    if missing:
        raise ProfileError(f"provider '{provider}' requires tracker keys: {missing}")
    required, allowed = _STATE_REPR_KEYS[provider]
    for state, repr_ in profile.tracker["states"].items():
        keys = set(repr_)
        if not required <= keys or not keys <= allowed:
            raise ProfileError(
                f"state '{state}': representation keys {sorted(keys)} invalid for "
                f"'{provider}' (required {sorted(required)}, allowed {sorted(allowed)})")
```

- [ ] **Step 6: Зелёные** — `uv run pytest tests/unit/test_profile.py -v` → PASS.

- [ ] **Step 7: Правка спеки**

В `docs/specs/2026-07-28-adapter-seam-design.md`:
- в секции «Профиль клиента и схема» после строки про `tracker.states` добавить пункт:
  `- Провайдер-специфичные ключи tracker: jira требует url (базовый URL сервера), project (ключ проекта), issue_type (тип создаваемых карточек); для yougile дополнительных ключей нет. Валидатор проверяет их вместе с формой фенса и представлений.`
- в секции «jira» первый буллет дополнить: базовый URL берётся из `tracker.url`
  профиля; создание карточек — в `tracker.project` с типом `tracker.issue_type`.

- [ ] **Step 8: Commit**

```bash
git add templates/ adapters/office_adapter/profile.py adapters/tests/unit/test_profile.py docs/specs/2026-07-28-adapter-seam-design.md
git commit -m "feat: схема профиля клиента, пример и валидатор (§6)"
```

---

### Task 5: Фейк-провайдер для юнит-тестов

**Files:**
- Create: `adapters/office_adapter/testing.py`
- Test: `adapters/tests/unit/test_fake_provider.py`

**Interfaces:**
- Consumes: `Card`, `CardComment`, `Capabilities` (Task 3), `Profile` (Task 4),
  `TrackerError` (Task 1).
- Produces: `FakeProvider(profile: Profile, capabilities: Capabilities | None = None,
  lose_writes: bool = False)` — in-memory реализация протокола `Provider`.
  Особенности: `name = "fake"`; фенс — карточка считается в фенсе, если значение
  фенса из профиля есть в `card.labels`; `create_card` сам добавляет фенс-метку;
  `lose_writes=True` — provider молча «теряет» `comment`/`move` (для тестов read-back);
  метод `plant_card(card: Card) -> None` — подложить карточку напрямую (в т.ч. вне фенса);
  `comment` НЕ маркирует (маркирует base — Task 6), автор фейковых комментов — `"fake-provider"`.

- [ ] **Step 1: Failing-тест**

`adapters/tests/unit/test_fake_provider.py`:

```python
from office_adapter.interface import Capabilities, Card
from office_adapter.profile import load_profile_data
from office_adapter.testing import FakeProvider

PROFILE = load_profile_data({
    "tracker": {
        "provider": "yougile",
        "fence": "board:fence-1",
        "states": {"idea": {"column": "c1"}, "analysis": {"column": "c2"},
                   "done": {"column": "c3"}},
    }
})


def make_provider(**kwargs) -> FakeProvider:
    return FakeProvider(PROFILE, **kwargs)


def test_create_read_roundtrip():
    p = make_provider()
    card_id = p.create_card("title", "body", "idea")
    card = p.read_card(card_id)
    assert card.title == "title"
    assert card.state == "idea"
    assert p.in_fence(card)


def test_list_cards_filters_by_state():
    p = make_provider()
    a = p.create_card("a", "", "idea")
    p.create_card("b", "", "analysis")
    assert [c.id for c in p.list_cards("idea")] == [a]


def test_move_and_comment():
    p = make_provider()
    card_id = p.create_card("a", "", "idea")
    p.move(card_id, "done")
    p.comment(card_id, "note")
    card = p.read_card(card_id)
    assert card.state == "done"
    assert card.comments[-1].body == "note"


def test_lose_writes_mode():
    p = make_provider(lose_writes=True)
    card_id = p.create_card("a", "", "idea")
    p.move(card_id, "done")
    p.comment(card_id, "note")
    card = p.read_card(card_id)
    assert card.state == "idea"
    assert card.comments == []


def test_planted_card_out_of_fence():
    p = make_provider()
    p.plant_card(Card(id="alien", key="alien", title="x", description="",
                      state="idea", raw_state="c1", url="", labels=[]))
    assert not p.in_fence(p.read_card("alien"))
    assert all(c.id != "alien" for c in p.list_cards("idea"))
```

Замечание: `list_cards` фейка обязан отдавать только карточки в фенсе — как
честный провайдер, который ищет по участку.

- [ ] **Step 2: Падает** — `uv run pytest tests/unit/test_fake_provider.py -v` → FAIL.

- [ ] **Step 3: Реализация**

`adapters/office_adapter/testing.py`:

```python
"""In-memory провайдер для юнит-тестов base/CLI и полигон логики без сети."""
from __future__ import annotations

import itertools
from dataclasses import replace

from office_adapter.errors import CapabilityMissing, TrackerError
from office_adapter.interface import Capabilities, Card, CardComment
from office_adapter.profile import Profile


class FakeProvider:
    name = "fake"

    def __init__(self, profile: Profile,
                 capabilities: Capabilities | None = None,
                 lose_writes: bool = False) -> None:
        self._profile = profile
        self._caps = capabilities or Capabilities(attach=True, link=True)
        self._lose = lose_writes
        self._cards: dict[str, Card] = {}
        self._seq = itertools.count(1)
        _, self._fence_value = profile.fence

    # --- служебное для тестов ---
    def plant_card(self, card: Card) -> None:
        self._cards[card.id] = card

    # --- контракт Provider ---
    def capabilities(self) -> Capabilities:
        return self._caps

    def list_cards(self, state: str) -> list[Card]:
        column = self._profile.state_repr(state)["column"]
        return [c for c in self._cards.values()
                if c.raw_state == column and self.in_fence(c)]

    def read_card(self, card_id: str) -> Card:
        try:
            return self._cards[card_id]
        except KeyError:
            raise TrackerError(f"card not found: {card_id}") from None

    def move(self, card_id: str, state: str) -> None:
        column = self._profile.state_repr(state)["column"]
        card = self.read_card(card_id)
        if not self._lose:
            self._cards[card_id] = replace(card, raw_state=column, state=state)

    def comment(self, card_id: str, body: str) -> None:
        from office_adapter.marking import detect
        card = self.read_card(card_id)
        if not self._lose:
            card.comments.append(CardComment(
                author="fake-provider", created="2026-01-01T00:00:00",
                body=body, office_marker=detect(body)))

    def create_card(self, title: str, body: str, state: str) -> str:
        column = self._profile.state_repr(state)["column"]
        card_id = f"fake-{next(self._seq)}"
        self._cards[card_id] = Card(
            id=card_id, key=card_id, title=title, description=body,
            state=state, raw_state=column, url="",
            labels=[self._fence_value])
        return card_id

    def link(self, card_id: str, other_id: str) -> None:
        if not self._caps.link:
            raise CapabilityMissing("fake provider configured without link")
        card = self.read_card(card_id)
        other = self.read_card(other_id)
        if not self._lose:
            card.links.append(other.key)

    def attach(self, card_id: str, file_path: str) -> None:
        from pathlib import Path
        if not self._caps.attach:
            raise CapabilityMissing("fake provider configured without attach")
        card = self.read_card(card_id)
        if not self._lose:
            card.attachments.append(Path(file_path).name)

    def in_fence(self, card: Card) -> bool:
        return self._fence_value in card.labels
```

- [ ] **Step 4: Зелёный** — `uv run pytest tests/unit/test_fake_provider.py -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/office_adapter/testing.py adapters/tests/unit/test_fake_provider.py
git commit -m "test: in-memory фейк-провайдер для юнитов шва"
```

---

### Task 6: Обвязка base.py — фенс, маркировка, read-back

**Files:**
- Create: `adapters/office_adapter/base.py`
- Test: `adapters/tests/unit/test_base.py`

**Interfaces:**
- Consumes: всё из Task 1–5.
- Produces: `class Adapter` — единственное, что видят CLI и роли:
  - `Adapter(provider: Provider, profile: Profile)`;
  - `capabilities() -> Capabilities`;
  - `list_cards(state: str) -> list[Card]`;
  - `read_card(card_id: str) -> Card` — `FenceViolation` вне фенса;
  - `move(card_id: str, state: str) -> Card` — после записи перечитывает и сверяет;
  - `comment(card_id: str, role: str, body: str) -> Card` — маркирует и верифицирует;
  - `create_card(role: str, title: str, body: str, state: str) -> Card`;
  - `link(card_id: str, other_id: str) -> Card`;
  - `attach(card_id: str, file_path: str) -> Card` — верификация: вложений стало больше.
  Мутирующие методы возвращают перечитанную карточку — CLI печатает её как результат.

- [ ] **Step 1: Failing-тесты**

`adapters/tests/unit/test_base.py`:

```python
import pytest

from office_adapter.base import Adapter
from office_adapter.errors import (CapabilityMissing, FenceViolation,
                                   VerificationFailed)
from office_adapter.interface import Capabilities, Card
from office_adapter.profile import load_profile_data
from office_adapter.testing import FakeProvider

PROFILE = load_profile_data({
    "tracker": {
        "provider": "yougile",
        "fence": "board:fence-1",
        "states": {"idea": {"column": "c1"}, "done": {"column": "c3"}},
    }
})


def make_adapter(**kwargs) -> tuple[Adapter, FakeProvider]:
    provider = FakeProvider(PROFILE, **kwargs)
    return Adapter(provider, PROFILE), provider


ALIEN = Card(id="alien", key="alien", title="x", description="", state="idea",
             raw_state="c1", url="", labels=[])


def test_read_card_out_of_fence_raises():
    adapter, provider = make_adapter()
    provider.plant_card(ALIEN)
    with pytest.raises(FenceViolation):
        adapter.read_card("alien")


def test_mutations_out_of_fence_raise():
    adapter, provider = make_adapter()
    provider.plant_card(ALIEN)
    with pytest.raises(FenceViolation):
        adapter.move("alien", "done")
    with pytest.raises(FenceViolation):
        adapter.comment("alien", "clerk", "hi")


def test_comment_is_marked_and_verified():
    adapter, _ = make_adapter()
    card = adapter.create_card("clerk", "t", "body", "idea")
    updated = adapter.comment(card.id, "clerk", "отчёт")
    assert updated.comments[-1].body == "[ai-office:clerk]\nотчёт"
    assert updated.comments[-1].office_marker == "clerk"


def test_create_card_marks_description():
    adapter, _ = make_adapter()
    card = adapter.create_card("clerk", "t", "постановка", "idea")
    assert card.description.startswith("[ai-office:clerk]\n")
    assert card.state == "idea"


def test_move_verifies_read_back():
    adapter, _ = make_adapter()
    card = adapter.create_card("clerk", "t", "", "idea")
    assert adapter.move(card.id, "done").state == "done"


def test_lost_move_raises_verification_failed():
    adapter, _ = make_adapter(lose_writes=True)
    card = adapter.create_card_unverified("clerk", "t", "", "idea")
    with pytest.raises(VerificationFailed):
        adapter.move(card, "done")


def test_lost_comment_raises_verification_failed():
    adapter, _ = make_adapter(lose_writes=True)
    card = adapter.create_card_unverified("clerk", "t", "", "idea")
    with pytest.raises(VerificationFailed):
        adapter.comment(card, "clerk", "note")


def test_capability_missing_for_link():
    adapter, _ = make_adapter(capabilities=Capabilities(attach=True, link=False))
    a = adapter.create_card("clerk", "a", "", "idea")
    b = adapter.create_card("clerk", "b", "", "idea")
    with pytest.raises(CapabilityMissing):
        adapter.link(a.id, b.id)


def test_link_verified_when_supported():
    adapter, _ = make_adapter()
    a = adapter.create_card("clerk", "a", "", "idea")
    b = adapter.create_card("clerk", "b", "", "idea")
    assert b.key in adapter.link(a.id, b.id).links
```

Замечание: `lose_writes`-фейк «теряет» и `create_card`-содержимое? Нет — создание
в фейке работает всегда (см. Task 5), теряются `move`/`comment`. Поэтому для
тестов потерянных записей нужен хелпер `create_card_unverified(role, title, body,
state) -> str` на `Adapter` — создаёт через провайдера без read-back-проверки
маркера (иначе сам `create_card` упал бы раньше времени). Он же пригодится
конформанс-сьюту для посадки карточек. Возвращает id.

- [ ] **Step 2: Падают** — `uv run pytest tests/unit/test_base.py -v` → FAIL.

- [ ] **Step 3: Реализация**

`adapters/office_adapter/base.py`:

```python
"""Обвязка провайдера: безусловные законы конституции (фенс §5, маркировка Р-25,
read-back). Провайдер не получает управления раньше этих проверок."""
from __future__ import annotations

from pathlib import Path

from office_adapter.errors import (CapabilityMissing, FenceViolation,
                                   UsageError, VerificationFailed)
from office_adapter.interface import Capabilities, Card, Provider
from office_adapter.marking import detect, mark
from office_adapter.profile import Profile


class Adapter:
    def __init__(self, provider: Provider, profile: Profile) -> None:
        self._provider = provider
        self._profile = profile

    # --- чтение ---
    def capabilities(self) -> Capabilities:
        return self._provider.capabilities()

    def list_cards(self, state: str) -> list[Card]:
        cards = self._provider.list_cards(state)
        for card in cards:
            self._assert_fence(card)
        return cards

    def read_card(self, card_id: str) -> Card:
        card = self._provider.read_card(card_id)
        self._assert_fence(card)
        return card

    # --- запись (всегда: фенс → действие → перечитать → сверить) ---
    def move(self, card_id: str, state: str) -> Card:
        self.read_card(card_id)
        self._provider.move(card_id, state)
        updated = self.read_card(card_id)
        if updated.state != state:
            raise VerificationFailed(
                f"move not confirmed: expected state '{state}', "
                f"tracker shows '{updated.state}' ({updated.raw_state})",
                card_id=card_id)
        return updated

    def comment(self, card_id: str, role: str, body: str) -> Card:
        self.read_card(card_id)
        marked = mark(role, body)
        self._provider.comment(card_id, marked)
        updated = self.read_card(card_id)
        published = [c for c in updated.comments
                     if c.body == marked and c.office_marker == role]
        if not published:
            raise VerificationFailed(
                "comment not confirmed by read-back", card_id=card_id, role=role)
        return updated

    def create_card(self, role: str, title: str, body: str, state: str) -> Card:
        card_id = self.create_card_unverified(role, title, body, state)
        card = self.read_card(card_id)  # заодно проверяет фенс созданного
        if card.state != state:
            raise VerificationFailed(
                f"create not confirmed: expected state '{state}', "
                f"got '{card.state}'", card_id=card_id)
        if detect(card.description) != role:
            raise VerificationFailed(
                "create not confirmed: office marker missing in description",
                card_id=card_id)
        return card

    def create_card_unverified(self, role: str, title: str, body: str,
                               state: str) -> str:
        return self._provider.create_card(title, mark(role, body), state)

    def link(self, card_id: str, other_id: str) -> Card:
        if not self.capabilities().link:
            raise CapabilityMissing(
                f"provider '{self._provider.name}' does not support link")
        other = self.read_card(other_id)
        self.read_card(card_id)
        self._provider.link(card_id, other_id)
        updated = self.read_card(card_id)
        if other.key not in updated.links:
            raise VerificationFailed("link not confirmed by read-back",
                                     card_id=card_id, other=other.key)
        return updated

    def attach(self, card_id: str, file_path: str) -> Card:
        if not self.capabilities().attach:
            raise CapabilityMissing(
                f"provider '{self._provider.name}' does not support attach")
        if not Path(file_path).is_file():
            raise UsageError(f"attach: file not found: {file_path}")
        before = len(self.read_card(card_id).attachments)
        self._provider.attach(card_id, file_path)
        updated = self.read_card(card_id)
        if len(updated.attachments) <= before:
            raise VerificationFailed("attach not confirmed by read-back",
                                     card_id=card_id, file=file_path)
        return updated

    def _assert_fence(self, card: Card) -> None:
        if not self._provider.in_fence(card):
            raise FenceViolation(
                f"card '{card.id}' is outside the office fence "
                f"{self._profile.tracker['fence']!r}", card_id=card.id)
```

- [ ] **Step 4: Зелёные** — `uv run pytest tests/unit/test_base.py -v` → PASS,
затем весь набор: `uv run pytest tests/unit -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/office_adapter/base.py adapters/tests/unit/test_base.py
git commit -m "feat: обвязка адаптера — фенс, маркировка, read-back (Р-15/Р-25)"
```

---

### Task 7: Реестр провайдеров и CLI

**Files:**
- Create: `adapters/office_adapter/providers/__init__.py`
- Create: `adapters/office_adapter/cli.py`
- Test: `adapters/tests/unit/test_cli.py`

**Interfaces:**
- Consumes: `Adapter` (Task 6), `Profile`/`find_profile_path`/`load_profile` (Task 4),
  `AdapterError` (Task 1).
- Produces:
  - `providers.get_provider(profile: Profile) -> Provider` — реестр
    `{"yougile": ..., "jira": ...}`; на этом таске оба значения ещё отсутствуют —
    реестр заполняется лениво импортом внутри функции, unknown → `ProfileError`;
  - `cli.main(argv: list[str] | None = None) -> int` и `cli.run() -> None`
    (обёртка `raise SystemExit(main())` — точка входа `office-adapter`);
  - JSON-контракт stdout: карточка — `dataclasses.asdict(card)`; список —
    `{"cards": [...]}`; capabilities — `{"attach": bool, "link": bool}`;
    validate-profile — `{"ok": true, "provider": "...", "profile": "<path>"}`;
    ошибки — `err.to_json()` на stderr, код выхода `err.exit_code`.

- [ ] **Step 1: Failing-тесты**

`adapters/tests/unit/test_cli.py`:

```python
import json

import pytest
import yaml

import office_adapter.cli as cli
import office_adapter.providers as providers
from office_adapter.profile import load_profile_data
from office_adapter.testing import FakeProvider

PROFILE_DICT = {
    "tracker": {
        "provider": "yougile",
        "fence": "board:fence-1",
        "states": {"idea": {"column": "c1"}, "done": {"column": "c3"}},
    }
}


@pytest.fixture()
def client_dir(tmp_path, monkeypatch):
    office = tmp_path / ".office"
    office.mkdir()
    (office / "profile.yaml").write_text(yaml.safe_dump(PROFILE_DICT))
    monkeypatch.chdir(tmp_path)
    fake = FakeProvider(load_profile_data(PROFILE_DICT))
    monkeypatch.setattr(providers, "get_provider", lambda profile: fake)
    return tmp_path, fake


def run_cli(capsys, *argv) -> tuple[int, dict]:
    code = cli.main(list(argv))
    out = capsys.readouterr()
    payload = json.loads(out.out) if out.out.strip() else {}
    return code, payload


def test_validate_profile_ok(client_dir, capsys):
    code, payload = run_cli(capsys, "validate-profile")
    assert code == 0
    assert payload["ok"] is True
    assert payload["provider"] == "yougile"


def test_validate_profile_missing(tmp_path, monkeypatch, capsys):
    monkeypatch.chdir(tmp_path)
    code = cli.main(["validate-profile"])
    err = json.loads(capsys.readouterr().err)
    assert code == 1
    assert err["error"] == "profile_error"


def test_create_list_move_comment_flow(client_dir, capsys):
    code, card = run_cli(capsys, "create-card", "--role", "clerk",
                         "--title", "T", "--state", "idea")
    assert code == 0 and card["state"] == "idea"

    code, listing = run_cli(capsys, "list-cards", "--state", "idea")
    assert code == 0
    assert [c["id"] for c in listing["cards"]] == [card["id"]]

    code, moved = run_cli(capsys, "move", card["id"], "--state", "done")
    assert code == 0 and moved["state"] == "done"

    body = client_dir[0] / "body.txt"
    body.write_text("отчёт клерка")
    code, commented = run_cli(capsys, "comment", card["id"],
                              "--role", "clerk", "--body-file", str(body))
    assert code == 0
    assert commented["comments"][-1]["office_marker"] == "clerk"


def test_capabilities(client_dir, capsys):
    code, caps = run_cli(capsys, "capabilities")
    assert code == 0
    assert caps == {"attach": True, "link": True}


def test_error_json_on_stderr(client_dir, capsys):
    code = cli.main(["read-card", "no-such-id"])
    captured = capsys.readouterr()
    err = json.loads(captured.err)
    assert code == 2
    assert err["error"] == "tracker_error"
```

- [ ] **Step 2: Падают** — `uv run pytest tests/unit/test_cli.py -v` → FAIL.

- [ ] **Step 3: Реестр**

`adapters/office_adapter/providers/__init__.py`:

```python
"""Реестр провайдеров: единственное место, знающее конкретные реализации."""
from __future__ import annotations

from office_adapter.errors import ProfileError
from office_adapter.interface import Provider
from office_adapter.profile import Profile


def get_provider(profile: Profile) -> Provider:
    name = profile.provider_name
    if name == "yougile":
        from office_adapter.providers.yougile import YougileProvider
        return YougileProvider(profile)
    if name == "jira":
        from office_adapter.providers.jira import JiraProvider
        return JiraProvider(profile)
    raise ProfileError(f"unknown tracker provider: {name!r}")
```

(До Task 8/11 импорты внутри веток будут падать `ModuleNotFoundError` — это
нормально: юнит-тесты CLI подменяют `get_provider` целиком.)

- [ ] **Step 4: CLI**

`adapters/office_adapter/cli.py`:

```python
"""CLI шва: команды повторяют интерфейс §6, вывод — JSON для ролей-скиллов."""
from __future__ import annotations

import argparse
import dataclasses
import json
import sys
from pathlib import Path

import office_adapter.providers as providers
from office_adapter.base import Adapter
from office_adapter.errors import AdapterError, UsageError
from office_adapter.profile import Profile, find_profile_path, load_profile


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="office-adapter")
    parser.add_argument("--profile", help="explicit path to .office/profile.yaml")
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("list-cards")
    p.add_argument("--state", required=True)

    p = sub.add_parser("read-card")
    p.add_argument("card_id")

    p = sub.add_parser("move")
    p.add_argument("card_id")
    p.add_argument("--state", required=True)

    p = sub.add_parser("comment")
    p.add_argument("card_id")
    p.add_argument("--role", required=True)
    p.add_argument("--body-file", required=True)

    p = sub.add_parser("create-card")
    p.add_argument("--role", required=True)
    p.add_argument("--title", required=True)
    p.add_argument("--state", required=True)
    p.add_argument("--body-file")

    p = sub.add_parser("link")
    p.add_argument("card_id")
    p.add_argument("other_id")

    p = sub.add_parser("attach")
    p.add_argument("card_id")
    p.add_argument("file")

    sub.add_parser("capabilities")
    sub.add_parser("validate-profile")
    return parser


def _load(args: argparse.Namespace) -> Profile:
    path = Path(args.profile) if args.profile else find_profile_path(Path.cwd())
    return load_profile(path)


def _read_body(path: str | None) -> str:
    if path is None:
        return ""
    try:
        return Path(path).read_text()
    except OSError as exc:
        raise UsageError(f"cannot read body file: {exc}") from exc


def _card_json(card) -> dict:
    return dataclasses.asdict(card)


def _emit(payload: dict) -> None:
    json.dump(payload, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        profile = _load(args)
        if args.command == "validate-profile":
            _emit({"ok": True, "provider": profile.provider_name,
                   "profile": str(profile.path)})
            return 0
        adapter = Adapter(providers.get_provider(profile), profile)
        if args.command == "list-cards":
            _emit({"cards": [_card_json(c) for c in adapter.list_cards(args.state)]})
        elif args.command == "read-card":
            _emit(_card_json(adapter.read_card(args.card_id)))
        elif args.command == "move":
            _emit(_card_json(adapter.move(args.card_id, args.state)))
        elif args.command == "comment":
            _emit(_card_json(adapter.comment(
                args.card_id, args.role, _read_body(args.body_file))))
        elif args.command == "create-card":
            _emit(_card_json(adapter.create_card(
                args.role, args.title, _read_body(args.body_file), args.state)))
        elif args.command == "link":
            _emit(_card_json(adapter.link(args.card_id, args.other_id)))
        elif args.command == "attach":
            _emit(_card_json(adapter.attach(args.card_id, args.file)))
        elif args.command == "capabilities":
            _emit(dataclasses.asdict(adapter.capabilities()))
        return 0
    except AdapterError as err:
        json.dump(err.to_json(), sys.stderr, ensure_ascii=False)
        sys.stderr.write("\n")
        return err.exit_code


def run() -> None:
    raise SystemExit(main())
```

- [ ] **Step 5: Зелёные** — `uv run pytest tests/unit/test_cli.py -v` → PASS;
весь юнит-набор: `uv run pytest tests/unit -v` → PASS.
Смоук руками: `uv run office-adapter --help` печатает usage.

- [ ] **Step 6: Commit**

```bash
git add adapters/office_adapter/providers/__init__.py adapters/office_adapter/cli.py adapters/tests/unit/test_cli.py
git commit -m "feat: CLI office-adapter и реестр провайдеров"
```

---

### Task 8: Провайдер yougile

**Files:**
- Create: `adapters/office_adapter/providers/yougile.py`
- Test: `adapters/tests/unit/test_yougile_mapping.py`

**Interfaces:**
- Consumes: `Profile` (Task 4), `interface.py` (Task 3), ошибки (Task 1),
  `marking.detect` (Task 2).
- Produces: `YougileProvider(profile)` — реализация протокола `Provider`,
  `capabilities() == Capabilities(attach=True, link=False)`.
  Внутренние функции, покрываемые юнитами: `_task_to_card(task: dict,
  column_to_state: dict[str, str], fence_board: str, columns_board: dict[str, str],
  comments: list[CardComment]) -> Card`, `_message_to_comment(msg: dict) -> CardComment`.

Факты API (сняты с исходника локального yougile-mcp, `~/yougile-mcp/src/tools/`):
база `https://yougile.com/api-v2/`, Bearer-токен; `GET/POST tasks`, `GET/PUT tasks/{id}`,
`GET task-list?...` (листинг с фильтрами), `GET columns/{id}`, `GET/POST
chats/{taskId}/messages` (тело сообщения — ключ `text`), `POST upload-file`
(multipart, ответ содержит `url`). Списки приходят обёрнутыми в `{"content": [...]}`.

- [ ] **Step 1: Failing-тесты маппинга**

`adapters/tests/unit/test_yougile_mapping.py`:

```python
from office_adapter.providers.yougile import _message_to_comment, _task_to_card


def test_message_to_comment_detects_marker():
    msg = {"fromUserId": "u1", "timestamp": 1753660800000,
           "text": "[ai-office:clerk]\nотчёт"}
    comment = _message_to_comment(msg)
    assert comment.author == "u1"
    assert comment.office_marker == "clerk"
    assert comment.created.startswith("2026-07-28")


def test_task_to_card_maps_state_and_fence():
    task = {"id": "t1", "title": "T", "description": "d", "columnId": "c1"}
    card = _task_to_card(task, column_to_state={"c1": "idea"},
                         fence_board="b1", columns_board={"c1": "b1"},
                         comments=[])
    assert card.state == "idea"
    assert card.raw_state == "c1"
    assert card.labels == ["b1"]  # метка фенса = id доски карточки


def test_task_to_card_unknown_column():
    task = {"id": "t2", "title": "T", "description": "", "columnId": "cX"}
    card = _task_to_card(task, column_to_state={"c1": "idea"},
                         fence_board="b1", columns_board={"cX": "b2"},
                         comments=[])
    assert card.state is None
    assert card.labels == ["b2"]
```

Соглашение фенса: `in_fence` для yougile — id доски, на которой стоит колонка
карточки, равен доске фенса; в `Card.labels` кладётся ровно этот id доски
(единственная «метка» yougile-карточки в v1).

- [ ] **Step 2: Падают** — `uv run pytest tests/unit/test_yougile_mapping.py -v` → FAIL.

- [ ] **Step 3: Реализация**

`adapters/office_adapter/providers/yougile.py`:

```python
"""Провайдер YouGile: REST API v2, Bearer-токен из YOUGILE_API_KEY."""
from __future__ import annotations

import os
from datetime import datetime, timezone
from pathlib import Path

import requests

from office_adapter.errors import CapabilityMissing, TrackerError, UsageError
from office_adapter.interface import Capabilities, Card, CardComment
from office_adapter.marking import detect, mark
from office_adapter.profile import Profile

API = "https://yougile.com/api-v2"


def _message_to_comment(msg: dict) -> CardComment:
    ts = msg.get("timestamp", 0) / 1000
    created = datetime.fromtimestamp(ts, tz=timezone.utc).isoformat()
    body = msg.get("text", "")
    return CardComment(author=str(msg.get("fromUserId", "")), created=created,
                       body=body, office_marker=detect(body))


def _task_to_card(task: dict, column_to_state: dict[str, str], fence_board: str,
                  columns_board: dict[str, str], comments: list[CardComment]) -> Card:
    column_id = task.get("columnId", "")
    board_id = columns_board.get(column_id, "")
    attachments = [line for c in comments if c.office_marker == "office"
                   for line in c.body.splitlines() if line.startswith("http")]
    return Card(
        id=task["id"], key=task["id"][:8], title=task.get("title", ""),
        description=task.get("description", ""),
        state=column_to_state.get(column_id), raw_state=column_id,
        url="", labels=[board_id] if board_id else [],
        comments=comments, links=[], attachments=attachments)


class YougileProvider:
    name = "yougile"

    def __init__(self, profile: Profile) -> None:
        key = os.environ.get("YOUGILE_API_KEY")
        if not key:
            raise UsageError("YOUGILE_API_KEY is not set in the environment")
        self._profile = profile
        _, self._board_id = profile.fence
        self._states = profile.tracker["states"]
        self._column_to_state = {r["column"]: s for s, r in self._states.items()}
        self._columns_board: dict[str, str] = {}
        self._session = requests.Session()
        self._session.headers["Authorization"] = f"Bearer {key}"

    # --- транспорт: один ретрай на сеть/5xx (Global Constraints) ---
    def _request(self, method: str, path: str, **kwargs):
        url = f"{API}/{path}"
        last_error: Exception | None = None
        for _ in range(2):
            try:
                response = self._session.request(method, url, timeout=30, **kwargs)
                if response.status_code >= 500:
                    last_error = TrackerError(
                        f"yougile {method} {path}: HTTP {response.status_code}")
                    continue
                if response.status_code >= 400:
                    raise TrackerError(
                        f"yougile {method} {path}: HTTP {response.status_code}",
                        body=response.text[:500])
                return response.json() if response.text else {}
            except requests.RequestException as exc:
                last_error = TrackerError(f"yougile {method} {path}: {exc}")
        raise last_error  # type: ignore[misc]

    def _board_of_column(self, column_id: str) -> str:
        if column_id not in self._columns_board:
            column = self._request("GET", f"columns/{column_id}")
            self._columns_board[column_id] = column.get("boardId", "")
        return self._columns_board[column_id]

    def _comments_of(self, task_id: str) -> list[CardComment]:
        data = self._request("GET", f"chats/{task_id}/messages")
        return [_message_to_comment(m) for m in data.get("content", [])]

    # --- контракт Provider ---
    def capabilities(self) -> Capabilities:
        return Capabilities(attach=True, link=False)

    def list_cards(self, state: str) -> list[Card]:
        column = self._profile.state_repr(state)["column"]
        data = self._request("GET", f"task-list?columnId={column}&limit=1000")
        cards = []
        for task in data.get("content", []):
            self._board_of_column(task.get("columnId", ""))
            cards.append(_task_to_card(
                task, self._column_to_state, self._board_id,
                self._columns_board, self._comments_of(task["id"])))
        return cards

    def read_card(self, card_id: str) -> Card:
        task = self._request("GET", f"tasks/{card_id}")
        self._board_of_column(task.get("columnId", ""))
        return _task_to_card(task, self._column_to_state, self._board_id,
                             self._columns_board, self._comments_of(card_id))

    def move(self, card_id: str, state: str) -> None:
        column = self._profile.state_repr(state)["column"]
        self._request("PUT", f"tasks/{card_id}", json={"columnId": column})

    def comment(self, card_id: str, body: str) -> None:
        self._request("POST", f"chats/{card_id}/messages", json={"text": body})

    def create_card(self, title: str, body: str, state: str) -> str:
        column = self._profile.state_repr(state)["column"]
        created = self._request("POST", "tasks", json={
            "title": title, "columnId": column, "description": body})
        task_id = created.get("id")
        if not task_id:
            raise TrackerError("yougile create task: no id in response",
                               response=created)
        return task_id

    def link(self, card_id: str, other_id: str) -> None:
        raise CapabilityMissing("yougile provider does not support card links")

    def attach(self, card_id: str, file_path: str) -> None:
        path = Path(file_path)
        with path.open("rb") as fh:
            uploaded = self._request("POST", "upload-file",
                                     files={"file": (path.name, fh)})
        url = uploaded.get("url") or uploaded.get("fileUrl")
        if not url:
            raise TrackerError("yougile upload-file: no url in response",
                               response=uploaded)
        self.comment(card_id, mark("office", url))

    def in_fence(self, card: Card) -> bool:
        return self._board_id in card.labels
```

Замечания:
- `attach` публикует URL файла маркированным сообщением от псевдо-роли `office`
  (вложение = офисный контент, Р-25 действует и тут); `attachments` карточки —
  URL-строки из таких сообщений. Верификация в `base.attach` (счётчик вырос) с
  этим согласована.
- `list_cards` делает по одному запросу чата на карточку — на полигоне и участке
  офиса карточек единицы, оптимизация не нужна (YAGNI).

- [ ] **Step 4: Зелёные юниты** — `uv run pytest tests/unit/test_yougile_mapping.py -v` → PASS.

- [ ] **Step 5: Сверка форм ответов с живым API**

Перед коммитом подтвердить два неочевидных допущения кодом против живого API
(токен уже в окружении MCP-конфига; на этом шаге можно `export YOUGILE_API_KEY=...`
из `~/.claude.json` руками, не печатая значение в вывод):

```bash
curl -s -H "Authorization: Bearer $YOUGILE_API_KEY" "https://yougile.com/api-v2/projects" | head -c 300
```

Проверить: ответ — `{"paging": ..., "content": [...]}` (форма `content`
подтверждается) и код 200. Если форма иная — поправить `_request`-вызовы
(`data.get("content", ...)`) по факту и отметить здесь.

- [ ] **Step 6: Commit**

```bash
git add adapters/office_adapter/providers/yougile.py adapters/tests/unit/test_yougile_mapping.py
git commit -m "feat: yougile-провайдер — REST v2, фенс по доске, attach через чат"
```

---

### Task 9: Полигон YouGile — bootstrap и каркас конформанс-сьюта

**Files:**
- Create: `adapters/tests/conformance/bootstrap_yougile.py`
- Create: `adapters/tests/conformance/conftest.py`
- Create: `adapters/tests/conformance/polygons.yaml` (генерируется bootstrap-скриптом, коммитится)

**Interfaces:**
- Consumes: `YougileProvider` (Task 8), `Adapter` (Task 6), `load_profile_data` (Task 4).
- Produces:
  - `polygons.yaml` — по провайдеру: ключ `profile` (полный tracker-словарь полигона)
    и ключ `cleanup` (провайдер-специфика уборки); yougile-секцию пишет bootstrap,
    jira-секция добавляется в Task 12 (значения статусов CRM3 известны: см. Task 12);
  - fixtures: `adapter` (по `OFFICE_TEST_PROVIDER`, иначе `pytest.skip`),
    `tracked` (фабрика-регистратор созданных id для teardown),
    `plant_foreign_card()` (карточка вне фенса для тестов огораживания),
    `plant_unmarked_comment(card_id)` (немаркированный коммент «от владельца»),
    `RUN_ID: str` (уникален на прогон, входит в заголовки карточек).

- [ ] **Step 1: Bootstrap-скрипт**

`adapters/tests/conformance/bootstrap_yougile.py`:

```python
"""Одноразовая закладка YouGile-полигона: проект + две доски + колонки.

Запуск: uv run python tests/conformance/bootstrap_yougile.py
Идемпотентен: если polygons.yaml уже содержит yougile-секцию и проект жив — выходит.
Пишет yougile-секцию polygons.yaml сам; ничего не печатает, кроме прогресса.
"""
from __future__ import annotations

import sys
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from office_adapter.profile import load_profile_data  # noqa: E402
from office_adapter.providers.yougile import YougileProvider  # noqa: E402

POLYGONS = Path(__file__).parent / "polygons.yaml"
PROJECT_TITLE = "office-polygon"
STATES = ("idea", "analysis", "done")


def main() -> None:
    existing = yaml.safe_load(POLYGONS.read_text()) if POLYGONS.exists() else {}
    if "yougile" in (existing or {}):
        print("polygons.yaml already has a yougile section — nothing to do")
        return

    # Провайдеру нужен формально валидный профиль — фенс уточним после создания доски.
    stub = load_profile_data({"tracker": {
        "provider": "yougile", "fence": "board:stub",
        "states": {"idea": {"column": "stub"}}}})
    api = YougileProvider(stub)

    project = api._request("POST", "projects", json={"title": PROJECT_TITLE})
    project_id = project["id"]
    fence_board = api._request("POST", "boards", json={
        "title": "polygon-fence", "projectId": project_id})["id"]
    outside_board = api._request("POST", "boards", json={
        "title": "polygon-outside", "projectId": project_id})["id"]

    columns = {}
    for state in STATES:
        columns[state] = api._request("POST", "columns", json={
            "title": state, "boardId": fence_board})["id"]
    outside_column = api._request("POST", "columns", json={
        "title": "idea", "boardId": outside_board})["id"]

    section = {
        "yougile": {
            "profile": {
                "tracker": {
                    "provider": "yougile",
                    "fence": f"board:{fence_board}",
                    "states": {s: {"column": columns[s]} for s in STATES},
                }
            },
            "cleanup": {"mode": "delete"},
            "outside": {"board": outside_board, "column": outside_column},
        }
    }
    POLYGONS.write_text(yaml.safe_dump({**(existing or {}), **section},
                                       allow_unicode=True, sort_keys=False))
    print(f"polygon ready: project={project_id} fence={fence_board}")


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Запустить bootstrap**

```bash
cd adapters
uv run python tests/conformance/bootstrap_yougile.py
```

Ожидание: печатает `polygon ready: ...`, появляется `tests/conformance/polygons.yaml`
с yougile-секцией. Проверить глазами: id непустые, states — три колонки.
(Использование приватного `api._request` в bootstrap — сознательное: скрипт
одноразовый и живёт рядом с тестами, публичный интерфейс для «создай доску»
офису не нужен — YAGNI.)

- [ ] **Step 3: conftest конформанс-сьюта**

`adapters/tests/conformance/conftest.py`:

```python
"""Каркас конформанс-сьюта: полигон, teardown, посадка чужих артефактов.

Запуск: OFFICE_TEST_PROVIDER=yougile uv run pytest tests/conformance -v
Без переменной окружения сьют пропускается (юниты остаются быстрыми).
"""
from __future__ import annotations

import os
import uuid
from pathlib import Path

import pytest
import yaml

from office_adapter.base import Adapter
from office_adapter.profile import load_profile_data
from office_adapter.providers import get_provider

POLYGONS = Path(__file__).parent / "polygons.yaml"
RUN_ID = f"conf-{uuid.uuid4().hex[:8]}"


def _polygon() -> dict:
    provider = os.environ.get("OFFICE_TEST_PROVIDER")
    if not provider:
        pytest.skip("OFFICE_TEST_PROVIDER is not set — conformance suite skipped")
    config = yaml.safe_load(POLYGONS.read_text())
    if provider not in config:
        pytest.skip(f"polygons.yaml has no '{provider}' section")
    return {"name": provider, **config[provider]}


@pytest.fixture(scope="session")
def polygon() -> dict:
    return _polygon()


@pytest.fixture(scope="session")
def profile(polygon):
    return load_profile_data(polygon["profile"])


@pytest.fixture(scope="session")
def provider(profile):
    return get_provider(profile)


@pytest.fixture(scope="session")
def adapter(provider, profile):
    return Adapter(provider, profile)


@pytest.fixture()
def tracked(provider, polygon):
    created: list[str] = []
    yield created.append
    for card_id in created:
        _cleanup(provider, polygon, card_id)


def _cleanup(provider, polygon, card_id: str) -> None:
    mode = polygon["cleanup"]["mode"]
    if mode == "delete":  # yougile: пометить задачу удалённой
        provider._request("PUT", f"tasks/{card_id}", json={"deleted": True})
    elif mode == "jira":  # jira: сначала снять метки (закрытые задачи часто
        # нередактируемы), затем увести в терминальный статус
        provider._set_labels(card_id, [])
        provider._transition_to(card_id, polygon["cleanup"]["state"])
    else:
        raise AssertionError(f"unknown cleanup mode: {mode}")


@pytest.fixture()
def plant_foreign_card(provider, polygon, tracked):
    """Карточка ВНЕ фенса: полигон-специфичная посадка через сырой API."""
    def _plant(title: str) -> str:
        if polygon["name"] == "yougile":
            created = provider._request("POST", "tasks", json={
                "title": title, "columnId": polygon["outside"]["column"]})
            return created["id"]
        if polygon["name"] == "jira":
            return provider._raw_create(title, labels=[polygon["outside"]["label"]])
        raise AssertionError(polygon["name"])
    return _plant


@pytest.fixture()
def plant_unmarked_comment(provider, polygon):
    """Немаркированный коммент — как будто его написал владелец руками."""
    def _plant(card_id: str, text: str) -> None:
        if polygon["name"] == "yougile":
            provider._request("POST", f"chats/{card_id}/messages",
                              json={"text": text})
        elif polygon["name"] == "jira":
            provider._raw_comment(card_id, text)
        else:
            raise AssertionError(polygon["name"])
    return _plant
```

Замечания:
- foreign-карточки yougile создаются на другой доске и teardown'ом `delete`
  зачищаются так же (`tracked` их регистрирует — тест обязан вызвать `tracked(id)`).
- conftest обращается к приватным методам провайдера (`_request`, `_raw_*`):
  сьют — единственный законный «сосед» провайдера, роли этих методов не видят.
  Методы `_transition_to`, `_set_labels`, `_raw_create`, `_raw_comment` появятся
  в jira-провайдере (Task 11).

- [ ] **Step 4: Смоук каркаса**

```bash
uv run pytest tests/conformance -v
```
Ожидание: `skipped` (без `OFFICE_TEST_PROVIDER`), юниты не затронуты.

- [ ] **Step 5: Commit**

```bash
git add adapters/tests/conformance/
git commit -m "test: YouGile-полигон (bootstrap) и каркас конформанс-сьюта"
```

---

### Task 10: Конформанс-сьют — секции контракта, зелёный против yougile

**Files:**
- Create: `adapters/tests/conformance/test_conformance.py`

**Interfaces:**
- Consumes: fixtures из Task 9; `Adapter` (Task 6); ошибки (Task 1);
  `marking.detect` (Task 2).

- [ ] **Step 1: Тесты секций контракта**

`adapters/tests/conformance/test_conformance.py`:

```python
"""Конформанс-сьют: «проверяем шов, а не заглушки» (§6).

Каждый тест — пункт контракта адаптера. Прогон против любого провайдера обязан
быть зелёным (опциональные способности — явный skip, не молчаливый пропуск).
"""
from __future__ import annotations

import pytest

from office_adapter.errors import CapabilityMissing, FenceViolation, ProfileError
from office_adapter.marking import detect
from conftest import RUN_ID  # pytest в prepend-режиме кладёт каталог теста в sys.path


def _title(name: str) -> str:
    return f"[{RUN_ID}] {name}"


def test_capabilities_shape(adapter):
    caps = adapter.capabilities()
    assert isinstance(caps.attach, bool)
    assert isinstance(caps.link, bool)


def test_create_and_read_back(adapter, tracked):
    card = adapter.create_card("clerk", _title("create"), "постановка", "idea")
    tracked(card.id)
    assert card.state == "idea"
    assert detect(card.description) == "clerk"
    again = adapter.read_card(card.id)
    assert again.title == _title("create")


def test_list_cards_sees_created(adapter, tracked):
    card = adapter.create_card("clerk", _title("list"), "", "analysis")
    tracked(card.id)
    listed = adapter.list_cards("analysis")
    assert card.id in [c.id for c in listed]


def test_fence_hides_foreign_card(adapter, plant_foreign_card, tracked):
    foreign_id = plant_foreign_card(_title("foreign"))
    tracked(foreign_id)
    for state in ("idea", "analysis", "done"):
        assert foreign_id not in [c.id for c in adapter.list_cards(state)]
    with pytest.raises(FenceViolation):
        adapter.read_card(foreign_id)
    with pytest.raises(FenceViolation):
        adapter.comment(foreign_id, "clerk", "не должно опубликоваться")


def test_move_changes_state(adapter, tracked):
    card = adapter.create_card("clerk", _title("move"), "", "idea")
    tracked(card.id)
    assert adapter.move(card.id, "analysis").state == "analysis"
    assert adapter.move(card.id, "done").state == "done"


def test_move_to_unmapped_state_fails(adapter, tracked):
    card = adapter.create_card("clerk", _title("unmapped"), "", "idea")
    tracked(card.id)
    with pytest.raises(ProfileError):
        adapter.move(card.id, "in_dev")  # полигон отображает только idea/analysis/done


def test_comment_marked_and_detected(adapter, tracked):
    card = adapter.create_card("clerk", _title("comment"), "", "idea")
    tracked(card.id)
    updated = adapter.comment(card.id, "clerk", "отчёт: всё сделано")
    published = [c for c in updated.comments if c.office_marker == "clerk"]
    assert published  # порядок сообщений трекера не фиксируем
    assert all(c.body.startswith("[ai-office:clerk]\n") for c in published)


def test_trust_rule_unmarked_comment_stays_unmarked(adapter, tracked,
                                                    plant_unmarked_comment):
    card = adapter.create_card("clerk", _title("trust"), "", "idea")
    tracked(card.id)
    plant_unmarked_comment(card.id, "Комментарий владельца без маркера")
    comments = adapter.read_card(card.id).comments
    owner_comments = [c for c in comments if c.office_marker is None]
    assert any("владельца" in c.body for c in owner_comments)


def test_link_or_explicit_capability_error(adapter, tracked):
    a = adapter.create_card("clerk", _title("link-a"), "", "idea")
    b = adapter.create_card("clerk", _title("link-b"), "", "idea")
    tracked(a.id)
    tracked(b.id)
    if adapter.capabilities().link:
        assert b.key in adapter.link(a.id, b.id).links
    else:
        with pytest.raises(CapabilityMissing):
            adapter.link(a.id, b.id)


def test_attach_or_explicit_capability_error(adapter, tracked, tmp_path):
    card = adapter.create_card("clerk", _title("attach"), "", "idea")
    tracked(card.id)
    artifact = tmp_path / "report.txt"
    artifact.write_text("отчёт приёмки")
    if adapter.capabilities().attach:
        updated = adapter.attach(card.id, str(artifact))
        assert len(updated.attachments) == 1
    else:
        with pytest.raises(CapabilityMissing):
            adapter.attach(card.id, str(artifact))
```

- [ ] **Step 2: Прогон против YouGile-полигона**

```bash
cd adapters
OFFICE_TEST_PROVIDER=yougile uv run pytest tests/conformance -v
```

Ожидание: все тесты PASS (link-тест идёт по ветке `CapabilityMissing`).
Падения здесь — находки о реальном API: чинить провайдера (формы ответов,
коды), не тесты. После зелёного прогона зайти в YouGile глазами: полигон-доска
пуста (teardown удалил созданное).

- [ ] **Step 3: Повторный прогон** — идемпотентность и отсутствие мусора:

```bash
OFFICE_TEST_PROVIDER=yougile uv run pytest tests/conformance -v
```
Ожидание: снова зелёный.

- [ ] **Step 4: Commit**

```bash
git add adapters/tests/conformance/test_conformance.py
git commit -m "test: конформанс-сьют шва — зелёный против YouGile-полигона"
```

---

### Task 11: Провайдер jira

**Files:**
- Create: `adapters/office_adapter/providers/jira.py`
- Test: `adapters/tests/unit/test_jira_mapping.py`

**Interfaces:**
- Consumes: `Profile` (Task 4), `interface.py` (Task 3), ошибки (Task 1), `detect` (Task 2).
- Produces: `JiraProvider(profile)` — реализация `Provider`,
  `capabilities() == Capabilities(attach=True, link=True)`; приватные методы
  для конформанс-conftest: `_transition_to(card_id, status_name)`,
  `_set_labels(card_id, labels)`, `_raw_create(title, labels) -> str`,
  `_raw_comment(card_id, text)`.
  Юнитами покрываются: `_issue_to_card(issue: dict, states: dict, fence_label: str,
  base_url: str) -> Card` и `_pick_transition(transitions: list[dict],
  target_status: str) -> str`.

Факты инстанса (jira-cli скилл, `reference/instance.md`): Server/DC 8.13,
REST `/rest/api/2`, **basic auth** (`JIRA_LOGIN` + `JIRA_API_TOKEN`), Bearer не
работает; select-поля читаются объектами; имена содержат look-alike символы —
копировать из discovery, не из памяти.

- [ ] **Step 1: Failing-тесты маппинга**

`adapters/tests/unit/test_jira_mapping.py`:

```python
import pytest

from office_adapter.errors import TrackerError
from office_adapter.providers.jira import _issue_to_card, _pick_transition

STATES = {"idea": {"status": "Backlog"},
          "analysis": {"status": "To Do"},
          "design_gate": {"status": "In Progress", "label": "office:design-gate"}}

ISSUE = {
    "key": "CRM3-999",
    "fields": {
        "summary": "T",
        "description": "[ai-office:clerk]\nтело",
        "status": {"name": "Backlog"},
        "labels": ["ai-office"],
        "comment": {"comments": [
            {"author": {"name": "kao"}, "created": "2026-07-28T10:00:00.000+0400",
             "body": "обычный коммент"},
            {"author": {"name": "kao"}, "created": "2026-07-28T11:00:00.000+0400",
             "body": "[ai-office:clerk]\nслед"},
        ]},
        "attachment": [{"filename": "shot.png"}],
        "issuelinks": [
            {"outwardIssue": {"key": "CRM3-1000"}},
            {"inwardIssue": {"key": "CRM3-998"}},
        ],
    },
}


def test_issue_to_card_maps_everything():
    card = _issue_to_card(ISSUE, STATES, "ai-office", "https://jira.example.com")
    assert card.key == "CRM3-999"
    assert card.state == "idea"
    assert card.raw_state == "Backlog"
    assert card.url == "https://jira.example.com/browse/CRM3-999"
    assert card.labels == ["ai-office"]
    assert card.comments[0].office_marker is None
    assert card.comments[1].office_marker == "clerk"
    assert card.attachments == ["shot.png"]
    assert sorted(card.links) == ["CRM3-1000", "CRM3-998"]


def test_state_disambiguation_by_label():
    issue = {"key": "X-1", "fields": {**ISSUE["fields"],
             "status": {"name": "In Progress"},
             "labels": ["ai-office", "office:design-gate"]}}
    card = _issue_to_card(issue, STATES, "ai-office", "https://j")
    assert card.state == "design_gate"


def test_unknown_status_gives_none_state():
    issue = {"key": "X-2", "fields": {**ISSUE["fields"],
             "status": {"name": "Suspended"}}}
    card = _issue_to_card(issue, STATES, "ai-office", "https://j")
    assert card.state is None
    assert card.raw_state == "Suspended"


def test_pick_transition_by_target_name():
    transitions = [{"id": "11", "to": {"name": "To Do"}},
                   {"id": "21", "to": {"name": "In Progress"}}]
    assert _pick_transition(transitions, "In Progress") == "21"


def test_pick_transition_missing_raises_with_available():
    with pytest.raises(TrackerError) as exc:
        _pick_transition([{"id": "11", "to": {"name": "To Do"}}], "Closed")
    assert "To Do" in str(exc.value.details.get("available"))
```

- [ ] **Step 2: Падают** — `uv run pytest tests/unit/test_jira_mapping.py -v` → FAIL.

- [ ] **Step 3: Реализация**

`adapters/office_adapter/providers/jira.py`:

```python
"""Провайдер Jira Server/DC: REST v2, basic auth (Bearer на инстансе не работает)."""
from __future__ import annotations

import os
from pathlib import Path

import requests

from office_adapter.errors import TrackerError, UsageError
from office_adapter.interface import Capabilities, Card, CardComment
from office_adapter.marking import detect
from office_adapter.profile import Profile

_FIELDS = "summary,description,status,labels,comment,attachment,issuelinks"


def _issue_to_card(issue: dict, states: dict, fence_label: str,
                   base_url: str) -> Card:
    fields = issue.get("fields", {})
    status = fields.get("status", {}).get("name", "")
    labels = fields.get("labels", [])

    candidates = [s for s, r in states.items() if r.get("status") == status]
    if len(candidates) > 1:
        candidates = [s for s in candidates
                      if states[s].get("label") in labels]
    state = candidates[0] if len(candidates) == 1 else None

    comments = []
    for c in fields.get("comment", {}).get("comments", []):
        body = c.get("body", "")
        comments.append(CardComment(
            author=c.get("author", {}).get("name", ""),
            created=c.get("created", ""), body=body,
            office_marker=detect(body)))

    links = []
    for link in fields.get("issuelinks", []):
        target = link.get("outwardIssue") or link.get("inwardIssue") or {}
        if target.get("key"):
            links.append(target["key"])

    return Card(
        id=issue["key"], key=issue["key"], title=fields.get("summary", ""),
        description=fields.get("description") or "",
        state=state, raw_state=status,
        url=f"{base_url}/browse/{issue['key']}",
        labels=labels, comments=comments, links=links,
        attachments=[a.get("filename", "") for a in fields.get("attachment", [])])


def _pick_transition(transitions: list[dict], target_status: str) -> str:
    for t in transitions:
        if t.get("to", {}).get("name", "").lower() == target_status.lower():
            return t["id"]
    raise TrackerError(
        f"no direct transition to status '{target_status}'",
        available=[t.get("to", {}).get("name") for t in transitions])


class JiraProvider:
    name = "jira"

    def __init__(self, profile: Profile) -> None:
        login = os.environ.get("JIRA_LOGIN")
        token = os.environ.get("JIRA_API_TOKEN")
        if not login or not token:
            raise UsageError("JIRA_LOGIN and JIRA_API_TOKEN must be set")
        tracker = profile.tracker
        self._profile = profile
        self._base_url = tracker["url"].rstrip("/")
        self._api = f"{self._base_url}/rest/api/2"
        self._project = tracker["project"]
        self._issue_type = tracker["issue_type"]
        _, self._fence_label = profile.fence
        self._states = tracker["states"]
        self._state_labels = {r["label"] for r in self._states.values()
                              if r.get("label")}
        self._session = requests.Session()
        self._session.auth = (login, token)

    def _request(self, method: str, path: str, **kwargs):
        url = f"{self._api}/{path}"
        last_error: Exception | None = None
        for _ in range(2):
            try:
                response = self._session.request(method, url, timeout=30, **kwargs)
                if response.status_code >= 500:
                    last_error = TrackerError(
                        f"jira {method} {path}: HTTP {response.status_code}")
                    continue
                if response.status_code >= 400:
                    raise TrackerError(
                        f"jira {method} {path}: HTTP {response.status_code}",
                        body=response.text[:500])
                return response.json() if response.text else {}
            except requests.RequestException as exc:
                last_error = TrackerError(f"jira {method} {path}: {exc}")
        raise last_error  # type: ignore[misc]

    # --- контракт Provider ---
    def capabilities(self) -> Capabilities:
        return Capabilities(attach=True, link=True)

    def list_cards(self, state: str) -> list[Card]:
        repr_ = self._profile.state_repr(state)
        jql = (f'project = {self._project} AND labels = "{self._fence_label}" '
               f'AND status = "{repr_["status"]}"')
        if repr_.get("label"):
            jql += f' AND labels = "{repr_["label"]}"'
        jql += " ORDER BY created ASC"
        data = self._request("GET", "search",
                             params={"jql": jql, "fields": _FIELDS,
                                     "maxResults": 100})
        return [_issue_to_card(i, self._states, self._fence_label, self._base_url)
                for i in data.get("issues", [])]

    def read_card(self, card_id: str) -> Card:
        issue = self._request("GET", f"issue/{card_id}",
                              params={"fields": _FIELDS})
        return _issue_to_card(issue, self._states, self._fence_label,
                              self._base_url)

    def move(self, card_id: str, state: str) -> None:
        repr_ = self._profile.state_repr(state)
        self._transition_to(card_id, repr_["status"])
        # Смена метки состояния: добавить новую, снять прочие состояние-метки.
        current = set(self.read_card(card_id).labels)
        target = {repr_["label"]} if repr_.get("label") else set()
        stale = (current & self._state_labels) - target
        update = ([{"add": lbl} for lbl in target - current]
                  + [{"remove": lbl} for lbl in stale])
        if update:
            self._request("PUT", f"issue/{card_id}",
                          json={"update": {"labels": update}})

    def comment(self, card_id: str, body: str) -> None:
        self._request("POST", f"issue/{card_id}/comment", json={"body": body})

    def create_card(self, title: str, body: str, state: str) -> str:
        repr_ = self._profile.state_repr(state)
        labels = [self._fence_label] + ([repr_["label"]] if repr_.get("label") else [])
        created = self._request("POST", "issue", json={"fields": {
            "project": {"key": self._project},
            "summary": title,
            "description": body,
            "issuetype": {"name": self._issue_type},
            "labels": labels,
        }})
        key = created.get("key")
        if not key:
            raise TrackerError("jira create issue: no key in response",
                               response=created)
        # Созданная карточка рождается в начальном статусе воркфлоу; довести до целевого.
        if self.read_card(key).raw_state.lower() != repr_["status"].lower():
            self._transition_to(key, repr_["status"])
        return key

    def link(self, card_id: str, other_id: str) -> None:
        self._request("POST", "issueLink", json={
            "type": {"name": "Relates"},
            "inwardIssue": {"key": card_id},
            "outwardIssue": {"key": other_id}})

    def attach(self, card_id: str, file_path: str) -> None:
        path = Path(file_path)
        with path.open("rb") as fh:
            url = f"{self._api}/issue/{card_id}/attachments"
            for _ in range(2):
                response = self._session.post(
                    url, files={"file": (path.name, fh)},
                    headers={"X-Atlassian-Token": "no-check"}, timeout=60)
                if response.status_code < 400:
                    return
                fh.seek(0)
            raise TrackerError(
                f"jira attach: HTTP {response.status_code}",
                body=response.text[:500])

    def in_fence(self, card: Card) -> bool:
        return self._fence_label in card.labels

    # --- сырые операции для конформанс-сьюта (teardown, посадка чужого) ---
    def _transition_to(self, card_id: str, status_name: str) -> None:
        data = self._request("GET", f"issue/{card_id}/transitions")
        transition_id = _pick_transition(data.get("transitions", []), status_name)
        self._request("POST", f"issue/{card_id}/transitions",
                      json={"transition": {"id": transition_id}})

    def _set_labels(self, card_id: str, labels: list[str]) -> None:
        self._request("PUT", f"issue/{card_id}",
                      json={"fields": {"labels": labels}})

    def _raw_create(self, title: str, labels: list[str]) -> str:
        created = self._request("POST", "issue", json={"fields": {
            "project": {"key": self._project}, "summary": title,
            "description": "conformance foreign card",
            "issuetype": {"name": self._issue_type}, "labels": labels}})
        return created["key"]

    def _raw_comment(self, card_id: str, text: str) -> None:
        self._request("POST", f"issue/{card_id}/comment", json={"body": text})
```

- [ ] **Step 4: Зелёные юниты** — `uv run pytest tests/unit/test_jira_mapping.py -v`
→ PASS; весь юнит-набор `uv run pytest tests/unit -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/office_adapter/providers/jira.py adapters/tests/unit/test_jira_mapping.py
git commit -m "feat: jira-провайдер — Server REST v2, транзишны, метки состояний"
```

---

### Task 12: Jira-смоук — конформанс против песочницы CRM3 (ручной гейт)

**Files:**
- Modify: `adapters/tests/conformance/polygons.yaml` (добавить jira-секцию)
- Modify: `docs/plans/2026-07-28-adapter-seam.md` (отметить результат прогона)

**Interfaces:**
- Consumes: весь сьют (Task 10), `JiraProvider` (Task 11).

Это **ручной гейт**: прогон против живой корпоративной Jira выполняется только
владельцем или с его явного согласия в сессии. Статусы CRM3 сняты при
планировании (`GET /project/CRM3/statuses`, 2026-07-28): все типы задач имеют
единый воркфлоу `Backlog, To Do, In Progress, Code Review, Ready for Test,
Testing, Closed, Reopened, Suspended, Cancelled, Ready for prod`.

- [ ] **Step 1: Добавить jira-секцию в polygons.yaml**

```yaml
jira:
  profile:
    tracker:
      provider: jira
      url: "https://jira.simbirsoft.com"
      project: "CRM3"
      issue_type: "Story"
      fence: "label:ai-office-sandbox"
      states:
        idea:     {status: "Backlog"}
        analysis: {status: "To Do"}
        done:     {status: "Closed"}
  cleanup:
    mode: jira
    state: "Cancelled"
  outside:
    label: "ai-office-sandbox-outside"
```

Перед прогоном проверить транзишны воркфлоу на одной пробной карточке:
создать руками (или первым прогоном) и убедиться, что `Backlog → To Do`,
`To Do → Closed`, `* → Cancelled` доступны как прямые переходы; если нет —
подобрать другую тройку статусов по факту `GET issue/{key}/transitions` и
поправить секцию (это правка данных полигона, не кода).

- [ ] **Step 2: Прогон**

```bash
cd adapters
export JIRA_LOGIN=<логин>   # значения не коммитить и не печатать в логи
OFFICE_TEST_PROVIDER=jira uv run pytest tests/conformance -v
```

Ожидание: все тесты PASS (link-тест идёт по ветке живого `link`).

- [ ] **Step 3: Проверка уборки**

```bash
~/.claude/skills/jira-cli/scripts/jira-rest.sh GET '/search?jql=labels%20in%20("ai-office-sandbox","ai-office-sandbox-outside")%20AND%20status%20!=%20Cancelled&fields=key'
```

Ожидание: `"total": 0` — вся песочница увезена в Cancelled, метки сняты
teardown'ом. Если нет — дочистить руками и починить `_cleanup`.

- [ ] **Step 4: Зафиксировать результат**

Отметить в этом файле чекбоксы Task 12 и дописать строку с датой прогона и
количеством пройденных тестов. Commit:

```bash
git add adapters/tests/conformance/polygons.yaml docs/plans/2026-07-28-adapter-seam.md
git commit -m "test: jira-смоук конформанс-сьюта против песочницы CRM3"
```

---

### Task 13: Финализация 1а — CHANGELOG, README, полный прогон

**Files:**
- Modify: `CHANGELOG.md` (секция Unreleased)
- Modify: `README.md` (секция «Статус»)

- [ ] **Step 1: Полный прогон юнитов**

```bash
cd adapters && uv run pytest tests/unit -v
```
Ожидание: все PASS. Затем контрольный конформанс:
`OFFICE_TEST_PROVIDER=yougile uv run pytest tests/conformance -v` → PASS.

- [ ] **Step 2: CHANGELOG**

В `CHANGELOG.md` над записью 0.1.0 добавить:

```markdown
## [Unreleased]

### Added
- Подпроект 1а «Шов»: CLI `office-adapter` (uv, Python 3.12+) — профиль клиента
  (JSON-схема + валидатор), провайдеры `yougile` и `jira`, безусловная маркировка
  офисного контента (Р-25), огораживание (§5), read-back после каждой записи.
- Конформанс-сьют шва: зелёный против YouGile-полигона и песочницы CRM3.

Версия 0.2.0 будет выпущена по завершении этапа 1 (подпроект 1б).
```

- [ ] **Step 3: README**

В секции «Статус» README.md отметить: этап 1 в работе; подпроект 1а «Шов»
реализован (адаптеры yougile/jira, конформанс-сьют); следующий шаг — 1б
«Роль и раннер» (Делопроизводитель).

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "docs: статус 1а «Шов» — адаптеры и конформанс-сьют реализованы"
```

---

## Self-review плана

- **Покрытие спеки:** раскладка/CLI (Task 1, 7); маркировка Р-25 (Task 2, 6);
  интерфейс и модель (Task 3); профиль/схема/валидатор + провайдер-ключи jira
  (Task 4); фенс и read-back (Task 6); yougile (Task 8); полигон и сьют
  (Task 9–10); jira (Task 11); смоук CRM3 (Task 12); критерий готовности —
  Task 10/12/13. Секция «attach деградация» — покрыта `capabilities` +
  `CapabilityMissing` (Task 6, 10).
- **Расхождений типов** между задачами нет: сигнатуры `Adapter`/`Provider`/`Profile`
  зафиксированы в блоках Interfaces и повторены в коде.
- **Незакрытое сознательно:** `needs_input`-парковка, базис Р-19, лизы — 1б по спеке.
