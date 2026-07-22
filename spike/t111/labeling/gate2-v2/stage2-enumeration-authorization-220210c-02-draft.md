# GATE2-V2 Stage-2 enum-02 authorization worksheet — 220210c candidate

**Status: DRAFT — NOT AUTHORIZATION. ENUMERATION REMAINS BLOCKED.**

This is the successor worksheet after terminal enumeration attempt 01. It is
not canonical JSON, is not stored at the enumerator's executable authorization
path, carries no `AUTHORIZED` payload, and adds no PLAN approval anchor. The
terminal attempt and its preserved ceremony evidence remain untouched.

## Candidate identity and promotion rule

Candidate remediation implementation commit:
`220210c2f321a4da8d9795b6c8d714f00777564e`

Requested provisional draft identifier:
`t111-gate2-v2-enum-220210c-02`

This identifier is not fireable. Let C be the full commit that independently
accepts the r8 implementation review, and let S be its first seven hexadecimal
characters. The canonical successor ID must be
`t111-gate2-v2-enum-S-02`; `review.accepted_commit` and `binding.commit` must
both equal C. The authorization must be regenerated from the accepted bytes
and final r8 digest rather than copying the terminal enum-01 payload.

## Candidate artifact bindings

| Artifact | SHA-256 |
|---|---|
| `stage2_enumerate.py` | `sha256:c624913debea72fed95b0acfd3836db6cd2d1480a89d49d3b9f2495ab4c3e25a` |
| `stage2_enumerate_test.py` | `sha256:251edde88f96170f6a8e99f39f2c2de893ad98a7ebbc112aabfcef32bb396188` |
| r8 review request | `sha256:74f01e7000fdebee3432b000ecd9af83ce52fdbb65ecc51373422701552cfc75` |
| enumeration failure review r3 | `sha256:653d19e4f042b87a3199594fc850648a0db1e82487c349b5ca9b3ed93aebf0f9` |
| Stage-1 snapshot verifier | `sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1` |

The r8 record is `PENDING INDEPENDENT REVIEW`. Its current digest is a request
binding only and must never be copied into a live authorization. Independent
acceptance must preserve the exact candidate enumerator and test bytes while
rewriting r8 to one machine verdict and exact artifact bindings in a later
accepted implementation commit C.

## R7 site-row contract

- Each of the four external frame producers projects its rows onto the exact
  line sampling key `(system, path, line)` before cardinality calculation.
- Distinct producer identities at one key become one deterministic
  representative carrying `sampling_site_multiplicity`, sorted
  `sampling_site_member_ids`, and a SHA-256 of the complete sorted members.
- Invalid rows are not normalized away. Repeated producer identities and
  byte-identical duplicate rows remain separate, so the established
  `stage2_inputs` integrity boundary still refuses them.
- `stage2_inputs`, including the exact
  `frame has duplicate sampling-site coordinates` error, is byte-identical to
  terminal-review commit `9ed830236860a2643d08d08dd1ca141f69d8c79c`.
- Population and census arithmetic count the resulting distinct site rows,
  not the number of facts or source invocations on a line.

The isolated regression suite proves two synthetic facts on one line produce
one precision site, multiplicity two, and population one; it separately proves
a genuinely duplicated emitted row reaches and triggers the unchanged refusal.
The bound CLT Python 3.9 run passes 48/48 tests. Carry-forward parser and
executor suites pass 18/18 and 36/36.

## Versioned trust closure

The completed P0-03 evidence ancestry stays permanently bound to:

- accepted implementation commit
  `d1420272acaf01063cdfce486bbe2b047d41e214`;
- historical enumerator
  `sha256:926190eee4e0b6d30d97b468fa625ea405ea98b11a7d4f8f20ea2f5cb19c1a91`;
- historical r7 review
  `sha256:82eacc1c633a53096a5dbeccf7515b8b74b7029e4b82e4c2a989157110bbeb61`;
- accepted r4 executor review
  `sha256:cc0b4d9e81caa5fd728776dbc07e43ba81f4f54b4a2279d74328749c8dd9dbf6`.

