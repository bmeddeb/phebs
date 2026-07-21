# GATE2-V2 Stage-2 P0-02 authorization worksheet — 4f579b7 candidate

**Status: DRAFT — NOT AUTHORIZATION. LIVE P0 REMAINS BLOCKED.**

This is the successor worksheet after terminal P0-01. It is not a canonical P0
authorization, it does not use the executable stage2-prebuild-authorization.json
path, and it carries no AUTHORIZED status or PLAN approval row. P0-01 and its
preserved ceremony directory remain untouched.

## Candidate identity and promotion rule

Candidate remediation implementation commit:
4f579b7920958bee5ab21f5c4bc59b1edb727b4c

Requested provisional draft identifier:
t111-gate2-v2-p0-4f579b7-02

This identifier is not fireable. Let C be the full commit that independently
accepts r3 and r6, and let S be its first seven hex characters. The canonical
successor must be t111-gate2-v2-p0-S-02. Its source-clone commit,
implementation_review.accepted_commit, and implementation_binding.commit must
all equal C. If C differs from the candidate above, regenerate this worksheet
and the canonical authorization rather than patching an old candidate.

## Candidate artifact bindings

| Artifact | SHA-256 |
|---|---|
| stage2_prebuild.py | sha256:bef0f3aea88f45de1ce3d9426e69ac5cfd7c36e777c7ed8838187a7435481b56 |
| stage2_prebuild_test.py | sha256:b4373dac3e5504f9d0a58257bb2101359f62d211a55a81e10d13a88924d89a8e |
| stage2_prebuild_execute.py | sha256:c4404d1d9ca5187aa6a5c7fbb2c236172b188aeb1864c3e0fe1d7f475cea5c54 |
| stage2_prebuild_execute_test.py | sha256:9ef3668edc98b1806c21b813aae2a633c358c89fc5fb1cd7a43ea9bf90c5e84e |
| stage2_enumerate.py | sha256:2684f5c917713ff320adef0fc0bdadbc7c3c2a660a62d3469e6528f6a3a01873 |
| stage2_enumerate_test.py | sha256:81b46cc2cd26a0898c5d22448659261d35f447eeb7f166d05834c038e382ca88 |
| r3 review request | sha256:4ea7e3928d3c7fdd8f5eef844fe92a73817cc5cfb703b5f2c50a32294109119d |
| r6 review request | sha256:9ed1450b502549a900c72c8c9cd93682db1c8659b6cd7aac685f51e0257fc799 |

The r3/r6 records are PENDING INDEPENDENT REVIEW. Their current digests are
review-request bindings only and must not be copied into a live authorization.

## P0-02 corpus contract

Each source_history must be:
- is_shallow_repository: false
- old_commit: the row below
- sealed_commit: the matching Stage-1 head
- old_commit_is_ancestor: true

| Fixture | Old commit | Sealed head | Source ref |
|---|---|---|---|
| temporal | 8224a5375112079ad905c4ea829420306431462c | f95c865cc08c1ac075a709d525977e17103e6417 | refs/gate2-v2/temporal |
| dapr | 08aebd8b2effa2ed939ad5531e25ff8b21a36ef1 | f4d431123309a2bd11fcc32523661b6b14e8462b | refs/gate2-v2/dapr |
| loki | 1362d2770ee2abba5e130d67cf30bcc4eefa0da0 | 562a762ab1d07985edc561920d74e792f4a6aab9 | refs/gate2-v2/loki |
| online-boutique | 9a4616e77f0f9cbcbecaf27d711c38890dda1404 | 9a4616e77f0f9cbcbecaf27d711c38890dda1404 | refs/gate2-v2/online-boutique |

For each fixture, bind precisely:
- sequence = [ref, bundle, init, normalize, fetch, checkout]
- update-ref: git -C source update-ref source_ref sealed_head 0000000000000000000000000000000000000000
- bundle: git -C source bundle create bundle_path source_ref
- fetch: git -C destination fetch --no-tags bundle_path source_ref:source_ref
- checkout: git -C destination checkout --detach sealed_head

Admission and the pre-M0 executor check must prove non-shallowness, both commit
objects, and old-to-sealed ancestry. The source ref must be absent or exactly
the sealed head before zero-CAS creation. Destination fetch/check-out is the
sole completeness arbiter; git bundle verify is never an acceptance oracle.

## Terminal, namespace, and carry-forward bindings

T0 must use t111-gate2-v2-stage2-prebuild-terminal-receipt-v2. Completed T0
has failure_diagnostic: null. A command refusal or timeout contains only the
bounded sanitized fields step, exit_code, stderr_sha256, stderr_preview, and
stderr_truncated.

For final ID I:
- root = /Users/ben/.local/share/I-derived
- ceremony = /Users/ben/.local/share/I-ceremony
- M0/E1/T0 = ceremony/consumed.json, ceremony/evidence.json, ceremony/terminal.json
- scratch = ceremony/scratch/hydration and ceremony/scratch/offline
- bundles = ceremony/bundles
- run IDs = I-run1 and I-run2

Substitute I through every derived, bundle, capture, fact-run, projection, and
phase-environment path. No P0-02 path may overlap P0-01.

Rehash at promotion and carry forward only if byte-identical:
- Stage-1 runner: sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1
- Stage-1 receipt/response: sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef / sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e
- Stage-0 inventory: sha256:a5d8e5635f57585b60ad9692dd41334d19661a8ca068f20a31ecad022327441e
- Base/derived locks: sha256:a0fe717d168dc1a857720dd9bfb5957e50dd9e6944470f7d17ec4671b550027b / sha256:d02cd5ef2baff3101fd72ac02eb57c14fee91593d1ca80c772584153eed9540b
- Harness: sha256:13c2dbf04348ae09d53ebf56d3824748c2e0e4ead34171d6fe45d0e3cf33c4f9
- t111/oracle: sha256:0648ea773fc6590ce0d0f67aff940d40251634de1805655be7cde8fba4cdb8d4 / sha256:43f4a0328ac5f5cd8e62236163552871c514d7a60eb1f161db9016575b46c9cc
- Protocol: sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65
- Estimator authorization: sha256:f39c617471d4e8e0b92dafa41e3281f67e5b27dffd2188cc9b4c4420089d233d

P0-01 historical evidence remains:
- authorization: t111-gate2-v2-p0-f47490d-01, sha256:2ea8965ce408628c8181bc127cd03209fcec8bcbf6c89fbac0c74c63db11cc12
- marker: sha256:832c19f480c1422f5109375d90155a8c286e261ed42775b1dcbdf8e92ed7f7d0
- terminal: sha256:9968917dcdd341b7fd718f170b176349a2335b560a992af730d43f4eb9f1495a
- failure review: sha256:60d9ec1efb58e0e2c8b8d0b12f50936c18c767a7967d9f1d9264856b11ea5d84

Do not add p0_01 to the canonical prior_authorizations object: the reviewed
parser and enumerator presently permit only estimator. This preserves P0-01
custody without widening executable schema.

## Promotion sequence

1. Independently accept r3 and r6 exact bytes in accepted implementation C,
   with matching PLAN ACCEPT anchors.
2. Regenerate canonical stage2-prebuild-authorization.json from C, rehash all
   carry-forward inputs and review records, and add its exact digest to an
   authorization approval row.
3. Obtain separate operator fire-time approval. Then, and only then, one P0-02
   run may occur. Enumeration, preparation, selection, and disclosure remain
   blocked pending independent evidence review and final enumeration authority.
