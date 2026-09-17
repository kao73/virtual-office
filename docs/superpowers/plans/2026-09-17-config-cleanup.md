---
change: config-cleanup
design-doc: docs/superpowers/specs/2026-09-17-config-cleanup-design.md
base-ref: 33395e52146750ff3db5cb3a1829db332501a54e
archived-with: 2026-09-17-config-cleanup
---

# config-cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the office repository a pure framework — no project list, no per-machine values — so that a clean clone starts on a new machine without editing a single committed file, and the runner serves every tracker its projects name without a `--tracker` flag.

**Architecture:** Six ordered layers, each depending on the one before it: (1) the rules every role shares (git/`rm` denies, generic registries) move from `projects.yaml: defaults` into `roles/_base/base.yaml` and `runner.LoadRole` unions them into every role — `tracker.unionStrings` becomes `runner.Union` so both packages merge the same way; (2) `tracker.LoadProjects` reads the machine file alone (`default_branch` required, `branch_prefix` defaults to `agent/`), guards `defaults` with one allow-list, and a `RefuseLeftoverOfficeFile` guard refuses a stale `projects.yaml`; (3) `cmd/runner` builds one `pipeline.Office` per tracker in use and walks them through a small `offices` type (`each`, `byProject`, `cycle`, `loop`); `pipeline.Office.Loop`/`tickOnce` are deleted — the only pipeline edit; (4) `tracker.example.yaml` becomes copy-ready and `scripts/jira-setup.sh` creates the `Depends` link type; (5) `projects.yaml` is deleted, this machine's `~/.office/projects.local.yaml` is migrated, and the two no-token live checks run; (6) `docs/DESIGN.md` §2.5, `README.md` and `docs/ONBOARDING.md` are corrected where they would otherwise be false.

**Tech Stack:** Go (stdlib, `gopkg.in/yaml.v3`, table-driven `testing`, `httptest` for the JIRA fake), bash + inline python3 in `scripts/jira-setup.sh`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-17-config-cleanup-design.md` (Russian; this plan translates and operationalizes it). High-level decisions D1–D9 are in `docs/openspec/changes/config-cleanup/design.md`; acceptance scenarios in `docs/openspec/changes/config-cleanup/specs/{config-boundary,runner-multi-tracker,role-sandbox-permissions}/spec.md`. Task boundaries mirror `docs/openspec/changes/config-cleanup/tasks.md` — task numbers below (1–6) are the same groups, and each step cites its `tasks.md` item (e.g. "tasks.md 2.1").

## Global Constraints

- **Repository is the framework.** After this change no committed file carries an instance value; `projects.yaml` does not exist; the only rules layer shipped with the repository is `roles/_base/base.yaml`.
- **`--backend` is untouched** (owner's decision). `--tracker` is removed from every `runner` subcommand.
- **The pipeline package does not change behaviour.** The only edit inside `internal/pipeline` is deleting `Office.Loop` and `tickOnce` (and adjusting the two tests that called `Loop`). Multi-tracker is a `cmd/runner` concern.
- **Strict build, isolated tick** (D5): a tracker that cannot be opened refuses the whole command before any office works; inside one `tick`, every office is visited and errors are joined; `loop` logs and continues.
- **`tracker.yaml` is opened only when some project says `tracker: jira`** (D6), and it is listed after `projects.local.yaml` in the configuration-source printout.
- **`default_branch` is required, `branch_prefix` defaults to `agent/`** (D2); `defaults` accepts only `network` and `tools` (D3).
- **Comments in Go are Russian prose explaining WHY**, at the density of the surrounding code; identifiers English; user-facing messages Russian. Every file must be `gofmt`-clean.
- **TDD is the repo's mode:** every code step writes the failing test first, runs it to see it fail, implements, runs it to see it pass.
- **After every task:** `go build ./... && go vet ./... && go test ./...` must be green before moving on (this is a running invariant, not a one-time final step).
- **Every commit message ends with** `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`. Subjects follow the repo's style (`feat(config): …`, `refactor(runner): …`, `docs: …`); bodies in Russian are fine.
- **No paid agent runs anywhere in this plan.** The two live checks (Task 4.3 — JIRA script; Task 5.3 — `runner ls`, `run-agent --dry-run`) spend no tokens.
- **One step is outside the repository** (Task 5.2: editing `~/.office/projects.local.yaml`). It is an operator step, flagged as such, with the exact YAML from the Design Doc.
- **Docs edited here are only DESIGN.md §2.5, README.md, ONBOARDING.md** (D9). `docs/notes/`, `docs/comet/`, `docs/STAGE-*.md`, `docs/contracts/*.md` and `bootstrap/` are history or the follow-up documentation change and are not touched; Task 5.1 lists the expected residual `git grep projects.yaml` hits explicitly.

---

## Task 1: Shared role rules move to `roles/_base/base.yaml`

Corresponds to `tasks.md` §1 (1.1–1.3). Also rewrites one `cmd/run-agent` test that asserted the old behaviour ("no `--project` → no repo-wide rules"), because after this task the base layer arrives through `LoadRole` for every run.

**Files:**
- Create: `roles/_base/base.yaml`
- Modify: `internal/runner/role.go` (constants `BaseDir`, `BaseRulesFile`; `baseRules`; `loadBaseRules`; `Union`; `LoadRole`)
- Modify: `internal/tracker/config.go` (delete `unionStrings`, call `runner.Union`), `internal/tracker/rules.go` (call `runner.Union`)
- Test: `internal/tracker/boundary_test.go`, `internal/runner/role_test.go`, `internal/tracker/config_test.go` (move the union test out), `internal/adapters/claude/adapter_test.go` (fixture), `cmd/run-agent/main_test.go`

**Interfaces:**
- Produces: `runner.BaseDir = "_base"`, `runner.BaseRulesFile = "base.yaml"`, `runner.Union(layers ...[]string) []string` (sorted, deduplicated, `nil` for no input). `runner.LoadRole(configRoot, name)` now returns a role whose `Network.Allow`, `Tools.Allow`, `Tools.Deny` already include `roles/_base/base.yaml`. Task 2 calls `runner.Union` from `LoadProjects` and `MergeProjectRules`; Task 2's `RefuseLeftoverOfficeFile` names `runner.RolesDir/BaseDir/BaseRulesFile` in its message.

### Background

`internal/runner/role.go` has `LoadRole` (line 93) that reads `roles/<name>/role.yaml` strictly, sets `r.dir`/`r.configRoot`, calls `r.validate(name)` and returns. `internal/tracker/config.go` has `unionStrings(layers ...[]string) []string` (line 659) used by `LoadProjects` and by `MergeProjectRules` in `rules.go`. Test fixtures that build a config root with `roles/_base/base.md` but no `base.yaml`: `fixtureOffice` in `internal/runner/role_test.go:28` and `fixtureOffice` in `internal/adapters/claude/adapter_test.go:49`. After this task, a `roles/_base/` directory without `base.yaml` is a refusal, so both fixtures must write a `base.yaml` (empty layer).

Two existing assertions are order-sensitive and will need adjusting once `Union` sorts: `internal/runner/role_test.go` `TestLoadRole` (`role.Tools.Allow[1] != "Bash(git *)"`) and `TestRoleKeepsNetworkAllowAsWritten` (exact order). Their intent — "rules are not mangled at parse" — survives as a set comparison.

- [x] **Step 1: Write the failing shipping test (tasks.md 1.3)**

Append to `internal/tracker/boundary_test.go` (add `"github.com/kao73/virtual-office/internal/runner"` to imports):

```go
// Базовые правила ролей — единственный слой правил, который поставляется
// репозиторием: он лежит рядом с ролями, к которым относится, а не в файле
// проектов, которого в репозитории больше нет. Гарантия, что origin
// принадлежит раннеру, а не агенту, держится одной строкой этого файла,
// и исчезнуть молча она не должна — ни переименованием файла, ни правкой.
func TestShippedBaseRulesKeepGitRemoteDeny(t *testing.T) {
	path := filepath.Join("..", "..", runner.RolesDir, runner.BaseDir, runner.BaseRulesFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("базовые правила ролей не поставлены: %v", err)
	}
	if !strings.Contains(string(raw), `"Bash(git *push*)"`) {
		t.Errorf("%s не запрещает git push: пуш — дело раннера, а не агента", path)
	}
}
```

- [x] **Step 2: Add the constants so the test compiles, run it to see it fail**

In `internal/runner/role.go`, after `const RoleFile = "role.yaml"`:

```go
// BaseDir — каталог правил, общих для всех ролей; BaseRulesFile — их
// машиночитаемая половина рядом с base.md. Каталог с подчёркиванием
// ролью не считается (см. shippedRoles в тестах): в нём лежит то, от чего
// наследуют все.
const (
	BaseDir       = "_base"
	BaseRulesFile = "base.yaml"
)
```

Run: `go test ./internal/tracker/ -run TestShippedBaseRulesKeepGitRemoteDeny -v`
Expected: FAIL with "базовые правила ролей не поставлены: open ../../roles/_base/base.yaml: no such file or directory".

- [x] **Step 3: Create `roles/_base/base.yaml` (tasks.md 1.1)**

Content is the generic part of today's `projects.yaml: defaults`, evidence comments kept. EXP-specific hosts (`mcr.microsoft.com`, `*.data.mcr.microsoft.com`, `bun.sh`, `cdn.playwright.dev`, `playwright.download.prss.microsoft.com`, `ports.ubuntu.com`, `deb.nodesource.com`) are **not** carried over — they move to the `EXP` entry of this machine's file in Task 5.2. The comments must not contain `/Users/`, `/home/`, `/opt/`, `/var/` or `customfield_` (the boundary test scans `roles/*/*`).

```yaml
# Правила, общие для всех ролей. Действуют всегда, поверх любой роли:
# роль может добавить, но не отменить (base.md). Слияние — объединение:
# LoadRole (internal/runner/role.go) кладёт эти списки в каждую роль при
# загрузке, с проектом или без — ручной run-agent получает те же запреты,
# что и конвейер. Форма — та же, что у блоков network/tools в role.yaml,
# потому что это и есть роль, от которой наследуют все.
#
# Хосты, найденные для одного проекта (Playwright, MCR, bun, apt-зеркала),
# здесь не лежат: они свойство этого проекта на этой машине и живут в его
# записи в ${OFFICE_HOME}/projects.local.yaml. Свидетельства, откуда взялся
# каждый хост, остаются в истории: `git log -- projects.yaml`.
network:
  allow:
    # Docker Hub — проверено эмпирически 2026-08-27 (пустая одноразовая
    # песочница sbx → голая попытка → добавление хостов по факту ошибки),
    # docs/notes/followup-network-and-permissions.md, «Находка 1».
    - registry-1.docker.io
    - auth.docker.io
    - "*.docker.io"
    - production.cloudfront.docker.com   # CDN раздачи blob'ов, не cloudflare.docker.com
    - "*.cloudfront.docker.com"
    # GitHub — проверено эмпирически 2026-08-27, тем же способом. `git clone`
    # по HTTPS упёрся только в github.com; api/codeload/raw/objects.*
    # в `sbx policy log` не всплыли и как непроверенные сюда не добавлены.
    - github.com
    # npm — проверено эмпирически 2026-08-27: `npm view left-pad` целиком
    # уложился в registry.npmjs.org.
    - registry.npmjs.org
    # PyPI — проверено эмпирически 2026-08-27: `uv run --with pytest`
    # потребовал ровно эти два хоста.
    - pypi.org
    - files.pythonhosted.org
    # Go modules — проверено эмпирически 2026-08-27: `go get` потребовал
    # module-proxy и sumdb порознь.
    - proxy.golang.org
    - sum.golang.org
    # GitHub Container Registry — проверено эмпирически 2026-09-13:
    # `docker pull ghcr.io/astral-sh/uv` голой попыткой упал 403 на ghcr.io,
    # после allow — второй 403 на blob-CDN; с обоими пулл прошёл целиком.
    - ghcr.io
    - pkg-containers.githubusercontent.com
    # GitHub Releases — проверено эмпирически 2026-09-13: бинарники релизов
    # качаются с release-assets.githubusercontent.com, а не с
    # objects.githubusercontent.com из списка-догадки.
    - release-assets.githubusercontent.com
tools:
  deny:
    # Общие для всех ролей: работу с origin, переключение веток и подмену
    # личности коммитов ведёт раннер, а не агент. Форма со звёздочкой МЕЖДУ
    # "git" и подкомандой (не по краям) — единственная, что ловит и флаговую
    # форму: проверено эмпирически 2026-08-27,
    # docs/contracts/role-sandbox-permissions.md.
    - "Bash(git *push*)"
    - "Bash(git *remote*)"
    - "Bash(git *checkout*)"
    - "Bash(git *switch*)"
    - "Bash(git *branch*)"
    - "Bash(git *worktree*)"
    - "Bash(git *config*)"
    # Переписывание истории — та же природа: чужая, уже опубликованная
    # работа не должна переписываться ни одной ролью.
    - "Bash(git *filter-branch*)"
    - "Bash(git *filter-repo*)"
    # Рекурсивное/принудительное удаление — тем же приёмом: звёздочка
    # вокруг флага, а не по краям, чтобы не зависеть от его формы
    # (-rf/-fr или --recursive/--force) и места (до или после пути).
    - "Bash(rm *-r*)"
    - "Bash(rm *-f*)"
    - "Bash(rm *--recursive*)"
    - "Bash(rm *--force*)"
