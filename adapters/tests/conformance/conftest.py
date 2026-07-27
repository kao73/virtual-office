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
