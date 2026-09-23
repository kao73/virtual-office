---
comet_change: doctor
role: technical-design
canonical_spec: openspec
archived-with: 2026-09-23-doctor
status: final
---

# `runner doctor` — Technical Design

Capability spec: `docs/openspec/changes/doctor/specs/office-doctor/spec.md`.
Open-phase framework: `docs/openspec/changes/doctor/design.md`. This
document deepens that framework into implementation-level design; it does
not restate proposal.md's motivation or the spec's normative requirements.

## Loading sequence and failure isolation

The central technical decision this document adds beyond the open-phase
design: `doctor` does not call `cmd/runner/office.go`'s `newOffices`. That
function is the pipeline's real assembly point and is deliberately
fail-fast — "не открылся один трекер — не стартует ни один офис." Doctor's
entire purpose is the opposite contract: collect every finding in one
pass. Reusing `newOffices` would mean a broken `projects.local.yaml`
(the single most likely state right after `runner init`) produces exactly
one error and nothing else — the least useful possible output for exactly
the user this command is for.

`doctorCommand` therefore runs four independent stages in order, where a
stage's own failure is reported as one finding and skips only the stages
that structurally depend on it — never the stages that don't:

```go
type finding struct {
	check string // stable id, e.g. "tool:git", "config:tracker.yaml"
	level string // "ok" | "warn" | "fail"
	msg   string
}

func doctorCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	backend := fs.String("backend", runagent.DefaultBackend, "...")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var findings []finding
	report := func(f finding) { findings = append(findings, f) }

	// Stage 1 — always runs, no project data needed.
	report(checkTool("git"))
	report(checkTool("claude"))
	if *backend == "sbx" {
		report(checkTool("sbx"))
		report(checkSandboxNetwork()) // sbx.BasePolicy{}.Notice(), only if sbx tool found
	}
	report(checkStaleOfficeSnapshots()) // runner.ResolveOffice, SourcePayload only

	// Stage 2 — projects.local.yaml.
	home, err := runner.Home()
	if err != nil {
		report(finding{"config:home", "fail", err.Error()})
		return concludeExit(out, findings)
	}
	projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
	if err != nil {
		report(finding{"config:projects.local.yaml", "fail", err.Error()})
		report(finding{"skip:project-dependent", "warn",
			"tool(comet)/credential/JIRA checks skipped: projects.local.yaml did not load"})
		return concludeExit(out, findings)
	}

	for _, key := range projects.Keys() {
		if projects[key].Forge != "" {
			report(checkTool("comet"))
			break
		}
	}

	jiraProjects := projects.For("jira") // reuses Projects.For, no new method
	if len(jiraProjects) == 0 {
		return concludeExit(out, findings)
	}

	// Stage 3 — tracker.yaml, only reached when some project uses jira.
	cfg, err := jira.LoadConfig(filepath.Join(home, jira.TrackerFile))
	if err != nil {
		report(finding{"config:tracker.yaml", "fail", err.Error()})
		report(finding{"skip:jira", "warn",
			"credential/JIRA checks skipped: tracker.yaml did not load"})
		return concludeExit(out, findings)
	}

	// Stage 4 — credentials, then live JIRA checks (one instance, N projects).
	report(checkCredentials(cfg.Accounts)...)
	trk, err := jira.Open(cfg)
	if err != nil {
		report(finding{"jira:reachability", "fail", err.Error()})
		return concludeExit(out, findings)
	}
	report(checkAccount(trk), checkFields(trk), checkLinkType(trk))
	for _, key := range jiraProjects.Keys() {
		report(checkWorkflow(trk, key, cfg.StatusMap[workingStatusFor(key)]))
	}

	return concludeExit(out, findings)
}
```

This is illustrative, not final Go — exact signatures are decided during
implementation (tasks.md group 2), but the stage boundaries, what each
stage's failure skips, and that every stage before a failure has already
been appended to `findings` (and therefore will print) are the contract
this design commits to, and what the two new spec scenarios (config
isolation) test against.

`concludeExit` is one function: print every finding in `findings` order
(`ok`/`warn`/`fail` prefix + `check` + `msg`), count `level == "fail"`,
return `nil` when zero, `fmt.Errorf("doctor: %d check(s) failed", n)`
otherwise. Every one of the early `return concludeExit(...)` calls above
is a normal, expected exit for a partially-configured machine, not a
short-circuit around unrelated checks — by the time any of them runs,
every check that doesn't depend on the missing piece has already been
appended.

