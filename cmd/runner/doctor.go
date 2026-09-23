// runner doctor — преflight без побочных эффектов: инструменты, projects.local.yaml,
// tracker.yaml, живая JIRA, сеть песочницы, старые снапшоты офиса. Порядок стадий и то,
// что пропускает отказ каждой, — docs/superpowers/specs/2026-09-23-doctor-design.md,
// «Loading sequence and failure isolation». Одна command line, один финальный отчёт;
// падение середины не значит тишину по остальному — все стадии копят находки в один
// список и печатают его целиком, даже когда сами отказали на середине.
//
// check-id находок, по стадиям: tool:<имя>, sbx:network, office:stale-snapshots,
// config:home, config:projects.local.yaml, skip:project-dependent, config:tracker.yaml,
// skip:jira, cred:<ПЕРЕМЕННАЯ>, jira:reachability, jira:account, jira:fields (весь
// GET /field упал), jira:field:<owner|run_id|lease_until|attempts>, jira:link-type,
// jira:workflow:<проект>:<роль>, skip:workflow:<проект>.
package main

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/kao73/virtual-office/internal/adapters/claude"
	"github.com/kao73/virtual-office/internal/backends/sbx"
	"github.com/kao73/virtual-office/internal/runagent"
)

// finding — одна строка отчёта: что проверялось, чем кончилось, что сказать
// человеку. check — устойчивый адрес находки (для тестов и сообщений
// о пропуске), не текст на естественном языке.
type finding struct {
	check string
	level string // "ok" | "warn" | "fail"
	msg   string
}

// lookPath — exec.LookPath за переменной: тест подменяет её, не трогая
// настоящий PATH машины. Тот же приём, что у cometExecutable в
// internal/pipeline/archive.go, только здесь подменяется сам поиск,
// а не имя утилиты.
var lookPath = exec.LookPath

// checkTool сообщает, резолвится ли утилита на PATH — по имени, а не единым
// «чего-то не хватает»: спецификация требует назвать конкретный инструмент.
func checkTool(name string) finding {
	if _, err := lookPath(name); err != nil {
		return finding{"tool:" + name, "fail", "не найден на PATH"}
	}
	return finding{"tool:" + name, "ok", "найден"}
}

// doctorCommand — точка входа подкоманды. Флагов кроме --backend нет: ни
// --role (доктор не привязан к роли), ни --json (спецификация требует
// простого текста).
func doctorCommand(args []string, out io.Writer) error {
	fs := flags("doctor")
	backend := fs.String("backend", runagent.BackendLocal, "бэкенд агента: sbx или local — какие проверки бэкенда включать")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var findings []finding
	report := func(f finding) { findings = append(findings, f) }

	// Stage 1a — инструменты, не зависящие ни от проектов, ни от трекера.
	report(checkTool("git"))
	report(checkTool(claude.Executable))
	if *backend == "sbx" {
		report(checkTool(sbx.Executable))
	}

	return concludeExit(out, findings)
}

// concludeExit печатает каждую находку в порядке появления и отказывает,
// только если хоть одна — fail: warn ни на что не влияет, ok — тоже.
func concludeExit(out io.Writer, findings []finding) error {
	var failed int
	for _, f := range findings {
		fmt.Fprintf(out, "%-4s %-28s %s\n", f.level, f.check, f.msg)
		if f.level == "fail" {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("doctor: %d check(s) failed", failed)
}
