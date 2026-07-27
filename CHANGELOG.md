# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/);
версионирование — [semver](https://semver.org/lang/ru/): `0.x` до боевой
обкатки этапа 1, минор — на этап дорожной карты, патч — на исправления.

## [0.1.0] — 2026-07-28

### Added

- Скелет плагина Claude Code: манифест `.claude-plugin/plugin.json`
  (этап 0 дорожной карты, спека `docs/specs/2026-07-27-plugin-skeleton-design.md`).
- Маркетплейс-манифест `.claude-plugin/marketplace.json` — репозиторий
  одновременно плагин и его маркетплейс (Р-08).
- Скилл-маркер `office-about`: справка об офисе, его версии и текущем этапе;
  проверяет путь «установка → загрузка скилла» для этапа 1.