```

- [x] **Step 4: Run the shipping test and the boundary scan to see them pass**

Run: `go test ./internal/tracker/ -run 'TestShippedBaseRulesKeepGitRemoteDeny|TestRepoCarriesNoMachineValues' -v`
Expected: PASS (the boundary scan already globs `roles/*/*`, so the new file is scanned for machine values).

- [x] **Step 5: Commit**

```bash
git add roles/_base/base.yaml internal/runner/role.go internal/tracker/boundary_test.go
git commit -m "$(cat <<'MSG'
feat(roles): ship roles/_base/base.yaml — the rules every role inherits

Запреты git push/remote/checkout/switch/branch/worktree/config, переписывания
истории и rm -rf, плюс общие реестры (Docker Hub, GitHub, ghcr, GitHub
Releases, npm, PyPI, Go) переезжают из projects.yaml: defaults рядом
с base.md — это правила ролей, а не инстанса. Хосты одного проекта
(Playwright, MCR, bun, apt) не переносятся: их место — запись проекта
в ${OFFICE_HOME}/projects.local.yaml. Тест поставки сторожит, что запрет
на git push не исчезнет молча.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [x] **Step 6: Write the failing `Union` test in `internal/runner/role_test.go`**

Append (the file already imports `slices`):

```go
// Union — дедуп и сортировка через все слои разом: один приём для
// network.allow и для каждого из tools.allow/tools.deny, у базового слоя
// (LoadRole) и у проектных (tracker.LoadProjects, tracker.MergeProjectRules).
// Без слоёв — nil, а не пустой срез: так ведёт себя и разбор YAML без ключа.
func TestUnionDedupsAndSorts(t *testing.T) {
	got := Union([]string{"b", "a"}, nil, []string{"a", "c"})
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("Union = %v, ожидалось %v", got, want)
	}
	if got := Union(); got != nil {
		t.Errorf("Union() без слоёв = %v, ожидался nil", got)
	}
}
```

Run: `go test ./internal/runner/ -run TestUnionDedupsAndSorts -v`
Expected: FAIL — "undefined: Union".

- [x] **Step 7: Move `unionStrings` to `runner.Union`**

In `internal/runner/role.go` (after `Network`'s definition, before `LoadRole`):

```go
// Union сливает несколько слоёв правил в один список без потерь: то, что
// назвал любой слой, остаётся в итоге, повторы схлопываются, порядок —
// алфавитный, чтобы два прогона отдавали агенту одинаковые настройки.
// Один и тот же приём — для network.allow и для каждого из tools.allow/
// tools.deny по отдельности. Живёт здесь, а не в tracker: базовый слой
// (LoadRole) и проектные (tracker.LoadProjects, tracker.MergeProjectRules)
// сливаются им одинаково, а tracker импортирует runner, не наоборот.
func Union(layers ...[]string) []string {
	var all []string
	for _, l := range layers {
		all = append(all, l...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}
```

In `internal/tracker/config.go`: delete `unionStrings` (with its comment, lines 654–666) and replace the three `unionStrings(` calls inside `LoadProjects` with `runner.Union(`. In `internal/tracker/rules.go`: replace the three `unionStrings(` with `runner.Union(`, and make the doc comment of `MergeProjectRules` truthful for the new layering — replace its first sentence with: `// MergeProjectRules сливает роль (уже несущую базовый слой roles/_base/base.yaml,` / `// см. runner.LoadRole) с машинным и проектным слоями, уже объединёнными` / `// в project теми же union-правилами внутри LoadProjects.` In `internal/tracker/config_test.go`: delete `TestUnionStringsDedupsAndSorts` (lines 877–887).

Run: `go build ./... && go test ./internal/runner/ ./internal/tracker/`
Expected: PASS.

- [x] **Step 8: Commit**

```bash
git add internal/runner/role.go internal/runner/role_test.go internal/tracker/config.go internal/tracker/rules.go internal/tracker/config_test.go
git commit -m "$(cat <<'MSG'
refactor(runner): move tracker.unionStrings to runner.Union

Базовый слой ролей будет сливаться в LoadRole тем же sort+compact, что и
проектные слои в tracker; две копии одного приёма — лишнее, а обратное
направление импорта невозможно.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [x] **Step 9: Write the failing base-layer tests (tasks.md 1.2)**

Append to `internal/runner/role_test.go`:

```go
// Роль во временном roles/ без каталога _base загружается без базового
// слоя: у временных ролей в тестах базы нет, и это законно.
func TestLoadRoleWithoutBaseDirHasNoBaseLayer(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, "includes:\n  - ../_base/base.md\n", "includes: []\n", 1)
	root := fixtureOffice(t, yaml)
	if err := os.RemoveAll(filepath.Join(root, RolesDir, BaseDir)); err != nil {
		t.Fatalf("каталог _base не убран: %v", err)
	}

	role, err := LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль без базы не загружена: %v", err)
	}
	if len(role.Tools.Deny) != 0 || len(role.Network.Allow) != 0 {
		t.Errorf("роль без базы получила правила из ниоткуда: %+v %+v", role.Tools, role.Network)
	}
}

// С roles/_base/base.yaml роль получает и базовые строки, и свои; повторы
// схлопнуты. Проверяется каждый из трёх списков: механизм один, но
// пропустить один из них при слиянии — самая вероятная ошибка.
func TestLoadRoleUnionsBaseRules(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, `deny: []`, `deny: ["Bash(git *push*)", "Bash(git *reset*)"]`, 1)
	yaml += "network:\n  allow: [example.test]\n"
	root := fixtureOffice(t, yaml)
	base := "network:\n  allow: [pypi.org]\ntools:\n  allow: [Grep]\n  deny: [\"Bash(git *push*)\", \"Bash(rm *-r*)\"]\n"
	if err := os.WriteFile(filepath.Join(root, RolesDir, BaseDir, BaseRulesFile), []byte(base), 0o644); err != nil {
		t.Fatalf("base.yaml не записан: %v", err)
	}

	role, err := LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль с базой не загружена: %v", err)
	}
	if want := []string{"Bash(git *push*)", "Bash(git *reset*)", "Bash(rm *-r*)"}; !slices.Equal(role.Tools.Deny, want) {
		t.Errorf("tools.deny = %v, ожидалось %v", role.Tools.Deny, want)
	}
	if want := []string{"Bash(git *)", "Grep", "Read"}; !slices.Equal(role.Tools.Allow, want) {
		t.Errorf("tools.allow = %v, ожидалось %v", role.Tools.Allow, want)
	}
	if want := []string{"example.test", "pypi.org"}; !slices.Equal(role.Network.Allow, want) {
		t.Errorf("network.allow = %v, ожидалось %v", role.Network.Allow, want)
	}
}

// Каталог _base есть, base.yaml нет — отказ с путём, а не пустой слой:
// половина базы (промпт) без другой половины (правила) — сломанная поставка.
func TestLoadRoleRefusesBaseDirWithoutRules(t *testing.T) {
	root := fixtureOffice(t, fixtureRoleYAML)
	path := filepath.Join(root, RolesDir, BaseDir, BaseRulesFile)
	if err := os.Remove(path); err != nil {
		t.Fatalf("base.yaml не убран: %v", err)
	}

	_, err := LoadRole(root, "tester")
	if err == nil {
		t.Fatal("роль загружена без базовых правил при наличии каталога _base")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "каталог есть, файла нет") {
		t.Errorf("отказ не назвал недостающий файл: %v", err)
	}
}

// Разбор базы строгий, как у роли: неизвестное поле — ошибка, а не молча
// забытая настройка; битый хост в базе ломал бы сеть каждой роли.
func TestLoadRoleRejectsBrokenBaseRules(t *testing.T) {
	for name, base := range map[string]string{
		"неизвестное поле": "limits:\n  max_turns: 5\n",
		"хост со схемой":   "network:\n  allow: [\"https://pypi.org\"]\n",
		"запрет Write":     "tools:\n  deny: [Write]\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := fixtureOffice(t, fixtureRoleYAML)
			if err := os.WriteFile(filepath.Join(root, RolesDir, BaseDir, BaseRulesFile), []byte(base), 0o644); err != nil {
				t.Fatalf("base.yaml не записан: %v", err)
			}
			_, err := LoadRole(root, "tester")
			if err == nil {
				t.Fatal("битые базовые правила приняты")
			}
			if !strings.Contains(err.Error(), BaseRulesFile) {
				t.Errorf("отказ не назвал файл: %v", err)
			}
		})
	}
}

// Общие запреты git, переписывания истории и rm -rf раньше ехали к каждому
// проекту через projects.yaml: defaults; теперь они — базовый слой ролей, и
// получать их обязана каждая поставляемая роль, с проектом или без.
func TestShippedRolesInheritBaseRules(t *testing.T) {
	wantDeny := []string{
		"Bash(git *branch*)", "Bash(git *checkout*)", "Bash(git *config*)",
		"Bash(git *push*)", "Bash(git *remote*)", "Bash(git *switch*)", "Bash(git *worktree*)",
		"Bash(git *filter-branch*)", "Bash(git *filter-repo*)",
		"Bash(rm *-r*)", "Bash(rm *-f*)", "Bash(rm *--recursive*)", "Bash(rm *--force*)",
	}
	wantNet := []string{"registry-1.docker.io", "github.com", "pypi.org", "proxy.golang.org", "ghcr.io"}
	for _, name := range shippedRoles(t) {
		role, err := LoadRole(filepath.Join("..", ".."), name)
		if err != nil {
			t.Fatalf("roles/%s не загружена: %v", name, err)
		}
		for _, rule := range wantDeny {
			if !slices.Contains(role.Tools.Deny, rule) {
				t.Errorf("roles/%s: tools.deny не содержит %q (база не доехала)", name, rule)
			}
		}
		for _, host := range wantNet {
			if !slices.Contains(role.Network.Allow, host) {
				t.Errorf("roles/%s: network.allow не содержит %q (база не доехала)", name, host)
			}
		}
	}
}
```

Update the fixture so the base directory ships its rules half. In `fixtureOffice` (`internal/runner/role_test.go:28`), after the `base.md` line:

```go
	// Базовых правил у фикстуры нет, но файл обязан быть: каталог _base без
	// base.yaml — сломанная поставка, а не пустой слой (LoadRole).
	write(filepath.Join(RolesDir, BaseDir, BaseRulesFile), "# слой есть, но пуст\n")
```

Fix the two order-sensitive assertions:

In `TestLoadRole`, replace
```go
	if len(role.Tools.Allow) != 2 || role.Tools.Allow[1] != "Bash(git *)" {
```
with
```go
	// Порядок после слияния с базой алфавитный; здесь важно, что правило
	// доехало как написано — со звёздочкой и пробелом.
	if len(role.Tools.Allow) != 2 || !slices.Contains(role.Tools.Allow, "Bash(git *)") {
```

In `TestRoleKeepsNetworkAllowAsWritten`, replace
```go
	want := []string{"pypi.org", "*.pythonhosted.org", "registry.npmjs.org:443"}
	if !slices.Equal(role.Network.Allow, want) {
```
with
```go
	// Слияние с базой сортирует список; «как есть» здесь про текст записей,
	// а не про их порядок.
	want := []string{"*.pythonhosted.org", "pypi.org", "registry.npmjs.org:443"}
	if !slices.Equal(role.Network.Allow, want) {
```

Run: `go test ./internal/runner/ -run 'TestLoadRole|TestShippedRolesInheritBaseRules' -v`
Expected: FAIL — `TestLoadRoleUnionsBaseRules` (deny lacks base strings), `TestLoadRoleRefusesBaseDirWithoutRules` (no error), `TestLoadRoleRejectsBrokenBaseRules` (no error), `TestShippedRolesInheritBaseRules` (denies missing).

- [x] **Step 10: Implement the base layer in `LoadRole`**

In `internal/runner/role.go`, add `"io"` to imports. After the `Union` function:

```go
// baseRules — то, что наследует каждая роль. Форма совпадает с role.yaml,
// а не с projects.local.yaml: это роль, от которой наследуют все.
type baseRules struct {
	Network Network `yaml:"network"`
	Tools   Tools   `yaml:"tools"`
}

// loadBaseRules читает roles/_base/base.yaml.
//
// Каталога нет — слоя нет: временные роли в тестах живут без базы. Каталог
// есть, файла нет — отказ: половина базы (промпт base.md) без другой
// половины (правила) — сломанная поставка, а не пустой слой. Файл обязателен
// ровно там, где обязателен base.md.
//
// Проверки те же, что у роли, в части, которая к правилам относится: битый
// хост в базе оставил бы без сети каждую роль, а запрет Write целиком —
// без результата.
func loadBaseRules(configRoot string) (baseRules, error) {
	dir := filepath.Join(configRoot, RolesDir, BaseDir)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return baseRules{}, nil
	}
	path := filepath.Join(dir, BaseRulesFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return baseRules{}, fmt.Errorf("%s: базовые правила ролей не найдены: каталог есть, файла нет", path)
	}
	if err != nil {
		return baseRules{}, fmt.Errorf("%s не прочитан: %w", path, err)
	}

	var base baseRules
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// Файл из одних комментариев — пустой слой, а не ошибка: io.EOF здесь
	// значит «нечего добавить», как и у остальных загрузчиков конфигурации.
	if err := dec.Decode(&base); err != nil && !errors.Is(err, io.EOF) {
		return baseRules{}, fmt.Errorf("%s не разобран: %w", path, err)
	}

	var errs []error
	for i, host := range base.Network.Allow {
		if err := validHost(host); err != nil {
			errs = append(errs, fmt.Errorf("network.allow[%d]=%q: %w", i, host, err))
		}
	}
	if slices.Contains(base.Tools.Deny, WriteTool) {
		errs = append(errs, fmt.Errorf("tools.deny запрещает %s целиком: ни одной роли нечем будет записать результат", WriteTool))
	}
	if err := errors.Join(errs...); err != nil {
		return baseRules{}, fmt.Errorf("%s нарушает контракт базовых правил: %w", path, err)
	}
	return base, nil
}
```

In `LoadRole`, replace the tail

```go
	if err := r.validate(name); err != nil {
		return Role{}, fmt.Errorf("%s нарушает контракт роли: %w", path, err)
	}
	return r, nil
```

with

```go
	if err := r.validate(name); err != nil {
		return Role{}, fmt.Errorf("%s нарушает контракт роли: %w", path, err)
	}

	// Базовый слой кладётся после проверки роли, а не до: роль обязана быть
	// годной сама по себе (пустой allow — её ошибка, а не базы), а сливаться
	// с базой она может только добавлением — убрать унаследованное нельзя.
	base, err := loadBaseRules(configRoot)
	if err != nil {
		return Role{}, err
	}
	r.Network.Allow = Union(base.Network.Allow, r.Network.Allow)
	r.Tools.Allow = Union(base.Tools.Allow, r.Tools.Allow)
	r.Tools.Deny = Union(base.Tools.Deny, r.Tools.Deny)
	return r, nil
```

Also update the `fixtureOffice` in `internal/adapters/claude/adapter_test.go` (line 63), adding after the `base.md` line:

```go
	write(filepath.Join("roles", runner.BaseDir, runner.BaseRulesFile), "# слой есть, но пуст\n")
```
(`runner` is already imported there.)

Run: `gofmt -l ./internal && go test ./internal/runner/ ./internal/adapters/... ./internal/pipeline/ -v -run 'TestLoadRole|TestShippedRoles|TestRoleKeeps|TestBuild|TestEveryGraphRoleIsShipped'`
Expected: `gofmt -l` prints nothing; all PASS.

- [x] **Step 11: Rewrite the `cmd/run-agent` test that asserted the old behaviour**

`cmd/run-agent/main_test.go` `TestDryRunProjectFlagMergesRepoWideRules` (line ~282) asserts that without `--project` the implementer does not see `registry-1.docker.io`. That is now false: the base layer arrives through `LoadRole`. Replace the whole function with:

```go
// Базовые правила ролей (roles/_base/base.yaml) приходят через LoadRole и
// потому есть у роли и без --project; с флагом поверх них ложатся слои
// projects.local.yaml — сравнение двух прогонов и есть тест механизма.
// Хост machine-only.test существует только в машинном файле: увидеть его
// без флага значило бы, что слои перепутаны.
func TestDryRunProjectFlagMergesMachineRulesOverBase(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	home := t.TempDir()
	// Машинная половина обязана назвать все три проекта офиса (парность
	// office/machine, пока projects.yaml жив) — значения репозиториев здесь
	// не важны, --dry-run ничего не клонирует.
	machine := "OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  network: [machine-only.test]\n" +
		"VO:\n  repo_url: https://example.test/v.git\n  tracker: mock\n" +
		"EXP:\n  repo_url: https://example.test/e.git\n  tracker: mock\n"
	if err := os.WriteFile(filepath.Join(home, "projects.local.yaml"), []byte(machine), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	env := []string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	_, withoutFlag := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--dry-run")
	for _, want := range []string{"registry-1.docker.io", "Bash(git *push*)"} {
		if !strings.Contains(withoutFlag, want) {
			t.Errorf("без --project роль не получила базовое правило %q:\n%s", want, withoutFlag)
		}
	}
	if strings.Contains(withoutFlag, "machine-only.test") {
		t.Errorf("без --project роль уже видит машинный слой:\n%s", withoutFlag)
	}

	code, withFlag := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--project", "OFFICE", "--dry-run")
	if code != 0 {
		t.Fatalf("код %d, ожидался 0; вывод: %s", code, withFlag)
	}
	if !strings.Contains(withFlag, "machine-only.test") {
		t.Errorf("с --project OFFICE в сети нет машинного хоста:\n%s", withFlag)
	}
	if !strings.Contains(withFlag, "registry-1.docker.io") {
		t.Errorf("с --project OFFICE базовый хост потерян при слиянии:\n%s", withFlag)
	}
}
```

Run: `go test ./cmd/run-agent/ -run TestDryRun -v`
Expected: PASS.

- [x] **Step 12: Full check and commit**

Run: `gofmt -l . ; go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all green. (At this point both layers exist — `projects.yaml: defaults` still reaches projects via `LoadProjects`, and the base reaches roles via `LoadRole`; `Union` collapses the duplicates.)

```bash
git add internal/runner/role.go internal/runner/role_test.go internal/adapters/claude/adapter_test.go cmd/run-agent/main_test.go
git commit -m "$(cat <<'MSG'
feat(runner): union roles/_base/base.yaml into every role at load

LoadRole кладёт базовый слой (сеть и инструменты, общие для всех ролей)
в роль после её собственной проверки: роль может добавить, но не отменить.
Каталога _base нет — слоя нет (временные роли в тестах); каталог есть,
файла нет — отказ с путём: половина базы без другой половины — сломанная
поставка. Следствие для run-agent: прогон без --project впервые получает
запреты на git push и прочее, с --project поверх них ложатся слои
projects.local.yaml.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

## Task 2: Loader — projects live on the machine only

Corresponds to `tasks.md` §2 (2.1–2.4).

**Files:**
- Modify: `internal/tracker/config.go` (constants; `machineProject`; `checkKeys`; `LoadProjects(machinePath)`; `TrackersInUse`; `RefuseLeftoverOfficeFile`; messages of `Projects.Get`; comments of `Project.Tracker`, `Projects.For`)
- Modify: `internal/tracker/tracker.go` (`SkipUnknownProject` message and comments)
- Modify: `cmd/runner/office.go:107-110`, `cmd/runner/board.go:29-32`, `cmd/run-agent/main.go:69-71,126-130` (call sites — minimal, so the build stays green; the real `cmd/runner` restructure is Task 3)
- Test: `internal/tracker/config_test.go` (LoadProjects section rewritten), `internal/tracker/boundary_test.go` (drop `ProjectsFile`), `cmd/run-agent/main_test.go` (machine files gain `default_branch`)

**Interfaces:**
- Consumes: `runner.Union`, `runner.RolesDir`, `runner.BaseDir`, `runner.BaseRulesFile` (Task 1).
- Produces: `tracker.LoadProjects(machinePath string) (Projects, error)`; `tracker.OfficeProjectsFile = "projects.yaml"`; `tracker.DefaultBranchPrefix = "agent/"`; `tracker.RefuseLeftoverOfficeFile(configRoot string) error`; `(Projects).TrackersInUse() []string` (sorted, no duplicates). `tracker.ProjectsFile` no longer exists. `tracker.Project`/`tracker.Projects` keep their fields unchanged, so tests that build `Projects` by hand (`pipeline_test.go`, `prpass_test.go`, `board_test.go`, `workspace_test.go`, `rules_test.go`) compile as they are — only their comments mentioning `projects.yaml` change (Task 5.1).

### Background

`internal/tracker/config.go` today: `ProjectsFile`/`ProjectsLocalFile` constants (lines 24–35); `Project` (461–490); `officeProject`/`machineProject` (494–510); `machineKeys` (514); `Projects.Get` (600–609); `reservedRulesKey`, `extractDefaultsOffice`, `extractDefaultsMachine` (611–652); `LoadProjects(officePath, machinePath)` (668–781); `checkOfficeHalf` (783–804); `decodeLoose`/`decodeStrict` (806–836). `internal/tracker/tracker.go:47-53` `SkipUnknownProject` names `ProjectsFile`.

The spec scenario "An unknown key in a project entry is refused" wants the refusal to name **the project, the key, and the file**. yaml.v3's strict decoder names only the field and a line number. So the loose pre-pass (already needed for `defaults`) checks every entry against an allow-list: `defaults` → `{network, tools}`; a project → the nine contract keys. The strict decode remains as the second line (types). `projectKeys` must mirror `machineProject`'s tags — a test loads an entry with every key set, so a forgotten entry in the list fails loudly.

- [x] **Step 1: Replace the LoadProjects test section in `internal/tracker/config_test.go` (tasks.md 2.1, 2.2, 2.3)**

Delete these from `config_test.go`: the `projects.yaml` part of `TestShippedConfigIsValid` (lines 32–48: from the comment "Машинную половину репозиторий не хранит" to the closing `}` of `if len(office) == 0`; keep the workflow part), the `validOffice`/`validMachine` const block, `loadHalves`, `TestLoadProjects`, `TestLoadProjectsRejectsIncomplete`, `TestLoadProjectsRejectsMachineKeysInOfficeFile`, `TestLoadProjectsRequiresBothHalves`, `TestLoadProjectsExplainsEmptyMachineHalf`, `TestLoadProjectsRejectsEmptyOffice`, `TestOfficeProjectAcceptsInlineNetworkAndTools`, `TestLoadProjectsAllowsDefaultsInEitherOrBothFiles`, `TestLoadProjectsRejectsDefaultBranchUnderDefaultsKey`, `TestLoadProjectsRejectsRepoURLUnderDefaultsKeyInMachineFile`, `TestLoadProjectsDefaultsSkipsParityCheck`, `TestLoadProjectsProjectInheritsOnlyDefaults`, `TestLoadProjectsProjectSpecificsAreIsolated`, `TestLoadProjectsWithoutDefaultsAtAllIsUnaffected`, `TestLoadProjectsCarriesAutoMerge`, `TestShippedDefaultsCarrySevenCommonDenyRules`, `TestShippedDefaultsCarryDangerousCommandDenyRules`. Keep `TestProjectsFor` and `TestPRBranch` as they are.

Add in their place (the file already imports `os`, `path/filepath`, `slices`, `strings`, `testing`):

```go
// Проект целиком описан одной записью machine-файла: второй половины нет.
// Обязательны repo_url, tracker и default_branch; branch_prefix — умолчание.
const validMachine = `OFF:
  repo_url: https://example.test/office.git
  tracker: mock
  default_branch: master
`

// load — проекты из временного projects.local.yaml.
func load(t *testing.T, machine string) (Projects, error) {
	t.Helper()
	return LoadProjects(writeTemp(t, ProjectsLocalFile, machine))
}

// Минимальной записи хватает, чтобы вести задачу: ветка — agent/<KEY>,
// база PR-прохода — default_branch.
func TestLoadProjects(t *testing.T) {
	projects, err := load(t, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	if got := p.Branch("OFF-12"); got != "agent/OFF-12" {
		t.Errorf("ветка %q, ожидалась agent/OFF-12 (branch_prefix по умолчанию)", got)
	}
	if got := p.PRBranch(); got != "master" {
		t.Errorf("база PR-прохода %q, ожидалась master", got)
	}
	if p.RepoURL != "https://example.test/office.git" || p.Tracker != "mock" {
		t.Errorf("запись доехала не целиком: %+v", p)
	}

	// Задачу неизвестного проекта раннер брать не вправе: ему негде взять
	// репозиторий и некуда пушить. Отказ называет единственный файл проектов.
	_, err = projects.Get("НЕТ")
	if err == nil {
		t.Fatal("неизвестный проект найден")
	}
	if !strings.Contains(err.Error(), ProjectsLocalFile) {
		t.Errorf("отказ не назвал файл проектов: %v", err)
	}
}

// Явный branch_prefix уважается: agent/ — умолчание, а не закон.
func TestLoadProjectsHonoursBranchPrefix(t *testing.T) {
	projects, err := load(t, validMachine+"  branch_prefix: office/\n")
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, _ := projects.Get("OFF")
	if got := p.Branch("OFF-1"); got != "office/OFF-1" {
		t.Errorf("ветка %q, ожидалась office/OFF-1", got)
	}
}

// Каждый отказ называет ключ, проект и файл: чинить надо там, а не гадать.
func TestLoadProjectsRejectsIncomplete(t *testing.T) {
	cases := []struct {
		name, machine, want string
	}{
		{"неизвестное поле", strings.Replace(validMachine, "repo_url:", "repo:", 1), "repo"},
		{"нет репозитория", strings.Replace(validMachine, "  repo_url: https://example.test/office.git\n", "", 1), "repo_url"},
		{"нет ветки по умолчанию", strings.Replace(validMachine, "  default_branch: master\n", "", 1), "default_branch"},
		{"относительный worktree_root", validMachine + "  worktree_root: ../рядом\n", "worktree_root"},
		{"нет трекера", strings.Replace(validMachine, "  tracker: mock\n", "", 1), "tracker"},
		{"чужой трекер", strings.Replace(validMachine, "tracker: mock", "tracker: youtrack", 1), "youtrack"},
		{"auto_merge без forge", validMachine + "  auto_merge:\n    enabled: true\n", "forge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, tc.machine)
			if err == nil {
				t.Fatal("неполный проект загружен без ошибки")
			}
			for _, want := range []string{tc.want, "OFF", ProjectsLocalFile} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке не назван %q: %v", want, err)
				}
			}
		})
	}
}

// projectKeys обязан повторять теги machineProject: запись со всеми ключами
// контракта грузится, иначе забытый в списке ключ отвергал бы годный файл.
func TestLoadProjectsAcceptsEveryContractKey(t *testing.T) {
	machine := validMachine +
		"  branch_prefix: a/\n  worktree_root: /abs/OFF\n  forge: github\n" +
		"  auto_merge:\n    enabled: true\n    target_branch: office-integration\n" +
		"  network: [a.test]\n  tools:\n    allow: [Read]\n    deny: [\"Bash(rm*)\"]\n"
	projects, err := load(t, machine)
	if err != nil {
		t.Fatalf("запись со всеми ключами контракта отвергнута: %v", err)
	}
	p, _ := projects.Get("OFF")
	if !p.AutoMerge.Enabled || p.PRBranch() != "office-integration" || p.Forge != "github" {
		t.Errorf("auto_merge/forge доехали не целиком: %+v", p)
	}
	if !slices.Equal(p.Network, []string{"a.test"}) ||
		!slices.Equal(p.Tools.Allow, []string{"Read"}) || !slices.Equal(p.Tools.Deny, []string{"Bash(rm*)"}) {
		t.Errorf("network/tools на уровне записи не разобраны: %+v %+v", p.Network, p.Tools)
	}
}

// «Завёл файл, ещё не заполнил» — обычное состояние на новой машине; отказ
// называет файл и причину, а не «не разобран: EOF». Ни одного проекта — тоже
// отказ: промолчать значило бы крутить пустые тики без объяснений.
func TestLoadProjectsRejectsFileWithoutProjects(t *testing.T) {
	for name, body := range map[string]string{
		"пустой":          "",
		"из комментария":  "# сюда допишу позже\n",
		"только defaults": "defaults:\n  network: [a.test]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, body)
			if err == nil {
				t.Fatal("файл без проектов принят за годный")
			}
			if strings.Contains(err.Error(), "EOF") {
				t.Errorf("отказ говорит про EOF вместо причины: %v", err)
			}
			if !strings.Contains(err.Error(), ProjectsLocalFile) || !strings.Contains(err.Error(), "ни одного проекта") {
				t.Errorf("отказ не назвал файл или причину: %v", err)
			}
		})
	}
}

// Файла нет — отказ называет его и обязательные ключи: заводить его
// человеку, и сказать надо, из чего.
func TestLoadProjectsRejectsMissingFile(t *testing.T) {
	_, err := LoadProjects(filepath.Join(t.TempDir(), ProjectsLocalFile))
	if err == nil {
		t.Fatal("проекты загружены без файла")
	}
	for _, want := range []string{ProjectsLocalFile, "repo_url", "tracker", "default_branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
}

// defaults — правила, а не проект: любой ключ, кроме network и tools, —
// отказ по имени. Раньше auto_merge под defaults молча пропадал.
func TestLoadProjectsRejectsProjectKeysUnderDefaults(t *testing.T) {
	for key, body := range map[string]string{
		"auto_merge":     "defaults:\n  auto_merge:\n    enabled: true\n",
		"default_branch": "defaults:\n  default_branch: main\n",
		"branch_prefix":  "defaults:\n  branch_prefix: x/\n",
		"repo_url":       "defaults:\n  repo_url: https://example.test/x.git\n",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := load(t, validMachine+body)
			if err == nil {
				t.Fatalf("%s под defaults принят без ошибки", key)
			}
			if !strings.Contains(err.Error(), "defaults") || !strings.Contains(err.Error(), key) {
				t.Errorf("ошибка не называет причину: %v", err)
			}
		})
	}
}

// Пустой defaults законен — слоя нет.
func TestLoadProjectsAllowsEmptyDefaults(t *testing.T) {
	for name, body := range map[string]string{"пусто": "defaults:\n", "фигурные": "defaults: {}\n"} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, validMachine+body); err != nil {
				t.Errorf("пустой defaults отвергнут: %v", err)
			}
		})
	}
}

// Машинный слой достаётся каждому проекту; специфика одного проекта другому
// не видна; проект без единого правила остаётся без правил.
func TestLoadProjectsLayersDefaultsAndProjectRules(t *testing.T) {
	machine := "defaults:\n  network: [common.test]\n  tools:\n    deny: [\"Bash(git *push*)\"]\n" +
		validMachine +
		"VO:\n  repo_url: https://example.test/vo.git\n  tracker: mock\n  default_branch: main\n  network: [vo-only.test]\n"
	projects, err := load(t, machine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	off, _ := projects.Get("OFF")
	vo, _ := projects.Get("VO")
	if !slices.Equal(off.Network, []string{"common.test"}) {
		t.Errorf("OFF.network = %v, ожидалось [common.test] без утечки VO", off.Network)
	}
	if !slices.Equal(vo.Network, []string{"common.test", "vo-only.test"}) {
		t.Errorf("VO.network = %v, ожидалось [common.test vo-only.test]", vo.Network)
	}
	if !slices.Equal(vo.Tools.Deny, []string{"Bash(git *push*)"}) {
		t.Errorf("VO.tools.deny = %v, defaults не доехал", vo.Tools.Deny)
	}

	bare, err := load(t, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, _ := bare.Get("OFF")
	if len(p.Network) != 0 || len(p.Tools.Allow) != 0 || len(p.Tools.Deny) != 0 {
		t.Errorf("проект без единого правила получил их из ниоткуда: %+v", p)
	}
}

// Трекеры, которые назвали проекты, — по алфавиту и без повторов: по этому
// списку раннер обходит офисы, и два запуска обязаны обходить их одинаково.
func TestTrackersInUse(t *testing.T) {
	projects := Projects{
		"OFF": {Tracker: "mock"},
		"VO":  {Tracker: "jira"},
		"EXP": {Tracker: "jira"},
	}
	if got := projects.TrackersInUse(); !slices.Equal(got, []string{"jira", "mock"}) {
		t.Errorf("TrackersInUse = %v, ожидалось [jira mock]", got)
	}
	if got := (Projects{}).TrackersInUse(); len(got) != 0 {
		t.Errorf("у пустого списка проектов нашлись трекеры: %v", got)
	}
}

// projects.yaml из прежней раскладки под корнем конфигурации — отказ, а не
// молча пропущенный файл: он носил и проекты, и общие правила, и оба адреса,
// куда они переехали, отказ называет.
func TestRefuseLeftoverOfficeFile(t *testing.T) {
	root := t.TempDir()
	if err := RefuseLeftoverOfficeFile(root); err != nil {
		t.Errorf("без projects.yaml отказ не положен: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, OfficeProjectsFile), []byte("OFF:\n"), 0o644); err != nil {
		t.Fatalf("projects.yaml не записан: %v", err)
	}
	err := RefuseLeftoverOfficeFile(root)
	if err == nil {
		t.Fatal("оставшийся projects.yaml пропущен молча")
	}
	for _, want := range []string{OfficeProjectsFile, ProjectsLocalFile, "roles/_base/base.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
}
```

Also in `internal/tracker/boundary_test.go`, delete the line `filepath.Join(root, ProjectsFile),` from `named` and change the comment at lines 16–17 to: `// Первый способ — загрузчик: он принимает под defaults только network и tools` / `// и отвергает projects.yaml из прежней раскладки.`

Run: `go vet ./internal/tracker/`
Expected: compile errors — `LoadProjects` still takes two arguments, `OfficeProjectsFile`, `TrackersInUse`, `RefuseLeftoverOfficeFile` undefined.

- [x] **Step 2: Rewrite the loader in `internal/tracker/config.go`**

Replace the file-constants block (lines 19–35) with:

```go
// Файлы конфигурации. Граф переходов описывает офис и живёт в репозитории;
// проекты описывают инстанс и живут в хозяйстве раннера. Граница — не вкус
// раскладки: репозиторий — это фреймворк, и всё, что правят, заводя новую
// машину, обязано лежать вне его, иначе правка под себя пачкает рабочее
// дерево и метка config:…-dirty перестаёт что-либо значить.
const (
	// WorkflowFile — граф переходов. Его читает раннер; агент его не видит.
	// Описывает офис: одинаков у всех, кто его поднимет.
	WorkflowFile = "workflow.yaml"
	// ProjectsLocalFile — единственный файл проектов: где репозиторий, ветка
	// по умолчанию, трекер, forge, добавки к правилам. Живёт в ${OFFICE_HOME}.
	ProjectsLocalFile = "projects.local.yaml"
	// OfficeProjectsFile — имя файла, которого в репозитории больше нет.
	// Остался от прежней раскладки — раннер о нём скажет, а не промолчит
	// (RefuseLeftoverOfficeFile).
	OfficeProjectsFile = "projects.yaml"
)

// DefaultBranchPrefix — префикс веток задач, если проект не назвал свой.
const DefaultBranchPrefix = "agent/"
```

In `Project`, change the `Tracker` field comment to:

```go
	// Tracker — трекер проекта. Обязателен: одна и та же задача не живёт разом
	// в файловом трекере и в JIRA, а офис одного трекера не должен видеть
	// чужих проектов.
	Tracker string `yaml:"tracker"`
```

Replace the `officeProject`/`machineProject` type block and `machineKeys` (lines 494–514) with:

```go
// machineProject — запись проекта в ProjectsLocalFile. Всё, что у проекта
// есть, — здесь: второй половины больше нет. Отдельный от Project тип нужен
// разбору: строгое чтение отвергает поле, которого в типе нет, а Network/Tools
// в Project — уже слитые слои, не сырые поля файла.
type machineProject struct {
	RepoURL       string    `yaml:"repo_url"`
	DefaultBranch string    `yaml:"default_branch"`
	BranchPrefix  string    `yaml:"branch_prefix"`
	WorktreeRoot  string    `yaml:"worktree_root"`
	Tracker       string    `yaml:"tracker"`
	Forge         string    `yaml:"forge"`
	AutoMerge     AutoMerge `yaml:"auto_merge"`
	Rules         `yaml:",inline"`
}

// projectKeys — ключи записи проекта, ровно теги machineProject. Нужны
// свободному разбору: строгий отверг бы лишний ключ и сам, но назвал бы
// только поле и строку, а человеку нужен проект, ключ и файл.
var projectKeys = []string{
	"repo_url", "default_branch", "branch_prefix", "worktree_root",
	"tracker", "forge", "auto_merge", "network", "tools",
}
```

Replace `Projects.For`'s comment (lines 578–583) with:

```go
// For — проекты одного трекера.
//
// Офис ведёт один трекер и работает только со своими проектами. Чужие
// не просто бесполезны: спрашивать о них трекер — значит получать ошибку
// «нет такого проекта» на каждом проходе, а сносить их рабочие папки уборкой
// системного прохода — терять чужую работу.
```

After `For`, add:

```go
// TrackersInUse — трекеры, названные проектами, по алфавиту и без повторов.
// Порядок устойчив намеренно: по нему раннер обходит офисы, и два запуска
// обязаны обходить их одинаково.
func (p Projects) TrackersInUse() []string {
	var names []string
	for _, project := range p {
		names = append(names, project.Tracker)
	}
	return runner.Union(names)
}
```

Replace `Projects.Get` (lines 596–609) with:

```go
// Get отдаёт проект по ключу. Задачу неизвестного проекта раннер брать не вправе:
// ему негде взять репозиторий и некуда пушить.
func (p Projects) Get(key string) (Project, error) {
	project, found := p[key]
	if !found {
		return Project{}, fmt.Errorf("проект %q не описан в %s: задача не может быть взята в работу",
			key, ProjectsLocalFile)
	}
	return project, nil
}
```

Replace `reservedRulesKey`, `extractDefaultsOffice`, `extractDefaultsMachine` (lines 611–652) with:

```go
// reservedRulesKey — имя, под которым в ProjectsLocalFile живёт машинный
// слой умолчаний network/tools. Не проект: требования к обычным записям
// к нему не применяются, а ключи проекта под ним — ошибка.
const reservedRulesKey = "defaults"

// defaultsKeys — всё, что defaults вправе содержать. Один allow-list вместо
// перечня запрещённого: новое поле в machineProject не сможет снова
// проскочить под defaults молча, как проскакивал auto_merge.
var defaultsKeys = []string{"network", "tools"}

// checkKeys сверяет ключи каждой записи со списком дозволенных — до строгого
// разбора, потому что строгий назвал бы только поле, а человеку нужно знать
// проект, ключ и файл; для defaults — ещё и почему ключ не положен.
func checkKeys(path string, raw map[string]map[string]any) error {
	var errs []error
	for _, entry := range slices.Sorted(maps.Keys(raw)) {
		for _, key := range slices.Sorted(maps.Keys(raw[entry])) {
			switch {
			case entry == reservedRulesKey && !slices.Contains(defaultsKeys, key):
				errs = append(errs, fmt.Errorf("%s: ключ %s не положен — это свойство проекта, "+
					"а не умолчаний; здесь только %s", reservedRulesKey, key, strings.Join(defaultsKeys, " и ")))
			case entry != reservedRulesKey && !slices.Contains(projectKeys, key):
				errs = append(errs, fmt.Errorf("%s: ключ %s не описан контрактом проекта; известны %s",
					entry, key, strings.Join(projectKeys, ", ")))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
```

(Add `"strings"` to the imports of `config.go`.)

Replace `LoadProjects` and `checkOfficeHalf` (lines 654–804 after the earlier deletions) with:

```go
// LoadProjects читает проекты из единственного файла — машинного.
//
// Репозиторий — это фреймворк: проектов в нём нет, и всё, что у проекта есть,
// лежит в одной записи ProjectsLocalFile. Слои правил объединяются: базовый
// (roles/_base/base.yaml) кладёт в роль LoadRole, машинный (defaults здесь)
// и проектный (сама запись) — этот загрузчик; назвать можно, убрать — нет.
func LoadProjects(machinePath string) (Projects, error) {
	if _, err := os.Stat(machinePath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s не заведён: проекты — свойство инстанса, а не офиса, "+
			"и где они на этой машине — узнать неоткуда. Заведите файл, дав каждому проекту "+
			"ключи repo_url, tracker и default_branch; branch_prefix, worktree_root, forge, "+
			"auto_merge, network и tools необязательны", machinePath)
	}

	// Сперва свободный разбор — ради имён: строгий принял бы под defaults
	// любое поле machineProject, и правило молча стало бы «проектом», а лишний
	// ключ записи назвал бы без имени проекта.
	var raw map[string]map[string]any
	if err := decodeLoose(machinePath, &raw); err != nil {
		return nil, err
	}
	if err := checkKeys(machinePath, raw); err != nil {
		return nil, err
	}

	var machine map[string]machineProject
	if err := decodeStrict(machinePath, &machine); err != nil {
		return nil, err
	}
	defaults := machine[reservedRulesKey].Rules
	delete(machine, reservedRulesKey)

	// Ни одного проекта — отказ, и это не педантизм: прочие беды говорят вслух,
	// а «ни одного проекта» промолчало бы, и раннер крутил бы пустые тики.
	if len(machine) == 0 {
		return nil, fmt.Errorf("%s не называет ни одного проекта: офису нечего вести", machinePath)
	}

	var errs []error
	projects := Projects{}
	for _, key := range slices.Sorted(maps.Keys(machine)) {
		local := machine[key]
		if local.RepoURL == "" {
			errs = append(errs, fmt.Errorf("%s: repo_url не задан (%s)", key, machinePath))
		}
		if local.DefaultBranch == "" {
			errs = append(errs, fmt.Errorf("%s: default_branch не задан (%s)", key, machinePath))
		}
		// Пустой префикс здесь, а не в Branch(): проект никогда не увидит
		// ветку без префикса, и умолчание записано в одном месте.
		if local.BranchPrefix == "" {
			local.BranchPrefix = DefaultBranchPrefix
		}
		if root := local.WorktreeRoot; root != "" && !filepath.IsAbs(root) {
			errs = append(errs, fmt.Errorf("%s: worktree_root=%q должен быть абсолютным (%s)", key, root, machinePath))
		}
		if !slices.Contains(trackers, local.Tracker) {
			errs = append(errs, fmt.Errorf("%s: tracker=%q, ожидается один из %v (%s)",
				key, local.Tracker, trackers, machinePath))
		}
		if local.AutoMerge.Enabled && local.Forge == "" {
			errs = append(errs, fmt.Errorf(
				"%s: auto_merge.enabled=true, но forge не задан — мержить через API "+
					"некуда (%s)", key, machinePath))
		}
		projects[key] = Project{
			RepoURL:       local.RepoURL,
			DefaultBranch: local.DefaultBranch,
			BranchPrefix:  local.BranchPrefix,
			WorktreeRoot:  local.WorktreeRoot,
			Tracker:       local.Tracker,
			Forge:         local.Forge,
			AutoMerge:     local.AutoMerge,
			Network:       runner.Union(defaults.Network, local.Network),
			Tools: runner.Tools{
				Allow: runner.Union(defaults.Tools.Allow, local.Tools.Allow),
				Deny:  runner.Union(defaults.Tools.Deny, local.Tools.Deny),
			},
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("проекты нарушают контракт: %w", err)
	}
	return projects, nil
}

// RefuseLeftoverOfficeFile — отказ, если под корнем конфигурации лежит
// projects.yaml. Проекты живут в projects.local.yaml, общие правила —
// в roles/_base/base.yaml; файл, который раньше носил и то и другое,
// не читается, и молча пройти мимо него значило бы запустить офис на
// половине конфигурации: с проектами, но без правил, что в нём лежали.
func RefuseLeftoverOfficeFile(configRoot string) error {
	path := filepath.Join(configRoot, OfficeProjectsFile)
	_, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%s не проверен: %w", path, err)
	}
	return fmt.Errorf("%s: этот файл больше не читается. Проекты живут в ${OFFICE_HOME}/%s, "+
		"общие правила ролей — в %s; уберите файл", path, ProjectsLocalFile,
		filepath.Join(runner.RolesDir, runner.BaseDir, runner.BaseRulesFile))
}
```

`decodeLoose` and `decodeStrict` stay as they are.

- [x] **Step 3: Update the call sites minimally so everything compiles**

`cmd/runner/office.go` lines 107–113: replace

```go
	projects, err := tracker.LoadProjects(
		sources.office(configRoot, tracker.ProjectsFile),
		sources.machine(home, tracker.ProjectsLocalFile),
	)
```
with
```go
	projects, err := tracker.LoadProjects(sources.machine(home, tracker.ProjectsLocalFile))
```

`cmd/runner/board.go` lines 29–32: replace the error with
```go
			return fmt.Errorf("проект %q не описан в %s либо заведён под другой трекер",
				*project, tracker.ProjectsLocalFile)
```
(Task 3 replaces this whole branch with `byProject`; this keeps the build green now.)

`cmd/run-agent/main.go`: line 69 flag help `"проект из projects.yaml/projects.local.yaml: подмешивает "+` → `"проект из ${OFFICE_HOME}/projects.local.yaml: подмешивает "+`, and the continuation `"repo-wide, project- и machine-слои network/tools поверх роли, как это делает конвейер; "+` → `"машинные и проектные слои network/tools поверх роли (базовый слой роль получает и без флага), как это делает конвейер; "+`; lines 126–130 replace

```go
		projects, err := tracker.LoadProjects(
			filepath.Join(configRoot, tracker.ProjectsFile),
			filepath.Join(home, tracker.ProjectsLocalFile),
		)
```
with
```go
		projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
```

`internal/tracker/tracker.go` (tasks.md 2.4): line 30 comment `описанного в projects.yaml` → `описанного в projects.local.yaml`; line 45 `строка в projects.yaml выглядит рабочей` → `строка в projects.local.yaml выглядит рабочей`; line 52 `project, ProjectsFile), true` → `project, ProjectsLocalFile), true`.

`cmd/run-agent/main_test.go`: in `TestDryRunProjectFlagMergesMachineRulesOverBase` and `TestDryRunProjectFlagRejectsUnknownProject` the machine files must now carry `default_branch` and no longer need `VO`/`EXP`. Replace the first test's `machine` with:

```go
	// --dry-run ничего не клонирует: значение repo_url здесь не важно.
	machine := "OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n  network: [machine-only.test]\n"
```
and the second test's file body with `"OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n"`. Delete the old comment about "парность office/machine".

Run: `gofmt -l . ; go build ./... && go vet ./... && go test ./internal/tracker/ ./cmd/... -run 'TestLoadProjects|TestTrackersInUse|TestRefuseLeftover|TestRepoCarries|TestDryRun|TestShippedConfigIsValid' -v`
Expected: no gofmt output; all PASS. Note `TestShippedConfigIsValid` no longer touches `projects.yaml` (it still exists in the tree until Task 5.1; nothing reads it now).

- [x] **Step 4: Full test run and commit**

Run: `go test ./...`
Expected: green.

```bash
git add internal/tracker/config.go internal/tracker/config_test.go internal/tracker/tracker.go internal/tracker/boundary_test.go cmd/runner/office.go cmd/runner/board.go cmd/run-agent/main.go cmd/run-agent/main_test.go
git commit -m "$(cat <<'MSG'
feat(config): LoadProjects reads projects.local.yaml alone

Второй половины у проекта больше нет: default_branch обязателен в записи
машинного файла, branch_prefix по умолчанию agent/. Уходят ProjectsFile,
officeProject, checkOfficeHalf, проверки парности. Под defaults допустимы
только network и tools — один allow-list вместо двух deny-списков, и
auto_merge под ним больше не пропадает молча. Лишний ключ записи отказ
называет вместе с проектом и файлом. RefuseLeftoverOfficeFile отвергает
projects.yaml из прежней раскладки (подключается в cmd/runner и run-agent
следующими задачами). TrackersInUse — список трекеров для обхода офисов.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

## Task 3: Runner — one office per tracker

Corresponds to `tasks.md` §3 (3.1–3.7). Split into three commits: (a) the `offices` walker type with its unit tests; (b) the `offices()` constructor, every subcommand, `ls`, `worktree rm`, their tests; (c) deleting `pipeline.Office.Loop`/`tickOnce`.

**Files:**
- Create: `cmd/runner/offices.go`, `cmd/runner/offices_test.go`
- Modify: `cmd/runner/office.go` (`office()` → `offices()`; `tickCommand`, `reapCommand`, `completeSplitsCommand`, `loopCommand`), `cmd/runner/board.go`, `cmd/runner/worktree.go`, `cmd/runner/main.go` (usage text), `internal/pipeline/pipeline.go` (delete `Loop`, `tickOnce`)
- Test: `cmd/runner/office_test.go`, `cmd/runner/board_test.go`, `cmd/runner/worktree_test.go`, `internal/pipeline/pipeline_test.go` (two `Loop` tests)

**Interfaces:**
- Consumes: `tracker.LoadProjects(machinePath)`, `(Projects).TrackersInUse()`, `(Projects).For(name)`, `tracker.RefuseLeftoverOfficeFile` (Task 2); `pipeline.Office` methods `Reap`, `TickAll`, `Tick`, `CompleteSplits` (unchanged).
- Produces (package `main`, `cmd/runner`):
  - `type namedOffice struct { name string; *pipeline.Office }`
  - `type offices struct { list []namedOffice; workspaces *workspace.Manager; out io.Writer }`
  - `func offices(fs *flag.FlagSet, args []string, out io.Writer) (*offices, error)` — same place and signature shape as today's `office()`, no `--tracker` flag
  - `func (all *offices) each(ctx context.Context, fn func(namedOffice) error) error`
  - `func (all *offices) byProject(key string) (namedOffice, error)`
  - `func (all *offices) cycle(ctx context.Context, role string)` and `func (all *offices) loop(ctx context.Context, every time.Duration, role string) error`
  - `func printBoards(all *offices, project string, now time.Time, out io.Writer) error` and `func removeWorktree(all *offices, key string, force bool, now time.Time, out io.Writer) error` (the testable halves of `ls` and `worktree rm`)

### Background

`cmd/runner/office.go:31` `office()` declares `--tracker` (default `mock`) and `--backend`, opens **one** tracker in a `switch`, filters `projects.For(*trackerName)` and returns one `*pipeline.Office`. All subcommands call it: `tickCommand` (line 202), `reapCommand` (225), `completeSplitsCommand` (235), `loopCommand` (247, calls `o.Loop`), `boardCommand` (`board.go:18`), `worktreeRemove` (`worktree.go:85`). `main.go:30` usage says "Общие флаги: --tracker (mock или jira), --backend". `pipeline.Office.Loop` (`pipeline.go:1129`) does reap → `tickOnce` → complete-splits, logs errors via `o.logf` (which writes to `o.Log`, i.e. `out`), sleeps, stops on `ctx.Done()` printing "остановка по сигналу". `pipeline_test.go:2396` and `:2430` call `o.Loop(ctx, time.Minute, "reviewer")` with an already-cancelled ctx to run exactly one cycle.

`offices()` needs a git repository as config root (`runner.ConfigSHA` runs `git rev-parse HEAD`), which is why the test fixture below runs `git init` + one empty commit in the temp root.

Design deviation, documented: the `offices` type carries the shared `*workspace.Manager` (`workspaces` field) so `worktree rm` can list folders before it knows which office owns the task; the Design Doc's sketch listed only `list` and `out`. Everything else follows the Design Doc.

Error prefixing: `each` wraps every office error as `<name>: <err>` regardless of how many offices there are (the Design Doc: "по строке на офис с его именем в префиксе"). Only **stdout** is byte-identical with one office (no `== трекер … ==` header); the error text on stderr gains the prefix.

- [x] **Step 1: Write the failing walker tests — `cmd/runner/offices_test.go` (tasks.md 3.2, 3.4)**

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
)

// twoOffices — два офиса над файловыми трекерами во временных каталогах,
// по проекту на каждый; имена «jira» и «mock» — метки обхода, под обоими
// лежит mock. Граф — поставляемый, корень конфигурации — репозиторий:
// cycle зовёт настоящие Reap/Tick/CompleteSplits, а им нужны роли и статусы.
func twoOffices(t *testing.T, out *bytes.Buffer) (*offices, *mock.Tracker, *mock.Tracker) {
	t.Helper()
	root := filepath.Join("..", "..")
	wf, err := tracker.LoadWorkflow(filepath.Join(root, tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	office := func(name, project string) (namedOffice, *mock.Tracker) {
		tr := mock.New(t.TempDir())
		return namedOffice{name: name, Office: &pipeline.Office{
			Tracker:  tr,
			Trackers: map[string]tracker.Tracker{},
			Workflow: wf,
			Projects: tracker.Projects{project: {
				Tracker: name, DefaultBranch: "master", BranchPrefix: "agent/",
			}},
			ConfigRoot: root,
			Log:        out,
		}}, tr
	}
	jira, a := office("jira", "VO")
	local, b := office("mock", "OFF")
	return &offices{list: []namedOffice{jira, local}, out: out}, a, b
}

// expiredTask заводит задачу с истёкшей арендой: единственное, что Reap
// делает наблюдаемо без агента, — возвращает такую задачу в очередь.
func expiredTask(t *testing.T, tr *mock.Tracker, key, project string) {
	t.Helper()
	if err := tr.Add(tracker.Task{Key: key, Project: project, Status: "Ready", Summary: "задача"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if err := tr.Claim(tracker.ClaimRequest{
		Key: key, RunID: "прогон-" + key, Owner: "implementer",
		LeaseUntil: time.Now().Add(-time.Hour), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
}

func status(t *testing.T, tr *mock.Tracker, key string) string {
	t.Helper()
	task, err := tr.Get(key)
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	return task.Status
}

// Заголовок «== трекер X ==» печатается только когда офисов больше одного:
// при одном вывод совпадает с прежним байт в байт.
func TestEachPrintsHeadersOnlyForSeveralOffices(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	visit := func(no namedOffice) error {
		out.WriteString("visited " + no.name + "\n")
		return nil
	}

	if err := all.each(context.Background(), visit); err != nil {
		t.Fatalf("обход не прошёл: %v", err)
	}
	want := "== трекер jira ==\nvisited jira\n== трекер mock ==\nvisited mock\n"
	if out.String() != want {
		t.Errorf("вывод обхода двух офисов:\n%q\nожидалось:\n%q", out.String(), want)
	}

	out.Reset()
	all.list = all.list[:1]
	if err := all.each(context.Background(), visit); err != nil {
		t.Fatalf("обход не прошёл: %v", err)
	}
	if strings.Contains(out.String(), "== трекер") {
		t.Errorf("при одном офисе напечатан заголовок:\n%s", out.String())
	}
}

// Ошибка одного офиса не останавливает остальных: все собираются разом,
// и у каждой в префиксе имя офиса — иначе не понять, чей отказ.
func TestEachVisitsEveryOfficeAndJoinsErrors(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	var visited []string
	err := all.each(context.Background(), func(no namedOffice) error {
		visited = append(visited, no.name)
		if no.name == "jira" {
			return errors.New("поиск задач не удался")
		}
		return nil
	})

	if err == nil {
		t.Fatal("ошибка офиса потеряна")
	}
	if !strings.Contains(err.Error(), "jira: поиск задач не удался") {
		t.Errorf("ошибка не подписана именем офиса: %v", err)
	}
	if strings.Join(visited, ",") != "jira,mock" {
		t.Errorf("обойдены %v, ожидались оба офиса по порядку", visited)
	}
}

// Контекст проверяется между офисами: сигнал, пришедший во время прогона
// в первом, останавливает обход перед вторым, не дожидаясь его.
func TestEachStopsBetweenOfficesOnCancel(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	ctx, cancel := context.WithCancel(context.Background())
	var visited []string
	err := all.each(ctx, func(no namedOffice) error {
		visited = append(visited, no.name)
		cancel()
		return nil
	})

	if err != nil {
		t.Fatalf("обход вернул ошибку: %v", err)
	}
	if strings.Join(visited, ",") != "jira" {
		t.Errorf("обойдены %v, ожидался только первый офис", visited)
	}
}

// У задачи есть проект, у проекта — трекер, у трекера — офис.
func TestByProjectFindsTheOwningOffice(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)

	no, err := all.byProject("VO")
	if err != nil || no.name != "jira" {
		t.Errorf("byProject(VO) = %q, %v; ожидался офис jira", no.name, err)
	}
	no, err = all.byProject("OFF")
	if err != nil || no.name != "mock" {
		t.Errorf("byProject(OFF) = %q, %v; ожидался офис mock", no.name, err)
	}
	if _, err := all.byProject("NOPE"); err == nil || !strings.Contains(err.Error(), tracker.ProjectsLocalFile) {
		t.Errorf("неизвестный проект не отвергнут с именем файла: %v", err)
	}
}

// Один заход цикла делает reap в каждом офисе: зависшая задача возвращается
// в очередь и там, и там. Роль для tick — reviewer: в её очереди пусто,
// и агент не нужен.
func TestCycleReapsEveryOffice(t *testing.T) {
	var out bytes.Buffer
	all, a, b := twoOffices(t, &out)
	expiredTask(t, a, "VO-1", "VO")
	expiredTask(t, b, "OFF-1", "OFF")

	all.cycle(context.Background(), "reviewer")

	if got := status(t, a, "VO-1"); got != "Ready" {
		t.Errorf("VO-1 в %q, ожидался Ready: reap не дошёл до офиса jira", got)
	}
	if got := status(t, b, "OFF-1"); got != "Ready" {
		t.Errorf("OFF-1 в %q, ожидался Ready: reap не дошёл до офиса mock", got)
	}
}

// cancelOnExpired отменяет контекст первым же ListExpired: так выглядит
// сигнал, пришедший во время прогона первого офиса.
type cancelOnExpired struct {
	tracker.Tracker
	cancel context.CancelFunc
}

func (c cancelOnExpired) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	c.cancel()
	return c.Tracker.ListExpired(project, now)
}

// Сигнал во время прогона первого офиса: его заход дорабатывает, второй
// офис не начинается — стоп между прогонами, а не посреди.
func TestCycleStopsBeforeNextOfficeOnCancel(t *testing.T) {
	var out bytes.Buffer
	all, a, b := twoOffices(t, &out)
	expiredTask(t, a, "VO-1", "VO")
	expiredTask(t, b, "OFF-1", "OFF")
	ctx, cancel := context.WithCancel(context.Background())
	all.list[0].Tracker = cancelOnExpired{Tracker: a, cancel: cancel}

	all.cycle(ctx, "reviewer")

	if got := status(t, a, "VO-1"); got != "Ready" {
		t.Errorf("VO-1 в %q, ожидался Ready: первый офис обязан дорабатывать заход", got)
	}
	if got := status(t, b, "OFF-1"); got != "InProgress" {
		t.Errorf("OFF-1 в %q, ожидался InProgress: второй офис не должен был начаться", got)
	}
}

// loop с уже отменённым контекстом не ждёт таймера и говорит, почему встал.
func TestLoopStopsOnSignalWithoutWaiting(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := all.loop(ctx, time.Hour, "reviewer"); err != nil {
		t.Fatalf("цикл вернул ошибку: %v", err)
	}
	if !strings.Contains(out.String(), "остановка по сигналу") {
		t.Errorf("о причине остановки не сказано:\n%s", out.String())
	}
}
```

Run: `go vet ./cmd/runner/`
Expected: compile errors — `namedOffice`, `offices` undefined.

- [x] **Step 2: Implement `cmd/runner/offices.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

// namedOffice — офис и имя его трекера: у pipeline.Office имени нет,
// а обход и заголовки в выводе — забота этой обёртки, не пайплайна.
type namedOffice struct {
	name string
	*pipeline.Office
}

// offices — все офисы этого раннера, по одному на трекер, в порядке
// TrackersInUse. Пайплайн о множественности не знает: он получает один
// офис и работает в нём, как и раньше.
//
// Хозяйство рабочих папок общее на машину, а не на офис, и лежит здесь
// отдельно: worktree rm перечисляет папки раньше, чем знает, чей проект.
type offices struct {
	list       []namedOffice
	workspaces *workspace.Manager
	out        io.Writer
}

// each обходит офисы по порядку. Заголовок «== трекер jira ==» печатается
// только когда офисов больше одного: при одном вывод совпадает с прежним
// байт в байт. Ошибка одного офиса не останавливает остальных — все
// собираются errors.Join и уходят наверх разом, с именем офиса в префиксе.
//
// ctx проверяется перед каждым офисом, а не только между заходами: сигнал,
// пришедший во время прогона в jira, останавливает обход после этого
// прогона, не дожидаясь mock. Накопленное к этому моменту возвращается.
func (all *offices) each(ctx context.Context, fn func(namedOffice) error) error {
	var errs []error
	for _, no := range all.list {
		if ctx.Err() != nil {
			break
		}
		if len(all.list) > 1 {
			fmt.Fprintf(all.out, "== трекер %s ==\n", no.name)
		}
		if err := fn(no); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", no.name, err))
		}
	}
	return errors.Join(errs...)
}

// byProject — офис, которому принадлежит проект: для worktree rm и
// ls --project. У задачи есть проект, у проекта — трекер, у трекера — офис.
func (all *offices) byProject(key string) (namedOffice, error) {
	for _, no := range all.list {
		if _, mine := no.Projects[key]; mine {
			return no, nil
		}
	}
	return namedOffice{}, fmt.Errorf("проект %q не описан в %s", key, tracker.ProjectsLocalFile)
}

// loop гоняет цикл по расписанию, пока не остановят.
//
// Это не демон и не supervisor: он не следит за собой, не перезапускается
// и не держит состояния между заходами. Драйвер переехал сюда из
// pipeline.Office.Loop, когда офисов стало несколько; сам заход — cycle.
func (all *offices) loop(ctx context.Context, every time.Duration, role string) error {
	for {
		all.cycle(ctx, role)

		select {
		case <-ctx.Done():
			fmt.Fprintln(all.out, "остановка по сигналу")
			return nil
		case <-time.After(every):
		}
	}
}

// cycle — один заход цикла: по каждому офису reap → tick → complete-splits.
// Ошибка шага — повод сказать о ней и пойти дальше, а не умереть: следующий
// заход может пройти. Поэтому fn всегда возвращает nil, а each здесь нужен
// ради порядка обхода и остановки между офисами.
//
// CompleteSplits идёт после tick, а не до него, и порядок не косметика:
// tick первым делом разбирает ответы человека (HumanReplies), и реплика,
// пришедшая между заходами, обязана увести задачу из Blocked раньше, чем
// до неё дойдёт CompleteSplits. Иначе тикет с двумя подтверждающими
// split-маркерами всё ещё лежал бы в Blocked, когда CompleteSplits его
// увидит, и автосоздание детей состоялось бы вопреки ответу, которого
// никто ещё не прочитал. Reap перед tick не переставлен: он разбирает
// задачи с истёкшей арендой, а не задачи в Blocked.
func (all *offices) cycle(ctx context.Context, role string) {
	_ = all.each(ctx, func(no namedOffice) error {
		if err := no.Reap(ctx); err != nil {
			fmt.Fprintf(all.out, "reap: %v\n", err)
		}
		if err := tick(ctx, no, role); err != nil {
			fmt.Fprintf(all.out, "tick: %v\n", err)
		}
		if err := no.CompleteSplits(ctx); err != nil {
			fmt.Fprintf(all.out, "complete-splits: %v\n", err)
		}
		return nil
	})
}

// tick — цикл одного офиса: по всем ролям графа или по одной названной.
func tick(ctx context.Context, no namedOffice, role string) error {
	if role == "" {
		return no.TickAll(ctx)
	}
	_, err := no.Tick(ctx, role)
	return err
}
```

Run: `gofmt -l ./cmd/runner; go test ./cmd/runner/ -run 'TestEach|TestByProject|TestCycle|TestLoop' -v`
Expected: no gofmt output; PASS.

- [x] **Step 3: Commit**

```bash
git add cmd/runner/offices.go cmd/runner/offices_test.go
git commit -m "$(cat <<'MSG'
feat(runner): offices — обход офисов по трекерам: each, byProject, cycle, loop

Тип-обёртка над pipeline.Office для раннера с несколькими трекерами:
заголовок офиса только когда их больше одного, ошибки офисов собираются
errors.Join с именем в префиксе, контекст проверяется между офисами —
сигнал во время прогона в jira не дожидается mock. Драйвер цикла
(reap → tick → complete-splits, ошибки в лог) переезжает сюда из
pipeline.Office.Loop; сам пайплайн о множественности не знает.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [x] **Step 4: Write the failing constructor tests in `cmd/runner/office_test.go` (tasks.md 3.1, 3.7)**

Replace the comment sentence in `TestConfigSourcesPrintImmediately` that says `тогда строка про tracker.yaml выйдет и под --tracker mock, где файл не открывается` with `тогда строка про tracker.yaml выйдет и на машине без единого jira-проекта, где файл не открывается`. Then append (add imports `bytes`, `net/http`, `net/http/httptest`, `os`, `os/exec`, `io`, and `github.com/kao73/virtual-office/internal/tracker`):

```go
// fixtureRunner — временные корень конфигурации и хозяйство раннера.
// Корень — git-репозиторий с одним коммитом: runner.ConfigSHA читает HEAD.
// В нём копия поставляемого workflow.yaml; ролей нет — их читает tick,
// а не конструктор. Оба пути уходят в окружение, откуда их берёт offices().
func fixtureRunner(t *testing.T, projectsLocal string) (root, home string) {
	t.Helper()
	root, home = t.TempDir(), t.TempDir()

	wf, err := os.ReadFile(filepath.Join("..", "..", tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("поставляемый граф не прочитан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, tracker.WorkflowFile), wf, 0o644); err != nil {
		t.Fatalf("граф не скопирован: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "master"},
		{"-c", "user.name=t", "-c", "user.email=t@local", "commit", "-q", "--allow-empty", "-m", "конфигурация"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(projectsLocal), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	t.Setenv("OFFICE_CONFIG_ROOT", root)
	t.Setenv("OFFICE_HOME", home)
	return root, home
}

const (
	mockProject = "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n"
	jiraProject = "VO:\n  repo_url: https://example.test/v.git\n  tracker: jira\n  default_branch: master\n"
)

// trackerYAML — минимальный tracker.yaml, смотрящий на тестовый сервер.
func trackerYAML(baseURL string) string {
	return "base_url: " + baseURL + "\nauth: { mode: basic }\n" +
		"accounts:\n  default: { user_env: JIRA_USER, secret_env: JIRA_PASSWORD }\n" +
		"status_map: { Ready: Ready }\n" +
		"fields:\n  agent_owner: customfield_10001\n  run_id: customfield_10002\n" +
		"  lease_until: customfield_10003\n  attempts: customfield_10004\n" +
		"human_flag_label: office-waits-human\n"
}

// Машина, где все проекты mock, стартует без tracker.yaml, и строки о нём
// в раскладке нет: файл не открывается — значит и не упоминается.
func TestOfficesMockOnlyNeedsNoTrackerFile(t *testing.T) {
	fixtureRunner(t, mockProject)
	var out bytes.Buffer

	all, err := offices(flags("tick"), nil, &out)
	if err != nil {
		t.Fatalf("офис на одном mock не собран: %v", err)
	}
	if len(all.list) != 1 || all.list[0].name != "mock" {
		t.Errorf("офисы: %+v, ожидался один — mock", all.list)
	}
	if strings.Contains(out.String(), "tracker.yaml") {
		t.Errorf("tracker.yaml упомянут там, где не открывался:\n%s", out.String())
	}
	if !strings.Contains(out.String(), tracker.ProjectsLocalFile) {
		t.Errorf("projects.local.yaml не назван в раскладке:\n%s", out.String())
	}
}

// Проект jira без tracker.yaml — отказ с именем файла, до всякой работы.
func TestOfficesJiraProjectWithoutTrackerFileIsRefused(t *testing.T) {
	fixtureRunner(t, mockProject+jiraProject)
	var out bytes.Buffer

	all, err := offices(flags("tick"), nil, &out)
	if err == nil || !strings.Contains(err.Error(), "tracker.yaml") {
		t.Fatalf("отказ не назвал tracker.yaml: %v", err)
	}
	if all != nil {
		t.Error("при отказе одного трекера собран офис другого")
	}
	// Файл назван и в раскладке — «нет» такой же ответ, как путь, и строка
	// идёт после projects.local.yaml: список трекеров известен только после проектов.
	printed := out.String()
	if !strings.Contains(printed, "tracker.yaml") || strings.Index(printed, "tracker.yaml") < strings.Index(printed, tracker.ProjectsLocalFile) {
		t.Errorf("tracker.yaml не назван после projects.local.yaml:\n%s", printed)
	}
}

// Отвергнутый кред jira — отказ всей команды: под планировщиком это обязано
// быть отказом, а не строкой в логе, и mock-офис при этом не собирается.
func TestOfficesRejectedJiraCredentialRefusesWholeCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errorMessages":["Login required"]}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, "tracker.yaml"), []byte(trackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "неверный")

	all, err := offices(flags("tick"), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "учётк") {
		t.Fatalf("отказ не про учётку: %v", err)
	}
	if all != nil {
		t.Error("при отвергнутом креде jira собран офис mock")
	}
}

// Два трекера — два офиса, по алфавиту, с общими проектами каждому своими.
func TestOfficesBuildOnePerTracker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/myself") {
			fmt.Fprint(w, `{"name":"office"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, "tracker.yaml"), []byte(trackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	var out bytes.Buffer

	all, err := offices(flags("tick"), nil, &out)
	if err != nil {
		t.Fatalf("офисы не собраны: %v", err)
	}
	if len(all.list) != 2 || all.list[0].name != "jira" || all.list[1].name != "mock" {
		t.Fatalf("офисы %+v, ожидались jira, mock", all.list)
	}
	if _, ok := all.list[0].Projects["VO"]; !ok || len(all.list[0].Projects) != 1 {
		t.Errorf("офис jira видит не только VO: %v", all.list[0].Projects.Keys())
	}
	if _, ok := all.list[1].Projects["OFF"]; !ok || len(all.list[1].Projects) != 1 {
		t.Errorf("офис mock видит не только OFF: %v", all.list[1].Projects.Keys())
	}
	if all.list[0].Workspaces != all.list[1].Workspaces || all.list[0].Ledger != all.list[1].Ledger {
		t.Error("хозяйство машины не общее для офисов")
	}
}

// --tracker больше нет: неизвестный флаг, и отказ приходит раньше, чем
// раннер тронул конфигурацию.
func TestOfficesRejectTrackerFlag(t *testing.T) {
	fixtureRunner(t, mockProject)
	var out bytes.Buffer

	_, err := offices(flags("tick"), []string{"--tracker", "jira"}, &out)
	if err == nil || !strings.Contains(err.Error(), "tracker") {
		t.Fatalf("флаг --tracker принят: %v", err)
	}
	if strings.Contains(out.String(), "конфигурация:") {
		t.Errorf("конфигурация прочитана до разбора флагов:\n%s", out.String())
	}
}
```

Add `"fmt"` to the test imports as well. Run: `go vet ./cmd/runner/`
Expected: compile error — `offices` is a type, not a function yet (`offices(flags(...))` undefined).

- [x] **Step 5: Replace `office()` with `offices()` and rewire the commands (tasks.md 3.1, 3.3, 3.4)**

In `cmd/runner/office.go`, replace `office()` (the doc comment and the function, lines 25–168) with:

```go
// offices собирает конвейер: по офису на каждый трекер, названный проектами,
// с общим хозяйством — рабочие папки, реестр, бюджеты, уборщик песочниц.
//
// Сборка вся здесь, в точке входа: pipeline не знает ни какой трекер, ни
// какой бэкенд ему достались, ни что офисов несколько, — и именно поэтому
// его можно проверить целиком на файловом трекере с поддельным агентом.
//
// Порядок загрузки — по зависимостям: граф; проекты; трекеры, которые
// проекты назвали (tracker.yaml открывается только если среди них jira);
// forge по всем проектам разом; общее хозяйство; и уже из этого — офисы.
// Сборка строгая: не открылся один трекер — не стартует ни один офис.
// Неверный кред под планировщиком обязан быть отказом, а не строкой в логе.
func offices(fs *flag.FlagSet, args []string, out io.Writer) (*offices, error) {
	backend := fs.String("backend", runagent.DefaultBackend, "бэкенд агента: sbx или local")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	configRoot, err := configRoot()
	if err != nil {
		return nil, err
	}
	// Хозяйство раннера — вторая половина конфигурации. Репозиторий описывает
	// офис, ${OFFICE_HOME} — этот инстанс: проекты, адреса, учётки, номера полей.
	home, err := runner.Home()
	if err != nil {
		return nil, err
	}
	// projects.yaml из прежней раскладки не читается — и не пропускается молча.
	if err := tracker.RefuseLeftoverOfficeFile(configRoot); err != nil {
		return nil, err
	}
	sources := configSources{out: out}

	workflow, err := tracker.LoadWorkflow(sources.office(configRoot, tracker.WorkflowFile))
	if err != nil {
		return nil, err
	}
	projects, err := tracker.LoadProjects(sources.machine(home, tracker.ProjectsLocalFile))
	if err != nil {
		return nil, err
	}

	// Трекеров столько, сколько назвали проекты, и у каждого столько учёток,
	// сколько ролей со своей. Открываются они здесь и разом: узнать о неверном
	// креде роли в середине цикла, уже захватив задачу, было бы поздно.
	type opened struct {
		tasks    tracker.Tracker
		byRole   map[string]tracker.Tracker
		accounts []string
	}
	trackers := map[string]opened{}
	for _, name := range projects.TrackersInUse() {
		var o opened
		switch name {
		case "mock":
			office, err := mock.Default()
			if err != nil {
				return nil, err
			}
			o.tasks, o.byRole, o.accounts = office, map[string]tracker.Tracker{}, []string{mock.Account}
			for _, role := range workflow.Order() {
				o.byRole[role] = office.As(mock.RoleAccount(role))
				o.accounts = append(o.accounts, mock.RoleAccount(role))
			}
		case "jira":
			cfg, err := jira.LoadConfig(sources.machine(home, jira.TrackerFile))
			if err != nil {
				return nil, err
			}
			office, err := jira.Open(cfg)
			if err != nil {
				return nil, err
			}
			// Сверка имени с сервером — здесь, а не в Open: она стоит запроса,
			// и делать её на каждом открытии трекера незачем.
			if err := office.CheckAccount(); err != nil {
				return nil, fmt.Errorf("общая учётка офиса: %w", err)
			}
			o.tasks, o.byRole = office, map[string]tracker.Tracker{}
			for _, role := range workflow.Order() {
				roleTracker, err := jira.OpenAs(cfg, role)
				if err != nil {
					return nil, fmt.Errorf("трекер роли %s не открыт: %w", role, err)
				}
				if err := roleTracker.CheckAccount(); err != nil {
					return nil, fmt.Errorf("учётка роли %s: %w", role, err)
				}
				o.byRole[role] = roleTracker
			}
			if o.accounts, err = cfg.AgentAccounts(); err != nil {
				return nil, err
			}
		default:
			// LoadProjects уже отверг чужое имя; ветка на случай, если список
			// трекеров там и здесь однажды разойдётся.
			return nil, fmt.Errorf("неизвестный трекер %q: доступны %s", name, strings.Join(tracker.Trackers(), ", "))
		}
		trackers[name] = o
	}

	// Общее хозяйство машины: одно на все офисы. Реестр ведётся всегда,
	// бюджеты — необязательны: файлов у них два — дефолты офиса и накладка
	// машины, — и нет ни одного значит нет лимитов; учёт от этого не зависит.
	forges, err := forgesOf(projects)
	if err != nil {
		return nil, err
	}
	workspaces, err := workspace.Default()
	if err != nil {
		return nil, err
	}
	runs, err := ledger.Default()
	if err != nil {
		return nil, err
	}
	budgets, err := budget.Load(
		sources.office(configRoot, budget.File),
		sources.machine(home, budget.File),
	)
	if err != nil {
		return nil, err
	}
	configSHA, err := runner.ConfigSHA(configRoot)
	if err != nil {
		return nil, err
	}
	// Уборщик песочниц нужен reap: убитый раннер оставляет за собой живую
	// microVM, и снести её больше некому.
	sandboxes, err := runagent.SandboxesOf(*backend)
	if err != nil {
		return nil, err
	}

	all := &offices{workspaces: workspaces, out: out}
	for _, name := range projects.TrackersInUse() {
		tr := trackers[name]
		all.list = append(all.list, namedOffice{name: name, Office: &pipeline.Office{
			Tracker:    tr.tasks,
			Trackers:   tr.byRole,
			Workspaces: workspaces,
			Workflow:   workflow,
			// Офис видит только свои проекты: чужие трекер не знает, а уборка
			// системного прохода снесла бы их рабочие папки.
			Projects:   projects.For(name),
			Forges:     forges,
			Agent:      pipeline.SandboxAgent{ConfigRoot: configRoot, Backend: *backend, Log: out},
			Sandboxes:  sandboxes,
			Ledger:     runs,
			Budgets:    budgets,
			ConfigRoot: configRoot,
			ConfigSHA:  configSHA,
			Accounts:   tr.accounts,
			Log:        out,
		}})
	}
	return all, nil
}
```

Replace `tickCommand`, `reapCommand`, `completeSplitsCommand`, `loopCommand` (lines 200–263) with:

```go
// tickCommand — один цикл в каждом офисе: разобрать ответы человека, взять
// не больше одной задачи на роль, выполнить и вернуть в граф. Ошибка одного
// офиса не останавливает остальных; код возврата ненулевой, если хоть один
// отказал.
func tickCommand(args []string, out io.Writer) error {
	fs := flags("tick")
	role := fs.String("role", "", "роль из workflow.yaml; без неё — по циклу на каждую роль")

	all, err := offices(fs, args, out)
	if err != nil {
		return err
	}
	ctx := context.Background()
	return all.each(ctx, func(no namedOffice) error {
		if *role == "" {
			return no.TickAll(ctx)
		}
		worked, err := no.Tick(ctx, *role)
		if err != nil {
			return err
		}
		if !worked {
			fmt.Fprintf(out, "%s: работы нет\n", *role)
		}
		return nil
	})
}

// reapCommand возвращает в очередь задачи с истёкшей арендой — в каждом офисе.
func reapCommand(args []string, out io.Writer) error {
	all, err := offices(flags("reap"), args, out)
	if err != nil {
		return err
	}
	ctx := context.Background()
	return all.each(ctx, func(no namedOffice) error { return no.Reap(ctx) })
}

// completeSplitsCommand достраивает и связывает тикеты-детей подтверждённых
// split-предложений — отдельно от loop, вручную или по cron (по образцу reap).
func completeSplitsCommand(args []string, out io.Writer) error {
	all, err := offices(flags("complete-splits"), args, out)
	if err != nil {
		return err
	}
	ctx := context.Background()
	return all.each(ctx, func(no namedOffice) error { return no.CompleteSplits(ctx) })
}

// loopCommand гоняет цикл по расписанию, пока не остановят сигналом.
//
// Это не демон: он не следит за собой и не перезапускается. Запускать его
// должен cron, launchd или systemd-timer — примеры в bootstrap/.
func loopCommand(args []string, out io.Writer) error {
	fs := flags("loop")
	role := fs.String("role", "", "роль из workflow.yaml; без неё — по циклу на каждую роль")
	every := fs.Duration("every", 2*time.Minute, "пауза между циклами")

	all, err := offices(fs, args, out)
	if err != nil {
		return err
	}
	// Остановка между прогонами, а не посреди: прерванный прогон оставил бы
	// задачу арендованной до истечения аренды.
	ctx, stop := signalContext()
	defer stop()

	fmt.Fprintf(out, "цикл каждые %s, остановка по SIGINT или SIGTERM\n", *every)
	return all.loop(ctx, *every, *role)
}
```

Update the `configSources` doc comment (line 280): `Конфигурация лежит в двух местах: репозиторий описывает офис, ${OFFICE_HOME} — этот инстанс.` → `Конфигурация лежит в двух местах: репозиторий — фреймворк, ${OFFICE_HOME} — этот инстанс.`

In `cmd/runner/main.go` line 30: `Общие флаги: --tracker (mock или jira), --backend (sbx или local).` → `Общий флаг: --backend (sbx или local). Трекеры берутся из проектов: по офису на каждый.`

- [x] **Step 6: `ls` — configuration once, boards per tracker, `--project` via `byProject` (tasks.md 3.5)**

First the failing tests. Append to `cmd/runner/board_test.go` (add imports `context`? no — `printBoards` takes no ctx; add `"github.com/kao73/virtual-office/internal/pipeline"`):

```go
// boardOffices — два офиса с задачей в каждом, поверх файловых трекеров.
func boardOffices(t *testing.T, out *bytes.Buffer) *offices {
	t.Helper()
	office := func(name, project, key string) namedOffice {
		tr := mock.New(t.TempDir())
		tr.Now = func() time.Time { return boardNow }
		if err := tr.Add(tracker.Task{Key: key, Project: project, Status: "Ready", Summary: "задача " + key}); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
		return namedOffice{name: name, Office: &pipeline.Office{
			Tracker:  tr,
			Workflow: tracker.Workflow{Statuses: []string{"Ready", "Done"}, Terminal: []string{"Done"}},
			Projects: tracker.Projects{project: {Tracker: name}},
		}}
	}
	return &offices{list: []namedOffice{office("jira", "VO", "VO-1"), office("mock", "OFF", "OFF-1")}, out: out}
}

// Два трекера — доска каждого под его именем; конфигурацию печатает
// конструктор, здесь её нет. Один трекер — прежний вывод, без заголовка.
func TestBoardsListTasksPerTracker(t *testing.T) {
	var out bytes.Buffer
	all := boardOffices(t, &out)

	if err := printBoards(all, "", boardNow, &out); err != nil {
		t.Fatalf("доски не напечатаны: %v", err)
	}
	got := out.String()
	jira, vo := strings.Index(got, "== трекер jira =="), strings.Index(got, "VO-1")
	local, off := strings.Index(got, "== трекер mock =="), strings.Index(got, "OFF-1")
	if !(jira >= 0 && jira < vo && vo < local && local < off) {
		t.Errorf("задачи не под заголовками своих трекеров:\n%s", got)
	}

	out.Reset()
	all.list = all.list[1:]
	if err := printBoards(all, "", boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}
	if strings.Contains(out.String(), "== трекер") || !strings.Contains(out.String(), "OFF-1") {
		t.Errorf("при одном трекере вывод изменился:\n%s", out.String())
	}
}

// --project — доска только его офиса; чужой проект — отказ с именем файла.
func TestBoardsProjectFlagPicksTheOwningOffice(t *testing.T) {
	var out bytes.Buffer
	all := boardOffices(t, &out)

	if err := printBoards(all, "VO", boardNow, &out); err != nil {
		t.Fatalf("доска проекта не напечатана: %v", err)
	}
	if !strings.Contains(out.String(), "VO-1") || strings.Contains(out.String(), "OFF-1") || strings.Contains(out.String(), "== трекер") {
		t.Errorf("--project VO показал не только VO:\n%s", out.String())
	}
	if err := printBoards(all, "NOPE", boardNow, &out); err == nil || !strings.Contains(err.Error(), tracker.ProjectsLocalFile) {
		t.Errorf("неизвестный проект не отвергнут с именем файла: %v", err)
	}
}
```

Also fix the two comments in `board_test.go` that name `projects.yaml` (lines 97 and 132) → `projects.local.yaml`.

Then in `cmd/runner/board.go` replace `boardCommand` (lines 13–36) with (add `"context"` to imports):

```go
// boardCommand печатает плоский список: задачи всех проектов во всех статусах
// графа, по трекеру за раз.
//
// Это не очередь: задачи с живой арендой из неё не выбрасываются, потому что
// «кто работает прямо сейчас» — первое, что человек ищет глазами. Переписку
// команда не тянет вовсе — она стоит запроса на задачу, а показать её негде.
func boardCommand(args []string, out io.Writer) error {
	fs := flags("ls")
	project := fs.String("project", "", "показывать только этот проект")

	all, err := offices(fs, args, out)
	if err != nil {
		return err
	}
	return printBoards(all, *project, time.Now(), out)
}

// printBoards — доска каждого офиса под его именем (заголовок ставит each,
// и только когда офисов больше одного); с проектом — только его офис.
// Раскладку конфигурации печатает конструктор, один раз на все офисы.
func printBoards(all *offices, project string, now time.Time, out io.Writer) error {
	if project != "" {
		no, err := all.byProject(project)
		if err != nil {
			return err
		}
		return printBoard(no.Tracker, []string{project}, no.Workflow.Statuses, no.Workflow.IsTerminal, now, out)
	}
	return all.each(context.Background(), func(no namedOffice) error {
		return printBoard(no.Tracker, no.Projects.Keys(), no.Workflow.Statuses, no.Workflow.IsTerminal, now, out)
	})
}
```

Run: `go test ./cmd/runner/ -run TestBoards -v`
Expected: PASS.

- [x] **Step 7: `worktree rm` — office via `byProject(entry.Project)` (tasks.md 3.6)**

Failing test first. Append to `cmd/runner/worktree_test.go` (add imports `bytes`, `os`, `os/exec`, `path/filepath`, `github.com/kao73/virtual-office/internal/pipeline`, `github.com/kao73/virtual-office/internal/tracker/mock`):

```go
// bareOrigin — bare-репозиторий с одним коммитом в master: Ensure ответвляет
// ветку задачи от origin/master, и без коммита такой ссылки нет.
func bareOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare, seed := filepath.Join(root, "client.git"), filepath.Join(root, "seed")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@local",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--bare", "-b", "master", bare)
	run("init", "-q", "-b", "master", seed)
	run("-C", seed, "commit", "-q", "--allow-empty", "-m", "начало")
	run("-C", seed, "remote", "add", "origin", bare)
	run("-C", seed, "push", "-q", "origin", "master")
	return bare
}

// Аренду спрашивают у трекера, в котором задача живёт. В «чужом» офисе та же
// задача арендована живым прогоном: спроси rm не тот трекер — и получил бы
// отказ «над VO-1 работает прогон», хотя свой офис знает её свободной.
func TestRemoveWorktreeAsksTheOfficeOwningTheProject(t *testing.T) {
	origin := bareOrigin(t)
	ws := workspace.New(t.TempDir())
	project := tracker.Project{RepoURL: origin, DefaultBranch: "master", BranchPrefix: "agent/", Tracker: "jira"}
	if _, err := ws.Ensure(tracker.TaskRef{Key: "VO-1", Project: "VO"}, project); err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	add := func(tr *mock.Tracker) {
		t.Helper()
		if err := tr.Add(tracker.Task{Key: "VO-1", Project: "VO", Status: "Ready", Summary: "задача"}); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
	}
	own := mock.New(t.TempDir())
	add(own)
	foreign := mock.New(t.TempDir())
	add(foreign)
	if err := foreign.Claim(tracker.ClaimRequest{
		Key: "VO-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: moment.Add(time.Hour), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	var out bytes.Buffer
	all := &offices{
		list: []namedOffice{
			{name: "jira", Office: &pipeline.Office{Tracker: own, Projects: tracker.Projects{"VO": project}}},
			{name: "mock", Office: &pipeline.Office{Tracker: foreign, Projects: tracker.Projects{"OFF": {Tracker: "mock"}}}},
		},
		workspaces: ws,
		out:        &out,
	}
	if err := removeWorktree(all, "VO-1", false, moment, &out); err != nil {
		t.Fatalf("папка не удалена: %v", err)
	}
	if entries, _ := ws.List(); len(entries) != 0 {
		t.Errorf("рабочая папка осталась: %+v", entries)
	}
	if !strings.Contains(out.String(), "удалена") {
		t.Errorf("об удалении не сказано:\n%s", out.String())
	}
}
```

Then in `cmd/runner/worktree.go` replace `worktreeRemove` (lines 84–125) with:

```go
// worktreeRemove удаляет рабочую папку задачи.
func worktreeRemove(args []string, out io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("нужен ключ задачи первым аргументом\n\n%s", worktreeUsage)
	}
	key := args[0]

	fs := flags("worktree rm")
	force := fs.Bool("force", false, "снести, несмотря на незакоммиченное и живую аренду")

	// Трекер нужен ровно за одним: узнать, не работает ли сейчас над задачей
	// агент. Всё остальное команда знает из git и с диска. Флаги разбирает
	// offices — он же их и объявляет, поэтому раньше него парсить нечего.
	all, err := offices(fs, args[1:], out)
	if err != nil {
		return err
	}
	return removeWorktree(all, key, *force, time.Now(), out)
}

// removeWorktree — само удаление, отдельно от сборки офисов ради теста.
//
// Папка знает свой проект, проект — трекер, трекер — офис: про аренду
// спрашивается тот трекер, в котором задача живёт, а не первый попавшийся.
func removeWorktree(all *offices, key string, force bool, now time.Time, out io.Writer) error {
	entries, err := all.workspaces.List()
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Key != key {
			continue
		}

		no, err := all.byProject(entry.Project)
		if err != nil {
			return err
		}
		task, err := no.Tracker.Get(key)
		if err != nil && !errors.Is(err, tracker.ErrNotFound) {
			return err
		}
		if err := removable(entry, task, now, force); err != nil {
			return err
		}
		if err := all.workspaces.Remove(entry.Workspace); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: рабочая папка удалена, ветка %s осталась в клоне\n", key, entry.Branch)
		return nil
	}
	return fmt.Errorf("рабочей папки задачи %s нет; что есть — покажет `runner worktree ls`", key)
}
```

Run: `gofmt -l ./cmd/runner; go vet ./cmd/runner/ && go test ./cmd/runner/ -v`
Expected: no gofmt output; everything in `cmd/runner` PASS, including the five `TestOffices*` tests from Step 4.

- [x] **Step 8: Commit the runner restructure**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: green. (`pipeline.Office.Loop` still exists and its two tests still pass; deleting it is the next step.)

```bash
git add cmd/runner/office.go cmd/runner/office_test.go cmd/runner/board.go cmd/runner/board_test.go cmd/runner/worktree.go cmd/runner/worktree_test.go cmd/runner/main.go
git commit -m "$(cat <<'MSG'
refactor(runner): build one office per tracker, drop --tracker

offices() поднимает граф, проекты, затем каждый трекер, названный
проектами (tracker.yaml открывается и печатается только при jira, после
projects.local.yaml), forge по всем проектам разом и общее хозяйство —
и собирает по pipeline.Office на трекер с его проектами. Сборка строгая:
отказ любого трекера — отказ команды. tick/reap/complete-splits/ls идут
через each, loop — через offices.loop, worktree rm находит офис по
проекту папки. Оставшийся projects.yaml под корнем конфигурации — отказ
при старте: до удаления файла (Task 5) живой ./bin/runner на этом
checkout'е откажет — это сторож и делает.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [x] **Step 9: Delete `pipeline.Office.Loop` and `tickOnce`; adjust the two tests (tasks.md 3.4)**

In `internal/pipeline/pipeline_test.go`, add a helper next to `tickAll` (around line 210):

```go
// cycle — один заход цикла в том порядке, в каком его гоняет cmd/runner
// (offices.cycle): reap → tick роли → complete-splits, ошибки в лог, а не
// наверх. Сам драйвер живёт там; здесь проверяется, что конвейер под этим
// порядком ведёт себя как задумано.
func (o *office) cycle(role string) {
	ctx := context.Background()
	if err := o.Reap(ctx); err != nil {
		o.logf("reap: %v", err)
	}
	if _, err := o.Tick(ctx, role); err != nil {
		o.logf("tick: %v", err)
	}
	if err := o.CompleteSplits(ctx); err != nil {
		o.logf("complete-splits: %v", err)
	}
}
```

In `TestLoopRunsCompleteSplitsEachCycle` (line ~2396) and `TestLoopProcessesHumanReplyBeforeCompletingSplits` (line ~2430) replace

```go
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Loop(ctx, time.Minute, "reviewer"); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}
```
with
```go
	o.cycle("reviewer")
```

Rename them to `TestCycleRunsCompleteSplitsAfterTick` and `TestCycleProcessesHumanReplyBeforeCompletingSplits`, and in their doc comments replace every `Loop`/`tickOnce` with `cycle` (the driver comment now reads: `Порядок cycle (cmd/runner/offices.go) обязан пропускать tick (а с ним и HumanReplies, которого зовёт Tick) вперёд CompleteSplits …`). Replace the sentence `Контекст отменяется заранее: Loop проходит ровно один цикл (…) и останавливается на ctx.Done(), не дожидаясь таймера.` with `cycle делает ровно один заход — reap, tick, CompleteSplits, — тот же порядок, что у драйвера в cmd/runner.`

In `internal/pipeline/pipeline.go` delete `Loop` with its doc comment and `tickOnce` (lines 1108–1156). Check `time` is still imported and used (`keepLease`) — it is.

Run: `gofmt -l ./internal/pipeline; go vet ./internal/pipeline/ && go test ./internal/pipeline/ -run 'TestCycle' -v && go test ./...`
Expected: no gofmt output; PASS; full suite green.

- [x] **Step 10: Commit**

```bash
git add internal/pipeline/pipeline.go internal/pipeline/pipeline_test.go
git commit -m "$(cat <<'MSG'
refactor(pipeline): drop Office.Loop and tickOnce — the driver lives in cmd/runner

Единственная правка пайплайна в этом изменении: цикл по расписанию
теперь гоняет offices.loop, по офису на трекер. Два теста на порядок
reap → tick → complete-splits переписаны на явный заход того же порядка:
поведение конвейера под ним не меняется.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

## Task 4: Shipped example and setup script

Corresponds to `tasks.md` §4 (4.1–4.3).

**Files:**
- Modify: `tracker.example.yaml`, `scripts/jira-setup.sh`
- Test: `internal/tracker/jira/jira_test.go` (`TestShippedTrackerConfigIsValid` extended)

**Interfaces:**
- Consumes: `jira.LoadConfig`, `jira.ExampleFile` (unchanged).
- Produces: a `tracker.example.yaml` that loads with `len(cfg.Accounts.Roles)==0`, `len(cfg.AlsoAgents)==0`, `cfg.IssueType==""`, `cfg.DependsOnLink=="Depends"`; a script that prints `depends_on_link: Depends` after the four field ids.

### Background

`tracker.example.yaml` today opens with "ОБРАЗЕЦ подключения к JIRA. Раннер этот файл не читает." and ships `accounts.roles.reviewer`, `also_agents: []`, `issue_type: Task` active. `internal/tracker/jira/jira_test.go:1357` `TestShippedTrackerConfigIsValid` loads the example and checks that every `accounts.roles` key is a graph role. `scripts/jira-setup.sh` has `add_field` (line 83) as the find-or-create pattern and a final summary (lines 147–153) printing the four field ids. JIRA Server's `GET /rest/api/2/issueLinkType` returns `{"issueLinkTypes":[{"id":…,"name":…,"inward":…,"outward":…}]}`; `POST` with `{name, outward, inward}` creates one.

- [x] **Step 1: Extend the shipped-example test (tasks.md 4.1)**

In `internal/tracker/jira/jira_test.go`, replace `TestShippedTrackerConfigIsValid` with:

```go
// tracker.example.yaml — то, из чего собирают конфигурацию нового инстанса,
// и битый образец обнаружился бы первым же циклом против JIRA, то есть на живой
// доске. Сам tracker.yaml проверить нечем: он машинный и в репозитории его нет.
//
// Образец копируется дословно и правится в пяти значениях (base_url и четыре
// поля), поэтому всё необязательное в нём выключено: одна учётка на все роли,
// ни одного чужого бота, тип задачи по умолчанию. Тип связи, напротив,
// включён — его заводит scripts/jira-setup.sh.
func TestShippedTrackerConfigIsValid(t *testing.T) {
	root := filepath.Join("..", "..", "..")

	cfg, err := LoadConfig(filepath.Join(root, ExampleFile))
	if err != nil {
		t.Fatalf("%s не загружен: %v", ExampleFile, err)
	}
	if len(cfg.Accounts.Roles) != 0 {
		t.Errorf("accounts.roles в образце активен (%v): копия заставила бы заводить учётку роли", cfg.Accounts.Roles)
	}
	if len(cfg.AlsoAgents) != 0 {
		t.Errorf("also_agents в образце не пуст: %v", cfg.AlsoAgents)
	}
	if cfg.IssueType != "" {
		t.Errorf("issue_type в образце задан (%q): умолчание кода — Task, поле незачем включать", cfg.IssueType)
	}
	if cfg.DependsOnLink != "Depends" {
		t.Errorf("depends_on_link = %q, ожидался Depends — его заводит jira-setup.sh", cfg.DependsOnLink)
	}

	// Заголовок написан для рабочего файла: после копии в ${OFFICE_HOME} он
	// не должен называть себя образцом, который раннер не читает.
	raw, err := os.ReadFile(filepath.Join(root, ExampleFile))
	if err != nil {
		t.Fatalf("%s не прочитан: %v", ExampleFile, err)
	}
	if strings.Contains(string(raw), "не читает") {
		t.Errorf("%s всё ещё описывает себя как образец, который раннер не читает", ExampleFile)
	}
}
```

(Add `"os"` to the imports if not already there.) Run: `go test ./internal/tracker/jira/ -run TestShippedTrackerConfigIsValid -v`
Expected: FAIL on `accounts.roles`, `issue_type` and the header sentence.

- [x] **Step 2: Rewrite `tracker.example.yaml` (tasks.md 4.1)**

Replace the file with (the values stay polygon values; `customfield_*` is allowed here — the boundary test excludes the example on purpose):

```yaml
# Подключение офиса к JIRA: ${OFFICE_HOME}/tracker.yaml.
#
# Образец лежит в репозитории как tracker.example.yaml и копируется сюда
# целиком:
#     mkdir -p "${OFFICE_HOME:-$HOME/.office}"
#     cp tracker.example.yaml "${OFFICE_HOME:-$HOME/.office}/tracker.yaml"
# После копии правятся ровно пять значений: base_url и четыре customfield_*
# из вывода scripts/jira-setup.sh. Остальное совпадёт где угодно: режим
# авторизации, имена переменных с учётками, метка ожидания человека и карта
# статусов (нетождественна одна запись, InProgress, и та стандартна для Jira).
# Необязательные разделы — accounts.roles, also_agents, issue_type — ниже
# закомментированы: раскомментируй, когда понадобятся.
#
# Секретов здесь нет и не будет: кред живёт в окружении, в файле — только имена
# переменных. Настройка инстанса, из которой берутся идентификаторы полей
# и тип связи, — scripts/jira-setup.sh и docs/notes/jira-setup.md.

base_url: http://localhost:2990/jira

auth:
  # Режим один: персональные токены появились в Jira Server с 8.14, а целевая
  # версия — 8.13. Появится инстанс с токенами — добавится и режим; обещать его
  # заранее незачем, см. docs/notes/jira-api.md.
  #
  # Как ходить — свойство инстанса, а под кем — свойство роли, поэтому учётки
  # живут отдельным разделом.
  mode: basic

# Учётки офиса. В файле — только имена переменных: секреты живут в окружении,
# и имя пользователя тоже, чтобы обе половины креда лежали рядом.
#
# Комментарий от учётки, которой здесь нет, считается словами человека. Список
# агентских учёток раннер выводит отсюда сам — вручную его не перечисляют:
# забытая роль стала бы «голосом человека» и возвращала бы задачу в очередь
# собственным отчётом.
accounts:
  # Общая учётка. Под ней идёт всё, у чего роли нет: reap, разбор ответов
  # человека, системные записи.
  default:
    user_env: JIRA_USER
    secret_env: JIRA_PASSWORD

  # Учётки ролей — опция. Одна учётка на всех агентов — штатный режим: роли
  # раннер различает по маркеру в первой строке комментария, а не по автору.
  # Отдельная нужна затем, чтобы история тикета читалась людьми и чтобы права
  # в JIRA можно было развести. Учётку заводит scripts/jira-setup.sh.
  # Раскомментируй, если решил вести reviewer'а под своей учёткой:
  # roles:
  #   reviewer:
  #     user_env: JIRA_REVIEWER_USER
  #     secret_env: JIRA_REVIEWER_PASSWORD

# Чужая автоматизация: боты, чья проза тоже не человеческая. Единственный список,
# который пишется руками, — вывести его неоткуда.
# Раскомментируй, если на инстансе пишут боты, которых нельзя принять за человека:
# also_agents: [bot-user]

# Статус графа (workflow.yaml) → имя статуса на инстансе. «In Progress» с пробелом
# остаётся здесь: раннер знает только имена из графа.
#
# Ключ не `statuses`: одно слово не должно значить и узел графа, и его перевод.
#
# Названы все статусы графа, включая человеческие: имя, совпадающее с именем
# на инстансе, работает и без записи, но тогда — случайно, а опечатка молча
# превратилась бы в несуществующий статус.
status_map:
  Backlog: Backlog
  Analysis: Analysis
  Ready: Ready
  InProgress: In Progress
  Review: Review
  Approved: Approved
  Done: Done
  Blocked: Blocked

# Кастомные поля аренды. Идентификаторы печатает scripts/jira-setup.sh —
# на другом инстансе они будут другими.
fields:
  agent_owner: customfield_10101
  run_id: customfield_10102
  lease_until: customfield_10103
  attempts: customfield_10104

# Атрибут «ждёт человека» — метка: её видно в списке задач, и она не требует
# настройки экранов.
human_flag_label: office-waits-human

# Тип задачи для тикетов, которые заводит CreateTask (авто-создание детей
# разбиения). Без записи код берёт "Task": на большинстве инстансов он есть
# из коробки. Раскомментируй, если на инстансе тип зовётся иначе:
# issue_type: Task

# Тип связи «зависит от» для LinkDependsOn (POST /issueLink). Заводит
# scripts/jira-setup.sh под этим именем; переименовать — значит править
# и скрипт, и эту строку.
depends_on_link: Depends
```

Run: `go test ./internal/tracker/jira/ -run TestShippedTrackerConfigIsValid -v && go test ./internal/tracker/...`
Expected: PASS.

- [x] **Step 3: Commit the example**

```bash
git add tracker.example.yaml internal/tracker/jira/jira_test.go
git commit -m "$(cat <<'MSG'
docs(tracker): make tracker.example.yaml copy-ready

Заголовок написан для рабочего файла ${OFFICE_HOME}/tracker.yaml и
отсылает к образцу за командой копирования, а не наоборот. accounts.roles,
also_agents и issue_type закомментированы с одной строкой «раскомментируй,
если…»: свежая копия требует ровно пяти правок — base_url и четыре поля.
depends_on_link: Depends остаётся активным — тип заводит jira-setup.sh.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [x] **Step 4: `scripts/jira-setup.sh` — `add_link_type` (tasks.md 4.2)**

The script has no unit test harness; the check is the live run in Step 5. Edit in three places.

(a) After the constants block (after line 29 `reviewer_password=…`), add:

```sh
# Имя типа связи «зависит от» — константа скрипта: оно же стоит
# в tracker.example.yaml (depends_on_link), переименовать — значит править обоих.
link_type=Depends
```

(b) After the screens section (after line 114 `done`) and before `# --- учётки ---`, add:

```sh
# --- тип связи «зависит от» ---------------------------------------------------
# Его читает LinkDependsOn (depends_on_link в tracker.yaml). Направление,
# в котором этот сервер рисует outward/inward, компенсирует код раннера
# (internal/tracker/jira, LinkDependsOn), а не имена здесь.
add_link_type() {
	local name=$1 outward=$2 inward=$3
	if api "$url/rest/api/2/issueLinkType" |
		python3 -c "import json,sys; sys.exit(0 if any(t['name']==sys.argv[1] for t in json.load(sys.stdin)['issueLinkTypes']) else 1)" "$name"; then
		echo "  тип связи $name уже есть"
		return
	fi
	api -X POST -d "{\"name\":\"$name\",\"outward\":\"$outward\",\"inward\":\"$inward\"}" \
		"$url/rest/api/2/issueLinkType" >/dev/null
	echo "  тип связи $name заведён"
}

echo "тип связи:"
add_link_type "$link_type" 'depends on' 'is depended on by'
```

(c) In the final summary, after `echo "  attempts:    $attempts_id"`, add:

```sh
echo "  depends_on_link: $link_type"
```

Also update the header comment line 2: `# Настройка полигона JIRA под офис: статусы, кастомные поля аренды, экраны, учётки.` → `# Настройка полигона JIRA под офис: статусы, кастомные поля аренды, экраны, тип связи, учётки.`

Run: `bash -n scripts/jira-setup.sh && shellcheck scripts/jira-setup.sh 2>/dev/null || true`
Expected: `bash -n` prints nothing (syntax OK); shellcheck, if installed, reports nothing new.

- [x] **Step 5: Live check against the polygon (tasks.md 4.3) — no tokens, no agent**

The polygon address is `base_url` in `~/.office/tracker.yaml`; the admin password comes from `JIRA_PASSWORD` in the environment (see the operator's memory note on tracker creds; never put it on the command line). Run:

```sh
url=$(sed -n 's/^base_url: *//p' ~/.office/tracker.yaml)
# состояние до: сколько типов Depends
curl -sS -u "admin:$JIRA_PASSWORD" "$url/rest/api/2/issueLinkType" | python3 -c 'import json,sys; print([t["name"] for t in json.load(sys.stdin)["issueLinkTypes"]])'
scripts/jira-setup.sh --url "$url" --user admin | sed -n '/^тип связи:/,/^учётки:/p;/depends_on_link/p'
scripts/jira-setup.sh --url "$url" --user admin | sed -n '/^тип связи:/,/^учётки:/p'
# состояние после: ровно один Depends, outward = depends on
curl -sS -u "admin:$JIRA_PASSWORD" "$url/rest/api/2/issueLinkType" | python3 -c 'import json,sys; ts=[t for t in json.load(sys.stdin)["issueLinkTypes"] if t["name"]=="Depends"]; print(len(ts), ts and ts[0]["outward"])'
```

Expected and what to record in the verification report (`docs/superpowers/reports/2026-09-17-config-cleanup-verify.md`, section "Task 4.3"):
- If the polygon had no `Depends` before: first run prints `тип связи Depends заведён`, second prints `тип связи Depends уже есть`; the final count is `1 depends on`.
- If the polygon already had `Depends` (likely — it was created by hand during the split-autocreate change): both runs print `уже есть`, the count stays `1`. Idempotence is proven; the **creation** path is then exercised against a fresh container — `bootstrap/jira/README.md` describes bringing one up — or, if no fresh instance is reachable, the report says so explicitly and lists the creation branch as verified by reading the script only.
- Both runs print `depends_on_link: Depends` in the summary next to the four field ids.
- Note for the report: on this polygon `outward`/`inward` render reversed in the UI (known finding, compensated in `jira.LinkDependsOn`); the API answer above is what counts.

- [x] **Step 6: Commit the script**

```bash
git add scripts/jira-setup.sh
git commit -m "$(cat <<'MSG'
feat(scripts): jira-setup.sh creates the Depends link type idempotently