The enum-02 executable trust closure is separate: it must bind the R7
enumerator above, the final accepted r8 record, the byte-identical Stage-1
snapshot verifier, and the sole strict PLAN implementation row
`GATE2-V2 Stage-2 enumeration verifier, r8 | ACCEPT`. The implementation
predicate must require C to be an ancestor of promotion HEAD and must read all
three accepted blobs plus that PLAN row from C. Historical r7 remains the P0
dependency; it cannot bless the new enumerator, and r8 cannot be substituted
into the completed P0 evidence chain.

## Complete P0 evidence-chain carry-forward

The enum-02 authorization must bind the same accepted P0 evidence as enum-01:

- P0-03 authorization
  `spike/t111/labeling/gate2-v2/stage2-prebuild-authorization.json`,
  `sha256:01814b900086620f0d3091c36c61d49b87a2198f693a848a96a302f26106a2b4`,
  promotion commit `cba24190c646d1c09345971ae5518eb0d5abc2bb`;
- sanitized evidence declaration
  `spike/t111/labeling/gate2-v2/stage2-prebuild-evidence.json`,
  `sha256:022ca2c7540655b909994a71ee77ac7e84cc6a4ca9f0853a216cb58896ddd0a7`;
- strictly later evidence review
  `spike/t111/labeling/gate2-v2/stage2-prebuild-evidence-review-r1.md`,
  `sha256:1696187097558ddb7f31278a3dc711e1426f265e26b563d3704db141ef55f58b`,
  accepted evidence commit
  `fbd84744edb6791ac2e6af1c47e1ef6e009767cf`;
- terminal receipt
  `sha256:360610e3c5ab738905690d4cf695035e211f112ecea978377c36e25c9d55e8a7`
  and evidence receipt
  `sha256:52c3470a8fcb83b26bb1c8a711517ce1bc58c5523a0cdcba25767d43137dcaba`;
- run1/run2 receipts
  `sha256:ffe162bab9b3209bc82716b74aadec8e369dacab267b7cee3fe2b4004bea9f97`
  and
  `sha256:c789b82da4e6eecc35ee320f4bf25bce90c6c53a8402ccda9239cdc461c43f6a`;
- run IDs `t111-gate2-v2-p0-d142027-03-run1` and
  `t111-gate2-v2-p0-d142027-03-run2`.

Both run bindings retain the same four fact digests:

| Fixture | Fact SHA-256 |
|---|---|
| dapr | `sha256:651cb712b4d3b00ef0fb694e20985aead6cb0c53cdee9555f5f8fe3e46bb0089` |
| loki | `sha256:d6a3a30f5748a20eebe937840573f1edc34276245e2fc4fa82d66a701b01c82d` |
| online-boutique | `sha256:aeb5f9538b639793831c0282b977a247427a327eae70971423d9d2eba7915034` |
| temporal | `sha256:85a3816a189fdb6e6b175a4f011e7933d08f029f179266454028edd12f174d4f` |

The four sealed heads remain:

| Fixture | Head |
|---|---|
| dapr | `f4d431123309a2bd11fcc32523661b6b14e8462b` |
| loki | `562a762ab1d07985edc561920d74e792f4a6aab9` |
| online-boutique | `9a4616e77f0f9cbcbecaf27d711c38890dda1404` |
| temporal | `f95c865cc08c1ac075a709d525977e17103e6417` |

## Required 33-field authorization projection

Promotion must materialize exactly the enumerator's 33 `AUTHORIZATION_FIELDS`,
with no omissions or extensions:

`schema`, `status`, `authorization_id`, `p0_authorization`,
`enumerator_sha256`, `stage1_snapshot_sha256`, `receipt_sha256`,
`response_sha256`, `stage0_inventory_sha256`, `base_lock_sha256`,
`derived_lock_sha256`, `stage0_harness_manifest_sha256`,
`derived_harness_manifest_sha256`, `cache_tree_sha256`,
`t111_binary_sha256`, `typedcalloracle_binary_sha256`, `python_executable`,
`python_version`, `python_mode`, `python_sha256`, `git_executable`,
`git_sha256`, `go_executable`, `go_sha256`,
`producer_toolchain_identity`, `review`, `binding`, `environment`,
`prior_authorizations`, `prebuild_evidence`, `heads`, `fact_runs`, and
`state`.

