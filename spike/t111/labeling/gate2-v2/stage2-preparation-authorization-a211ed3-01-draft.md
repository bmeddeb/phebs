# GATE2-V2 Stage-2 preparation authorization worksheet — a211ed3-01

**Status: DRAFT — NOT AUTHORIZATION. PREPARATION REMAINS BLOCKED.**

This worksheet fixes the complete candidate input and authority set for one
Stage-2 carry-forward and exact-power preparation. It is not stored at the
future canonical authorization path, creates no `AUTHORIZED` record or PLAN
approval anchor, and grants no execution authority. No preparation, selection,
or coordinate disclosure is authorized by this file.

## Candidate identity and promotion rule

- Accepted implementation/review commit:
  `a211ed37008daf1a2d62fe07e31f7646daa207f1`
- Accepted implementation parent:
  `d1301ee6d660cd9819a0e295cc6b4beeaf78d19d`
- Candidate authorization ID:
  `t111-gate2-v2-prep-a211ed3-01`
- Future canonical authorization path:
  `spike/t111/labeling/gate2-v2/stage2-preparation-authorization.json`
- Future canonical schema:
  `t111-gate2-v2-stage2-preparation-authorization-v1`
- Canonical candidate size: `10,314` bytes
- Canonical candidate SHA-256:
  `sha256:9164f8b4d671517349c82a4bd92306602e5adb2b7243fba9ad483870092d4ea8`

The ID is non-fireable while this worksheet is a draft. It is keyed to the
commit that independently accepted the exact R8 bytes and Finding-4 closure,
analogous to the accepted-commit ID rules used by P0 and enumeration. After an
independent worksheet review, promotion must materialize the canonical record
from these exact bindings and add the sole strict preparation-authorization
PLAN row. Any changed bound byte requires a regenerated worksheet and review.

The r3 acceptance at `a211ed3` is an exact reviewed prose record and dated PLAN
decision, not an executable loader predicate or the later strict machine-row
grammar used by P0/enumeration. `stage2_prepare.py` has no `--authorization`
loader. This worksheet binds that authority model honestly: the future JSON is
an operator-bound record and the exact CLI inputs remain independently
digest-bound. The external worksheet reviewer must accept this model; if a
strict r3 authority-normalization commit is required instead, this draft is
rejected and regenerated with that later commit in its ID and bindings.

## Accepted implementation and review

| Artifact | SHA-256 | Authority |
|---|---|---|
| `spike/t111/labeling/gate2-v2/stage2_prepare.py` | `sha256:1e64fddc20756604ce12f636dfd96ab01f3b4dd123f478ae196902b8709059f4` | r3 accepted executable |
| `spike/t111/labeling/gate2-v2/stage2_prepare_test.py` | `sha256:ea677860be674889fa1ba11485b8417badc10f1c1687f7c3fb0f810cc15a8239` | r3 accepted regression companion |
| `spike/t111/labeling/gate2-v2/stage2-prepare-review-r3.md` | `sha256:6fe8fbb5c5ade81c5579ef5105e9a8b42f0b0774ea59165b96118d9314e067c2` | independent ACCEPT at `a211ed3` |
| r3 request before review | `sha256:00e67931eec23625dbe182ffcf039bf35537164d5c3387467b20cdff9ff2111a` | immutable request lineage |

R8's output schema is
`t111-gate2-v2-stage2-preparation-v3`. Its feasibility result is the
conjunction of the four aggregate frames and both precision families × four
fixtures. The future selector must satisfy every fixture minimum and then top
up as needed to satisfy its aggregate-frame minimum; consuming only the
aggregate minimum is forbidden and is outside this authorization.

## Complete Finding-4 input manifest

