from office_adapter.providers.yougile import _message_to_comment, _task_to_card


def test_message_to_comment_detects_marker():
    msg = {"fromUserId": "u1", "timestamp": 1785196800000,
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
