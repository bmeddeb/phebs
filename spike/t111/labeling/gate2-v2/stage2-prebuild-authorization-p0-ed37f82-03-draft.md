# GATE2-V2 Stage-2 P0-03 authorization worksheet — ed37f82 candidate

**Status: DRAFT — NOT AUTHORIZATION. LIVE P0 REMAINS BLOCKED.**

This is the successor worksheet after terminal P0-02. It is not a canonical
P0 authorization, it does not use the executable
`stage2-prebuild-authorization.json` path, and it carries no `AUTHORIZED`
status or PLAN approval row. P0-01 and P0-02 and both preserved ceremony
directories remain untouched.

## Candidate identity and promotion rule

Candidate remediation implementation commit:
`ed37f8236b753e1944e8b3131caa8a46e0c92948`

Producer-source foundation commit:
`8f105581f2a231b4be9ac28fb7c238bcf11a37cd`

Requested provisional draft identifier:
`t111-gate2-v2-p0-ed37f82-03`

This identifier is not fireable. Let C be the full commit that independently
accepts r4 and r7, and let S be its first seven hexadecimal characters. The
canonical successor ID must be `t111-gate2-v2-p0-S-03`. Its source-clone
commit, `implementation_review.accepted_commit`, and
`implementation_binding.commit` must all equal C. If C differs from the
candidate above, regenerate this worksheet and the canonical authorization
rather than patching an old candidate.

## Candidate artifact bindings

| Artifact | SHA-256 |
|---|---|
| stage2_prebuild.py | sha256:bef0f3aea88f45de1ce3d9426e69ac5cfd7c36e777c7ed8838187a7435481b56 |
| stage2_prebuild_test.py | sha256:b4373dac3e5504f9d0a58257bb2101359f62d211a55a81e10d13a88924d89a8e |
| stage2_prebuild_execute.py | sha256:382fef28c7bffc0f045df6b04e455818580cff3321783918f2672fd7979ee49e |
| stage2_prebuild_execute_test.py | sha256:348ea0851113f6a1df856ac60def3308b733f9b3d77fe376212133dd0dc76ae4 |
| stage2_enumerate.py | sha256:926190eee4e0b6d30d97b468fa625ea405ea98b11a7d4f8f20ea2f5cb19c1a91 |
| stage2_enumerate_test.py | sha256:50f5169a42f6180c056a9bf97d7c139d69dc37b2fad6b76a743f14ec6cfa7c0c |
| module_cache.go | sha256:efb4548efd8f263e35cf5f34bd642278606e82ac8cf12d01e22caabe3abc393c |
| module_cache_test.go | sha256:fae2fd4550722253aaf28ab01cf88f61a7b504b926623ec203b9297429c83e74 |
| r4 review request | sha256:f6801bd51ff760b6ae952a67c3b7f85b317795f89cffbb83bce8ded58637008c |
| r7 review request | sha256:e4a512c926c53ea58decdf0e50464bfb9e12c27a60c65ca28725c089003b13dd |
| P0-02 failure review r2 | sha256:b8ba1386b532999d861544981646410b39a1d54a2e1748699830d3e7856cdab2 |

The r4/r7 records are `PENDING INDEPENDENT REVIEW`. Their current digests are
review-request bindings only and must not be copied into a live authorization.
The accepted r3/r6 records remain immutable terminal P0-02 history.

## R6 hydration/verification contract

The chosen design is **narrow hydration plus closure-scoped independent h1
verification**; complete-graph hydration was not chosen.

- Hydration and verification use exactly
  `./spike/t111` and `./spike/t111/typedcalloracle`, obtained from the same
  implementation function.
- The pre-build and post-build verifier resolves only those package closures
  with network disabled, checks dedicated-cache sealing and containment, and
  directly checks each selected external source tree and cached `go.mod`
  against the committed `go.sum` h1 values.
- The two closure descriptor digests must be identical across the builds.
- Graph-wide `go mod verify` is forbidden on this path because it exceeds the
  hydrated scope.
- The regression graph carries a non-selected older dependency version. Narrow
  hydration and offline bound build pass without its source; deleting an
  actually selected closure module refuses.

The executor r4 review predicate binds both `module_cache.go` and the
regression at the accepted commit. The r7 verifier independently recomputes
those commit-blob digests and requires the r4 record to bind them before E1
can touch derived state. The strict PLAN row remains transitively ordered as
parser, executor, enumerator, r7 review, r4 review.

## Refreshed harness inputs

The ignored source harness was rebuilt offline from clean committed producer
source at `8f105581f2a231b4be9ac28fb7c238bcf11a37cd`, using the bound Go 1.26.5
binary. A later promotion must rehash these files and stop on any drift.

- Stage-0 harness manifest:
  `sha256:3e30ea0069b5773af8154ebca8e5576dcc0e55094b808d1c72c97316a169c1b3`
- t111 bootstrap/derived target:
  `sha256:7d6db7fca68981e05758ca41b0b0ae109d935ace71ef228d52b5184928e93f65`
- typedcalloracle (byte-identical carry-forward):
  `sha256:43f4a0328ac5f5cd8e62236163552871c514d7a60eb1f161db9016575b46c9cc`

The ignored files are authorization inputs, not committed implementation
blobs. The future authorization must bind their exact current bytes; the
accepted implementation commit binds the tracked producer source that must
reproduce the t111 digest.

