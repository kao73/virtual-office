// Package office — поставка офиса внутри бинарника раннера: роли, скиллы,
// хуки, граф переходов, дефолтные бюджеты, оба образца и кит песочницы —
// дерево репозитория как есть.
//
// Пакет несёт только данные и не импортирует ничего из internal/: поведение
// над поставкой — распаковка, хеш, разрешение офиса — живёт в internal/office
// и internal/runner. Пакет лежит рядом с содержимым, которое встраивает:
// каталог office/ — это и есть офис, а go:embed вниз по дереву умеет, вверх
// каталога пакета — нет.
package office

import "embed"

// Payload — дерево офиса. Префикс all: обязателен: без него embed молча
// пропускает пути на «_» и «.» — roles/_base и .source.yaml скиллов и кита.
// Прав у embed.FS нет: биты исполняемости восстанавливает распаковка
// по shebang (internal/office).
//
//go:embed all:roles all:skills all:hooks all:sbx-kits all:scheduler
//go:embed workflow.yaml budgets.yaml tracker.example.yaml projects.local.example.yaml
var Payload embed.FS

// Version — версия релиза, вшитая сборкой:
//
//	go build -ldflags "-X github.com/kao73/virtual-office/office.Version=v0.7.0" ./cmd/runner
//
// Пустая у сборки из клона: тогда личность раннера — commit из build info
// (internal/runner.ResolveOffice).
var Version string
