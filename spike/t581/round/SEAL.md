# T58.1 seal — Go gRPC syntactic consumer/registration site-coverage round

Protocol: `docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md` at commit `4d538f582c7f43a1a57b6118ee4c5c4956cc3c93`
Commit subject: t58.1: regenerate the seal manifest so it binds the current seal_prepare.py digest

Published under §7 before `export` runs on the population. This gist is the
seal; the thresholds and every digest below were frozen before any extractor
run over the population.

## Disclosed breach

The exporter ran over the locked population before any seal existed because a placeholder gist URL cleared a prefix-and-length guard; SIGPIPE stopped it after two rows and no candidate file was written.

Disposition: Ben elected to seal with the breach disclosed (2026-10-14).
Lead recommendation: interested — the lead caused the breach.
See protocol §7 for what was and was not compromised.

## Pre-seal oracle pass (no extractor)

- scored sites: **34089** (floor 300)
- out-of-domain candidates: 2870212 (1 : 84.2)
- units 13, aggregates 13, 8.6s

## Frozen thresholds (§6, Ben 2026-10-14, before any oracle pass)

| Check | Bar |
|---|---|
| Coverage: scored sites with exactly one row | ≥ 0.99 |
| State agreement on covered sites | ≥ 0.99 |
| Resolved/ambiguous inversions | ≤ 0 |
| Call/registration swaps (GD9) | ≤ 0 |
| Confirmed spurious rows per scored site | ≤ 0.01 |
| Pending after adjudication | ≤ 0 |
| Scored sites | ≥ 300 |
| Production-role state agreement | ≥ 0.99 |

Disclosed but not scored: per_repository_scored_counts_with_concentration, test_generated_mock_vendor_role_agreement, out_of_domain_candidate_counts.

## Bound digests

| Artifact | sha256 | bytes |
|---|---|---|
| `docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md` | `sha256:eeba4b65f07e214466500f16fa7cc12ae70d6a61882dad502f010a1c4e17dc5c` | 45784 |
| `spike/t581/corpus.lock.json` | `sha256:989f10ae48389647eb5cc332e3e93d6325ad4b2d657324f299001865aa803175` | 6923 |
| `spike/t581/population/units.jsonl.gz` | `sha256:95cc4cb4ca92dcd72551c40ccd6556d059ee5a9196ccca46e9112ac2b5ef5c62` | 305431 |
| `spike/t581/population/population.json` | `sha256:e3c786dda449e78d3d6e6513a5a416c8a53da42087da3ad0b1cd8c9b234ef3eb` | 2460 |
| `spike/t581/round/sites.jsonl.gz` | `sha256:05f23347b48cb620c3670ea74a80e27dba897008095a77705c6db0a66bb0c815` | 612614 |
| `spike/t581/lock_corpus.py` | `sha256:3f98f6314908f78964a225df579d624841617430452e5dd9abfe1a80b4eab59a` | 9288 |
| `spike/t581/enumerate_population.py` | `sha256:5e403718cec6e58eebbf75aff29d6d9c3dcbcb823103ad000924bd24edf7fea0` | 7556 |
| `spike/t581/export/main.go` | `sha256:6553954e230195bf6d8816f1faba365a22c5d7899b09a99fac8029fa9da561b0` | 11938 |
| `spike/t581/score.py` | `sha256:7c8579bbec03d49f7cb0822a11935e7ffa26259cb7afeb5e0a547cedd575f565` | 17878 |
| `spike/t581/test_score.py` | `sha256:b59b577496de3a002a58ce50bf1e378695628dee6e626a8da9e2e5251d7cff85` | 14644 |
| `spike/t581/oracle/main.go` | `sha256:87b78bba86fa6f69c5c96fa33ea30218d7de258582220dd09c7e3db6b050ff93` | 34768 |
| `spike/t581/oracle/main_test.go` | `sha256:0dc608f4d5a1219e855fda0e2963b0f72fa3c97a8814873be0d61fdbda601213` | 42377 |
| `spike/t581/seal_prepare.py` | `sha256:f66bee48956667a5f33fb632293ed91fab953c7cdf6c8457ec0df7ded5535b35` | 11246 |

Bound artifacts clean at commit: **True**

## Claim boundary

Site coverage only. Emitted operation and service identities are recorded but
not scored, so a pass is not identity accuracy and T13.1's deferred ≥98%/≥90%
bar is neither met nor addressed. The population is biased toward repositories
vendoring modern split `*_grpc.pb.go` stubs in-tree; combined-style stubs,
external-module contracts and generic instantiation calls are all silently out
of domain. A pass says nothing about Go gRPC code outside that class.

The oracle's independence is procedural, not technical: its author was
instructed not to read the extractor, which cannot be enforced. See §3.
