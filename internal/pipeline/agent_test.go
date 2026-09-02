package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
)

// agentWorktreeFixture — bare-репозиторий и настоящий git worktree на ветке
// branch, тем же приёмом, что internal/workspace/clone_test.go: cloneOptionsFor
// зовёт workspace.CloneSource на уже готовой рабочей папке, а не заводит её сама.
func agentWorktreeFixture(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare-репозиторий не создан: %v\n%s", err, out)
	}
	seed := filepath.Join(root, "seed")
	if out, err := exec.Command("git", "clone", "-q", bare, seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной клон не создан: %v\n%s", err, out)
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=тест", "GIT_AUTHOR_EMAIL=test@office.local",
		"GIT_COMMITTER_NAME=тест", "GIT_COMMITTER_EMAIL=test@office.local")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# проект\n"), 0o644); err != nil {
		t.Fatalf("README не записан: %v", err)
	}
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "начало")
	run(seed, "push", "-q", "origin", "master")

	dir := filepath.Join(root, "worktree")
	run(seed, "worktree", "add", "-q", "-b", branch, dir)
	run(dir, "commit", "-q", "--allow-empty", "-m", "работа задачи")
	return dir
}

func TestCloneOptionsForLocalKeepsRealWorkdirAndNoClone(t *testing.T) {
	req := Request{Workdir: "/куда-угодно", Branch: "agent/OFF-1"}

	workdir, clone, cleanup, err := cloneOptionsFor(context.Background(), runagent.BackendLocal, req)
	if err != nil {
		t.Fatalf("cloneOptionsFor: %v", err)
	}
	if workdir != req.Workdir {
		t.Errorf("workdir = %q, ожидался req.Workdir %q — local обязан остаться на настоящей рабочей папке", workdir, req.Workdir)
	}
	if clone != nil {
		t.Errorf("Clone = %+v, ожидался nil на local", clone)
	}
	if cleanup == nil {
		t.Fatal("cleanup не должен быть nil даже на local — вызывающий зовёт его безусловно")
	}
	if err := cleanup(); err != nil {
		t.Errorf("cleanup на local не должна ничего чистить и не должна падать: %v", err)
	}
}

func TestCloneOptionsForSbxBuildsDisposableCloneSource(t *testing.T) {
	dir := agentWorktreeFixture(t, "agent/OFF-1")
	req := Request{Workdir: dir, Branch: "agent/OFF-1"}

	workdir, clone, cleanup, err := cloneOptionsFor(context.Background(), runagent.DefaultBackend, req)
	if err != nil {
		t.Fatalf("cloneOptionsFor: %v", err)
	}
	defer cleanup()

	if workdir == dir {
		t.Error("workdir не сменился на одноразовый клон-источник")
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("клон-источник не найден: %v", err)
	}
	if clone == nil {
		t.Fatal("Clone не выставлен для бэкенда sbx")
	}
	if clone.FetchInto != dir {
		t.Errorf("FetchInto = %q, ожидалась настоящая рабочая папка %q", clone.FetchInto, dir)
	}
	if clone.Branch != "agent/OFF-1" {
		t.Errorf("Branch = %q, ожидалось agent/OFF-1", clone.Branch)
	}
	if len(clone.Dirs) != 2 || clone.Dirs[0] != runner.Dir || clone.Dirs[1] != ".comet/runtime" {
		t.Errorf("Dirs = %q, ожидалось [%s .comet/runtime]", clone.Dirs, runner.Dir)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("клон-источник %s не убран", workdir)
	}
}

func TestCloneOptionsForSbxPropagatesCloneSourceFailure(t *testing.T) {
	dir := agentWorktreeFixture(t, "agent/OFF-1")
	req := Request{Workdir: dir, Branch: "нет-такой-ветки"}

	if _, _, _, err := cloneOptionsFor(context.Background(), runagent.DefaultBackend, req); err == nil {
		t.Fatal("неудача клона-источника (несуществующая ветка) прошла без ошибки")
	}
}