По образцу add_field: найти по имени → создать → напечатать. Имя —
константа скрипта, она же стоит в tracker.example.yaml. Итоговая сводка
получает depends_on_link: Depends рядом с полями аренды, так что инстанс,
настроенный скриптом, содержит всё, на что образец ссылается по имени.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

## Task 5: Repository and this machine

Corresponds to `tasks.md` §5 (5.1–5.3). One step (5.2) is **outside the repository** and is an operator step.

**Files:**
- Delete: `projects.yaml`
- Modify: `cmd/run-agent/main.go` (wire `RefuseLeftoverOfficeFile`), comments in `bin/runner`, `roles/analyst/role.yaml`, `roles/implementer/role.yaml`, `roles/reviewer/role.yaml`, `internal/pipeline/pipeline.go:1230`, `internal/pipeline/pipeline_test.go:1951,2889,2911`, `internal/tracker/jira/jira.go:362`, `internal/tracker/jira/jira_test.go:101,1543`, `internal/tracker/boundary_test.go` (comment), `cmd/runner/office_test.go`/`board_test.go` (if any comment still names the file)
- Outside the repo: `~/.office/projects.local.yaml`

**Interfaces:**
- Consumes: `tracker.RefuseLeftoverOfficeFile` (Task 2), `offices()` (Task 3).