**Why not a generic "checks with dependencies" graph** (each check
declares what it needs, a small scheduler resolves order and skips
transitively): considered and rejected. The dependency shape here is a
fixed 4-level chain (tools/independent → projects → tracker.yaml →
JIRA-live), not a graph — `LoadConfig` is the only OpenSpec revealed by
proposal.md, config-cleanup already made `tracker.yaml` load only when
needed, per `runner-multi-tracker`. A scheduler would add indirection
proposal.md never asked for, for a shape simple enough to write out
directly and read top to bottom.

## `internal/tracker/jira` additions

```go
// FieldCheck — one configured customfield_* checked against the live
// instance's field schema.
type FieldCheck struct {
	Config       string // "lease_until", "run_id", "owner", "attempts"
	ID           string // the configured customfield_NNNNN
	Present      bool
	ExpectedType string
	ActualType   string // "" when Present is false
}

// expectedFieldTypes — kept next to Fields so a new field added there
// doesn't silently ship without an expected type here.
var expectedFieldTypes = map[string]string{
	"lease_until": "datetime",
	"run_id":      "string",
	"owner":       "string",
	"attempts":    "number",
}

func (t *Tracker) CheckFields() ([]FieldCheck, error) {
	var remote []struct {
		ID     string `json:"id"`
		Schema struct{ Type string `json:"type"` } `json:"schema"`
	}
	if err := t.call(http.MethodGet, "/field", nil, &remote); err != nil {
		return nil, err
	}
	byID := make(map[string]string, len(remote)) // id -> schema.type
	for _, f := range remote {
		byID[f.ID] = f.Schema.Type
	}
	configured := map[string]string{
		"lease_until": t.cfg.Fields.LeaseUntil, "run_id": t.cfg.Fields.RunID,
		"owner": t.cfg.Fields.Owner, "attempts": t.cfg.Fields.Attempts,
	}
	checks := make([]FieldCheck, 0, len(configured))
	for _, name := range []string{"lease_until", "run_id", "owner", "attempts"} { // stable order
		id := configured[name]
		actual, present := byID[id]
		checks = append(checks, FieldCheck{
			Config: name, ID: id, Present: present,
			ExpectedType: expectedFieldTypes[name], ActualType: actual,
		})
	}
	return checks, nil
}

func (t *Tracker) CheckLinkType() error {
	if t.cfg.DependsOnLink == "" {
		return nil // optional field, doc comment on DependsOnLink already says why
	}
	var types []struct{ Name string `json:"name"` }
	if err := t.call(http.MethodGet, "/issueLinkType", nil, &struct {
		IssueLinkTypes *[]struct{ Name string `json:"name"` } `json:"issueLinkTypes"`
	}{IssueLinkTypes: &types}); err != nil {
		return err
	}
	for _, lt := range types {
		if lt.Name == t.cfg.DependsOnLink {
			return nil
		}
	}
	return fmt.Errorf("issueLinkType %q (depends_on_link) not found on the instance", t.cfg.DependsOnLink)
}
```

Both reuse the existing unexported `t.call` — no new HTTP plumbing. The
`GET /issueLinkType` envelope (`{"issueLinkTypes": [...]}`) and `GET
/field`'s flat array are the documented JIRA REST v2 shapes; task 1.4
verifies them against the 8.13 polygon before this lands, the same way
`LinkDependsOn`'s doc comment already records one empirical correction
for this exact instance — if 8.13 disagrees, the doc comment here records
the correction and the code adjusts, not the other way around.