| CLI input | Exact path | SHA-256 |
|---|---|---|
| `--receipt` | `/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage1/receipt.json` | `sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef` |
| `--ledger` | `/Users/ben/phebs.com/spike/t111/labeling/burn-ledger.json` | `sha256:4e6e2382361f1a0223562d4cbac921f39944ceb36c916912fa0ca5c259e3044a` |
| `--cardinalities` | `/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/cardinalities.json` | `sha256:db2f702f14f8bf62534d0c4eb0abb9b5d83e8ddbb0431b45b4e836739d5baf90` |
| `--frame-membership` | `/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/frame-membership.json` | `sha256:9bdabf9567d2732db23444d55072ee8503dd6ebb2838818e4d80a621f0bb4b3e` |
| `--heads` | `/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-heads.json` | `sha256:80b1b6501e9d18bf832657df8e978565f14f29f62435f7740bd4ce98689bad9b` |
| `--design` | `/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-design.json` | `sha256:214cfdda24ea9cfff3159519b8faf5dba9d8af7103304b1e58392daf569300d2` |

The cardinalities and membership remain accepted enum-02 outputs in their
preserved namespace. They are read-only inputs and are not copied into the
repository. The coordinate-bearing membership is bound by path and digest
only; this worksheet discloses no membership key.

### Exact sealed design values

The tracked design file is canonical JSON and contains exactly the five rules
consumed by R8—no omitted or inert metadata:

| Design key | `p` | `threshold` | Protocol source |
|---|---:|---:|---|
| `client_call_precision` | `995/1000` | `98/100` | §5 overall precision / §10 |
| `client_call_recall` | `95/100` | `90/100` | §5 eligible-population recall / §10 |
| `registration_precision` | `995/1000` | `98/100` | §5 overall precision / §10 |
| `registration_recall` | `95/100` | `90/100` | §5 eligible-population recall / §10 |
| `per_fixture_precision` | `97/100` | `90/100` | §5 true per-fixture precision / §10 per-fixture lower bound |

Canonical payload, including one trailing LF:

```json
{"client_call_precision":{"p":"995/1000","threshold":"98/100"},"client_call_recall":{"p":"95/100","threshold":"90/100"},"per_fixture_precision":{"p":"97/100","threshold":"90/100"},"registration_precision":{"p":"995/1000","threshold":"98/100"},"registration_recall":{"p":"95/100","threshold":"90/100"}}
```

The run therefore cannot choose a design point at fire time. Exact sample
minima and feasibility are consequences of the bound populations, membership,
burn ledger, heads, sealed `minimal_n`, and these rationals.

### Exact sealed heads and old-commit rule

| Fixture | `old_commit` from last completed carry-forward receipt | `new_commit` from admitted Stage 1 | Exact mirror |
|---|---|---|---|
| dapr | `08aebd8b2effa2ed939ad5531e25ff8b21a36ef1` | `f4d431123309a2bd11fcc32523661b6b14e8462b` | `/Users/ben/phebs.com/spike/t111/corpus/dapr` |
| loki | `1362d2770ee2abba5e130d67cf30bcc4eefa0da0` | `562a762ab1d07985edc561920d74e792f4a6aab9` | `/Users/ben/phebs.com/spike/t111/corpus/loki` |
| online-boutique | `9a4616e77f0f9cbcbecaf27d711c38890dda1404` | `9a4616e77f0f9cbcbecaf27d711c38890dda1404` | `/Users/ben/phebs.com/spike/t111/corpus/online-boutique` |
| temporal | `8224a5375112079ad905c4ea829420306431462c` | `f95c865cc08c1ac075a709d525977e17103e6417` | `/Users/ben/phebs.com/spike/t111/corpus/temporal` |

The old side is the four-fixture projection of
`spike/t111/labeling/expansion-lineage.json`, file digest
`sha256:9c821523db05bf959f9e01e05529d18bfede1f3c505d54019fb9c33d602dc430`,
whole-record binding
`sha256:86d0a76a510ecd99e6be939132bf9efc46c96e0c314583fde2f9e6d72d865e16`,
committed at `f6818b9e8f74af2083211f3b61d515173677049a`. That receipt binds the exact
ledger digest above. R8 refuses any changed old/new commit, mirror path,
shallow repository, missing commit object, or failed old→new ancestry before
opening ledger coordinates.