- [x] **Step 1: Delete `projects.yaml`, wire the guard into `run-agent`, rename the file in every comment (tasks.md 5.1)**

```bash
git rm projects.yaml
```

In `cmd/run-agent/main.go`, inside `if *projectFlag != "" {` before `projects, err := tracker.LoadProjects(...)`:

```go
		// projects.yaml из прежней раскладки не читается — и не пропускается
		// молча: тот же сторож, что и у runner.
		if err := tracker.RefuseLeftoverOfficeFile(configRoot); err != nil {
			return 0, err
		}
```

Comment fixes (each is a one-line rename `projects.yaml` → `projects.local.yaml` unless stated):
- `bin/runner:5`: `# workflow.yaml и projects.yaml — и запоминает каталог вызова, чтобы` → `# workflow.yaml и роли — и запоминает каталог вызова, чтобы`.
- `roles/analyst/role.yaml:18-19`: `tools.deny ниже и defaults.tools.deny` / `в projects.yaml, плюс` → `tools.deny ниже и базовый слой` / `roles/_base/base.yaml, плюс`; `:31`: `в defaults.tools.deny (projects.yaml), не здесь` → `в roles/_base/base.yaml, не здесь`.
- `roles/implementer/role.yaml:20`: `defaults.tools.deny в projects.yaml, плюс` → `tools.deny в roles/_base/base.yaml, плюс`; `:28`: `Общие для всех ролей — в defaults.tools.deny (projects.yaml).` → `Общие для всех ролей — в roles/_base/base.yaml.`
- `roles/reviewer/role.yaml:33`: `в defaults.tools.deny (projects.yaml).` → `в roles/_base/base.yaml.`; `:65`: `(defaults.network в projects.yaml)` → `(общий allowlist сети — тогда в projects.yaml, теперь roles/_base/base.yaml)`.
- `internal/pipeline/pipeline.go:1230`, `internal/pipeline/pipeline_test.go:1951,2889,2911`, `internal/tracker/jira/jira.go:362`, `internal/tracker/jira/jira_test.go:101,1543`: `projects.yaml` → `projects.local.yaml`.