## R1–R5 carry-forward contract

Each `source_history` must remain:

- `is_shallow_repository: false`
- `old_commit`: the row below
- `sealed_commit`: the matching Stage-1 head
- `old_commit_is_ancestor: true`

| Fixture | Old commit | Sealed head | Source ref |
|---|---|---|---|
| temporal | 8224a5375112079ad905c4ea829420306431462c | f95c865cc08c1ac075a709d525977e17103e6417 | refs/gate2-v2/temporal |
| dapr | 08aebd8b2effa2ed939ad5531e25ff8b21a36ef1 | f4d431123309a2bd11fcc32523661b6b14e8462b | refs/gate2-v2/dapr |
| loki | 1362d2770ee2abba5e130d67cf30bcc4eefa0da0 | 562a762ab1d07985edc561920d74e792f4a6aab9 | refs/gate2-v2/loki |
| online-boutique | 9a4616e77f0f9cbcbecaf27d711c38890dda1404 | 9a4616e77f0f9cbcbecaf27d711c38890dda1404 | refs/gate2-v2/online-boutique |

For each fixture, bind precisely:

- sequence = `[ref, bundle, init, normalize, fetch, checkout]`
- update-ref: `git -C source update-ref source_ref sealed_head 0000000000000000000000000000000000000000`
- bundle: `git -C source bundle create bundle_path source_ref`
- fetch: `git -C destination fetch --no-tags bundle_path source_ref:source_ref`
- checkout: `git -C destination checkout --detach sealed_head`

Admission and the pre-M0 executor check must prove non-shallowness, both commit
objects, and old-to-sealed ancestry. The source ref must be absent or exactly
the sealed head before zero-CAS creation. Destination fetch/check-out remains
the sole completeness arbiter; `git bundle verify` is never an acceptance
oracle. Terminal receipts remain schema
`t111-gate2-v2-stage2-prebuild-terminal-receipt-v2` with bounded sanitized
failure diagnostics. The two-file result publication remains atomic.

## Terminal, namespace, and prior-authority bindings

For final ID I:

- root = `/Users/ben/.local/share/I-derived`
- ceremony = `/Users/ben/.local/share/I-ceremony`
- M0/E1/T0 = `ceremony/consumed.json`, `ceremony/evidence.json`,
  `ceremony/terminal.json`
- scratch = `ceremony/scratch/hydration` and `ceremony/scratch/offline`
- bundles = `ceremony/bundles`
- run IDs = `I-run1` and `I-run2`

Substitute I through every derived, bundle, capture, fact-run, projection, and
phase-environment path. P0-03 paths must overlap neither terminal P0-01 nor
terminal P0-02. Do not read, rewrite, or reference either preserved ceremony
directory as a P0-03 state target.

`prior_authorizations` remains exactly estimator-only. Neither `p0_01` nor
`p0_02` may be added: the reviewed parser/enumerator schema permits only the
estimator predecessor, while both terminal P0 records remain preserved
historical evidence outside that executable object.

## Other carry-forward bindings

Rehash at promotion and carry forward only if byte-identical:

- Stage-1 runner: `sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1`
- Stage-1 receipt/response:
  `sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef` /
  `sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e`
- Stage-0 inventory:
  `sha256:a5d8e5635f57585b60ad9692dd41334d19661a8ca068f20a31ecad022327441e`
- Base/derived locks:
  `sha256:a0fe717d168dc1a857720dd9bfb5957e50dd9e6944470f7d17ec4671b550027b` /
  `sha256:d02cd5ef2baff3101fd72ac02eb57c14fee91593d1ca80c772584153eed9540b`
- Protocol:
  `sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65`
- Estimator authorization:
  `sha256:f39c617471d4e8e0b92dafa41e3281f67e5b27dffd2188cc9b4c4420089d233d`
- Git executable: `sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818`
- Go executable: `sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0`
- CLT Python 3.9: `sha256:bdea59019a38eb6600cc9e71e984a97fedadc406448431281e7657030f54987e`

P0-01 historical authorization remains
`t111-gate2-v2-p0-f47490d-01`,
`sha256:2ea8965ce408628c8181bc127cd03209fcec8bcbf6c89fbac0c74c63db11cc12`.
P0-02 historical authorization remains
`t111-gate2-v2-p0-79d1442-02`,
`sha256:fdccd52f10f7576695fe7e4a2f22194ec48084a5f4192b2c4859947847f9474e`;
its failure review is the r2 digest bound above. Neither may be retried.

## Promotion sequence

1. Independently review these candidate bytes. If accepted, rewrite r7 to one
   exact `**Verdict: ACCEPT.**` line plus its sealed bindings; then bind that
   final r7 digest from the exact r4 acceptance record.
2. Land r4/r7 acceptance and the two strict PLAN ACCEPT anchors in a new
   accepted implementation commit C. No execution authority is created by
   that commit.
3. Regenerate the canonical `stage2-prebuild-authorization.json` from C with
   ID `t111-gate2-v2-p0-C[:7]-03`, fresh state namespaces, exact current
   harness/toolchain/carry-forward digests, and a separately committed PLAN
   authorization row.
4. Obtain separate operator fire-time approval. Then, and only then, one
   P0-03 run may occur. Enumeration, preparation, selection, and disclosure
   remain blocked pending independent evidence review and final enumeration
   authority.
