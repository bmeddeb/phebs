# Claim card: phebs.protobuf.change-impact 1.0.0 (composed claim)

Owner: Ben Meddeb. Distribution: default-on in the release package
(PHEBS-41, 2026-11-25).

**Claim.** This claim admits the following surfaces only when two other
releases are both admitted:

- the released Buf WIRE verdict, `phebs.protobuf.wire.buf`;
- released protobuf field references, `phebs.protobuf.field.go`.

The surfaces are:

- proof-enriched compatibility:
  - `POST /api/check_contract_compatibility`;
  - MCP `check_contract_compatibility`;
  - capability `contract-compatibility`;
- Contract Impact reports for one field identity and for one proposed
  `.proto` change:
  - `GET` and `POST /api/contract_impact_report`;
  - saved reads at `/api/contract_impact_reports/{id}`;
  - capability `contract-impact-change`, and the Impact page's Field and
    Contract change tabs.

Each answer is composed of two parts only:

- the pinned Buf WIRE verdict over the caller's supplied before and after
  bytes;
- the released field-reference evidence for the affected field identities,
  in the caller's visible repositories.

Field reports also read released Thrift field references when a Thrift field
release is admitted.

**Inherited quality.** The verdict inherits the Buf WIRE claim's sealed T53.3
round 2 result. That result is no false-compatible verdict on real breaks,
conservative only in its two disclosed classes. The references inherit the
released field recipe's own result. This composition adds no extraction, no
new evidence domain and no new measurement.

**Non-claims.**

- No operation entry: operation questions stay on the separately released
  `phebs.contract.impact.operation`.
- No name-matching consumer evidence (`grpc-consumer`, `thrift-consumer`).
  Those stay experimental-only under the T58 NO-GO.
- No runtime, deployment or completeness claim.
- An absent reference is not proven absent.
- No accuracy claim beyond the two inherited claims.

**Withdrawal.** The claim is withdrawn, and its surfaces disappear, when any
of these hold:

- the claim, the wire claim or the protobuf field release is revoked,
  suspended or expired;
- the Buf checker did not start;
- a provisional extraction switch is on. That switch keeps the experimental
  proof surface instead.

Every saved report is re-admitted on read.

**Expiry.** 2027-01-10T23:59:59Z, the earliest expiry among the claims it
composes, or earlier revocation.

Current admission, withdrawal and boundary tests are in
`change-impact-validation-1.0.0.json`.
