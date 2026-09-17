# payload/validators

Сюда `scripts/build-validators.sh` кладёт кросс-собранные ограждения
`validate-result-<os>-<arch>` под три платформы релиза; их встраивают
`validators_<os>_<arch>.go` в корне под `-tags release`. Файлы не
отслеживаются git (см. `.gitignore`) — этот README держит каталог на месте,
чтобы embed-паттернам было куда смотреть. Сборка без тега ограждений не несёт.
