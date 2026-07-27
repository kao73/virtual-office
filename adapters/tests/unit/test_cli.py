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


def test_missing_required_arg_yields_usage_error_json(client_dir, capsys):
    code = cli.main(["comment", "some-id", "--body-file", "x.txt"])  # нет --role
    err = json.loads(capsys.readouterr().err)
    assert code == 1
    assert err["error"] == "usage_error"


def test_missing_subcommand_yields_usage_error_json(client_dir, capsys):
    code = cli.main([])
    err = json.loads(capsys.readouterr().err)
    assert code == 1
    assert err["error"] == "usage_error"
