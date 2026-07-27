"""In-memory провайдер для юнит-тестов base/CLI и полигон логики без сети."""
from __future__ import annotations

import itertools
from dataclasses import replace

from office_adapter.errors import CapabilityMissing, TrackerError
from office_adapter.interface import Capabilities, Card, CardComment
from office_adapter.profile import Profile


class FakeProvider:
    name = "fake"

    def __init__(self, profile: Profile,
                 capabilities: Capabilities | None = None,
                 lose_writes: bool = False) -> None:
        self._profile = profile
        self._caps = capabilities or Capabilities(attach=True, link=True)
        self._lose = lose_writes
        self._cards: dict[str, Card] = {}
        self._seq = itertools.count(1)
        _, self._fence_value = profile.fence

    # --- служебное для тестов ---
    def plant_card(self, card: Card) -> None:
        self._cards[card.id] = card

    # --- контракт Provider ---
    def capabilities(self) -> Capabilities:
        return self._caps

    def list_cards(self, state: str) -> list[Card]:
        column = self._profile.state_repr(state)["column"]
        return [c for c in self._cards.values()
                if c.raw_state == column and self.in_fence(c)]

    def read_card(self, card_id: str) -> Card:
        try:
            return self._cards[card_id]
        except KeyError:
            raise TrackerError(f"card not found: {card_id}") from None

    def move(self, card_id: str, state: str) -> None:
        column = self._profile.state_repr(state)["column"]
        card = self.read_card(card_id)
        if not self._lose:
            self._cards[card_id] = replace(card, raw_state=column, state=state)

    def comment(self, card_id: str, body: str) -> None:
        from office_adapter.marking import detect
        card = self.read_card(card_id)
        if not self._lose:
            card.comments.append(CardComment(
                author="fake-provider", created="2026-01-01T00:00:00",
                body=body, office_marker=detect(body)))

    def create_card(self, title: str, body: str, state: str) -> str:
        column = self._profile.state_repr(state)["column"]
        card_id = f"fake-{next(self._seq)}"
        self._cards[card_id] = Card(
            id=card_id, key=card_id, title=title, description=body,
            state=state, raw_state=column, url="",
            labels=[self._fence_value])
        return card_id

    def link(self, card_id: str, other_id: str) -> None:
        if not self._caps.link:
            raise CapabilityMissing("fake provider configured without link")
        card = self.read_card(card_id)
        other = self.read_card(other_id)
        if not self._lose:
            card.links.append(other.key)

    def attach(self, card_id: str, file_path: str) -> None:
        from pathlib import Path
        if not self._caps.attach:
            raise CapabilityMissing("fake provider configured without attach")
        card = self.read_card(card_id)
        if not self._lose:
            card.attachments.append(Path(file_path).name)

    def in_fence(self, card: Card) -> bool:
        return self._fence_value in card.labels
