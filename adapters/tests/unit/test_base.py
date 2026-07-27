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
