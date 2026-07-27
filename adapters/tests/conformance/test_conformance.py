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
