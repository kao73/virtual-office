## ADDED Requirements

### Requirement: The YouGile connection file is required only when used
`${OFFICE_HOME}/tracker-yougile.yaml` SHALL be opened only when at least
one project declares `tracker: yougile`. When no project does, the file
SHALL neither be required nor listed among the configuration sources.

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