## Enumeration authority and receipts

- Enum-02 authorization ID:
  `t111-gate2-v2-enum-4a01935-02`
- Canonical enum authorization:
  `spike/t111/labeling/gate2-v2/stage2-enumeration-authorization.json`,
  `sha256:3c756c24d507028c5346d69caa03c333c08db024e4e4626b6a7b1f818b23e385`,
  promotion commit `71f4ea1381aa3d6ea9922b8e20613154bc984513`
- Enum-02 terminal receipt:
  `sha256:ad9aee01c7a024b6d4c21fbf597b8a41a4e4e95bcf234bb60a919feffaf08a5f`
- Enum-02 enumeration receipt:
  `sha256:5d2b7a01529691fb3f75b1b55a5f52fdae1ffdad970b44c53e5bc21114554546`
- Independent receipt review:
  `spike/t111/labeling/gate2-v2/stage2-enumeration-receipt-review-r1.md`,
  `sha256:a44746d6b3b904d0be824775cb9aab4cc5bb48449b8e347bbfdb26c21d2a0e17`,
  acceptance commit `0d736c8209160a36edb0948d4f72ccb4820c10fd`

The accepted site-counted populations are client-call precision 5,921,
client-call recall 6,002, registration precision 127, and registration recall
280. R8 independently derives precision populations by fixture from the bound
membership and refuses unless each derived sum equals its aggregate
cardinality.

## Sealed dependency closure

| Artifact | SHA-256 |
|---|---|
| GATE2-V2 protocol | `sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65` |
| `stage0/carry_forward.py` | `sha256:2bb3278fc086b8ce17dcb818959bdac63949112420622426499085882f58c589` |
| `stage0/power_advisory.py` | `sha256:8f59dd8e2256419a299fb61992e912b29582a7d946ffc909572ce674ea9d66c2` |
| `spike/t111/score.py` | `sha256:a1a04f51dee7d2044bd3433dadc2ef53f74519135f76e478810b1bc9366dece4` |
| `spike/t111/label_prep.py` | `sha256:53c83c4e4e5ca3370b52d3dd42a2fa125ecbafdeba09ae396dac1d72e6015d07` |
| `spike/t111/gate34_common.py` | `sha256:1e837da18b761fe1dd56e68322cc154124da704074295315325cd7647a89fb88` |
| `spike/t111/label_protocol.py` | `sha256:bcafdee6cab0b4ddd98ddbd56896546a47abe2c164be955ace28542686f329ee` |
| `spike/t111/label_adjudication.py` | `sha256:be1724b08b79e6877167dd3ebf99630a90cc18781e2aa83bb92e58fac42cf231` |
| `spike/t111/reviewer_kits.py` | `sha256:64289ec04c295a939d62ae5770323aba134c04498bb72df45a0d2178e17c2ab7` |
| `spike/t111/labeling/expansion-lineage.json` | `sha256:9c821523db05bf959f9e01e05529d18bfede1f3c505d54019fb9c33d602dc430` |
| historical `stage2-prepare-review-r2.md` | `sha256:fc2e5395756d5cdac9a9cf1c3c09cb1ba0a6113d625cf92c2cfc0b1807e919d9` |
| `stage1_snapshot.py` | `sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1` |
| `stage0/STAGE0.md` | `sha256:11e8faef6a5dfd671fb25bd7414c6380e31ec4e41f990b5d2ff2c2628704e1a6` |
| `stage0/snapshot-query.graphql` | `sha256:8e9f76872c955e0bad76dfde432e846fbc7c340dfd23bba7a67fda14a55d897b` |
| `stage0/snapshot-constants.json` | `sha256:5908318e1c1b25d59bf0d78f5b4027b50bb52e28d4ff0f529486c75d4380dc76` |
| Stage-1 response | `sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e` |

No dependency byte may change between `a211ed3`, worksheet review, and
promotion. The canonical authorization must bind every row above.

## Toolchain and clean execution context

