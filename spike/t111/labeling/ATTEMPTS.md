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
