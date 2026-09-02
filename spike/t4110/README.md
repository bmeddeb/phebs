# T41.10 — Neutral 10,000-service closure receipt

> **Retained engineering contract.** This package defines the source-free
> receipt and authoring seam for T41.10. It does not yet contain or claim a
> passing live runner result.

The receipt binds the exact retained T41.1 envelope and receipt plus the
canonical accepted-10,000 target and small transition-profile digests. The
live gate consumes those corpora through `t411.BuildTargetCorpus` and
`t411.BuildTransitionCorpus`; it does not copy the T41.1 service, membership,
placement, fixture, or transition generators.
The target file is encoded for the strict expanded v3 decoder; it must not pass
through the legacy v2 canonicalizer or validator, whose 4,000-service ceiling
continues to govern v2 only. Mapped transition successors use the same v3
validation boundary.

The frozen target contains no protobuf source and therefore has a truthful
empty relationship distribution. The live gate derives an exact proto-only
observation inventory, candidate manifest, and empty resolver catalog from
that repository and commit, then drives the production `ReconcileV3` path,
claims its durable memory chunk, runs `HandleV3`, and completes the chunk. It
requires the selected root to contain every live service and exactly zero
projections or references. The retained T41.1 mixed and dense distributions
remain separately composed `BuildV3`/`PublishV3` evidence; they are not
relabeled as target-backed production-runtime evidence.

`measured_phases` is the fixed inventory of work measured directly by the
live runner. Each phase records wall time, the highest 50-ms sampled aggregate
RSS of the author process tree, absolute logical and allocated custody footprint,
exact selected-state `ReadCache` root/member reads and their committed
root/member validations, state-chunk transactions, and state-row counts,
physical source work, production-reader queries, and sparse-preimage work.
`physical_work.corpus_passes` is frozen at exactly one and counts the sole
content-bearing search/index pass.
The cold relationship setup additionally performs production repository
metadata censuses for the proto-only observation and candidate authorities;
because the frozen target has no `.proto` file, those authorities select zero
fixture blobs. The receipt does not misstate those metadata censuses as
mixed/dense relationship work. Cold cost records the repository-source census
records/members/placements/declared bytes, observation input members and
immutable inventory receipt, candidate result members/records/declared bytes,
claimed relationship chunks, component-root publications, and relationship
publication members/records. The production candidate operation receipt does
not expose a tree-entry input-read count, so the cold cost explicitly records
`candidate_input_reads_unavailable=true` instead of estimating one. Every later
phase must keep the source, observation, candidate, and component-publication
counters at zero; only catalog-changing phases may record the exact claimed
relationship chunks and empty relationship publications they require.
The validator is closed by phase: state, relationship, lifecycle, preimage,
archive, browser, and product counters are accepted only where the live call
graph emits them. Product-query counts are exactly
`0/1/10001/4/103/8/8/32/2/1/9`; sparse preimage writes, collections, and
relationship service-record totals are likewise frozen to the eleven-phase
sequence. State rows cannot exceed the recorded 512-row transactions, cache
reads cannot exceed the target-plus-page bound, and lifecycle deletion/byte
cost retains its production turn and artifact-family ceilings. Each phase is
also refused above 24 hours or 9 TiB for any one RSS/logical/allocated gauge.
`archive_restore` separately records the six-artifact inventory count and
summed artifact bytes returned by `recovery.Create`, then the same inventory
volume returned by `recovery.Restore`. The two returned manifests, counts, and
byte totals must agree exactly; individual artifacts retain the recovery
package's 1 TiB bound except for the 4 TiB caller archive, for a closed 9 TiB
aggregate ceiling. These counters describe manifest inventory/end-footprint
volume, not operation reads, writes, or an estimate of filesystem I/O.
The cache fields are the smallest exact counters exposed by `ReadCache`: they
record validation work for roots and members and remain equal to the successful
miss-side reads. The runner does not invent a cache-hit count.
Fault-only, authorization, and transport regressions are not relabeled as measurements:
`composed_gates` retains the exact closed Go/Vitest identities that must pass
beside the live run.

