# office/validators

Сюда `scripts/build-validators.sh` кладёт кросс-собранные ограждения
`validate-result-<os>-<arch>` под три платформы релиза; их встраивают
`validators_<os>_<arch>.go` рядом под `-tags release`. Файлы не
отслеживаются git (см. `.gitignore`); этот README держит каталог в дереве,
чтобы место было видно и описано — сборка создала бы его и сама. Сборка без
тега ограждений не несёт.