- Launcher: `/usr/bin/python3`, Python `3.9.6`,
  `sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818`
- Resolved CLT runtime:
  `/Library/Developer/CommandLineTools/Library/Frameworks/Python3.framework/Versions/3.9/bin/python3.9`,
  `sha256:bdea59019a38eb6600cc9e71e984a97fedadc406448431281e7657030f54987e`
- Git: `/usr/bin/git`, `git version 2.50.1 (Apple Git-155)`,
  `sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818`
- Resolved CLT Git:
  `/Library/Developer/CommandLineTools/usr/bin/git`,
  `sha256:be4afb2b003904725826250de9fb76567bbacf82323457b5a1ec26706b66bcae`

The launch environment is empty except for:

```text
HOME=/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/home
LANG=C
LC_ALL=C
PATH=/usr/bin:/bin
```

No `GIT_*` variable is present. The fresh HOME prevents user-global Git
configuration from entering the run. R8 independently requires bare `git` to
resolve to `/usr/bin/git` and refuses any Git-affecting inherited override.
This is a path- and ambient-environment-constrained context, not a claim that
system/local repository configuration is hermetic. The authorization also
requires a procedural fire-time re-resolution and digest/version check for
both Python and Git; R8 itself checks Git's path/environment, not executable
digests. Network access is neither required nor authorized, and the no-network
condition is procedural rather than tool-enforced by R8.

## Fresh state and exact invocation

Let `I = t111-gate2-v2-prep-a211ed3-01`.

- Ceremony parent: `/Users/ben/.local/share/I-ceremony`
- Sole `--out`: `/Users/ben/.local/share/I-ceremony/output`
- Clean HOME: `/Users/ben/.local/share/I-ceremony/home`
- Operator stdout capture: `/Users/ben/.local/share/I-ceremony/stdout.log`
- Operator stderr capture: `/Users/ben/.local/share/I-ceremony/stderr.log`

Promotion and fire-time preflight must find the entire ceremony namespace
absent. The operator then creates only the ceremony parent with mode `0700`;
`output` must remain absent for R8's atomic publication. A pre-existing parent
or output is a stop—never remove or reuse it. The stdout/stderr files are
operator custody records, not tool-created terminal receipts. R8 has no
consumption-marker writer; exactly-once/no-retry remains a procedural
authorization condition and must not be misrepresented as mechanically
enforced.

The canonical authorization must bind this exact argv, with absolute paths:

```text
/usr/bin/python3
/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2_prepare.py
--ledger
/Users/ben/phebs.com/spike/t111/labeling/burn-ledger.json
--heads
/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-heads.json
--cardinalities
/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/cardinalities.json
--frame-membership
/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/frame-membership.json
--design
/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-design.json
--receipt
/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage1/receipt.json
--out
/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/output
```

Working directory is
`/Users/ben/phebs.com/spike/t111/labeling/gate2-v2`. `--synthetic` is
forbidden.
Promotion may format a shell launch line around this environment/argv and the
two log redirections, but may not change an argument or input path.

## Output and exit contract

The only tool-published output is the fresh atomic directory containing:

- `census-v2-seed.jsonl` — carry-forward decisions for previously disclosed
  coordinates; no fresh selection;
- `stage2-preparation.json` — deterministically serialized v3 counts, burn arithmetic,
  four aggregate power results, eight fixture-precision power results, and
  their combined feasibility. Its exact format is deterministic
  `sort_keys=True`, `indent=2`, plus one trailing LF; it is not the compact
  authorization serializer.

Exit semantics remain:

| Exit | Meaning | Authorized consequence |
|---:|---|---|
| 0 | all four aggregate and all eight fixture cells feasible | seal outputs; `gate_status` stays `PENDING` |
| 1 | at least one exact-power cell infeasible | capacity verdict; stop before selection |
| 2 | admission/input refusal | preserve logs and namespace; no retry |
| 3 | output collision | preserve evidence; no retry |
| 4 | unexpected failure | preserve evidence; no retry |

