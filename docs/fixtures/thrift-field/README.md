# T22.5 neutral Thrift field demo

This development-only fixture is a small synthetic Git repository containing
a committed root `index.scip`. It exercises the production
`scip-thrift-field` three-way join for the Thrift result success slot:

- the embedded `ThriftModule` satisfies `sha1(Raw) == SHA1`;
- Raw IDL, `Meta_Health_Result.Success`, and `wire.Field{ID: 0}` agree;
- the authored SCIP definition and read occurrence cover the exact
  `GetSuccess` identifier spans.

The index is an authored needle, not a completeness or accuracy measurement
and not output from a real indexer. Its symbol follows the production shape
independently checked by the T22.2 `scip-go` fixture. The authoring command is
retained at `cmd/author`; normal tests and an explicit specialized developer
invocation only verify and consume the committed bytes. T30.7 `make dev` does
not bind this fixture.

`t225-thrift-field-demo.bundle` is the cloneable, single-commit form used by
that specialized walkthrough. The retained author command recreates both the
deterministic index and a bundle advertising `HEAD` plus `refs/heads/main`,
avoiding host-Git default-branch inference. `receipt.json` pins the repository
commit, bundle and index bytes, question identity, and exact citation.
