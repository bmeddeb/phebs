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

## 8. Result (2026-10-14): fails, and underpowered

**Population.** The four searches returned candidates in order. Of 55
examined, 25 were eligible: the cap was reached holding **418**
oracle-resolved sites, below the 500 floor, so the round is underpowered and
cannot pass in any case. The lock is `spike/t583/corpus.lock.json`
(`bde54469`), committed before grpcgo ran. The oracle also found 703
`external` sites (stubs in other modules) and no `ambiguous` site.

| Measure | Result | Threshold |
|---|---|---|
| Precision | **22.3%** (375 / 1,685) | ≥ 98% — fails |
| Recall | **89.7%** (375 / 418) | ≥ 90% — fails |
| Calls only | precision 20.5% (337 / 1,647), recall 89.4% (337 / 377) | — |
| Registrations only | precision 100% (38 / 38), recall 92.7% (38 / 41) | — |

Every repository with at least 20 precision rows fails precision, except
`weeaa/jito-go` at 18/18. That repository has fewer than 20 rows, so it does
not qualify.

**Adjudication** (`spike/t583/round/adjudication.jsonl`, Claude as named by
Ben; every item is `phebs_wrong`, and no oracle verdict was overturned):

- **1,115 wrong rows: name-matched non-gRPC calls.** go/types resolves each
  callee to an ordinary method. Examples:
  - `r.URL.Query().Get(…)` labelled `/logrotate.api/Get`;
  - `fileStartTime.Add(…)` labelled a crontab operation;
  - generated `ent` predicates labelled `/pb.Agent/Gauge`.

  grpcgo's method index assigns any call whose method name is unique across
  the repository's stubs, so one common name (`Get`, `Add`) in one stub
  captures every call of that name in the repository.
- **195 wrong rows: grpc-gateway in-process handlers.** These calls in
  `*.pb.gw.go` invoke the server implementation directly. They are disclosed
  as borderline; crediting all of them gives 33.8% precision, so they cannot
  change the outcome.
- **33 misses: honest abstentions.** grpcgo abstained as ambiguous where
  go/types resolves uniquely.
- **10 misses: no row at all.** Proto method names that differ from the Go
  name (`insert` and `Insert`, `dry_run` and `DryRun`) are among the causes.
  grpcgo builds the wire identity from the Go method name, so such an
  operation's identity is wrong even when the name does match.

**What this establishes.** grpcgo's call identities are not fit for
lookup-by-operation. `find_operation_consumers` over `grpc-consumer` must not
be released on this extractor. T58.1's site-coverage pass stands, because it
measured something else. Registrations look sound (38/38), but 41 sites in
an underpowered round establish nothing.

## 9. Round 2: registrations only (frozen 2026-10-14, before selection)

Ben chose a registrations-only claim after §8. Calls stay dark, and
lookup-by-operation remains Caller Map's job (Epic 47).

**Claim.** `REGISTERS_GRPC_SERVICE pkg.Service` names the service actually
registered, and grpcgo finds the registrations go/types resolves.

**Unchanged.** The oracle and its rules (§3), the adjudication (§5), and the
thresholds (precision ≥ 98%, recall ≥ 90%, ≥ 90% per repository with at
least 20 scored rows or sites). Scoring is `score.py --kind registration`:
the same rules, restricted to registration rows and sites.

**Population.** §6's searches run again, with the same exclusions plus §8's
25 repositories. Eligibility is as in §6, but the site that counts is an
oracle-resolved **registration**. Admit candidates until at least 10
repositories and at least 150 resolved registration sites are held, capped
at 60 repositories; below the floors at the cap, the round is underpowered.
The selector is `spike/t583/select_r2.py`, and the lock is committed before
grpcgo runs.

## 10. Round 2 result (2026-10-14): fails

**Population.** Of 122 candidates examined, 34 were eligible: the floor of at
least 10 repositories and at least 150 sites was met with **153** resolved
registration sites. The lock is `spike/t583/round2/corpus.lock.json`
(`7071aa7a`), committed before grpcgo ran.

| Measure | Result | Threshold |
|---|---|---|
| Precision | **97.67%** (126 / 129) | ≥ 98% — fails, by 3 rows |
| Recall | **82.35%** (126 / 153) | ≥ 90% — fails |

The only qualifying repository, `kralicky/totem`, scores 100% / 100%.

**Adjudication** (`spike/t583/round2/adjudication.jsonl`): all 30
disagreements are `phebs_wrong` under the frozen rules, and no oracle
verdict was overturned.

- **3 wrong rows: false claims.**
  `btcwalletrpc.RegisterWalletServiceServer(…)` registers an external-module
  service, which grpcgo name-matched to the repository's own
  `wavewalletrpc.WalletService`.
- **22 misses: honest abstentions.** The `Register…Server` name occurs in two
  or more stubs: duplicate stub copies, or per-package cosmos-style
  `RegisterMsgServer`. go/types resolves each one uniquely.
- **3 misses: silent, combined-style `*.pb.go` stubs**, T58.1's declared
  blind spot.
- **2 misses: silent, a repository's own registering wrapper**, which is
  outside grpcgo's shapes.

**What this establishes.** grpcgo's registrations are mostly right when
emitted: 126 of 129, and the 3 errors are external-module name collisions.
But name-based resolution abstains or is silent on 27 of 153 real
registrations. Neither a registrations-only release nor any claim of finding
registrations is supported on this extractor. Both rounds point the same
way: identity needs type information that the pure-reader extractor does
not have.