Any signal or shell-level failure is likewise terminal under this
authorization. No result authorizes selection or disclosure. A feasible
preparation is capacity evidence only; Gate 2 cannot become ESTABLISHED here.

## Future canonical authorization contract

Promotion must materialize the exact canonical JSON candidate embedded below,
byte-for-byte. It has exactly these top-level fields and no extensions:

`authorization_id`, `binding`, `dependencies`, `enumeration`, `environment`,
`exit_contract`, `implementation`, `inputs`, `invocation`, `output_contract`,
`protocol`, `schema`, `scope`, `serializer`, `state`, `status`, `toolchain`.

Required fixed values include:

- `schema = t111-gate2-v2-stage2-preparation-authorization-v1`
- `status = AUTHORIZED`
- `authorization_id = t111-gate2-v2-prep-a211ed3-01`
- `binding.commit = a211ed37008daf1a2d62fe07e31f7646daa207f1`
- `binding.status = executable`
- the exact implementation, dependency, enumeration, input, environment,
  state, invocation, output, and exit values recorded above;
- scope limited to carry-forward and exact-power preparation, with
  enumeration, selection, disclosure, and labeling explicitly forbidden.

Because the preparation tool has no authorization loader, this record is an
operator commitment whose digest is checked procedurally before the single
launch. Adding a loader or wrapper is an implementation change requiring a
new review; it cannot be improvised during promotion.

## Canonical candidate payload

The single compact JSON line in this fence, followed by exactly one LF, is the
future canonical authorization candidate. Its `AUTHORIZED` status is inert
inside this non-executable worksheet and grants no authority.

