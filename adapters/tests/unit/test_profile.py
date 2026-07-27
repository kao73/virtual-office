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