Run:

```sh
git grep -n 'projects\.yaml' -- . ':!docs/notes' ':!docs/openspec/changes/archive' ':!docs/comet' ':!docs/STAGE-*' ':!docs/superpowers' ':!docs/openspec/changes/config-cleanup' | grep -v 'projects\.local\.yaml'
```

Expected output — **only** these, and the report records the list:
- `internal/tracker/config.go` — `OfficeProjectsFile` and the `RefuseLeftoverOfficeFile` doc comment (the guard itself);
- `internal/tracker/config_test.go` — `TestRefuseLeftoverOfficeFile`'s comments;
- `internal/tracker/boundary_test.go` — the comment "отвергает projects.yaml из прежней раскладки";
- `roles/reviewer/role.yaml:65` — the historical parenthesis above;
- `roles/_base/base.yaml` — the pointer `git log -- projects.yaml`;
- `README.md`, `docs/DESIGN.md`, `docs/ONBOARDING.md` — fixed in Task 6;
- `docs/contracts/tracker-protocol.md`, `docs/contracts/role-sandbox-permissions.md`, `docs/openspec/specs/role-sandbox-permissions/spec.md` — the reference documents; the main spec is rewritten at archive time by the delta merge, the two contracts belong to the follow-up documentation change (proposal.md: "the reference document … [is] a separate follow-up change"). List them in the report as known residuals.

