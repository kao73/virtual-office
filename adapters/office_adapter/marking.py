"""Маркировка офисного контента (Р-25): вшита в адаптер, роль не может её обойти."""
from __future__ import annotations

import re

from office_adapter.errors import UsageError

_ROLE_RE = re.compile(r"[a-z][a-z0-9_-]*")
_MARKER_RE = re.compile(r"\[ai-office:([a-z][a-z0-9_-]*)\]")


def mark(role: str, body: str) -> str:
    if not _ROLE_RE.fullmatch(role):
        raise UsageError(f"invalid role for office marker: {role!r}")
    return f"[ai-office:{role}]\n{body}"


def detect(body: str) -> str | None:
    if not body:
        return None
    first_line = body.splitlines()[0].strip()
    m = _MARKER_RE.fullmatch(first_line)
    return m.group(1) if m else None
