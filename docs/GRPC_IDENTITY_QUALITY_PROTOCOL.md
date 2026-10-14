# T58.3a Go gRPC identity quality protocol

Frozen before selection (Ben's decisions, 2026-10-14). The commit that adds
this file, together with `spike/t583/{oracle,export,score.py}`, is the seal.
A failed round is recorded as failed, never rescored.

## 1. Why this round exists

T58.1 passed on **site coverage** only. Its protocol (§1) says the emitted
operation and service identities were not scored and must never be cited as
identity accuracy. Its rules were also transcribed from the extractor, so the
pass is spec conformance. The served surface for this evidence,
`find_operation_consumers`, looks sites up **by operation identity**. Ben
chose to measure identity before any release (T58.3).

This is a new round, not a retry of T11.1, which is closed as
NOT_ESTABLISHED. T11.1 measured the typed `go/` spike as its candidate. Here
the candidate is the shipped syntactic extractor, and `go/types` is only the
oracle.

## 2. Claim

For Go code in a repository whose gRPC stubs are type-checkable from source,
the `grpc-consumer` extractor (`grpcgo` 1.2.0) emits resolved rows whose
identities are right:

- **`CALLS_OPERATION /pkg.Service/Method`**: the operation the call actually
  invokes;
- **`REGISTERS_GRPC_SERVICE pkg.Service`**: the service actually registered.

It also finds the gRPC call and registration sites the type checker resolves.

## 3. Oracle (`spike/t583/oracle`)

`go/packages` with `go/types`, tests included, loads the module at its locked
commit (`GOFLAGS=-mod=mod`, `GOWORK=off`). The oracle uses no file names,
generated-file markers or method-name matching.

| Item | How the oracle decides |
|---|---|
| Client-method identity | The constant `"/pkg.Service/Method"` string the method's own source body passes to an `Invoke` or `NewStream` call |
| Call through an interface method | Takes the identity of the concrete type in that interface's package that implements it, when there is exactly one; otherwise the site is `ambiguous` |
| Registration-function identity | The constant `ServiceName` of the `ServiceDesc` that its body passes to `RegisterService` |
| Call site | Any call expression whose callee `go/types` resolves to such a method or function, keyed by the call expression's byte span (grpcgo's span) |
| `external` site | A method whose signature is a gRPC client's (last parameter `...grpc.CallOption`) but whose package has no source in the checkout; its identity cannot be read |
| Unloaded or ill-typed files | Counted and listed, not scored |

## 4. Measures

The scorer is `spike/t583/score.py`.

- **Precision.** Over grpcgo's resolved rows in files the oracle loaded,
  excluding rows at `external` sites: the fraction where the oracle resolves
  the same span to the same kind and identity. A resolved row where the type
  checker sees no gRPC call is wrong.
- **Recall.** Over oracle `resolved` sites: the fraction where grpcgo emits a
  resolved row at that span with the same identity. grpcgo's abstentions
  (`UNRESOLVED_GRPC_*`) count as not found.
- **Reported, not thresholded:** `external` and `ambiguous` oracle sites,
  rows in unloaded files, ill-typed files, per-role figures, and
  concentration.

## 5. Thresholds (frozen; T13.1's deferred bar)

- precision ≥ 98% overall;
- recall ≥ 90% overall;
- precision ≥ 90% and recall ≥ 90% for every repository with at least 20
  scored rows (precision) or at least 20 resolved sites (recall).

Every disagreement is adjudicated by Claude, as named by Ben. The verdicts
are `phebs_wrong` or `oracle_wrong`, each with evidence; an `oracle_wrong`
item leaves the denominator. None may be `unsure`.

## 6. Population (run once, after this seal)

**Search.** Run `gh search code --limit 100` for each query, in order:

1. `"ClientConnInterface" --filename _grpc.pb.go`
2. `"FullMethodName" --filename _grpc.pb.go`
3. `"RegisterService" --filename _grpc.pb.go`
4. `"grpc.NewServer" --extension go`

Candidates are repositories in order of first appearance.

**Excluded:**
- T58.1's 13 population repositories;
- every repository in `spike/t472/corpus.lock.json`;
- grpc/grpc-go;
- hashicorp/go-plugin, used for this protocol's dry run;
- forks and archived repositories.

**Pinning.** Each candidate is pinned at default-branch HEAD (depth-1 clone).

**Eligibility, in candidate order.** A candidate is dropped, with the reason
recorded, when any of these holds:

- it has no root `go.mod`;
- its checkout exceeds 200 MiB or 5,000 `.go` files;
- the oracle does not finish within 15 minutes;
- the oracle loads under 80% of the tracked `.go` files;
- the oracle finds no resolved site.

**Floors.** Admit candidates until at least 10 repositories and at least 500
oracle-resolved sites are held, capped at 25 repositories. Below the floors
at the cap, the round is recorded as underpowered, never as a pass. The lock
is committed before grpcgo runs on any candidate.

## 7. Dry run (before this seal, on an excluded repository)

The tooling was exercised on hashicorp/go-plugin, a T58.1 repository, which
§6 excludes:

- **Precision 27/77** (35%). Examples are `s.Hash.Sum(nil)` and
  `c.config.Stderr.Write(line)`, labelled as `/proto.AddHelper/Sum` and
  `/proto.StreamerService/Write`, because those method names are unique in
  the stub index.
- **Recall 27/31.** grpcgo abstains, as ambiguous, on type-resolved calls
  such as `m.client.Put(…)`.

Every sampled oracle verdict was checked against the source and is right.
This shaped no rule or threshold, and grpcgo was not changed.
