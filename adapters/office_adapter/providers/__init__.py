"""Реестр провайдеров: единственное место, знающее конкретные реализации."""
from __future__ import annotations

from office_adapter.errors import ProfileError
from office_adapter.interface import Provider
from office_adapter.profile import Profile


def get_provider(profile: Profile) -> Provider:
    name = profile.provider_name
    if name == "yougile":
        from office_adapter.providers.yougile import YougileProvider
        return YougileProvider(profile)
    if name == "jira":
        from office_adapter.providers.jira import JiraProvider
        return JiraProvider(profile)
    raise ProfileError(f"unknown tracker provider: {name!r}")
