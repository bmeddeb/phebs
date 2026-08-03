# T21.14 neutral Change Workbench closure

This retained development-only fixture is one synthetic microservices
monorepo. Tests and an explicit historical-fixture invocation can bind its
cloneable Git bundle through the normal sync, zoekt index, and provisional
protobuf/Thrift extraction pipeline. T30.7 `make dev` uses the separate neutral
focused-service cohort instead. The source tree keeps declarations under
`idl/` and implementations and callers under `src/`.

The corpus contains:

- current, replacement, proposed, and retired contract examples for the add,
  modify, migrate/replace, and retire Workbench stories;
- protobuf and Thrift declarations with deliberately repeated operation names;
- minimal generated gRPC and Thrift client/server shapes plus a reviewed
  generated-from snapshot, allowing the normal pure readers to reproduce
  declaration-lineage-resolved caller and registration facts;
- one separate name-only ambiguous caller and an ambiguous unit-attribution
  mapping;
- a removed legacy implementation in Git history;
- no `index.scip`, plus a declared missing-history input;
- retained failed and stale coverage inputs used by the T21.14 acceptance
  harness; and
- explicit unsupported Kafka, Redis, document-store, SQL, and runtime planes.

Focused acceptance mirrors the committed bundle and runs the normal
declaration, consumer, and caller pure readers, requiring the protobuf and
Thrift caller lineages to join their retained IDL declarations.
`closure-states.json` separately describes composition-test inputs, not
observed extraction facts. The Workbench acceptance harness projects those
failed/stale/unsupported inputs through the existing shared services. It does
not install a production failure switch or a second evidence engine.

The fixture and its output make no claim about runtime use, completeness,
migration completion, migration safety, retirement safety, or extraction
accuracy. External accuracy remains `NOT_ESTABLISHED`.

`cmd/author` deterministically creates the two-commit bundle from the reviewed
`repo/` tree. The bundle advertises both `HEAD` and `refs/heads/main`, so bare
mirrors resolve the same default branch across supported Apple and Linux Git
versions. Normal tests verify and consume the committed bundle; they do not
re-author it.