Then: `go build ./... && go vet ./... && go test ./...` — green. The `-dirty` scenario is now real: `git status --porcelain` in the checkout must be empty after the commit.

```bash
git add -A bin/runner roles cmd/run-agent/main.go internal/pipeline internal/tracker
git commit -m "$(cat <<'MSG'
chore(config): delete projects.yaml — the repository carries no projects

Список проектов и общие правила уехали: проекты — в ${OFFICE_HOME}/
projects.local.yaml, правила — в roles/_base/base.yaml. run-agent получает
тот же сторож от оставшегося файла, что и runner. Комментарии, звавшие
projects.yaml, называют новые адреса.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [x] **Step 2: OPERATOR STEP — migrate `~/.office/projects.local.yaml` (tasks.md 5.2)**

This file is outside the repository; nothing is committed. Replace its contents with the Design Doc's YAML (section «Миграция этой машины»), verbatim:

```yaml
OFFICE:
  repo_url: /Users/aleksejkolesnikov/IdeaProjects/office-polygons/client.git
  default_branch: master
  tracker: mock

VO:
  repo_url: /Users/aleksejkolesnikov/IdeaProjects/office-polygons/client.git
  default_branch: master
  tracker: jira

EXP:
  repo_url: https://github.com/kao73/expense-tracker.git
  default_branch: main
  tracker: jira
  forge: github
  auto_merge:
    enabled: true
    target_branch: office-integration
  # Хосты, нужные только этому проекту (Playwright e2e в песочнице) —
  # раньше лежали в projects.yaml: defaults, свидетельства в git log.
  network:
    - mcr.microsoft.com
    - "*.data.mcr.microsoft.com"
    - bun.sh
    - cdn.playwright.dev
    - playwright.download.prss.microsoft.com
    - ports.ubuntu.com
    - deb.nodesource.com