`t.call`'s existing signature takes `(method, path string, in, out any)
error`; both new methods fit it without changes.

## `internal/runner` — stale snapshot listing

No new exported symbol needed on `runner.Office`/`ResolveOffice` — the
listing is a small helper local to `cmd/runner/doctor.go`:

```go
func checkStaleOfficeSnapshots() finding {
	o, err := runner.ResolveOffice(runner.Resolve{})
	if err != nil || o.Source != runner.SourcePayload {
		return finding{"office:stale-snapshots", "ok", "not applicable (dev/clone mode)"}
	}
	officeDir := filepath.Dir(o.Root) // ${OFFICE_HOME}/office
	current := filepath.Base(o.Root)
	entries, err := os.ReadDir(officeDir)
	if err != nil {
		return finding{"office:stale-snapshots", "fail", err.Error()}
	}
	var n int
	var size int64
	for _, e := range entries {
		if !e.IsDir() || e.Name() == current {
			continue
		}
		n++
		size += dirSize(filepath.Join(officeDir, e.Name())) // filepath.WalkDir sum
	}
	if n == 0 {
		return finding{"office:stale-snapshots", "ok", "none"}
	}
	return finding{"office:stale-snapshots", "warn",
		fmt.Sprintf("%d stale snapshot(s), %s — remove by hand: rm -r %s/<version>", n, humanSize(size), officeDir)}
}
```

Read-only: `os.ReadDir` + `filepath.WalkDir` for sizing, nothing else.
Matches design.md's decision that `office prune` (deletion) is a
separate, not-yet-built command — the message above says the manual
command, not a flag this change doesn't implement.

## Credential presence

```go
func checkCredentials(a jira.Accounts) []finding {
	seen := map[jira.Account]bool{}
	var out []finding
	check := func(label string, acc jira.Account) {
		if seen[acc] {
			return
		}
		seen[acc] = true
		for _, v := range []string{acc.UserEnv, acc.SecretEnv} {
			if _, ok := os.LookupEnv(v); !ok {
				out = append(out, finding{"cred:" + v, "fail", "not set"})
			} else {
				out = append(out, finding{"cred:" + v, "ok", "set"})
			}
		}
	}
	check("default", a.Default)
	for _, role := range slices.Sorted(maps.Keys(a.Roles)) {
		check(role, a.Roles[role])
	}
	return out
}
```

`jira.Account` is already a plain `{UserEnv, SecretEnv string}` — comparable,
so the `seen` dedup works without a custom key. Deliberately not reusing
`Config.AgentAccounts()` (open-phase design.md already recorded why: that
method resolves *identities* for comment-attribution and only checks
`UserEnv`, not `SecretEnv`, and its purpose — "who counts as an agent" —
is not "is this credential present").

## Testing strategy

- **`internal/tracker/jira`**: table tests for `CheckFields` (right type,
  wrong type, absent) and `CheckLinkType` (present, absent, empty
  `DependsOnLink`) against the package's existing fake HTTP server
  pattern (same style as existing `jira_test.go` tests for `CheckAccount`
  etc.) — no network.
- **`cmd/runner/doctor_test.go`**: stage-by-stage, with fakes for
  `exec.LookPath` (via a `var lookPath = exec.LookPath` seam, same pattern
  `cometExecutable` already uses in `archive.go`), a temp `${OFFICE_HOME}`
  per test, and a fake `*jira.Tracker`-shaped dependency injected the way
  the stage-4 functions are written to accept an interface covering just
  `CheckAccount`/`CheckFields`/`CheckLinkType`/`CheckWorkflow` — not the
  full `tracker.Tracker` interface, and not a live HTTP server, since
  those are already covered at the `jira` package level.
- Four isolation scenarios get their own test functions, matching the two
  new spec scenarios plus the two already-written ones: no
  `projects.local.yaml`; `projects.local.yaml` loads, no `tracker: jira`
  project (JIRA block silently absent, not an error); `tracker.yaml`
  missing/malformed; full success path with a mock-shaped fake tracker.
- `--backend local` vs `--backend sbx` covered by asserting which finding
  ids appear — `tool:sbx` and `sbx:network` must be entirely absent from
  `local`'s output, not merely `ok`.
- Task 3.4 (live polygon run) stays manual/scripted per tasks.md; this
  design doesn't add new machinery for it beyond what `install`'s
  verification already used.

## Risks / Trade-offs

- [Risk] `GET /field` on a large instance returns every field, including
  ones no config references — fine for a preflight command (not a hot
  path), but confirms this must never be called more than once per
  `doctor` invocation, matching the design above (Stage 4, once per
  instance, not once per project).
- [Risk] `checkCredentials`' dedup on `jira.Account` equality means two
  roles sharing the same env-var pair are reported once, not once per
  role name — intentional (reporting "ROLE_TOKEN not set" three times for
  three roles that all read the same variable would look like three bugs
  instead of one), but worth calling out since it's a deliberate
  departure from "one line per configured account."
- [Trade-off] Duplicating `projects.local.yaml`/`tracker.yaml` loading
  instead of reusing `newOffices` — accepted, see "Loading sequence"
  above; the alternative fails the primary scenario this change exists
  for.

## Migration Plan

No migration. Same as open-phase design.md: new subcommand only, no
existing behavior changes, `office prune` deletion stays a deliberately
separate future change.
