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
GraphQL ref snapshot requested by an automatic timer at the prospectively fixed
final cutoff `2026-07-15T04:40:00Z`. The exact query is committed as
`expansion-source-snapshot.graphql`; no later head or query may be substituted.
A candidate is intrinsically eligible only when all of these immutable source
properties hold:

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

### First snapshot failure

The originally committed `2026-07-15T04:20:00Z` snapshot did not establish a
lineage. The first request, launched at `2026-07-15T04:20:24Z`, contained
literal escaped newlines and GitHub rejected it during GraphQL parsing without
returning repository data. A corrected diagnostic request completed at
`2026-07-15T04:20:43Z`, outside the fixed boundary; its response is discarded
and cannot pin a source. Before any checkout, fact enumeration, or artifact,
the then-replacement cutoff `2026-07-15T04:30:00Z` was fixed prospectively
without changing candidate order, eligibility, thresholds, or prefix rules.
Only a valid response from that cutoff could have supplied expansion pins.

The manual scheduler then missed the superseded `2026-07-15T04:30:00Z`
boundary; no GraphQL request was launched and no response or repository data
was received for that cutoff. The final cutoff above and its exact tracked query
are committed in advance and launched by a timer. If that single request fails,
expansion stops rather than selecting another time.

### Final source snapshot

The timer launched the tracked query once at
`2026-07-15T04:40:00.099108000Z`; GitHub returned success at
`2026-07-15T04:40:01.241757000Z`. The raw response and derived receipt are
committed beside this protocol. In fixed order, the pins are:

1. `grpc/grpc-go` — `f8a85fa4d1dec72ace513a97ff27c60252de7c4d`
2. `etcd-io/etcd` — `6006f405800929b5e7e839e7a821d608a311579f`
3. `containerd/containerd` — `9e70782d9a0e92900f402b2c7a4e2aa30754503c`
4. `istio/istio` — `25f4803ee1e64fc2fcb95d07b1c0e3353594e9a9`
5. `vitessio/vitess` — `379c27ed8475d3b42419abc8f767338f64706db7`
6. `grafana/mimir` — `596d354150b912a86fbe77e51ff78b6861609c21`
7. `cockroachdb/cockroach` — `462ccb10dcae3e4ccbe27e8362b19ca831f8a3a1`

The response digest is
`sha256:d3ac1730d94917b03501792bbfef0c8b6699bc77c035f0f4552b14f883345d51`.
Only these commits may be evaluated, and only the mandatory ordered prefix may
enter the Gate 2 corpus.

### Mandatory-prefix eligibility

The first two pinned trees synchronized completely and cleanly through the
hardened corpus reader. Both `HEAD` values equal the receipt pins and neither
tree contains a gitlink. Source-only inspection, before any extractor run,
found:

- `grpc-go`: 1,042 tracked Go files, 13 tracked proto files, and eight proto
  files containing RPC declarations; and
- `etcd`: 1,106 tracked Go files, 12 tracked proto files, and three proto files
  containing RPC declarations.

Both therefore satisfy the immutable source eligibility rules. No exclusion is
needed, and the mandatory two-repository prefix cannot be skipped. These counts
describe tracked source shape only; no fact population, coordinate, or outcome
has been enumerated.

### Expanded lineage and burn projection

Before fact production, `expansion-lineage.json` fixes the six-system ordered
lineage as
`sha256:052ab651cf1346d97cf619387a39299e342bef08a2b827923408612b71c0f280`
and binds the committed harness, corpus lock, and append-only burn ledger.
Cross-fixture carry-forward produces 6,109 active intervals with digest
`sha256:1c3bfd7931e729a447aa0cc05eab059812fbb96696457513d51fca491f082d8b`.
Of those, nine occur in `grpc-go` and five in `etcd`; they are census, not
fresh capacity. The 9,819 deterministic resolution records are bound by
`sha256:55f78eb2f4269d459f71cfbd165f61821dd955fcdfed3d09d0abff4a88de17b9`.

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
