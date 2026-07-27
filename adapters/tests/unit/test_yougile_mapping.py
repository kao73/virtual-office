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


def test_attach_sends_bytes_not_live_handle(tmp_path, monkeypatch):
    monkeypatch.setenv("YOUGILE_API_KEY", "test-key")
    from office_adapter.profile import load_profile_data
    from office_adapter.providers.yougile import YougileProvider

    profile = load_profile_data({"tracker": {
        "provider": "yougile", "fence": "board:b1",
        "states": {"idea": {"column": "c1"}}}})
    provider = YougileProvider(profile)

    captured = {}

    class _Resp:
        status_code = 200
        text = '{"url": "https://yougile.com/files/report.txt"}'

        def json(self):
            return {"url": "https://yougile.com/files/report.txt"}

    def fake_request(method, url, timeout=None, **kwargs):
        if "upload-file" in url:
            captured.update(kwargs.get("files", {}))
        return _Resp()

    monkeypatch.setattr(provider._session, "request", fake_request)
    artifact = tmp_path / "report.txt"
    artifact.write_text("отчёт")
    provider.attach("t1", str(artifact))

    name, payload = captured["file"]
    assert name == "report.txt"
    assert isinstance(payload, bytes)
