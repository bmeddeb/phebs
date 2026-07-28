# T21.14 neutral Change Workbench closure

This retained development-only fixture is one synthetic microservices
monorepo. `make dev` binds its cloneable Git bundle through the normal sync,
zoekt index, and provisional protobuf/Thrift extraction pipeline. The source
tree keeps declarations under `idl/` and implementations and callers under
`src/`.

The corpus contains:

- current, replacement, proposed, and retired contract examples for the add,
  modify, migrate/replace, and retire Workbench stories;
- protobuf and Thrift declarations with deliberately repeated operation names;
- one declaration-shaped exact caller, one name-only ambiguous caller, and an
  ambiguous unit-attribution mapping;
- a removed legacy implementation in Git history;
- no `index.scip`, plus a declared missing-history input;
- retained failed and stale coverage inputs used by the T21.14 acceptance
  harness; and
- explicit unsupported Kafka, Redis, document-store, SQL, and runtime planes.

`closure-states.json` describes test inputs, not observed production facts.
The acceptance harness projects those inputs through the existing shared
Workbench services. It does not install a production failure switch or a
second evidence engine.

The fixture and its output make no claim about runtime use, completeness,
migration completion, migration safety, retirement safety, or extraction
accuracy. External accuracy remains `NOT_ESTABLISHED`.

`cmd/author` deterministically creates the two-commit bundle from the reviewed
`repo/` tree. Normal tests and `make dev` verify and consume the committed
bundle; they do not re-author it.