```

Keep a copy of the previous file next to it (`cp ~/.office/projects.local.yaml ~/.office/projects.local.yaml.pre-config-cleanup`) for the rollback described in the Design Doc: `git revert` the commits, drop `default_branch` and the `EXP` `network` block from this file, restore `projects.yaml` — the old strict loader would reject the new keys.

Verification: `./bin/runner ls` in Step 3 loads the file; a typo shows up as the loader's refusal naming key, project and file.

- [x] **Step 3: Live checks without tokens (tasks.md 5.3) — results go into the verification report**

With `JIRA_USER`/`JIRA_PASSWORD` exported (the jira office needs them; see the operator's tracker-creds note) and no `--tracker` flag:

```sh
./bin/runner ls
```

Expected: the configuration listing has `workflow.yaml`, `projects.local.yaml`, `tracker.yaml` (after `projects.local.yaml`), two `budgets.yaml` lines, and **no** `projects.yaml` line; then `== трекер jira ==` with the EXP and VO boards, then `== трекер mock ==` with the OFFICE board. Record the first ~10 lines in the report.

```sh
rm -rf /tmp/probe && mkdir -p /tmp/probe && git -C /tmp/probe init -q -b master
git -C /tmp/probe -c user.name=probe -c user.email=probe@local commit -q --allow-empty -m init
echo 'Создай hello.py и закоммить.' > /tmp/task.md
./bin/run-agent --role analyst --workdir /tmp/probe --task /tmp/task.md --backend local --dry-run
```

Expected: the printed `config_sha` has **no** `-dirty` suffix (the tree is clean: nothing under the repository was edited for this machine), and the effective `tools.deny` in the printed settings contains `Bash(git *push*)` (base layer, no `--project`). Also run `./bin/runner tick --tracker jira; echo "код $?"` and expect the unknown-flag error and a non-zero code, before any configuration line. Record all three outputs in the report.

No commit for this step.

---

## Task 6: Documentation that would otherwise be false

Corresponds to `tasks.md` §6 (6.1–6.3). Only statements that this change makes wrong are touched; additive documentation (the reference `CONFIG.md`, `projects.local.example.yaml`, `bootstrap/`) is the follow-up change. All text is Russian.

**Files:**
- Modify: `docs/DESIGN.md` (§2.5 rewritten; one phrase in §2.6), `README.md`, `docs/ONBOARDING.md`

There is no automated test for prose; the check is `git grep` (below) plus reading the diff against the Design Doc's sections «`docs/DESIGN.md` §2.5 — правка текста» and «README и ONBOARDING — только то, что стало бы ложью».

- [x] **Step 1: `docs/DESIGN.md` §2.5 (tasks.md 6.1)**

Replace the whole §2.5 section (from `### 2.5 …` up to but excluding `### 2.6 …`) with:

```markdown
### 2.5 Конфигурация как код: репозиторий — фреймворк, инстанс — в `${OFFICE_HOME}`

Граница добавлена на этапе 5 и ужесточена изменением config-cleanup; формулируется одной фразой: **репозиторий — это фреймворк, инстанс настраивается в `${OFFICE_HOME}`.** Ни одного файла репозитория не правят, заводя новую машину или второй инстанс. Причина не в чистоте раскладки, а в том, что репозиторий носил правду одной машины: с неё всё выглядело исправно, а любой другой обязан был переписать закоммиченные файлы, чтобы запуститься, и получал дерево, грязное навсегда, вместе с меткой `config:…-dirty` на каждом прогоне. Метка задумана как ответ на вопрос «тот ли конфиг достался агенту», и всюду, кроме машины-хозяйки, отвечать переставала.

- В репо: роли (`roles/<role>/role.md`, `role.yaml`) и их общие правила — `roles/_base/base.md` (промпт) и `roles/_base/base.yaml` (сеть и инструменты, которые наследует каждая роль), скиллы/плагины (формат Claude Code plugin/marketplace, SKILL.md — кросс-агентный стандарт), адаптеры под агентов, runner, bootstrap (Docker/sbx kit), документация, граф переходов, дефолтные бюджеты, образец `tracker.example.yaml`. **Проектов в репозитории нет.**
- В `${OFFICE_HOME}`: `projects.local.yaml` — каждый проект целиком (где репозиторий, ветка по умолчанию, трекер, forge, auto_merge, добавки к правилам: ключ `defaults` для всей машины и запись для одного проекта), `tracker.yaml` целиком (адрес, учётки, `customfield_*`, карта статусов), перекрытие бюджетов.
- Раннер обслуживает все трекеры, названные проектами: по офису на трекер, обход по имени трекера; флага выбора трекера нет. `tracker.yaml` открывается только когда среди проектов есть `jira`.
- Слои разрешений: базовый (`roles/_base/base.yaml`) → машинный (`defaults` в `projects.local.yaml`) → проектный (запись проекта) → ролевой (`role.yaml`). Каждый слой только добавляет; убрать названное менее специфичным нельзя — и это касается `tools.deny`.
- **Границу держит код, а не память того, кто правит файл.** Загрузчик принимает под `defaults` только `network` и `tools` и называет лишний ключ по имени; `projects.yaml` из прежней раскладки, оставшийся под корнем конфигурации, — отказ при старте с указанием, куда переехало содержимое; один тест проходит по файлам офиса и ловит абсолютные пути и `customfield_*`, другой — что `roles/_base/base.yaml` поставляется и держит запрет на `git push`.
- Раннер печатает при старте, откуда взял каждый файл конфигурации: половина её лежит в репозитории, половина — в хозяйстве, и гадать, какой именно файл он открыл, человеку не должно приходиться.
- Роль — агент-нейтральная спецификация + адаптер (`internal/adapters/claude`, позже `internal/adapters/codex` и т.д.), который превращает её в флаги конкретного CLI.
- Роль **не устанавливается на машину**, а передаётся runner'ом в момент запуска (промпт роли, настройки, папка скиллов из репо). Машина не хранит «состояние роли»; любая машина может выполнить любую роль.
- Обновления: runner делает `git pull` конфиг-репо перед запуском (или берёт закреплённый тег). Прод — тег/ветка `stable`, канарейка — `main`. SHA конфига пишется в комментарий каждого запуска → трассировка и откат одной командой. При незакоммиченной правке SHA помечается суффиксом `-dirty`: он больше не описывает то, что ушло агенту, и опираться на него нельзя.
- Секреты (ключ Anthropic, токен JIRA, GitHub PAT) — **не в репо**: env / secret store / `sbx secret`.
```

In §2.6, the bullet «Слоистая модель разрешений…» names `projects.yaml`; replace `repo-wide умолчания (зарезервированный ключ `defaults` в `projects.yaml`), проект, машина (тот же принцип в `projects.local.yaml`) и роль (`role.yaml`, как и было)` with `базовые правила ролей (`roles/_base/base.yaml`), машинные умолчания (ключ `defaults` в `projects.local.yaml`), проект (его запись там же) и роль (`role.yaml`, как и было)`. (One phrase; it is the same falsehood as §2.5's, in the same document.)

- [x] **Step 2: `README.md` (tasks.md 6.2)**

Quick start (lines 76–90): delete step 3 (`# 3. Назвать проект офису — projects.yaml в репозитории…` through its four YAML comment lines) and turn step 4 into the new step 3:

```sh
# 3. Сказать офису, где проект на этой машине. Каталога может ещё не быть:
mkdir -p "${OFFICE_HOME:-$HOME/.office}"
#    ${OFFICE_HOME}/projects.local.yaml — проекты живут только здесь,
#    списка проектов в репозитории нет:
#    OFF:
#      repo_url: /tmp/client.git
#      default_branch: master
#      tracker: mock          # чей проект: mock или jira
#      # branch_prefix: agent/ — умолчание, можно не писать
#      # forge не задан: локальный репозиторий, открывать PR негде
```

Renumber the following steps 5→4, 6→5, 7→6, and in the prose after the block change `Шестая команда делает всё` to `Пятая команда делает всё`.

JIRA section (lines 168–174): replace `Отличий два: настроенный инстанс и флаг.` with `Отличие одно: настроенный инстанс. Какой трекер у проекта, сказано в его записи в `projects.local.yaml`, и раннер обслуживает все названные — флага нет.` and the command `./bin/runner tick --tracker jira --role implementer` with `./bin/runner tick --role implementer`.

Layers paragraph (line 425–427): `repo-wide умолчания (`defaults` в `projects.yaml`), слой проекта, слой машины (`projects.local.yaml`) и, поверх них, слой роли` → `базовые правила ролей (`roles/_base/base.yaml`), машинные умолчания и слой проекта (оба в `projects.local.yaml`) и, поверх них, слой роли`.

«Где что лежит» (lines 568, 589): `roles/                 спецификации ролей: промпт, машиночитаемый контракт, шаблоны артефактов` → `roles/                 спецификации ролей: промпт, машиночитаемый контракт, шаблоны; _base/ — общий промпт и общие правила (base.yaml)`; delete the line `projects.yaml          проекты-клиенты офиса: имена и неизменные свойства веток`.

Boundary paragraph (lines 595–613): replace from `Конфигурация лежит в двух местах, и граница простая:` through `при отказе строки уже выведены.` with:

```markdown
Конфигурация лежит в двух местах, и граница простая: **репозиторий — это фреймворк,
`${OFFICE_HOME}` — инстанс.** Всё, что правят, заводя новую машину или второй
инстанс, живёт вне репозитория — эти три файла видны в дереве хозяйства выше,
а ключи у них такие:

```
projects.local.yaml   на каждый проект: repo_url, default_branch, tracker; необязательные
                      branch_prefix, worktree_root, forge, auto_merge, network, tools;
                      ключ defaults — добавки network/tools всей машине
tracker.yaml          адрес, учётки, номера полей, карта статусов
budgets.yaml          перекрытие пределов, необязательное
```

Списка проектов в репозитории нет, так что чистый клон стартует без единой правки,
и метка прогона `config:…-dirty` значит ровно то, что говорит: незакоммиченную
правку конфигурации. За границей следит загрузчик: под `defaults` он принимает только
`network` и `tools`, а `projects.yaml` из прежней раскладки, оставшийся под корнем
конфигурации, — отказ при старте с указанием, куда переехало содержимое. При запуске
раннер печатает, откуда взял каждый файл, и печатает по ходу: при отказе строки
уже выведены.
```

- [x] **Step 3: `docs/ONBOARDING.md` (tasks.md 6.3)**

Б2 checklist (line 240–241): `Статусы, четыре поля аренды, экраны и учётки заведены. Скрипт печатает в конце идентификаторы полей — **они понадобятся на шаге Б5, сохраните вывод.**` → `Статусы, четыре поля аренды, тип связи `Depends`, экраны и учётки заведены. Скрипт печатает в конце идентификаторы полей и имя типа связи — **они понадобятся на шаге Б5, сохраните вывод.**`

Б3 (lines 304–306): `Решено, нужна ли отдельная учётка роли. Не нужна — всё поедет под общей; убрать `accounts.roles` надо будет из `${OFFICE_HOME}/tracker.yaml`, а он заводится в Б5, так что правка идёт туда, а не сюда.` → `Решено, нужна ли отдельная учётка роли. Не нужна — всё поедет под общей, образец так и устроен. Нужна — раскомментировать `accounts.roles` надо будет в `${OFFICE_HOME}/tracker.yaml`, а он заводится в Б5, так что правка идёт туда, а не сюда.`

Б5 (lines 398–425): retitle `## Б5. Конфигурация: две половины` → `## Б5. Конфигурация: инстанс в `${OFFICE_HOME}``; replace the text from `Граница простая:` through the checkbox `Ключи обоих файлов совпадают…` with:

```markdown
Граница простая: **репозиторий — это фреймворк, `${OFFICE_HOME}` — инстанс.**
В репозитории проектов нет; каждый описывается одной записью
`${OFFICE_HOME}/projects.local.yaml`:

```yaml
OFF:
  repo_url: /tmp/client.git
  default_branch: master
  tracker: jira          # чей проект: mock или jira; раннер обслуживает все названные
  # branch_prefix: agent/  # умолчание
  # forge: github        # если PR нужны
  # auto_merge:          # опционально: сливать PR самим, без человека
  #   enabled: true
  #   target_branch: office-integration   # пусто — значит default_branch;
  #                                       # эту ветку заводит человек заранее
  # worktree_root — не задан, значит ${OFFICE_HOME}/worktrees/<проект>
```

- [x] Каждый проект назван один раз и целиком: `repo_url`, `default_branch`
      и `tracker` обязательны, остальное — по надобности. Проект, которого здесь
      нет, раннер не берёт; ключ, которого нет в контракте, — отказ при старте
      с именем проекта, ключа и файла.
```

In the `tracker.yaml` checklist of Б5 (lines 421–425) replace `Правлены ровно пять значений: `base_url` и четыре `customfield_NNNNN` из вывода `jira-setup.sh`. Решили в Б3 обойтись общей учёткой — здесь же уберите раздел `accounts.roles`: копия образца приносит его обратно.` with `Правлены ровно пять значений: `base_url` и четыре `customfield_NNNNN` из вывода `jira-setup.sh`. Остальное — как в образце: `accounts.roles`, `also_agents` и `issue_type` закомментированы, раскомментируйте `accounts.roles`, если в Б3 решили завести учётку роли. `depends_on_link: Depends` не трогайте: тип связи завёл `jira-setup.sh` в Б2.`

Line 459: `раскладка конфигурации показывает оба файла проектов и `tracker.yaml`` → `раскладка конфигурации показывает `projects.local.yaml` и `tracker.yaml``.

Б7 (lines 497–514): `./bin/runner ls --tracker jira` → `./bin/runner ls`; the listing block becomes:

```
конфигурация:
  workflow.yaml          …/virtual-office/workflow.yaml (офис, есть)
  projects.local.yaml    …/.office/projects.local.yaml (машина, есть)
  tracker.yaml           …/.office/tracker.yaml (машина, есть)
  budgets.yaml           …/virtual-office/budgets.yaml (офис, есть)
  budgets.yaml           …/.office/budgets.yaml (машина, нет)
```

and the sentence `Под `--tracker mock` строки `tracker.yaml` не будет вовсе — файл не открывается.` → `Если ни один проект не назвал `jira`, строки `tracker.yaml` не будет вовсе — файл не открывается. С двумя трекерами доска каждого печатается под заголовком `== трекер <имя> ==`.`

Lines 531–534: `После шага Б5 он у вас почти наверняка появится, и почему — сказано в конце документа, среди симптомов.` → `Чистый клон его не получает: в репозитории нечего править под себя. Появился — см. симптом в конце документа.`

Lines 543, 561–564: every `./bin/runner tick --tracker jira` → `./bin/runner tick` (keep the trailing comments).

Symptom (lines 629–634): replace the paragraph `**В метке прогона `config:…-dirty`.** …` through `…раздел «Зачем это было нужно».` with:

```markdown
**В метке прогона `config:…-dirty`.** Рабочее дерево конфиг-репозитория не чисто:
незакоммиченная правка или неотслеживаемый файл. Заводя инстанс, править репозиторий
не нужно — всё своё лежит в `${OFFICE_HOME}`, — так что метка означает именно вашу
правку под себя или забытый файл. `git status` в корне конфигурации покажет, что
именно; закоммитьте или уберите — иначе у ваших прогонов эта метка ничего не значит.
```

- [x] **Step 4: Check and commit**

Run:

```sh
git grep -n 'tracker jira\|projects\.yaml' -- README.md docs/DESIGN.md docs/ONBOARDING.md | grep -v 'projects\.local\.yaml'
```

Expected: no `--tracker` hits; `projects.yaml` only in the two sentences about the leftover-file refusal (DESIGN §2.5, README boundary paragraph). Then `go test ./...` (unchanged, still green) and:

```bash
git add docs/DESIGN.md README.md docs/ONBOARDING.md
git commit -m "$(cat <<'MSG'
docs: DESIGN §2.5, README, ONBOARDING follow the framework-only boundary

Репозиторий — фреймворк, инстанс — в ${OFFICE_HOME}: списки «что где
лежит» переписаны вокруг roles/_base/base.yaml и projects.local.yaml,
абзац «долг закрыт не до конца» убран, раннер без флага трекера назван.
README: шаг быстрого старта про projects.yaml удалён, default_branch
в записи проекта, команды без --tracker, абзац про -dirty. ONBOARDING:
Б5 без правки репозитория, Depends заводит скрипт (Б2), Б7 без флага,
симптом -dirty сведён к общему случаю.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

## Self-review against the Design Doc and the delta specs

**Spec coverage.**
- config-boundary «A clean clone starts without being edited» → Task 5.1 (delete) + 5.3 (`config_sha` without `-dirty`). «A leftover projects.yaml is refused» → Task 2 (`RefuseLeftoverOfficeFile`, test), wired in Task 3 (`offices()`) and Task 5 (`run-agent`). «A minimal entry is enough / missing default branch / explicit prefix / unknown key / no project at all» → Task 2 tests `TestLoadProjects`, `…RejectsIncomplete`, `…HonoursBranchPrefix`, `…RejectsFileWithoutProjects`. «auto_merge / relocated key under defaults refused» → `TestLoadProjectsRejectsProjectKeysUnderDefaults`. «Manual run without a project still carries the denies / pipeline run carries base, machine and role rules / missing base file is loud» → Task 1 (`TestDryRunProjectFlagMergesMachineRulesOverBase`, `TestLoadRoleUnionsBaseRules`, `TestLoadRoleRefusesBaseDirWithoutRules`). «Fresh copy needs five edits / copied header describes the working file» → Task 4.1 test. «Fresh instance gains the link type / second run creates nothing» → Task 4.2 + 4.3 live.
- runner-multi-tracker «removed flag rejected» → `TestOfficesRejectTrackerFlag`; «two trackers served without a flag / each tracker is its own office / tracker sees only its own projects» → `TestOfficesBuildOnePerTracker`; «mock-only machine has no tracker file / jira project without the file refused» → `TestOfficesMockOnlyNeedsNoTrackerFile`, `…JiraProjectWithoutTrackerFileIsRefused`; «loop tick covers every office / stop between runs» → `TestCycleReapsEveryOffice`, `TestCycleStopsBeforeNextOfficeOnCancel`, `TestLoopStopsOnSignalWithoutWaiting`; «board lists tasks per tracker, sources once» → `TestBoardsListTasksPerTracker`; «tracker that cannot be opened stops the command» → `…RejectedJiraCredentialRefusesWholeCommand`; «failing tracker does not stop the others within one tick» → `TestEachVisitsEveryOfficeAndJoinsErrors`; «machine-wide resources shared» → `TestOfficesBuildOnePerTracker` (same `Workspaces`/`Ledger` pointer) and the unchanged `sweepWorktrees` filtering by `o.Projects` (`TestSweepLeavesForeignProjects` already covers it).
- role-sandbox-permissions (modified) → Task 1 union in `LoadRole` + Task 2 layering test `TestLoadProjectsLayersDefaultsAndProjectRules`.
- Design Doc «Миграция этой машины» → Task 5.2; «Живьём, без токенов» → Tasks 4.3 and 5.3; «Граничные случаи»: `_base/` without `base.yaml` → Task 1 test; empty `defaults` → Task 2 test; mock-only with a stray `tracker.yaml` → not read, not listed (Task 3 constructor opens it only under `case "jira"`); two projects on one bare repo under different trackers → `Workspaces` shared, `sweepWorktrees` per office unchanged; signal mid-run → `each`/`cycle` tests.

**Placeholder scan.** No TBD/TODO; every code step carries the code; the two live steps carry exact commands and expected output.

**Type consistency.** `runner.Union` (Task 1) is what `LoadProjects`, `MergeProjectRules` (Task 2) and `TrackersInUse` call. `offices` fields `list`, `workspaces`, `out` are what `offices()` (Task 3 Step 5), `printBoards` (Step 6), `removeWorktree` (Step 7) and the tests use. `namedOffice{name, *pipeline.Office}` is constructed identically in `offices()`, `twoOffices`, `boardOffices` and the worktree test. `RefuseLeftoverOfficeFile(configRoot string) error` is called with the same argument in `offices()` and `run-agent`.

---

Plan complete and saved to `docs/superpowers/plans/2026-09-17-config-cleanup.md`. Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, review between tasks (`superpowers:subagent-driven-development`).
2. **Inline Execution** — execute tasks in this session with checkpoints (`superpowers:executing-plans`).
