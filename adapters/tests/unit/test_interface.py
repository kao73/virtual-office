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