```json
{"authorization_id":"t111-gate2-v2-prep-a211ed3-01","binding":{"authority_model":"operator-bound-record-no-loader","commit":"a211ed37008daf1a2d62fe07e31f7646daa207f1","status":"executable"},"dependencies":{"carry_forward":{"path":"spike/t111/labeling/gate2-v2/stage0/carry_forward.py","sha256":"sha256:2bb3278fc086b8ce17dcb818959bdac63949112420622426499085882f58c589"},"expansion_lineage":{"binding":"sha256:86d0a76a510ecd99e6be939132bf9efc46c96e0c314583fde2f9e6d72d865e16","commit":"f6818b9e8f74af2083211f3b61d515173677049a","path":"spike/t111/labeling/expansion-lineage.json","sha256":"sha256:9c821523db05bf959f9e01e05529d18bfede1f3c505d54019fb9c33d602dc430"},"gate34_common":{"path":"spike/t111/gate34_common.py","sha256":"sha256:1e837da18b761fe1dd56e68322cc154124da704074295315325cd7647a89fb88"},"historical_r2_review":{"accepted_commit":"105eaeb309edf59fa4c8b494cc15f135005f344b","path":"spike/t111/labeling/gate2-v2/stage2-prepare-review-r2.md","sha256":"sha256:fc2e5395756d5cdac9a9cf1c3c09cb1ba0a6113d625cf92c2cfc0b1807e919d9"},"label_adjudication":{"path":"spike/t111/label_adjudication.py","sha256":"sha256:be1724b08b79e6877167dd3ebf99630a90cc18781e2aa83bb92e58fac42cf231"},"label_prep":{"path":"spike/t111/label_prep.py","sha256":"sha256:53c83c4e4e5ca3370b52d3dd42a2fa125ecbafdeba09ae396dac1d72e6015d07"},"label_protocol":{"path":"spike/t111/label_protocol.py","sha256":"sha256:bcafdee6cab0b4ddd98ddbd56896546a47abe2c164be955ace28542686f329ee"},"power_advisory":{"path":"spike/t111/labeling/gate2-v2/stage0/power_advisory.py","sha256":"sha256:8f59dd8e2256419a299fb61992e912b29582a7d946ffc909572ce674ea9d66c2"},"reviewer_kits":{"path":"spike/t111/reviewer_kits.py","sha256":"sha256:64289ec04c295a939d62ae5770323aba134c04498bb72df45a0d2178e17c2ab7"},"score":{"path":"spike/t111/score.py","sha256":"sha256:a1a04f51dee7d2044bd3433dadc2ef53f74519135f76e478810b1bc9366dece4"},"stage0_manifest":{"path":"spike/t111/labeling/gate2-v2/stage0/STAGE0.md","sha256":"sha256:11e8faef6a5dfd671fb25bd7414c6380e31ec4e41f990b5d2ff2c2628704e1a6"},"stage1_response":{"path":"spike/t111/labeling/gate2-v2/stage1/response.json","sha256":"sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e"},"stage1_snapshot":{"path":"spike/t111/labeling/gate2-v2/stage1_snapshot.py","sha256":"sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1"},"stage1_snapshot_constants":{"path":"spike/t111/labeling/gate2-v2/stage0/snapshot-constants.json","sha256":"sha256:5908318e1c1b25d59bf0d78f5b4027b50bb52e28d4ff0f529486c75d4380dc76"},"stage1_snapshot_query":{"path":"spike/t111/labeling/gate2-v2/stage0/snapshot-query.graphql","sha256":"sha256:8e9f76872c955e0bad76dfde432e846fbc7c340dfd23bba7a67fda14a55d897b"}},"enumeration":{"accepted_commit":"0d736c8209160a36edb0948d4f72ccb4820c10fd","authorization":{"authorization_id":"t111-gate2-v2-enum-4a01935-02","path":"spike/t111/labeling/gate2-v2/stage2-enumeration-authorization.json","promotion_commit":"71f4ea1381aa3d6ea9922b8e20613154bc984513","sha256":"sha256:3c756c24d507028c5346d69caa03c333c08db024e4e4626b6a7b1f818b23e385"},"frame_counts":{"client_call_precision":{"census":0,"population":5921},"client_call_recall":{"census":0,"population":6002},"registration_precision":{"census":0,"population":127},"registration_recall":{"census":0,"population":280}},"receipt_sha256":"sha256:5d2b7a01529691fb3f75b1b55a5f52fdae1ffdad970b44c53e5bc21114554546","review":{"path":"spike/t111/labeling/gate2-v2/stage2-enumeration-receipt-review-r1.md","sha256":"sha256:a44746d6b3b904d0be824775cb9aab4cc5bb48449b8e347bbfdb26c21d2a0e17","status":"accepted"},"terminal_receipt_sha256":"sha256:ad9aee01c7a024b6d4c21fbf597b8a41a4e4e95bcf234bb60a919feffaf08a5f"},"environment":{"network":"forbidden-by-authorization-not-tool-enforced","variables":{"HOME":"/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/home","LANG":"C","LC_ALL":"C","PATH":"/usr/bin:/bin"},"working_directory":"/Users/ben/phebs.com/spike/t111/labeling/gate2-v2"},"exit_contract":{"0":"all-twelve-power-cells-feasible","1":"capacity-infeasible","2":"admission-or-input-refusal","3":"output-collision","4":"unexpected-failure","no_retry":true,"signal_or_shell_failure":"terminal-no-retry"},"implementation":{"review":{"accepted_commit":"a211ed37008daf1a2d62fe07e31f7646daa207f1","format":"independent-prose-acceptance","implementation_commit":"d1301ee6d660cd9819a0e295cc6b4beeaf78d19d","path":"spike/t111/labeling/gate2-v2/stage2-prepare-review-r3.md","request_blob_sha256":"sha256:00e67931eec23625dbe182ffcf039bf35537164d5c3387467b20cdff9ff2111a","sha256":"sha256:6fe8fbb5c5ade81c5579ef5105e9a8b42f0b0774ea59165b96118d9314e067c2","status":"accepted"},"script":{"path":"spike/t111/labeling/gate2-v2/stage2_prepare.py","sha256":"sha256:1e64fddc20756604ce12f636dfd96ab01f3b4dd123f478ae196902b8709059f4"},"test":{"path":"spike/t111/labeling/gate2-v2/stage2_prepare_test.py","sha256":"sha256:ea677860be674889fa1ba11485b8417badc10f1c1687f7c3fb0f810cc15a8239"}},"inputs":{"burn_ledger":{"path":"/Users/ben/phebs.com/spike/t111/labeling/burn-ledger.json","sha256":"sha256:4e6e2382361f1a0223562d4cbac921f39944ceb36c916912fa0ca5c259e3044a"},"cardinalities":{"path":"/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/cardinalities.json","sha256":"sha256:db2f702f14f8bf62534d0c4eb0abb9b5d83e8ddbb0431b45b4e836739d5baf90"},"design":{"path":"/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-design.json","sha256":"sha256:214cfdda24ea9cfff3159519b8faf5dba9d8af7103304b1e58392daf569300d2","values":{"client_call_precision":{"p":"995/1000","threshold":"98/100"},"client_call_recall":{"p":"95/100","threshold":"90/100"},"per_fixture_precision":{"p":"97/100","threshold":"90/100"},"registration_precision":{"p":"995/1000","threshold":"98/100"},"registration_recall":{"p":"95/100","threshold":"90/100"}}},"frame_membership":{"path":"/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/frame-membership.json","sha256":"sha256:9bdabf9567d2732db23444d55072ee8503dd6ebb2838818e4d80a621f0bb4b3e"},"heads":{"old_commit_rule":{"binding":"sha256:86d0a76a510ecd99e6be939132bf9efc46c96e0c314583fde2f9e6d72d865e16","lineage_path":"spike/t111/labeling/expansion-lineage.json","lineage_sha256":"sha256:9c821523db05bf959f9e01e05529d18bfede1f3c505d54019fb9c33d602dc430"},"path":"/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-heads.json","sha256":"sha256:80b1b6501e9d18bf832657df8e978565f14f29f62435f7740bd4ce98689bad9b","values":{"dapr":{"new_commit":"f4d431123309a2bd11fcc32523661b6b14e8462b","old_commit":"08aebd8b2effa2ed939ad5531e25ff8b21a36ef1","repo_dir":"/Users/ben/phebs.com/spike/t111/corpus/dapr"},"loki":{"new_commit":"562a762ab1d07985edc561920d74e792f4a6aab9","old_commit":"1362d2770ee2abba5e130d67cf30bcc4eefa0da0","repo_dir":"/Users/ben/phebs.com/spike/t111/corpus/loki"},"online-boutique":{"new_commit":"9a4616e77f0f9cbcbecaf27d711c38890dda1404","old_commit":"9a4616e77f0f9cbcbecaf27d711c38890dda1404","repo_dir":"/Users/ben/phebs.com/spike/t111/corpus/online-boutique"},"temporal":{"new_commit":"f95c865cc08c1ac075a709d525977e17103e6417","old_commit":"8224a5375112079ad905c4ea829420306431462c","repo_dir":"/Users/ben/phebs.com/spike/t111/corpus/temporal"}}},"stage1_receipt":{"path":"/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage1/receipt.json","sha256":"sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef"}},"invocation":{"argv":["/usr/bin/python3","/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2_prepare.py","--ledger","/Users/ben/phebs.com/spike/t111/labeling/burn-ledger.json","--heads","/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-heads.json","--cardinalities","/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/cardinalities.json","--frame-membership","/Users/ben/.local/share/t111-gate2-v2-enum-4a01935-02-ceremony/output/frame-membership.json","--design","/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage2-preparation-design.json","--receipt","/Users/ben/phebs.com/spike/t111/labeling/gate2-v2/stage1/receipt.json","--out","/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/output"],"synthetic":false},"output_contract":{"files":{"census-v2-seed.jsonl":"one-sort_keys-json-object-per-line-with-trailing-lf","stage2-preparation.json":"sort_keys-indent-2-with-trailing-lf"},"publication":"complete-two-file-staging-directory-fsync-and-atomic-rename","result_schema":"t111-gate2-v2-stage2-preparation-v3","selection_authorized":false},"protocol":{"design_sections":["5","10"],"path":"spike/t111/labeling/GATE2-V2.md","sha256":"sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65"},"schema":"t111-gate2-v2-stage2-preparation-authorization-v1","scope":{"allowed":["carry-forward","exact-power-preparation"],"forbidden":["enumeration","selection","coordinate-disclosure","labeling"],"gate_status":"PENDING"},"serializer":{"ensure_ascii":false,"separators":[",",":"],"sort_keys":true,"trailing_lf":true},"state":{"ceremony_directory":"/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony","home":"/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/home","output_dir":"/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/output","preflight":"entire-namespace-absent-then-operator-creates-0700-parent","stderr_log":"/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/stderr.log","stdout_log":"/Users/ben/.local/share/t111-gate2-v2-prep-a211ed3-01-ceremony/stdout.log"},"status":"AUTHORIZED","toolchain":{"git":{"fire_time_reverify":true,"launcher":"/usr/bin/git","launcher_sha256":"sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818","resolved_executable":"/Library/Developer/CommandLineTools/usr/bin/git","resolved_sha256":"sha256:be4afb2b003904725826250de9fb76567bbacf82323457b5a1ec26706b66bcae","version":"git version 2.50.1 (Apple Git-155)"},"python":{"fire_time_reverify":true,"launcher":"/usr/bin/python3","launcher_sha256":"sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818","resolved_executable":"/Library/Developer/CommandLineTools/Library/Frameworks/Python3.framework/Versions/3.9/bin/python3.9","resolved_sha256":"sha256:bdea59019a38eb6600cc9e71e984a97fedadc406448431281e7657030f54987e","version":"3.9.6"}}}
```

