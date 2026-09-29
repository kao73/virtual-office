## 1. Rebase and re-check delta framing

- [x] 1.1 Rebase this branch onto master after `yougile-adapter-core` has
      archived; confirm the `apiData` lease sub-schema this change builds
      on is unchanged from what `design.md` assumed.
      _Done: branch sits on master `69c5b89` (after `yougile-adapter-core` archived); the lease schema is the one this change namespaces (Design Doc §2)._
- [x] 1.2 Re-check whether `specs/tracker-yougile/spec.md` should be
      re-expressed as `MODIFIED` against the now-real base spec (see
      design.md Context); adjust if needed before continuing.
      _Done: stays `ADDED` — the new requirements add behavior and change no existing text (Design Doc §7)._

## 2. Dependencies

- [x] 2.1 Define the `depends_on` sub-object in `apiData` (task id
      reference), namespaced alongside the lease sub-object.
      _Reframed (Design Doc §2–3): `depends_on` is an array of task ids inside the single `apiData.virtual_office` object._
- [x] 2.2 Implement `LinkDependsOn`.
- [x] 2.3 Confirm `claim()` correctly consults `internal/pipeline.UnmetDependencies`
      using the recorded reference (no changes to `pipeline` expected —
      verify, don't reimplement).

## 3. Attachments

- [x] 3.1 Define the attachment manifest sub-object in `apiData` (id→url),
      namespaced alongside lease and dependency sub-objects.
      _Reframed (Design Doc §7): no manifest. Attachments are discovered from chat file messages and description links (§4.1)._
- [ ] 3.2 Implement `AddAttachment` (upload via `upload-file`, record the
      returned url in the manifest under a generated id).
- [ ] 3.3 Implement `GetAttachment` (resolve id via the manifest, GET the
      stored url).
- [ ] 3.4 Live-check that an `upload-file` URL fetched immediately after
      upload, and again after some delay, both return the same bytes
      (confirms or refutes the "URL stability" risk noted in design.md).
      _Reframed (Design Doc §1, §7): stability already confirmed live (identical bytes immediately and after 20 s); the remaining check is the live byte round-trip._

## 4. Interface completion and tests

- [ ] 4.1 Confirm `var _ tracker.Tracker = (*Tracker)(nil)` compiles.
- [ ] 4.2 Unit tests for `LinkDependsOn`/`AddAttachment`/`GetAttachment`
      against a fixture/fake HTTP transport.
- [ ] 4.3 Extend the gated live-API smoke tests from `yougile-adapter-core`
      to cover a dependency-blocked claim and an attachment round-trip.
