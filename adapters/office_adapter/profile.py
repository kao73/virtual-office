"""Профиль клиента (.office/profile.yaml): поиск, загрузка, валидация (§6)."""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import jsonschema
import yaml

from office_adapter.errors import ProfileError
from office_adapter.interface import ABSTRACT_STATES

SCHEMA_PATH = Path(__file__).resolve().parents[2] / "templates" / "profile.schema.json"

# Провайдер-специфика, которую JSON Schema не выражает без if/then-лапши:
# форма фенса, форма представления состояния, обязательные ключи tracker.
_FENCE_KIND = {"jira": "label", "yougile": "board"}
_REQUIRED_TRACKER_KEYS = {"jira": ("url", "project", "issue_type"), "yougile": ()}
_STATE_REPR_KEYS = {"jira": ({"status"}, {"status", "label"}),
                    "yougile": ({"column"}, {"column"})}


@dataclass
class Profile:
    raw: dict
    path: Path | None = None

    @property
    def tracker(self) -> dict:
        return self.raw["tracker"]

    @property
    def provider_name(self) -> str:
        return self.tracker["provider"]

    @property
    def fence(self) -> tuple[str, str]:
        kind, _, value = self.tracker["fence"].partition(":")
        return kind, value

    def state_repr(self, state: str) -> dict:
        if state not in ABSTRACT_STATES:
            raise ProfileError(f"unknown abstract state: {state!r}",
                               known=list(ABSTRACT_STATES))
        try:
            return self.tracker["states"][state]
        except KeyError:
            raise ProfileError(
                f"state '{state}' is not mapped for this client",
                mapped=sorted(self.tracker["states"])) from None


def find_profile_path(start: Path) -> Path:
    start = start.resolve()
    for directory in [start, *start.parents]:
        candidate = directory / ".office" / "profile.yaml"
        if candidate.is_file():
            return candidate
    raise ProfileError(f"no .office/profile.yaml found from {start} upward")


def load_profile(path: Path) -> Profile:
    try:
        data = yaml.safe_load(path.read_text())
    except (OSError, yaml.YAMLError) as exc:
        raise ProfileError(f"cannot read profile: {exc}") from exc
    return load_profile_data(data, path=path)


def load_profile_data(data: dict, path: Path | None = None) -> Profile:
    schema = yaml.safe_load(SCHEMA_PATH.read_text())
    try:
        jsonschema.validate(data, schema)
    except jsonschema.ValidationError as exc:
        raise ProfileError(f"profile schema violation: {exc.message}",
                           json_path=exc.json_path) from exc
    profile = Profile(raw=data, path=path)
    _check_provider_specifics(profile)
    return profile


def _check_provider_specifics(profile: Profile) -> None:
    provider = profile.provider_name
    kind, value = profile.fence
    if kind != _FENCE_KIND[provider]:
        raise ProfileError(
            f"provider '{provider}' requires fence '{_FENCE_KIND[provider]}:<...>', "
            f"got '{kind}:{value}'")
    missing = [k for k in _REQUIRED_TRACKER_KEYS[provider]
               if k not in profile.tracker]
    if missing:
        raise ProfileError(f"provider '{provider}' requires tracker keys: {missing}")
    required, allowed = _STATE_REPR_KEYS[provider]
    for state, repr_ in profile.tracker["states"].items():
        keys = set(repr_)
        if not required <= keys or not keys <= allowed:
            raise ProfileError(
                f"state '{state}': representation keys {sorted(keys)} invalid for "
                f"'{provider}' (required {sorted(required)}, allowed {sorted(allowed)})")