// BLOCKER-находка независимого ревью: до фикса runagent.Execute (syncErr)
// эта ветка Run — «результат агента уже есть, но что-то после него
// сорвалось, логируем и не хороним задачу» — была мертва: Execute никогда
// не возвращала одновременно и заполненный out.Result, и ошибку. Здесь она
// наконец достижима, и SandboxAgent.Run обязана реально залогировать беду
// и вернуть настоящий результат агента, а не ошибку.
// Improvement-находка независимого ревью (round 1): результат агента уже
// есть, но песочница отдала не всё (ErrSyncIncomplete из runagent.Execute) —
// раньше это шло тем же путём, что и безобидная неудача runner.Archive
// (Outcome заполнен → лог и «run, nil»), и задача репортилась в тикет как
// done, хотя коммиты агента могли остаться в уже снесённой песочнице.
// Run обязана провалить прогон явно, а не выдумывать успех.
func TestRunFailsHardOnSyncIncompleteEvenWithResult(t *testing.T) {
	orig := executeAgent
	defer func() { executeAgent = orig }()

	wantResult := runner.Result{Outcome: runner.OutcomeDone, Summary: "готово", NextOwner: "none"}
	syncErr := runagent.NewErrSyncIncomplete(errors.New("comet-state.yaml не подтянут"))
	executeAgent = func(_ context.Context, _ runagent.Options) (runagent.Outcome, error) {
		return runagent.Outcome{Result: wantResult}, syncErr
	}

	var log bytes.Buffer
	a := SandboxAgent{Backend: runagent.BackendLocal, Log: &log}
	run, err := a.Run(context.Background(), Request{
		Workdir: t.TempDir(), Passport: runner.Run{TaskKey: "OFF-1"},
	})
	if !errors.Is(err, syncErr) {
		t.Fatalf("Run = %v, ожидалась ошибка синхронизации — задача должна провалиться, а не репортоваться как done", err)
	}
	if run.Result.Outcome != "" {
		t.Errorf("Result не должен возвращаться вызывающему при провале: %+v", run)
	}
	if !strings.Contains(log.String(), syncErr.Error()) {
		t.Errorf("ошибка синхронизации не залогирована: %q", log.String())
	}
}

// Симметричный случай: обычная (не ErrSyncIncomplete) ошибка после того, как
// результат уже есть, — например, неудача runner.Archive — не теряет ничего
// (материал уже надёжно лежит в FetchInto), и Run обязана сохранить прежнее,
// снисходительное поведение: лог и настоящий результат агента, не провал.
func TestRunSurvivesNonSyncErrorWithResult(t *testing.T) {
	orig := executeAgent
	defer func() { executeAgent = orig }()

	wantResult := runner.Result{Outcome: runner.OutcomeDone, Summary: "готово", NextOwner: "none"}
	archiveErr := errors.New("прогон не заархивирован: диск занят")
	executeAgent = func(_ context.Context, _ runagent.Options) (runagent.Outcome, error) {
		return runagent.Outcome{Result: wantResult}, archiveErr
	}

	var log bytes.Buffer
	a := SandboxAgent{Backend: runagent.BackendLocal, Log: &log}
	run, err := a.Run(context.Background(), Request{
		Workdir: t.TempDir(), Passport: runner.Run{TaskKey: "OFF-1"},
	})
	if err != nil {
		t.Fatalf("Run вернула ошибку, хотя результат агента уже есть и это не ErrSyncIncomplete: %v", err)
	}
	if run.Result.Outcome != runner.OutcomeDone {
		t.Errorf("Result.Outcome = %q, ожидалось %q — настоящий результат агента потерян", run.Result.Outcome, runner.OutcomeDone)
	}
	if !strings.Contains(log.String(), archiveErr.Error()) {
		t.Errorf("ошибка архивации не залогирована: %q", log.String())
	}
}

// Симметричный случай: без результата вовсе (агент не успел ничего оставить)
// Run обязана вернуть саму ошибку, а не выдумывать успех.
func TestRunPropagatesErrorWithoutResult(t *testing.T) {
	orig := executeAgent
	defer func() { executeAgent = orig }()

	wantErr := errors.New("claude CLI не запустился")
	executeAgent = func(_ context.Context, _ runagent.Options) (runagent.Outcome, error) {
		return runagent.Outcome{}, wantErr
	}

	a := SandboxAgent{Backend: runagent.BackendLocal}
	if _, err := a.Run(context.Background(), Request{
		Workdir: t.TempDir(), Passport: runner.Run{TaskKey: "OFF-1"},
	}); !errors.Is(err, wantErr) {
		t.Errorf("Run = %v, ожидалась исходная ошибка %v", err, wantErr)
	}
}

// Мьютс и Clone несовместимы (runagent.Prepare это проверяет), и на бэкенде
// local Clone всегда nil (cloneOptionsFor) — Mounts обязаны дойти до opts
// как есть. Ловим это через executeAgent, а не полагаясь на то, что Prepare
// где-то ниже когда-нибудь откажет вместо тихого пропуска.
func TestRunPassesMountsThroughOnLocalBackend(t *testing.T) {
	orig := executeAgent
	defer func() { executeAgent = orig }()

	wantMounts := []runner.Workspace{{Path: "/repo.git"}}
	var gotMounts []runner.Workspace
	executeAgent = func(_ context.Context, opts runagent.Options) (runagent.Outcome, error) {
		gotMounts = opts.Mounts
		return runagent.Outcome{Result: runner.Result{Outcome: runner.OutcomeDone, NextOwner: "none"}}, nil
	}

	a := SandboxAgent{Backend: runagent.BackendLocal}
	if _, err := a.Run(context.Background(), Request{
		Workdir: t.TempDir(), Passport: runner.Run{TaskKey: "OFF-1"}, Mounts: wantMounts,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gotMounts) != 1 || gotMounts[0].Path != "/repo.git" {
		t.Errorf("opts.Mounts = %+v, ожидалось %+v", gotMounts, wantMounts)
	}
}
