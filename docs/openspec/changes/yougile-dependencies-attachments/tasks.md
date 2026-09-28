## 1. Rebase and re-check delta framing

- [ ] 1.1 Rebase this branch onto master after `yougile-adapter-core` has
      archived; confirm the `apiData` lease sub-schema this change builds
      on is unchanged from what `design.md` assumed.
- [ ] 1.2 Re-check whether `specs/tracker-yougile/spec.md` should be
      re-expressed as `MODIFIED` against the now-real base spec (see
      design.md Context); adjust if needed before continuing.

## 2. Dependencies

- [ ] 2.1 Define the `depends_on` sub-object in `apiData` (task id
      reference), namespaced alongside the lease sub-object.
- [ ] 2.2 Implement `LinkDependsOn`.
- [ ] 2.3 Confirm `claim()` correctly consults `internal/pipeline.UnmetDependencies`
      using the recorded reference (no changes to `pipeline` expected —
      verify, don't reimplement).

## 3. Attachments

- [ ] 3.1 Define the attachment manifest sub-object in `apiData` (id→url),
      namespaced alongside lease and dependency sub-objects.
- [ ] 3.2 Implement `AddAttachment` (upload via `upload-file`, record the
      returned url in the manifest under a generated id).
- [ ] 3.3 Implement `GetAttachment` (resolve id via the manifest, GET the
      stored url).
- [ ] 3.4 Live-check that an `upload-file` URL fetched immediately after
      upload, and again after some delay, both return the same bytes
      (confirms or refutes the "URL stability" risk noted in design.md).

## 4. Interface completion and tests

- [ ] 4.1 Confirm `var _ tracker.Tracker = (*Tracker)(nil)` compiles.
- [ ] 4.2 Unit tests for `LinkDependsOn`/`AddAttachment`/`GetAttachment`
      against a fixture/fake HTTP transport.
- [ ] 4.3 Extend the gated live-API smoke tests from `yougile-adapter-core`
      to cover a dependency-blocked claim and an attachment round-trip.