The unchanged non-state bindings are:

- schema `t111-gate2-v2-stage2-enumeration-authorization-v1`, status
  `AUTHORIZED`, and isolated-no-site Python mode;
- Stage-1 receipt/response
  `sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef` /
  `sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e`;
- Stage-0 inventory, base lock, and derived lock
  `sha256:a5d8e5635f57585b60ad9692dd41334d19661a8ca068f20a31ecad022327441e`,
  `sha256:a0fe717d168dc1a857720dd9bfb5957e50dd9e6944470f7d17ec4671b550027b`,
  and
  `sha256:d02cd5ef2baff3101fd72ac02eb57c14fee91593d1ca80c772584153eed9540b`;
- source/derived harness manifests and cache tree
  `sha256:3e30ea0069b5773af8154ebca8e5576dcc0e55094b808d1c72c97316a169c1b3`,
  `sha256:1de0135a57c7ba5353b808a80a3393e821dfc67e3b4f2e8e85eac2aeb8222fbe`,
  and
  `sha256:147b46b766f26214f1d03e9a2f7087c7eb7060132324f6b1e3fa2885bfb4e84f`;
- t111 and typed oracle binaries
  `sha256:7d6db7fca68981e05758ca41b0b0ae109d935ace71ef228d52b5184928e93f65`
  and
  `sha256:43f4a0328ac5f5cd8e62236163552871c514d7a60eb1f161db9016575b46c9cc`;
- Git, Go, and CLT Python 3.9 identities
  `sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818`,
  `sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0`,
  and
  `sha256:bdea59019a38eb6600cc9e71e984a97fedadc406448431281e7657030f54987e`;
- estimator-only prior authorization
  `spike/t111/labeling/estimator-authorization.json`,
  `sha256:f39c617471d4e8e0b92dafa41e3281f67e5b27dffd2188cc9b4c4420089d233d`.

The exact no-network environment and producer toolchain identity must be
carried forward byte-for-byte from the terminal enum-01 authorization and
reverified against the bound executables during promotion. The terminal
enum-01 authorization itself must not enter `prior_authorizations`; that
registry remains exactly estimator-only.

## Fresh enumeration-owned state

For final authorization ID I, use only:

- ceremony directory `/Users/ben/.local/share/I-ceremony`;
- sole authorized `--out` `/Users/ben/.local/share/I-ceremony/output`;
- consumption marker `/Users/ben/.local/share/I-ceremony/consumed.json`;
- terminal receipt `/Users/ben/.local/share/I-ceremony/terminal.json`.

Every state path must substitute the final ID I and be textually and
canonically disjoint from all prior namespaces. Promotion must fail if that
fresh namespace already exists or if any accepted input drifts. This drafting
step does not create, inspect, or access any ceremony or derived state.

## Serialization and promotion sequence

The future canonical JSON must use the sealed serializer exactly:

`json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n"`

1. Independently review the R7 implementation, regression tests, r8 request,
   and this worksheet. No execution authority is created by review.
2. If accepted, replace r8 with exactly one `**Verdict: ACCEPT.**` line and
   its exact bindings, then add the sole strict r8 implementation PLAN anchor
   in a later accepted implementation commit C.
3. Regenerate the canonical executable authorization with ID
   `t111-gate2-v2-enum-C[:7]-02`, the final r8 digest, and fresh I-keyed state.
   Commit it with the sole strict
   `GATE2-V2 Stage-2 enumeration authorization | AUTHORIZATION: APPROVED`
   PLAN row binding its canonical digest.
4. Obtain separate operator fire-time approval before one launch.

Until all four steps complete, enumeration, preparation, selection,
coordinate disclosure, and ceremony access remain blocked. `gate_status`
remains `PENDING`.
