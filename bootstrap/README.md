# Запуск раннера по расписанию

`runner loop` — не демон и не supervisor: он не следит за собой, не перезапускается
и не держит состояния между циклами. Поднимать и ронять его должен системный
планировщик. Ниже примеры для macOS и Linux; оба задают одно и то же:
рабочий каталог, кред агента, каталог хозяйства и лог.

Секретов в этих файлах нет и быть не должно. `CLAUDE_CODE_OAUTH_TOKEN` (или
`ANTHROPIC_API_KEY`) и `GITHUB_TOKEN` подставляются из окружения, которое готовит
администратор машины: `launchctl setenv`, `systemctl edit`, файл с правами 600 —
что угодно, кроме репозитория.

## macOS, launchd

`~/Library/LaunchAgents/local.office.runner.plist` — правь пути и загружай:

    launchctl load ~/Library/LaunchAgents/local.office.runner.plist
    launchctl unload ~/Library/LaunchAgents/local.office.runner.plist

launchd останавливает задание сигналом SIGTERM, и раннер завершает цикл сам —
между прогонами, а не посреди. Прерванный посреди прогона он оставил бы задачу
арендованной до истечения аренды; вернул бы её потом `reap`, но лишний круг ни к чему.

## Linux, systemd

Пара `office-runner.service` + `office-runner.timer`. Раннер работает
разовым запуском (`Type=oneshot`), а расписание держит таймер:

    systemctl --user enable --now office-runner.timer
    systemctl --user list-timers office-runner.timer

Разовый запуск вместо `loop` выбран намеренно: планировщик уже умеет расписание,
и дублировать его циклом внутри процесса незачем. `loop` пригодится там, где
планировщика нет вовсе.

## Проверить руками

    ./bin/runner tick --role implementer     # один цикл
    ./bin/runner reap                        # вернуть задачи с истёкшей арендой
    ./bin/runner loop --every 2m             # цикл до Ctrl+C
