## ADDED Requirements

### Requirement: `depends_on` reflects a real merge-order dependency, not a preferred ordering
When `analyst` proposes `split.children[]`, it SHALL set `depends_on` from
one child to another only when the dependent child's work cannot begin
before code from the other child is merged (a shared schema, migration,
data model, or exported interface the dependent child's implementation
relies on) — not merely because one ordering reads more naturally than
another.

#### Scenario: A genuine interface dependency is recorded
- **WHEN** one proposed child's implementation needs a data model,
  migration, or interface that another proposed child creates
- **THEN** `analyst` sets `depends_on` from the dependent child to the one
  it needs

#### Scenario: A convenience ordering is not recorded as a dependency
- **WHEN** two proposed children do not need each other's merged code to
  begin, even though one would conventionally be built before the other
- **THEN** `analyst` leaves `depends_on` empty between them
