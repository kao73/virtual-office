## ADDED Requirements

### Requirement: The YouGile connection file is required only when used
`${OFFICE_HOME}/tracker-yougile.yaml` SHALL be opened only when at least
one project declares `tracker: yougile`. When no project does, the file
SHALL neither be required nor listed among the configuration sources.
When it is opened, it SHALL describe exactly one YouGile project, whose key
matches the `tracker: yougile` project in `projects.local.yaml`, and the
runner SHALL refuse to start otherwise. The office's own YouGile account
(the owner of the API key) SHALL count as an agent account, not a human.

#### Scenario: A jira-and-mock-only machine has no YouGile file
- **WHEN** every project declares `tracker: mock` or `tracker: jira`, and
  `tracker-yougile.yaml` does not exist
- **THEN** the runner starts, and its configuration-source listing has no
  `tracker-yougile.yaml` line

#### Scenario: A yougile project without the file is refused
- **WHEN** some project declares `tracker: yougile` and
  `tracker-yougile.yaml` does not exist
- **THEN** the runner refuses to start and names the missing file

#### Scenario: All three trackers are served together without a flag
- **WHEN** `projects.local.yaml` declares projects with `tracker: mock`,
  `tracker: jira`, and `tracker: yougile` respectively, and both
  `tracker.yaml` and `tracker-yougile.yaml` exist
- **THEN** `runner tick` with no tracker flag serves all three offices in
  the same tick

#### Scenario: A second YouGile project is refused
- **WHEN** `projects.local.yaml` declares two projects with
  `tracker: yougile`, or `tracker-yougile.yaml` describes more than one
  project
- **THEN** the runner refuses to start and says that one YouGile project
  per runner is supported

#### Scenario: The ru. host is refused with a hint
- **WHEN** `tracker-yougile.yaml` has `base_url: https://ru.yougile.com`
- **THEN** the runner refuses to start, and the error says attachments
  would not download and names `https://yougile.com` as the value to use

#### Scenario: The office's own account is not a human
- **WHEN** a YouGile task's chat has a message written under the account
  whose API key the office uses
- **THEN** the runner does not treat that message as a human reply