## Sealed serializer and two-phase promotion

Canonical heads, design, and future authorization bytes use exactly:

```python
json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n"
```

The tracked heads and design files already use those bytes. No `jq`, pretty
printer, or hand-edited canonical authorization is permitted.

1. Independently review this worksheet, the two canonical tracked inputs,
   every digest, the clean source-history checks, the exact design
   transcription, fresh namespace, and authority-model caveat. This step
   grants no execution authority.
2. If accepted, materialize
   `stage2-preparation-authorization.json` using the sealed serializer and
   exact field contract above. Rehash every bound tracked/external input and
   stop on any drift.
3. In the same promotion commit, add the sole strict PLAN row:

   `| <date> | GATE2-V2 Stage-2 preparation authorization | AUTHORIZATION: APPROVED | authorization_id=t111-gate2-v2-prep-a211ed3-01;authorization_sha256=sha256:9164f8b4d671517349c82a4bd92306602e5adb2b7243fba9ad483870092d4ea8 |`

4. Obtain separate operator fire-time approval, verify the complete ceremony
   namespace is absent, create only its `0700` parent, and launch exactly once.

Until all four steps complete, preparation, selection, disclosure, and
labeling remain blocked. `gate_status` remains `PENDING`.

## Independent-review checklist

- [ ] Accepted code/test/review bytes recomputed at `a211ed3`
- [ ] Prose authority model explicitly accepted or worksheet rejected for a
      separate strict-anchor repair
- [ ] Enum-02 authorization, receipts, review, and both input digests verified
- [ ] Design has exactly five consumed entries and exact §5/§10 rationals
- [ ] Heads file matches the four sealed old/new commits and absolute mirrors
- [ ] Carry-forward receipt binding and burn-ledger digest independently
      recomputed
- [ ] All four repositories remain non-shallow with both objects and
      old→new ancestry
- [ ] Python/Git identities and clean environment verified
- [ ] Embedded 10,314-byte candidate reserializes byte-identically and hashes
      to `sha256:9164f8b4d671517349c82a4bd92306602e5adb2b7243fba9ad483870092d4ea8`
- [ ] Fresh ID-keyed ceremony namespace confirmed absent
- [ ] No canonical authorization or strict approval row exists during review
- [ ] No preparation, selection, coordinate disclosure, or ceremony mutation
      occurred during drafting/review
