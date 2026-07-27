"""Обвязка провайдера: безусловные законы конституции (фенс §5, маркировка Р-25,
read-back). Провайдер не получает управления раньше этих проверок."""
from __future__ import annotations

from pathlib import Path

from office_adapter.errors import (CapabilityMissing, FenceViolation,
                                   UsageError, VerificationFailed)
from office_adapter.interface import Capabilities, Card, Provider
from office_adapter.marking import detect, mark
from office_adapter.profile import Profile


class Adapter:
    def __init__(self, provider: Provider, profile: Profile) -> None:
        self._provider = provider
        self._profile = profile

    # --- чтение ---
    def capabilities(self) -> Capabilities:
        return self._provider.capabilities()

    def list_cards(self, state: str) -> list[Card]:
        cards = self._provider.list_cards(state)
        for card in cards:
            self._assert_fence(card)
        return cards

    def read_card(self, card_id: str) -> Card:
        card = self._provider.read_card(card_id)
        self._assert_fence(card)
        return card

    # --- запись (всегда: фенс → действие → перечитать → сверить) ---
    def move(self, card_id: str, state: str) -> Card:
        self.read_card(card_id)
        self._provider.move(card_id, state)
        updated = self.read_card(card_id)
        if updated.state != state:
            raise VerificationFailed(
                f"move not confirmed: expected state '{state}', "
                f"tracker shows '{updated.state}' ({updated.raw_state})",
                card_id=card_id)
        return updated

    def comment(self, card_id: str, role: str, body: str) -> Card:
        self.read_card(card_id)
        marked = mark(role, body)
        self._provider.comment(card_id, marked)
        updated = self.read_card(card_id)
        published = [c for c in updated.comments
                     if c.body == marked and c.office_marker == role]
        if not published:
            raise VerificationFailed(
                "comment not confirmed by read-back", card_id=card_id, role=role)
        return updated

    def create_card(self, role: str, title: str, body: str, state: str) -> Card:
        card_id = self.create_card_unverified(role, title, body, state)
        card = self.read_card(card_id)  # заодно проверяет фенс созданного
        if card.state != state:
            raise VerificationFailed(
                f"create not confirmed: expected state '{state}', "
                f"got '{card.state}'", card_id=card_id)
        if detect(card.description) != role:
            raise VerificationFailed(
                "create not confirmed: office marker missing in description",
                card_id=card_id)
        return card

    def create_card_unverified(self, role: str, title: str, body: str,
                               state: str) -> str:
        return self._provider.create_card(title, mark(role, body), state)

    def link(self, card_id: str, other_id: str) -> Card:
        if not self.capabilities().link:
            raise CapabilityMissing(
                f"provider '{self._provider.name}' does not support link")
        other = self.read_card(other_id)
        self.read_card(card_id)
        self._provider.link(card_id, other_id)
        updated = self.read_card(card_id)
        if other.key not in updated.links:
            raise VerificationFailed("link not confirmed by read-back",
                                     card_id=card_id, other=other.key)
        return updated

    def attach(self, card_id: str, file_path: str) -> Card:
        if not self.capabilities().attach:
            raise CapabilityMissing(
                f"provider '{self._provider.name}' does not support attach")
        if not Path(file_path).is_file():
            raise UsageError(f"attach: file not found: {file_path}")
        before = len(self.read_card(card_id).attachments)
        self._provider.attach(card_id, file_path)
        updated = self.read_card(card_id)
        if len(updated.attachments) <= before:
            raise VerificationFailed("attach not confirmed by read-back",
                                     card_id=card_id, file=file_path)
        return updated

    def _assert_fence(self, card: Card) -> None:
        if not self._provider.in_fence(card):
            raise FenceViolation(
                f"card '{card.id}' is outside the office fence "
                f"{self._profile.tracker['fence']!r}", card_id=card.id)
