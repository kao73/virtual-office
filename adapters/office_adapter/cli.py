"""CLI шва: команды повторяют интерфейс §6, вывод — JSON для ролей-скиллов."""
from __future__ import annotations

import argparse
import dataclasses
import json
import sys
from pathlib import Path

import office_adapter.providers as providers
from office_adapter.base import Adapter
from office_adapter.errors import AdapterError, UsageError
from office_adapter.profile import Profile, find_profile_path, load_profile


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="office-adapter")
    parser.add_argument("--profile", help="explicit path to .office/profile.yaml")
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("list-cards")
    p.add_argument("--state", required=True)

    p = sub.add_parser("read-card")
    p.add_argument("card_id")

    p = sub.add_parser("move")
    p.add_argument("card_id")
    p.add_argument("--state", required=True)

    p = sub.add_parser("comment")
    p.add_argument("card_id")
    p.add_argument("--role", required=True)
    p.add_argument("--body-file", required=True)

    p = sub.add_parser("create-card")
    p.add_argument("--role", required=True)
    p.add_argument("--title", required=True)
    p.add_argument("--state", required=True)
    p.add_argument("--body-file")

    p = sub.add_parser("link")
    p.add_argument("card_id")
    p.add_argument("other_id")

    p = sub.add_parser("attach")
    p.add_argument("card_id")
    p.add_argument("file")

    sub.add_parser("capabilities")
    sub.add_parser("validate-profile")
    return parser


def _load(args: argparse.Namespace) -> Profile:
    path = Path(args.profile) if args.profile else find_profile_path(Path.cwd())
    return load_profile(path)


def _read_body(path: str | None) -> str:
    if path is None:
        return ""
    try:
        return Path(path).read_text()
    except OSError as exc:
        raise UsageError(f"cannot read body file: {exc}") from exc


def _card_json(card) -> dict:
    return dataclasses.asdict(card)


def _emit(payload: dict) -> None:
    json.dump(payload, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        profile = _load(args)
        if args.command == "validate-profile":
            _emit({"ok": True, "provider": profile.provider_name,
                   "profile": str(profile.path)})
            return 0
        adapter = Adapter(providers.get_provider(profile), profile)
        if args.command == "list-cards":
            _emit({"cards": [_card_json(c) for c in adapter.list_cards(args.state)]})
        elif args.command == "read-card":
            _emit(_card_json(adapter.read_card(args.card_id)))
        elif args.command == "move":
            _emit(_card_json(adapter.move(args.card_id, args.state)))
        elif args.command == "comment":
            _emit(_card_json(adapter.comment(
                args.card_id, args.role, _read_body(args.body_file))))
        elif args.command == "create-card":
            _emit(_card_json(adapter.create_card(
                args.role, args.title, _read_body(args.body_file), args.state)))
        elif args.command == "link":
            _emit(_card_json(adapter.link(args.card_id, args.other_id)))
        elif args.command == "attach":
            _emit(_card_json(adapter.attach(args.card_id, args.file)))
        elif args.command == "capabilities":
            _emit(dataclasses.asdict(adapter.capabilities()))
        return 0
    except AdapterError as err:
        json.dump(err.to_json(), sys.stderr, ensure_ascii=False)
        sys.stderr.write("\n")
        return err.exit_code


def run() -> None:
    raise SystemExit(main())