The receipt is pass-only. Every phase, composed gate, and acceptance check must
appear once in frozen order and pass. The target must publish and expose 10,000
accepted services, match 10,000 independent queries exactly, exercise
target-backed authorized HTTP/MCP reads plus three live reads through the unchanged
production UI and the closed UI regressions, bound the cold corpus work, perform zero
Git/source reads in service-only work, and complete clean teardown.
Before navigation the browser context aborts every request outside the exact
loopback gate origin; the canonical report requires zero external requests, so
the bearer header cannot accompany a successful cross-origin request.

## Source-free boundary

Retained bytes contain aggregate counts, costs, environment class, the Phebs
implementation commit, and frozen neutral-input digests. They contain no
fixture repository or commit identity, source path or bytes, service key,
query text, result row, object ID, credential, host/user identity, raw error,
or log. The decoder rejects unknown fields, multiple JSON values, duplicate or
otherwise noncanonical encodings, forbidden source-bearing fragments, and any
receipt above 128 KiB.

The receipt is unauthenticated self-attestation, not a signature. Its direct
Git, Go, Node, npm, browser, SurrealDB, Phebs, author, and zoekt executable hashes do not
attest every transitive OS or package-cache byte; independent review must
recheck the exact clean commit, lock/checksum files, and admitted tools. The
composed source tree is reconstructed from exact HEAD blob IDs, so ignored
local files and checkout attributes cannot enter it. Its private Git metadata
fetches only that commit with depth one; ancestors, tags, and unrelated refs do
not enter composed custody.

The final `RunAndAuthor` step validates and canonicalizes the completed receipt, writes a synced
mode-0600 temporary file, then atomically links the destination without
replacing an existing path. A retained result is therefore created once; a
rerun must use a new destination rather than mutating evidence. If opening,
syncing, or closing the parent directory fails after the link, the author
removes that just-linked destination and joins any close/removal failure into
the returned error instead of reporting failure while silently retaining a
new receipt.

## Focused model gate

```sh
go test ./spike/t411 ./spike/t4110 -count=1
```

The cheap disposable-store transition smoke is opt-in and authors no receipt:

```sh
PHEBS_T4110_SMOKE=1 go test ./spike/t4110 -run '^TestT4110TransitionSmoke$' -count=1
```

It traverses the three frozen T41.1 transition catalogs through the public v3
candidate, state-plan, activation, and product-reader seams, checks the re-add
incarnation, closes the supervised local store, and removes its temporary
custody. It does not exercise or claim the 10,000-service target.

The full author command requires an exact clean-HEAD Phebs binary. Its Go build
metadata must name the same full `HEAD` revision with `vcs.modified=false`;
version text alone is not accepted. Closed builds accept only absent or `off`
`GOFIPS140` metadata and the frozen default setting for the current
architecture; `GOFIPS140=latest` is rejected. `measured_on` is captured from
the actual UTC wall date when the live draft begins, and the public author CLI
exposes no date override. The destination must not exist:

```sh
T4110_GO="$(command -v go)"
T4110_PATH="$(dirname "$T4110_GO"):$(dirname "$(command -v node)"):$(dirname "$(command -v npm)"):$(dirname "$(command -v surreal)"):/usr/bin:/bin"
T4110_BROWSER="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
env -i HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" PATH="$T4110_PATH" LANG=C LC_ALL=C TZ=UTC \
  CGO_ENABLED=0 GOENV=off GOEXPERIMENT= GOFIPS140=off GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOTELEMETRY=off \
  "$T4110_GO" build -trimpath -pgo=off -o /tmp/phebs-t4110-phebs ./cmd/phebs
env -i HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" PATH="$T4110_PATH" LANG=C LC_ALL=C TZ=UTC \
  CGO_ENABLED=0 GOENV=off GOEXPERIMENT= GOFIPS140=off GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOTELEMETRY=off \
  "$T4110_GO" build -trimpath -pgo=off -o /tmp/phebs-t4110-author ./spike/t4110/cmd/author
env -i HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" PATH="$T4110_PATH" LANG=C LC_ALL=C TZ=UTC \
  /tmp/phebs-t4110-author \
  -phebs-binary /tmp/phebs-t4110-phebs \
  -zoekt-binary ./bin/zoekt-git-index \
  -browser-binary "$T4110_BROWSER" \
  -out /absolute/new/results.json
```

