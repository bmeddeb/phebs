# Gate 2 attempt ledger

This ledger records public, coordinate-free attempt lineage. Persistent local
attempt claims and public commitment receipts remain the authoritative records;
this file explains why a lineage was retired and how any replacement corpus was
selected. It must never contain seed material, sampled coordinates, labels, or
hidden extractor outcomes.

## Attempt 1 — retired before artifact publication

- Source lineage: `sha256:5d22053962301b95735310f327d2179409d4d6d8d5b6854ee369efee52f9a39b`
- Input binding: `sha256:4ae520f1271efb38e44317ff34f543975eda2ab7d01bdce444d68498e0c2c4b3`
- Scheduled activation: `2026-07-14T14:14:41.709738Z`
- Initial public commitment: [GitHub Gist](https://gist.github.com/bmeddeb/7f6bbbdd6409b73bfa7d0b0da419c68f), revision `1ed2457d0c7ff90f823c9258552191d6845c5db9`
- First eligible public pulse: `https://beacon.nist.gov/beacon/2.0/chain/2/pulse/1859264`, timestamp `2026-07-14T14:15:00.000Z`
- Disposition: failed closed during `prepare`, before a sealed claim, bundle,
  staging directory, sampled coordinate, or labeler artifact was emitted.

The v1 input commitment accidentally included development-stratum allocation
counts realized from a private preflight rank. Reconstruction with the public
seed therefore produced different canonical commitment bytes and correctly
failed the publication hash check. The attempt claim, public Gist, GitHub
receipt, and seed receipt are preserved; this source lineage must not be reused.

## Attempt 2 — replacement lineage rule

The replacement uses every official default-branch fixture commit available at
the fixed cutoff `2026-07-14T14:32:26Z`; fixtures that had not advanced remain
unchanged. This rule was chosen before inspecting any replacement sample or
extractor outcome.

- `online-boutique`: `9a4616e77f0f9cbcbecaf27d711c38890dda1404` (unchanged)
- `dapr`: `b557df0b28abb88c1ef1ad95354ebea5c4a18266`
- `temporal`: `5e2a0eaabbd4807077172bef4beb12d6b0c710c0`
- `loki`: `aa5e221aa4d54fb5126e121e5f85d918270e1953`
- `temporal-helm`: `9f4d328c31c77c323d272d0c5f615cf02bd46dab` (unchanged companion)
- Source lineage: `sha256:bb347b827fe2d45b1bf3d2dff507e10462f9aee7d3dcf26be6241d03e446dc7b`

Attempt 2 uses `t111-gate2-input-commitment-v2`, whose public bytes and power
ceiling depend only on committed populations, census/holdout sizes, fixed
development quotas, and post-holdout capacities—not on a realized seed rank.

### Attempt 2 — sealed result

- Input binding: `sha256:eca70ead55594e43c635356444196ce6b9c0c7c4e9a50a0a21451918bc6c8a8d`
- Scheduled activation: `2026-07-14T16:42:40.009797Z`
- Initial input commitment: [GitHub Gist](https://gist.github.com/bmeddeb/889e67caa7eae870b3038ddee506bc7b), revision `90f008c2d975951f753f5155d660fc9f7c47eb11`
- First eligible public pulse: `https://beacon.nist.gov/beacon/2.0/chain/2/pulse/1859412`, timestamp `2026-07-14T16:43:00.000Z`
- Frozen labels: 3,028; SHA-256 `8ba4c8966802426b55d45bdbf83bfb78ed5955badcc741a492c2dcd94da39112`
- Adjudication: 307 overlap sites, 2 disagreements, 4 adjudicated, 0 unresolved
- Initial label commitment: [GitHub Gist](https://gist.github.com/bmeddeb/f0323d02e3969f0c76499a9a99aa4bfe), revision `20990867a593c4bac3e024d88b87498cdefe672e`
- Disposition: **Gate 2 NOT ESTABLISHED** on the one permitted holdout score.

Every client-call and registration confidence bound passed, as did benchmark
support and the blind-fraction requirement. Four role cohorts were exact, but
the `test` cohort was 148/149. The post-score burned-coordinate review found
that both source classifiers recognized exact `tests` and `testdata` path
segments but omitted the exact `testing` segment used by reusable test support.
The frozen human label was therefore correct and the extractor emitted
`production` incorrectly.

Attempt 2 remains immutable and must not be relabeled, rescored, reseeded, or
reused. The prospective correction is extractor `spike-0.5.1`, which adds the
exact `testing` segment to the fixed test-role taxonomy and makes that rule
explicit to reviewers. Any validation of the correction requires a genuinely
new four-commit source lineage and must carry every Attempt-2 disclosure into
the append-only burn census.

## Attempt 3 — replacement lineage rule

Before inspecting a replacement sample or extractor outcome, the next attempt
was fixed to every official default-branch commit available at cutoff
`2026-07-14T19:00:47Z`:

- `online-boutique`: `9a4616e77f0f9cbcbecaf27d711c38890dda1404` (unchanged)
- `dapr`: `08aebd8b2effa2ed939ad5531e25ff8b21a36ef1`
- `temporal`: `a5e6d3ed6335256319fff94f38bf74c4b7ba370c`
- `loki`: `d108ea11a62fbf7be7d25b58d44d396a3ce0c96c`
- `temporal-helm`: `9f4d328c31c77c323d272d0c5f615cf02bd46dab` (unchanged companion)
- Source lineage: `sha256:2d7bab803cf20c36e738534dd73018ecca96e9f87922bf96e7d66a1bbe346cbf`

Attempt 3 uses extractor `spike-0.5.1` and the full append-only burn ledger.
No attempt claim may be created until exact corpus synchronization, gitlink
review, deterministic fact regeneration, carry-forward resolution, full tests,
and a write-suppressed attainable-power preflight all pass.

After locked population enumeration and burn carry-forward—but before an
attempt claim, public commitment, external entropy, sampled-coordinate
publication, or labeling—the inherited 800-per-system client-call precision
quota failed the write-suppressed capacity check solely because its
seed-independent blind-fraction lower bound was 24.76%, below the fixed 30%
minimum. The prospective design was therefore changed to a probability-one
fresh precision holdout with exact enumeration, using the same
1,000,000-per-system sentinel as the registration precision frame and no
precision development allocation. This does not remove any burn, lower a
threshold, or use a realized random rank. The amendment used only locked
population and carry-forward capacity counts, never correctness outcomes. With
the locked populations it raises the conservative blind-fraction lower bound to
44.86%, eliminates precision-sampling uncertainty, and leaves the design
statistically attainable.

### Attempt 3 — terminal sealed result

- Input binding: `sha256:9164040c050299408b87903a2befdec976bf1a77a38c3bdfd74c77a3d05e5496`
- Scheduled activation: `2026-07-14T21:03:19.010209Z`
- Initial input commitment: [GitHub Gist](https://gist.github.com/bmeddeb/e7478531360f854005efc0245095c9ac), revision `f46df6a5796fa64aef304b363697157be42c3386`
- Commitment document SHA-256: `6ee0bc4b4389de71e318b5dbe429cc33ffb7311846003c28dd081d58252a06d4`
- First eligible public pulse: `https://beacon.nist.gov/beacon/2.0/chain/2/pulse/1859673`, timestamp `2026-07-14T21:04:00.000Z`
- Sealed artifact manifest SHA-256: `78336c1f04b7055d7d2ecb7d34ba18291aa26443c81c28de38fde36e5615aa6e`
- Label population: 3,051 permanent-census sites, 0 development sites, and
  2,693 blind holdout sites; realized blind fraction 46.88%.
- Reviewer assignment SHA-256: `cb767b2381decdef2d840d46c6649193644a43b33b5caf9d87f8cc9673d3ca6f`
- Burn cohort: 5,743 unique coordinates, ledger digest
  `sha256:82076bd76092e03f9de16f9c3bf44e1d80e89e2c6ac5973abe13c9eeee1bac87`.
- Frozen labels: 5,744; SHA-256
  `56bc44d41d44b4adecaa1284db8bd24cde1ae4cf034cbac4893c78e81a95e034`.
- Independent review: 577 overlap sites, 0 disagreements, 0 adjudicated, and
  0 unresolved.
- Initial label commitment: [GitHub Gist](https://gist.github.com/bmeddeb/896c13b3e7f6e6d99e207198a2523cc7),
  revision `1014884987f1e5b8fd8ae40125f0fcee0d2f5caa`.
- Label commitment document SHA-256:
  `76ffa1ca8ae366898410198888b9b2aa6e2f51c3a5c1c46050380eb85ae2a1e2`.
- One-shot score execution receipt SHA-256:
  `8b686e0201092622a8a057ab29e5ee225e00dd9b12b50b7f7994a168d8544883`.
- Disposition: **Gate 2 NOT ESTABLISHED** on the one permitted score
  invocation; no metric was emitted.

The score failed during deterministic frame recomputation because its shell
resolved Go 1.26.4 from the module toolchain cache while the sealed fact
producer identity requires the exact Go 1.26.5 binary and digest. The scorer
had already loaded the hidden key before this check, so Attempt 3 is consumed:
it must not be rescored, relabeled, reseeded, or reused under the same lineage.

Prospectively, the scorer exposes a key-free `--preflight-toolchain` command and
performs the same attestation before opening `key.jsonl`. A regression locks
that ordering. The correction cannot rehabilitate Attempt 3; any replacement
requires a new official-head lineage and a complete new ceremony carrying the
append-only burn ledger forward.

## Attempt 4 — replacement lineage rule

Before inspecting any replacement sample or regenerated extractor outcome,
Attempt 4 was fixed to every official default-branch commit available at
cutoff `2026-07-15T03:13:32Z`:

- `online-boutique`: `9a4616e77f0f9cbcbecaf27d711c38890dda1404` (unchanged)
- `dapr`: `08aebd8b2effa2ed939ad5531e25ff8b21a36ef1` (unchanged)
- `temporal`: `8224a5375112079ad905c4ea829420306431462c`
- `loki`: `1362d2770ee2abba5e130d67cf30bcc4eefa0da0`
- `temporal-helm`: `9f4d328c31c77c323d272d0c5f615cf02bd46dab`
  (unchanged companion)
- Source lineage: `sha256:504fd6a8f068713d4fb4d1590e9b0ff6d42fbb621afa09215ed81beec3bb85de`

The two declared gitlink exclusions retain their exact paths and object IDs at
the new Temporal and Loki commits; no new gitlink exists. Attempt 4 carries the
entire append-only burn ledger and uses the pre-key exact-toolchain guard added
after Attempt 3. No attempt claim may be created until exact corpus
synchronization, deterministic fact regeneration, carry-forward resolution,
full reusable verification, and write-suppressed attainable-power analysis all
pass. The standalone key-free scorer preflight is required later, after a
sealed bundle exists and immediately before scoring.

### Attempt 4 — pre-claim capacity result

The exact corpus synchronized cleanly. Temporal and Loki facts were regenerated
twice under the producer-bound Go 1.26.5/Git toolchain and were byte-identical;
all four fact files verify against their pinned Git objects. The 93-test Python
suite, `go vet ./...`, and the full Go suite passed.

The write-suppressed, coordinate-free power preflight then returned
`attainable=false` without creating an attempt claim or artifact:

- permanent-census unique sites: 5,745;
- fresh holdout unique sites: 4;
- selected unique ceiling: 5,749;
- seed-independent blind-fraction ceiling: `4/5749 = 0.0696%`; and
- fixed minimum blind fraction: 30%.

The sole failure reason was the blind-fraction rule. Every fresh call-precision,
call-recall, registration, and role site was already assigned to holdout, so no
sampling-quota change can add capacity. No public commitment, entropy, sampled
coordinate, reviewer kit, or label exists for Attempt 4. Proceeding requires a
prospective benchmark expansion with enough genuinely new fixture population,
or a separately reviewed estimator that incorporates the already frozen prior
labels. Dropping carried census sites or lowering the fixed floor is forbidden.

## Attempt 5 — strict expansion protocol

The selected recovery path is a prospective Gate 2 benchmark expansion, not a
new estimator over prior labels. Before any candidate checkout, extractor run,
fact count, or coordinate enumeration, `EXPANSION.md` fixes an ordered public
repository list, objective eligibility rules, a minimum two-repository prefix,
and a 35% conservative blind-capacity target while preserving the existing 30%
floor and every accuracy/confidence requirement.

Historical exposure will carry across fixture names by identical Git blob ID,
so copied or vendored source cannot manufacture freshness. Eligible candidates
must be accumulated in the fixed order and cannot be skipped for low capacity
or unfavorable output. Only a committed immutable-source eligibility failure
may advance to the next candidate; a fact-production or oracle-coverage failure
blocks for a prospective harness fix on that same candidate. The first prefix
that passes complete burn projection and write-suppressed power analysis is the
only permissible Attempt 5 corpus. No attempt claim, commitment, entropy,
sample, reviewer kit, or label exists at this stage.

The first fixed-cutoff source snapshot established no lineage: GitHub rejected
the initial request as malformed, and the corrected diagnostic response arrived
outside the committed boundary and was discarded. With no source checkout or
attempt state created, the same immutable protocol now fixes the replacement
snapshot at `2026-07-15T04:30:00Z`; no other rule changed.

No request was launched at that superseded boundary because the manual
scheduler overshot it. It therefore also established no lineage and returned
no data. The final cutoff is fixed at `2026-07-15T04:40:00Z`, with the fixed
tracked `expansion-source-snapshot.graphql` query launched by an automatic
timer. A failure there stops expansion; there is no further discretionary
retry.

The final timed request succeeded once, beginning at
`2026-07-15T04:40:00.099108000Z`. Its raw response and digest-bound receipt fix
all seven candidate commits; evaluation now starts with the mandatory
`grpc/grpc-go`, `etcd-io/etcd` prefix. No candidate has yet been checked out or
measured, and no attempt claim or sampling state exists.

The mandatory `grpc-go` and `etcd` pins then synchronized cleanly at exact
`HEAD`. Each contains Go and RPC-bearing proto source, and neither contains a
gitlink, so both are intrinsically eligible with no declared exclusion. No
extractor fact or coordinate had been enumerated at that decision point.

The prospective six-commit lineage and v3 cross-fixture burn projection are
now frozen in `expansion-lineage.json` before fact production. The projection
contains 6,109 active intervals; nine `grpc-go` and five `etcd` intervals are
carried census rather than fresh capacity. No claim, randomness, sample, or
label exists.

The two new operation-fact files were each generated twice under the exact
producer toolchain and were byte-identical: 1,124 `grpc-go` facts and 1,629
`etcd` facts. All six Gate 2 fact files verify against the pinned Git objects;
`expansion-facts.json` binds 14,596 facts. No claim or sampling state exists.

The first two write-suppressed capacity preparations stopped before emitting
an aggregate result: nested Go modules exposed multiple agreeing provenance
contexts for single physical sites, and the pinned `etcd` tree contains safe
in-tree Go source aliases. The prospective harness now collapses only complete
consensus mappings to the benchmark's unique-source-site unit and binds every
node in a source-alias chain before and after type loading. Disagreement,
escape, dangling targets, cycles, or content drift fail closed. Neither stopped
run created an attempt claim, commitment, randomness, sample, artifact,
coordinate, reviewer kit, or label.

The corrected coordinate-free preflight then completed for the mandatory
two-repository prefix with the labeling tree unchanged. Its conservative,
seed-independent blind lower bound is `1322/7348 = 17.9913%`, below the
unchanged 30% Gate 2 floor and the prospective 35% expansion stop target. The
only unattainable power rule is the blind-fraction rule; every accuracy and
confidence requirement remains attainable. The aggregate-only result is bound
in `expansion-capacity-prefix-2.json`. Under the frozen order the prefix must
therefore expand to candidate 3, `containerd/containerd` at
`9e70782d9a0e92900f402b2c7a4e2aa30754503c`. No later candidate has been
inspected, and Attempt 5 still has no claim or ceremony state.

Only then was candidate 3 synchronized. `containerd/containerd` is clean at
the fixed pin `9e70782d9a0e92900f402b2c7a4e2aa30754503c`, with 5,332 tracked Go
files, 106 tracked proto files, 41 RPC-bearing proto files, and no gitlink. Its
three symlinks are safe relative in-tree aliases and contain no Go or proto
source at their canonical targets. Strict object verification found no missing
required object. Containerd is therefore intrinsically eligible with no
exclusion. This source-only decision precedes all containerd fact generation,
typed-oracle execution, coordinate enumeration, and labeling.

The seven-system harness was then committed and the complete cross-fixture burn
projection regenerated before containerd fact production. Lineage
`sha256:60845d1e93de8583e146520abd256f8e23940c805d232c47ee0bdeb5d75cdfea`
has 6,120 active intervals; 11 land in containerd and are permanent census.
The 9,834 resolution records are bound by
`sha256:55929907e9ee024ae469936ee867c5c9c2c68fc9d9d5032d0d4dc1b76ccac545`.
`expansion-lineage.json` binds the committed harness, corpus lock, and
append-only ledger. No containerd fact, claim, commitment, randomness, sample,
coordinate, reviewer artifact, or label existed at this boundary.

Containerd facts were then generated twice offline under the exact producer
toolchain. Both complete runs produced 4,345 facts with byte-identical digest
`sha256:d8034932e23ab36caa005ee5e773d1df4b4c0d2552295ea534911e9d540a954e`;
the pinned tree remained clean. All seven fact files verify against their
immutable Git objects and share one exact producer identity.
`expansion-facts.json` now binds 18,941 facts to the seven-system lineage. No
claim, commitment, randomness, sample, coordinate, reviewer artifact, or label
exists.

The complete reusable test/vet matrix then passed. The claim-disabled,
write-suppressed seven-system preflight reconstructed every frame and the typed
oracle with the labeling tree unchanged. Its conservative blind lower bound is
`1541/7600 = 20.2763%`, below the unchanged 30% floor and 35% expansion target;
blind fraction is the only failed rule. Containerd contributes 222 independent
typed call sites with complete exact-fact coverage and no quarantine.
`expansion-capacity-prefix-3.json` binds only aggregate data and current input
digests. No claim or ceremony state exists. The frozen order therefore requires
candidate 4, `istio/istio@25f4803ee1e64fc2fcb95d07b1c0e3353594e9a9`;
no later candidate has been inspected.

Only then was candidate 4 synchronized. `istio/istio` is clean at the fixed
pin `25f4803ee1e64fc2fcb95d07b1c0e3353594e9a9`, with 1,991 tracked Go files,
seven tracked proto files, one RPC-bearing proto file, and neither gitlinks nor
symlinks. Strict verification found all required objects present. Istio is
therefore intrinsically eligible with no exclusion. This source-only decision
precedes all Istio fact generation, typed-oracle execution, coordinate
enumeration, and labeling.

The eight-system harness was then committed and the complete cross-fixture burn
projection regenerated before Istio fact production. Lineage
`sha256:51dbecc478d8a9728dae70739dce783238ce7b54cd2f3302d7469c6ea37970dd`
still has 6,120 active intervals because none lands in Istio. The 9,834
resolution records retain digest
`sha256:55929907e9ee024ae469936ee867c5c9c2c68fc9d9d5032d0d4dc1b76ccac545`.
`expansion-lineage.json` binds the committed harness, corpus lock, and
append-only ledger. No Istio fact, claim, commitment, randomness, sample,
coordinate, reviewer artifact, or label existed at this boundary.

The first subsequent exact-toolchain offline Istio extraction failed closed at
publication validation: the root module reported a `LOAD_ERRORS` diagnostic
with 102,097 package errors because its required versioned dependencies were
not present in the offline cache available to the harness. No Istio facts were
published and no oracle or ceremony state was created. Under the frozen
protocol this cannot exclude or skip candidate 4. A prospective generalized
repair must first commit explicit sum-checked dependency hydration, sealed
path-validated module-cache reads, isolated per-run Go state, and content-bound
producer/oracle binaries; the eight-system lineage must then be regenerated
before the same candidate is retried. Candidate 5 has not been inspected.

The first committed-repair hydration also stopped before corpus work when
cmd/go added 492 previously absent checksum lines to the root `go.sum` while
closing the harness's full resolved module graph. The source-state guard
rejected that mutation and the failure path resealed the 1.4 GiB cache with no
writable entry or symlink. Review found an additive checksum-only diff: no
`go.mod`, selected version, source, corpus, fact, oracle, or ceremony state
changed. The exact checksum closure is committed prospectively before another
hydration or candidate-4 run.

The retry under checksum-closure commit `8d8d1ee` passed the root harness
source-state guard, then stopped on online-boutique: the still-broad
`go mod download all` operation changed module files in its disposable pinned
snapshot while resolving modules outside the package closure consumed by the
producer and oracle. The source-state guard rejected that mutation, the
original pinned corpus remained clean, and the dedicated cache was resealed.
No fact, typed-oracle, coordinate, claim, commitment, randomness, reviewer, or
label state was created.

The prospective correction hydrates only `go list -deps` closures for the two
bound harness binaries and each corpus package/test closure under its fixed
build tags. Offline producer and oracle reads retain structural sealed-cache
checks and now directly h1-check both the source tree and cached `go.mod` of
every external module actually used by their loaded packages, before
publication and again after scanning. Regressions prove that an unavailable
unused requirement does not block hydration, while imported-module source or
`go.mod` tampering fails closed in both independent readers. This correction
must be committed before hydration or candidate 4 is retried; candidate 5
remains untouched.

After prospective repair commit `3644fce`, exact Go 1.26.5 completed the
actual-closure hydration for all nine locked corpus entries, including Istio.
Every root and corpus module input remained unchanged, and all pinned trees
remain clean at their exact commits. The resulting 8.1 GiB hidden cache has no
writable entry or symlink. Its sealed manifest has digest
`sha256:1f32cd6330256e868f204cec5cc95d732a82d861918cf16e590b2144bdb9ac23`,
records clean harness source `3644fce`, and binds producer
`sha256:818f073b094e37b5a3b3a7cb1af589a9b57e800a02a447fc2ebbc88e5a3672cb`
plus typed oracle
`sha256:88aa4fbd7d99bfa5038cd1acd101c10b78a1ec21447f73becc7993471c7c21b3`.
The bound producer independently accepted that manifest. No extraction,
typed-oracle census, fact, coordinate, claim, commitment, randomness,
reviewer, or label operation followed from hydration.

Before the first Istio fact run, expansion lineage v2 now fixes
`sha256:a6979c4698394003f844f19b2a488a63f1e7882f6ad7e16a19836f702a250c7f`.
It preserves the unchanged 6,120-coordinate/9,834-resolution burn projection
and binds the exact harness source, current preparation script, corpus lock,
burn ledger, sealed manifest, producer, and independent oracle. V1 remains
frozen historical evidence because its hand-authored binding algorithm was
not tracked. V2 is reproducible: one domain-separated SHA-256 covers the
canonical complete receipt with only `source_lineage_binding` omitted, and a
strict builder/validator plus golden and mutation regressions enforce that
definition. No Istio fact or Attempt 5 ceremony state existed when fixed;
candidate 5 remains untouched.
