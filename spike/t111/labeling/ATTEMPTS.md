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
