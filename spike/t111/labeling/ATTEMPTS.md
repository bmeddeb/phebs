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
