# T39.5 — Release, suspension, and continuation decision

T39.5 closes Epic 39 with `outcome: stop` and no release. T39.1 passes neutral
mechanics, and T39.3 passes its named security/lifecycle matrix, but neither
can promote the independently stopped gates: T39.2 stopped on a terminal
incremental pipeline failure, and T39.4 stopped before unsealing with every
quality/workflow measure unmeasured.

The validation decision is therefore `NOT_ESTABLISHED / DO_NOT_RELEASE`.
Every Protobuf/gRPC, Thrift, and Kafka pack remains `experimental_dark`; none
is eligible for shadow, advisory, or released status. Development opt-ins do
not change that decision.

The human continuation record remains separate and honest. The project
decision template permits a human continuation judgment only after an
`ESTABLISHED` validation record. That prerequisite is absent, so continuation
is `not_eligible`, no acting principal is invented, and no next action or
rerun is authorized. This is a first-class stop, not a missing approval
reinterpreted as assent.

The lifecycle record distinguishes never-released from suspended. Current
status is experimental-dark, so suspension does not apply now. It nevertheless
freezes the triggers that any future release must use, plus expiry, rollback,
and full revalidation requirements. Rollback cannot rewrite historical
evidence, and any future T39.2 rerun first requires T39.R1 and separate
authorization.

## Reproduction

```sh
t395_tmp=$(mktemp -d)
go run ./spike/t395/cmd/author -root . -out "$t395_tmp/results.json"
cmp "$t395_tmp/results.json" spike/t395/results.json
go test -race ./spike/t395
make docs-check
make verify-glossary
```

The author refuses to overwrite an existing receipt. The receipt is
source-free, changes no production behavior, and authorizes no “all runtime
callers,” accuracy/completeness, migration-complete, decommission-safe, SLO,
pilot-continuation, or release claim. `GATE2-V2` remains `NOT_ESTABLISHED`.
