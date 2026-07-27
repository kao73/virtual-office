import pytest

from office_adapter.errors import TrackerError
from office_adapter.providers.jira import _issue_to_card, _pick_transition

STATES = {"idea": {"status": "Backlog"},
          "analysis": {"status": "To Do"},
          "design_gate": {"status": "In Progress", "label": "office:design-gate"},
          "merge_gate": {"status": "In Progress", "label": "office:merge-gate"}}

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
    assert card.comments[0].author == "kao"
    assert card.comments[1].office_marker == "clerk"
    assert card.attachments == ["shot.png"]
    assert sorted(card.links) == ["CRM3-1000", "CRM3-998"]


def test_state_disambiguation_by_label():
    issue = {"key": "X-1", "fields": {**ISSUE["fields"],
             "status": {"name": "In Progress"},
             "labels": ["ai-office", "office:design-gate"]}}
    card = _issue_to_card(issue, STATES, "ai-office", "https://j")
    assert card.state == "design_gate"


def test_state_disambiguation_other_label():
    issue = {"key": "X-3", "fields": {**ISSUE["fields"],
             "status": {"name": "In Progress"},
             "labels": ["ai-office", "office:merge-gate"]}}
    card = _issue_to_card(issue, STATES, "ai-office", "https://j")
    assert card.state == "merge_gate"


def test_state_ambiguous_without_label_gives_none():
    issue = {"key": "X-4", "fields": {**ISSUE["fields"],
             "status": {"name": "In Progress"},
             "labels": ["ai-office"]}}  # ни одной state-метки — кандидатов два, выбрать нельзя
    card = _issue_to_card(issue, STATES, "ai-office", "https://j")
    assert card.state is None


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


def test_attach_sends_bytes_not_live_handle(tmp_path, monkeypatch):
    """Урок Task 8 (yougile): после первой попытки живой файловый хендл пуст —
    ретрай должен слать те же байты, а не пытаться перечитать fh."""
    monkeypatch.setenv("JIRA_LOGIN", "kao")
    monkeypatch.setenv("JIRA_API_TOKEN", "token")
    from office_adapter.profile import load_profile_data
    from office_adapter.providers.jira import JiraProvider

    profile = load_profile_data({"tracker": {
        "provider": "jira", "fence": "label:ai-office",
        "url": "https://jira.example.com", "project": "CRM3", "issue_type": "Task",
        "states": {"idea": {"status": "Backlog"}}}})
    provider = JiraProvider(profile)

    captured = {}

    class _Resp:
        status_code = 200
        text = ""

        def json(self):
            return {}

    def fake_post(url, timeout=None, **kwargs):
        if "attachments" in url:
            captured.update(kwargs.get("files", {}))
        return _Resp()

    monkeypatch.setattr(provider._session, "post", fake_post)
    artifact = tmp_path / "report.txt"
    artifact.write_text("отчёт")
    provider.attach("CRM3-1", str(artifact))

    name, payload = captured["file"]
    assert name == "report.txt"
    assert isinstance(payload, bytes)
