"""Абстракция трекера (§6): модель карточки и контракт провайдера."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Protocol

ABSTRACT_STATES: tuple[str, ...] = (
    "idea", "analysis", "design_gate", "ready_for_dev", "in_dev",
    "ai_qa", "merge_gate", "bookkeeping", "external_qa", "done", "needs_input",
)


@dataclass
class CardComment:
    author: str
    created: str  # ISO 8601
    body: str
    office_marker: str | None  # роль из маркера Р-25 либо None


@dataclass
class Card:
    id: str
    key: str
    title: str
    description: str
    state: str | None  # абстрактное состояние; None, если представление не отображается назад
    raw_state: str
    url: str
    labels: list[str] = field(default_factory=list)
    comments: list[CardComment] = field(default_factory=list)
    links: list[str] = field(default_factory=list)
    attachments: list[str] = field(default_factory=list)


@dataclass
class Capabilities:
    attach: bool
    link: bool


class Provider(Protocol):
    name: str

    def capabilities(self) -> Capabilities: ...
    def list_cards(self, state: str) -> list[Card]: ...
    def read_card(self, card_id: str) -> Card: ...
    def move(self, card_id: str, state: str) -> None: ...
    def comment(self, card_id: str, body: str) -> None: ...
    def create_card(self, title: str, body: str, state: str) -> str: ...
    def link(self, card_id: str, other_id: str) -> None: ...
    def attach(self, card_id: str, file_path: str) -> None: ...
    def in_fence(self, card: Card) -> bool: ...