The command checks clean `HEAD` before execution and again after the live and
composed gates, tears down the supervised store and all temporary custody, then
uses the create-only `Author` seam. No full 10,000-service run is part of the
ordinary test suite. SIGINT, SIGTERM, and SIGHUP trigger session teardown;
SIGKILL cannot, so an interrupted operator must verify process and temporary
custody are absent before retrying.

## PASS evidence map

`ValidateReceipt` carries the same closed mapping below. A check can pass only
when every referenced live phase, composed test, and receipt oracle has already
passed validation.

| Check | Evidence |
| --- | --- |
| `accepted_target_10000` | live `cold_publish_activate`, live `point_page_queries`, population/queryability oracles |
| `accepted_floor_8000_explicit` | frozen population oracle |
| `independent_queryability` | live `point_page_queries`, exact queryability oracle |
| `transition_profile` | live `transition_profile`; T41.1 transition-semantics test |
| `exact_bound_and_one_over` | T41.1 model test plus production v3 exact/one-over service, membership, path, logical/publication byte-admission, successor, member, and relationship-bucket boundaries |
| `cold` | live `cold_publish_activate` |
| `warm_noop` | live `warm_noop` |
| `point_and_page` | live `point_page_queries` |
| `one_service_delta` | live `one_service_delta` |
| `percent_delta` | live `percent_delta` |
| `removal_readd` | live `removal_readd` |
| `a_b_a` | live `a_b_a` |
| `partial_activation` | live first-chunk fence in `one_service_delta`; exact store partial-activation test |
| `crash_recovery` | exact store crash-replay test |
| `stale_worker` | exact generation-schedule retry/stale-fence test |
| `authorization` | exact authorization-before-authority-read API test |
| `pressure` | live target `CatalogV3GenerationOwner` through production `Controller` + `Run` under a deterministic 80% `collect` to 70% `normal` probe; exact preimage drain; one collected observation and every normal recovery turn in the receipt; owner-specific composed join test |
| `backup_restore` | live target relationship collection through the production relationship-v3 owner, then `archive_restore`; independent v3 composite archive test |
| `collection` | live `collection`; catalog lifecycle retention test |
| `authorized_http_mcp_ui` | live production selected-reader/scoped-search phase; exact 10,000-row service-directory pagination, v3 search stream parity, HTTP/MCP/SSE authority parity, and both named UI tests |
| `no_service_count_times_repository_bytes` | live cold physical-work counters and corpus-pass oracle |
| `source_free_receipt` | canonical source-free receipt test |
| `clean_teardown` | exact teardown oracle |

The live target itself traverses the production selected state reader,
selector-aware v3 scoped searcher, and authorized HTTP/MCP inventory, detail,
and search transports. Every returned target-search path is binary-searched in
the frozen fixture inventory and must carry that file's exact fixture content
plus the exact first-line marker range. Its truthful empty relationship root traverses the
production v3 scheduler/runtime and production relationship-v3 lifecycle
owner. Mixed and dense relationship distributions are direct-builder composed
evidence only. The live browser proof serves the exact exported `ui/dist`
through loopback, uses a synthetic authenticated status only to enter the
unchanged application, and sends its admitted bearer to the real `api.New`
directory handler. It records one inventory and two independently initiated
detail reads, requires the 10,000-service authority and 50-row page with exact
generation/revision identities, and rejects any console, page, request, or API
error. The two named Vitest cases remain separate wire-consumer regressions;
neither the browser proof nor those tests invent relationship evidence or a
presentation/screenshot claim.

Passing these tests validates only the receipt/corpus contract or named smoke
shape. It establishes
no large-repository envelope, target SLO, supported customer limit,
accuracy/completeness, release, migration, decommission, P6, or topology
claim.
