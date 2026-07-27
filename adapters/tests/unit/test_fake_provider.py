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
