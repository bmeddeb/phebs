# T11.1 Gate 2 strict benchmark expansion

This protocol is fixed after the Attempt 4 capacity stop and before cloning,
enumerating, or running the extractor over any candidate expansion repository.
It changes only the Gate 2 benchmark population. Gate 3 and Gate 4 retain their
existing locked fixtures, estimands, thresholds, and scorers.

## Objective and unchanged floor

The scored attempt still requires at least 30% of selected unique sites to be
blind. Expansion targets a conservative 35% seed-independent lower bound so a
small amount of duplicate-code burn or eligibility loss cannot put a ceremony
on the threshold. With the current 5,745-site permanent census, 3,094 genuinely
fresh sites is only the non-decisional algebraic minimum for 35%, assuming no
new carried burn and no added development-site ceiling. The four fresh sites
already present reduce that best-case incremental number to 3,090. Prefix
acceptance instead uses the complete preflight formula after burn projection;
its denominator includes the configured development unique-site ceiling.

No historical burn may be removed, no census site may be dropped, and neither
the 30% rule nor any accuracy/confidence threshold may be weakened.

## Candidate order

Candidate order was selected from project-level public metadata only, without a
candidate checkout, extractor run, fact count, coordinate enumeration, or label
outcome. The eligible repositories are evaluated as this immutable prefix:

1. `grpc/grpc-go`
2. `etcd-io/etcd`
3. `containerd/containerd`
4. `istio/istio`
5. `vitessio/vitess`
6. `grafana/mimir`
7. `cockroachdb/cockroach`

The order starts with the canonical Go gRPC implementation, then adds unrelated
production systems with increasing repository/module complexity. It cannot be
reordered after a checkout or because extractor performance, site capacity, or
labels are unfavorable.

## Eligibility and pinning

All candidates use their official repository's default-branch head from one
GraphQL ref snapshot requested at the prospectively fixed cutoff
`2026-07-15T04:20:00Z`. No later head may be substituted. A candidate is
intrinsically eligible only when all of these immutable source properties hold:

- the pinned tree contains Go plus protobuf/gRPC-relevant source;
- every required tracked-tree Git object is available from the official repo;
- a gitlink is excluded only when it contains no Gate 2 Go/proto candidate and
  its exact path and object ID are committed before fact generation;
- corpus enumeration can cover the relevant tracked source without executing
  repository code, hooks, generators, or build scripts.

An intrinsic eligibility failure, with evidence, is committed before evaluation
moves to the next candidate. Network/transient errors, incomplete or
nondeterministic fact production, and typed-oracle coverage failures are harness
blockers: work stops on that same candidate until a prospective fix and its
tests are committed. They cannot be reclassified as eligibility failures. Low
capacity, duplicate-code burns, or unfavorable extractor output likewise cannot
justify skipping a candidate.

For every eligible candidate, fact production runs with network access disabled,
must be complete, and must produce byte-identical output twice under the exact
producer-bound toolchain. The typed oracle must cover every emitted fact kind
admitted by Gate 2.

## Cross-fixture burn carry-forward

Historical exposure follows source content, not a fixture name. Every exposed
interval is resolved to its Git blob object ID in the historical source tree.
If that identical blob appears at any path in any current fixture, the interval
is carried into the permanent census at every occurrence; multiple identical
paths are all burned. This is additive to the existing same-fixture rules for
relocated blobs, exact unique line-span translation, and whole-path quarantine
when changed content cannot be translated uniquely. The binding records the
historical coordinate, source and destination systems/commits/paths, blob ID,
interval, and resolution status. Missing required historical source or changed
content that cannot be safely represented by those existing rules fails closed.
This rule is schema- and digest-bound before an expanded attempt can be claimed.

## Prefix stop rule

Eligible candidates accumulate in order. At least two new fixtures are required
for repository diversity. Selection stops at the first eligible prefix whose
fully projected, seed-independent population both:

1. satisfies every existing Gate 2 power rule; and
2. has a conservative blind-fraction lower bound of at least 35%.

The prefix, source lineage, cross-fixture burn projection, generalized harness,
and reusable tests must be committed before any attempt claim, public input
commitment, external randomness, sampled coordinate, reviewer artifact, or
label is created. A write-suppressed, coordinate-free preflight must then prove
attainability. If the complete ordered list cannot do so, Gate 2 remains **NOT
ESTABLISHED**; the floor and thresholds are not lowered.
