# T42 frozen-plan review package

Standing (2026-10-09): this document assembles the **frozen-plan review
procedure and today-bindable evidence** for the T42 combined-scale ceremony
freeze. No authenticated freeze exists at this commit: there is no sealed V5
plan artifact, no freeze envelope, no freeze signature, and no selected
ceremony identifier. Nothing here is, or substitutes for, the authenticated
freeze or its independent review. Sections 1–2 bind what is executable today;
sections 4–6 are the procedure a future freeze must pass; section 7 is the
ledger of open bindings; section 8 lists the nonclaims. A 2026-10-10
correction applies the independent review's findings on this package
(0 critical, 0 high, 1 medium, 3 low); the standing nonclaims and every
open-binding status are unchanged.

The governing acceptance contract (`docs/BACKLOG.md`, T42.2o ticket row,
verbatim):

> **T42.2o · V3 seal and exact-main freeze** — canonical author/seal after full
> acceptance, reviewed integration, exact-main preflight, real host/tool/profile
> admission and authenticated freeze. AC: independent signature/byte replay,
> retained V1/V2 unchanged, exact invocation and custody/expiry handoff; no
> ceremony execution, Epic closure, release or scale claim.

The governing record (`docs/BACKLOG.md`, 2026-09-21, verbatim):

> **T42.2o canonical V4 plan seal, 2026-09-21:** exact clean source
> `f0809ebbeee70c8a1fc8df67c7cb253224120c18` authored
> `spike/t421/plan-v4.json` at 171,630 bytes and
> `sha256:acfa4a98ba025d580ef1c3153e5929745b2807b2a9e8a4c6ae2ae819902daeb8`;
> a second clean worktree was byte-identical and retained V1/V2 artifacts remain
> exact. Canonical V4 plan author/seal is complete. Reviewed integration of this
> record, exact-main preflight, live host/tool/profile admission, authenticated
> freeze/signature replay, custody/expiry handoff, and frozen-plan review remain
> open. No execution, Epic closure, release or scale/SLO claim follows.

All `spike/t421/*.go` line numbers cite this repository at branch
`codex/t42.2o-frozen-plan-review`, commit `55a962e9` (before this package's own
commit). The host is the 2026-10-06 baseline: Ubuntu Linux amd64, observed
physical memory 15,991,791,616 bytes, native toolchain `go version go1.26.5
linux/amd64` (`/home/ben/.local/bin/go`).

## 1. Bound artifact identities (executed 2026-10-09)

Executed in this checkout on 2026-10-09:

```text
$ sha256sum plan.json plan-v2.json plan-v4.json
96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34  plan.json
2275b8cadca8f4e76a46db6d943380d1533a41da70a71c7009850e2c0229b422  plan-v2.json
acfa4a98ba025d580ef1c3153e5929745b2807b2a9e8a4c6ae2ae819902daeb8  plan-v4.json
$ wc -c plan.json plan-v2.json plan-v4.json
199561 plan.json
262140 plan-v2.json
171630 plan-v4.json
633331 total
```

| artifact | bytes | sha256 | recorded pin |
| --- | --- | --- | --- |
| `spike/t421/plan.json` (V1) | 199,561 | `96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34` | compiled-in `retainedPlanSHA256` (`spike/t421/contract_correction.go:11`) |
| `spike/t421/plan-v2.json` (V2) | 262,140 | `2275b8cadca8f4e76a46db6d943380d1533a41da70a71c7009850e2c0229b422` | compiled-in `retainedPlanV2SHA256` (`spike/t421/accounting_contract.go:26`) |
| `spike/t421/plan-v4.json` (V4) | 171,630 | `acfa4a98ba025d580ef1c3153e5929745b2807b2a9e8a4c6ae2ae819902daeb8` | documented only (see below) |

Honest note: V1 and V2 are pinned in compiled code; the V4 digest appears only
in retained documentation (`PLAN.md`, `docs/ROADMAP.md`, `docs/BACKLOG.md`
T42.2o record, `spike/t421/README.md`). There is no compiled-in V4 digest
constant at this commit. The future V5 seal (section 3) is the natural place to
add one for the active lineage; this package changes no code.

Integration state (verified 2026-10-09):

- The V4 seal commit `61d3c9d1` ("t42.2o seal canonical v4 plan", parent
  `f0809ebb`) added `spike/t421/plan-v4.json` and the BACKLOG record together.
  `git merge-base --is-ancestor 61d3c9d1 origin/main` passes at `origin/main`
  tip `55a962e9`; the artifact and record are observable in the integration
  history.
- At freeze time the reviewer re-proves that ancestry mechanically instead of
  trusting this note (section 5, item 9).

## 2. Linux cross-host re-author replay (executed 2026-10-09)

The 2026-09-21 V4 seal was authored on Darwin with the host compiler, SDK, and
Apple Git selected explicitly (`spike/t421/README.md`, V4 seal section). The
2026-10-06 host baseline makes this Linux amd64 machine the basis for every
future rehearsal and ceremony. This replay answers one bounded question: does
the unchanged create-only author at the exact recorded source commit produce
the byte-identical V4 artifact on linux/amd64 with the native toolchain? Yes.

Exact script (executed):

```bash
set -euo pipefail
base=$(mktemp -d /tmp/t422-v4-linux-replay.XXXXXX)
echo "REPLAY_BASE=$base"
echo "$base" > /tmp/t422-v4-linux-replay.path
git worktree add --detach "$base/wt" f0809ebbeee70c8a1fc8df67c7cb253224120c18
cd "$base/wt"
echo "--- worktree status (must be empty) ---"
git status --porcelain=v1 --untracked-files=all
echo "--- author ---"
time go run ./spike/t421/cmd/author -schema v4 -repository-root "$base/wt" \
  -source-commit f0809ebbeee70c8a1fc8df67c7cb253224120c18 -out "$base/plan-v4.json"
echo "--- produced identity ---"
sha256sum "$base/plan-v4.json"
wc -c "$base/plan-v4.json"
echo "--- byte comparison against sealed in-tree artifact ---"
if cmp "$base/plan-v4.json" /home/ben/phebs/spike/t421/plan-v4.json; then
  echo "BYTE-IDENTICAL: yes"
else
  echo "BYTE-IDENTICAL: NO"; exit 1
fi
echo "--- source checkout untouched (post-author) ---"
git status --porcelain=v1 --untracked-files=all
echo "REPLAY_OK"
```

Verbatim output:

```text
REPLAY_BASE=/tmp/t422-v4-linux-replay.KNqgCM
Preparing worktree (detached HEAD f0809ebb)
HEAD is now at f0809ebb fix(ci): screenshot job, wider race coverage, loud test skips (#13)
--- worktree status (must be empty) ---
--- author ---
T42.1 source-free plan: bytes=171630 sha256=sha256:acfa4a98ba025d580ef1c3153e5929745b2807b2a9e8a4c6ae2ae819902daeb8

real	1m58.120s
user	4m18.282s
sys	0m14.880s
--- produced identity ---
acfa4a98ba025d580ef1c3153e5929745b2807b2a9e8a4c6ae2ae819902daeb8  /tmp/t422-v4-linux-replay.KNqgCM/plan-v4.json
171630 /tmp/t422-v4-linux-replay.KNqgCM/plan-v4.json
--- byte comparison against sealed in-tree artifact ---
BYTE-IDENTICAL: yes
--- source checkout untouched (post-author) ---
REPLAY_OK
```

Cleanup proof (verified 2026-10-09): the replay worktree was removed with
`git worktree remove`, the registry pruned with `git worktree prune`, and the
private temporary root deleted. `git worktree list` no longer references the
replay (10 entries remain, all pre-existing review worktrees owned by other
sessions); no `/tmp/t422-v4-linux-replay.*` path exists; and `git status
--porcelain --untracked-files=all` in the main checkout shows only the
pre-existing untracked root `package-lock.json`, which predates and does not
belong to this work.

Notes for the reviewer:

- `f0809ebb`'s subject line ("fix(ci): screenshot job, wider race coverage,
  loud test skips (#13)") is misleading for this purpose, but it is the exact
  recorded V4 authoring commit from the 2026-09-21 seal; the replay used that
  exact object.
- The author is create-only and source-free: it reads the repository tree but
  the artifact embeds no source, path, or content. The replay produced a fresh
  artifact at a fresh path and compared bytes; it mutated no tracked file
  (post-author worktree status stayed empty).
- This is a reproducibility observation on the Linux baseline, not a ceremony
  pass, not an admission, and not a freeze. It supersedes nothing: the
  Darwin-authored artifacts remain the canonical sealed bytes.

## 3. Active schema vs sealed artifacts

Two independent version axes exist; do not conflate them:

- **Sealed plan artifacts**: V1 (`plan.json`), V2 (`plan-v2.json`), V4
  (`plan-v4.json`). All three bytes are bound in section 1 and remain exact.
- **Active plan schema**: `activeExecutionPlanSchema = PlanV5Schema`
  (`spike/t421/accounting_contract.go:103`), constructed by `BuildPlanV5`
  (`accounting_contract.go:86`, the V4 contract plus the caller
  restore-continuity correction). Plan schema constants:
  `PlanV3Schema = "t421-combined-gate-plan-v3"`, `PlanV4Schema =
  "t421-combined-gate-plan-v4"`, `PlanV5Schema = "t421-combined-gate-plan-v5"`
  (`spike/t421/model.go:20–22`). No sealed V5 artifact exists at this commit.

Freeze binding: `processAccountingFreezeSchema` (`accounting_contract.go:132–143`)
maps a plan schema to the one freeze schema it may bind —
`PlanV3Schema → "t422-combined-execution-freeze-v3"`,
`PlanV4Schema → "t422-combined-execution-freeze-v4"`,
`PlanV5Schema → "t422-combined-execution-freeze-v5"` (constants at
`accounting_contract.go:20–22`), anything else refused. The candidate
assembler requires V3-or-later semantics and the exact declared schema
(`spike/t421/freeze.go:179–183`), and the envelope carries the plan-declared
schema (`freeze.go:222`); validation and decode re-prove the same binding
(`freeze.go:300`, `freeze.go:408`). The base constant
`ExecutionFreezeSchema = "t422-combined-execution-freeze-v1"`
(`freeze.go:18`) belongs to the base plan builder (`plan.go:1082`) and cannot
be produced by the controlled-dispatch candidate path.

Consequence: the next canonical author is the `-schema v5` analogue of the
recorded `-schema v4` command (the author accepts v2/v3/v4/v5,
`spike/t421/cmd/author/main.go:45,62`). That invocation belongs to T42.2o's
later seal step after its remaining bindings (section 7) close. This package
authors no plan artifact and does not predict the V5 digest.

## 4. Freeze-envelope review procedure

### 4.1 Envelope contract inventory

The freeze envelope (`spike/t421/freeze.go`):

- Wire schema field and byte bound: envelope `Schema` must equal the plan's
  declared freeze schema, `MaxExecutionFreezeBytes = 64 << 10`
  (`freeze.go:19`; enforced on candidate build at `freeze.go:203`, on
  canonical encoding at `freeze.go:365`, and on decode at `freeze.go:431`).
- Fields: `ExecutionFreeze{Schema, PlanSHA256, SignerFingerprint,
  SignerNamespaceSHA256, Commits, DigestAlgorithm, Tools, Host, Profile,
  Pressure}` (`freeze.go:29–40`, source-free by construction). Commits carry
  the seven ancestry/cleanliness facts (`freeze.go:45–51`) — the envelope cannot
  authorize its own Git ancestry: `CheckoutAdmissionBinding`'s fields are
  private and verified elsewhere (`freeze.go:71–75`).
- Tool identities: `ExecutionToolIdentity` (`freeze.go:54–66`) with the
  closed provenance classes in `validateExecutionTools` (`freeze.go:453–499`):
  digest algorithm must be `sha256-executed-regular-file-v1` (`freeze.go:454`);
  repository-built roles (`phebs`, `phebs-focused-index`, `t422-author`,
  `t422-execute`) use `go-build-info-vcs-v1` with `BuildVCSRevision` equal to
  the T422 source commit (`freeze.go:481`); `buf` uses `go-module-build-v1`
  with a recorded recipe digest (`freeze.go:484`); `zoekt-git-index` uses
  `go-module-build-v1` or the zoekt offer provenance (`freeze.go:476–479`);
  every other role uses `external-executed-file-v1` with empty VCS fields
  (`freeze.go:492`). `FreezeBeforeExecution` must be set and the inventory
  must equal the eleven `policy.RequiredTools` roles (`plan.go:1096–1099`):
  `buf`, `git`, `go`, `hdiutil`, `phebs`, `phebs-focused-index`,
  `ssh-keygen`, `surreal`, `t422-author`, `t422-execute`,
  `zoekt-git-index`.
- Host admission: `validateExecutionHost` (`freeze.go:509–543`). **Current
  code refuses every host that is not darwin/arm64** (`freeze.go:510`). A
  Linux freeze is therefore not constructible at this commit; that refusal is
  the tracked implementation debt of T42.H2 (section 7, binding 3). The V4
  tool inventory names `hdiutil` (Darwin-only) and its pressure volume
  preparation string is
  `hdiutil_sparse_apfs_exact_96_gib_on_admitted_backing_volume;…`
  (`plan.go:1086,1097`), so a Linux-bound plan version must record the
  replacement semantics; retained plans decode under their original versions.
- Pressure geometry: `ExecutionPressureGeometry` (`freeze.go:99–116`) is
  redundant by design — "every derived scalar is retained so independent
  review can recompute the admission decision exactly" (`freeze.go:99`).
  `expectedExecutionPressureGeometry` (`freeze.go:546–625`) requires the plan's
  targets exactly `[80, 90, 75]` percent (`freeze.go:551`), actions
  `add`/`remove` (`freeze.go:571,573`), dispositions `collect`/`refuse`
  (`freeze.go:577,579`), and recovery `remove` at maximum used percent 74
  (`freeze.go:618`).
- Source-bearing refusal: the closed fragment list in
  `sourceOrWorkspaceFragment` (`freeze.go:729–741`) forbids `.go`, `.proto`,
  `.thrift`, `services/`, `structural/`, `example.invalid/`, `package `,
  `func `, `syntax =`, `/users/`, `/home/`, `/private/`, `/tmp/`, `/volumes/`,
  `../`, `./`, `~/`, `$home/`, `${home}/`, `file://` in retained envelope
  strings.
- Candidate vs authority: `assembleExecutionFreezeCandidate`
  (`freeze.go:169–208`) returns canonical, detached, non-authoritative bytes;
  `BuildExecutionFreeze` (`freeze.go:139–163`) performs no filesystem or
  process discovery. The signer path re-reads held inputs and compares them to
  the canonical candidate before signing (`signer_seal_unix.go:70–72`), so a
  signature only ever covers bytes the signer itself re-derived.

### 4.2 Reviewer commands

For a future frozen envelope `freeze.json`, its signature `freeze.sig`, the
allowlist, and the exact plan artifact `plan-vN.json`:

(a) Byte and digest replay: recompute `sha256sum plan-vN.json` and compare to
envelope `PlanSHA256` and the recorded artifact digest; `wc -c` the envelope
and refuse over 64 KiB; refuse any non-canonical encoding (mechanical proof:
`TestDecodeExecutionFreezeRejectsNoncanonicalAndSourceBearing`,
`freeze_test.go:139`). Version binding: envelope `Schema` must equal the plan's
declared schema under `processAccountingFreezeSchema` (section 3).

(b) Signature replay, mirroring the signer argv
(`signer_seal_unix.go:130–134`):

```bash
ssh-keygen -Y verify -f <allowlist> -I phebs-t422-ceremony \
  -n phebs-t422-freeze -s freeze.sig < freeze.json
```

The allowlist line format is `phebs-t422-ceremony <canonical-public-key-line>`
(`signer_key_unix.go:483–502`); the signature is bounded at 4 KiB
(`returned_package.go:14`) and the private key at 1 KiB (`signer_key.go:14`).
The signer fingerprint must reach the reviewer through a separate channel
recorded at freeze time (T40 precedent: "reverified its frozen plan digest and
signature", with the fingerprint recorded via a separate reviewed channel).

(c) Host/tool/provenance replay to the classes in 4.1; any `external-
executed-file-v1` row with non-empty VCS fields, any repository-built row with
a mismatched `BuildVCSRevision`, or a tool count other than eleven refuses.

(d) Pressure recomputation: recompute every derived scalar from the retained
ones; confirm targets/actions/dispositions/recovery per `freeze.go:546–625`.

(e) Receipt/namespace binding:
`TestExecutionFreezeReceiptBindingRechecksSignerNamespace`
(`signer_namespace_unix_test.go:80`) and
`TestBindExecutionFreezeForReceiptOwnsAndValidatesFreeze`
(`receipt_test.go:745`) must pass on the frozen bytes; both also cover
candidate mutation refusal.

(f) Source-bearing scan: the closed fragment list must not appear (mechanical
decode refusal; also grep the envelope text for the fragments as a second
check).

(g) Suite: the review-time suite in 4.3 re-executes the retained-identity,
freeze-envelope, signer, pressure, and expiry-contract regression set.

### 4.3 Review-time suite

First executed 2026-10-09 at this branch tip on the baseline host; re-executed
2026-10-10 after the review correction added
`TestDecodeExecutionFreezeRejectsNoncanonicalAndSourceBearing` (the mechanical
proof cited in 4.2(a)) to the pattern. The explicit
`-timeout=60m` matches the repository's CI allowance (`ci-go`); the Go default
10-minute timeout was observed insufficient on this host (600.135 s expiry
inside the heavy combined-corpus freeze fixture), so reviewers should keep the
explicit bound:

```bash
cd /home/ben/phebs
go test ./spike/t421 -count=1 -v -timeout=60m -run 'TestCorrectionPreservesRetainedV1BytesAndValidation|TestAccountingV3RetainsHistoricalCanonicalBytes|TestAccountingV3NormalLifecyclePolicy|TestSelectorHandoffCleanupOmissionRetainsHistoricalPlans|TestSelectedCleanupWorkDerivation|TestExecutionFreeze|TestDecodeExecutionFreezeRejectsNoncanonicalAndSourceBearing|TestExecutionSignerSealsCandidateAndIssuesAdmission|TestExecutionFreezeReceiptBindingRechecksSignerNamespace|TestBindExecutionFreezeForReceiptOwnsAndValidatesFreeze|TestPressureContinuity|TestExecutionFinalAdmissionDeadlineAnchorsAtFirstVerification'
```

Captured result (re-executed 2026-10-10 at this branch tip on the baseline
host; exit 0, zero failures):

```text
--- PASS: TestAccountingV3RetainsHistoricalCanonicalBytes (0.02s)
--- PASS: TestAccountingV3NormalLifecyclePolicy (0.26s)
--- PASS: TestSelectedCleanupWorkDerivation (0.03s)
--- PASS: TestCorrectionPreservesRetainedV1BytesAndValidation (37.91s)
--- PASS: TestExecutionFinalAdmissionDeadlineAnchorsAtFirstVerification (0.00s)
--- PASS: TestExecutionFreezeIsCanonicalBoundedAndExact (112.53s)
--- PASS: TestDecodeExecutionFreezeRejectsNoncanonicalAndSourceBearing (75.34s)
--- PASS: TestExecutionFreezeCandidateIsCanonicalDetachedAndNonauthoritative (101.90s)
--- PASS: TestExecutionFreezeCandidateRejectsInputMutations (0.32s)
--- PASS: TestExecutionFreezeCandidateFullPathRefusals (344.66s)
--- PASS: TestExecutionFreezeCandidateRejectsMutatedWireBytes (158.31s)
--- PASS: TestExecutionFreezeCandidatePreservesPublicLegacyConstruction (200.90s)
--- PASS: TestPressureContinuityV4DerivationIsNarrow (0.21s)
--- PASS: TestPressureContinuityV4RequiresCompleteV3 (0.45s)
--- PASS: TestPressureContinuityV4GeometryAndHistoricalOmission (0.14s)
--- PASS: TestPressureContinuityV4CanonicalArtifactRouting (0.01s)
--- PASS: TestPressureContinuityV4FullFrozenRoundTrip (161.30s)
--- PASS: TestPressureContinuityCanonicalFreezeVersioning (373.15s)
--- PASS: TestPressureContinuityV4CandidateFreezeRoundTrip (91.55s)
--- PASS: TestBindExecutionFreezeForReceiptOwnsAndValidatesFreeze (70.81s)
--- PASS: TestSelectorHandoffCleanupOmissionRetainsHistoricalPlans (0.15s)
--- PASS: TestExecutionFreezeReceiptBindingRechecksSignerNamespace (163.10s)
--- PASS: TestExecutionSignerSealsCandidateAndIssuesAdmission (103.50s)
ok  	github.com/bmeddeb/phebs/spike/t421	1996.692s
```

## 5. Absence-proof checklist

The T40 precedent proves, as a separate read-only step before acceptance, "no
surviving process, listener, holder, mount, custody, supervision, driver, or
bootstrap" plus a re-proof of the frozen plan digest and signature
(`AGENTS.md`, the T40.13u closure and neutral-40 custody records). A T42
freeze review repeats the same eight
nouns on this Linux host, with bounded observations:

1. **process** — a bounded process observation (for example `pgrep -af` over
   the ceremony's executable identities: `phebs`, `surreal`, `t422-author`,
   `t422-execute`, `zoekt-git-index`, the rehearsal driver). Every match must
   be attributable; ceremony-owned matches must be zero. Ordinary development
   servers are distinguishable by their run roots and must be named in the
   review record rather than ignored.
2. **listener** — no listener remains on the ceremony's recorded port (T40
   rehearsals recorded port 65499; a T42 freeze names its own). Check with
   `ss -ltn` plus a targeted probe.
3. **holder** — no process holds the run-root operation lock. The Phase-12
   teardown rehearsal already exercises lock lifetime and release after the
   simulated Execute return; the review re-verifies on the live host (T40
   precedent lock name `.t4013-operation.lock`; T42's custody names its own).
4. **mount** — no rehearsal pressure volume remains mounted beneath the
   ceremony root (`findmnt`/`mount`). Darwin precedent: the exact 96-GiB
   sparse APFS pressure image named by the V4 plan's preparation string
   (`plan.go:1086`). The Linux substitute primitive is owned by
   T42.H2's remaining pressure-filesystem slice; this package does not invent
   it, and the review checks whatever custody the frozen plan names.
5. **custody** — derived/scratch custody is absent, or retained and named with
   justification (T40 neutral-40 precedent: the whole custody directory was
   removed only after Ben's explicit approval following a separate read-only
   proof; signed evidence, prepared authority, supervision state, operation
   lock, ceremony root, and signing key remained).
6. **supervision** — no surviving supervisory session or descendant
   supervisor (T42.H2f lineage; Darwin-era rehearsals joined both launcher
   sessions).
7. **driver** — no rehearsal driver process survives.
8. **bootstrap** — no bootstrap process survives.
9. **clean checkout** — the frozen source checkout is porcelain-empty at the
   exact commit, and every ancestry fact carried in `ExecutionCommits`
   (`freeze.go:45–51`) re-proves mechanically (`git merge-base
   --is-ancestor`, tree hashes, clean-tree proof).

## 6. Custody/expiry handoff checklist

At freeze time, in order:

1. **Exact invocation recorded**: the canonical author/seal invocation (the
   `-schema v5` form of the recorded `-schema v4` command, section 3) and the
   freeze build/sign invocation, captured verbatim with the producing commit.
   Sign argv shape: `ssh-keygen -Y sign -f <key> -n phebs-t422-freeze`
   with the canonical candidate bytes on stdin and the signature on stdout
   (`signer_seal_unix.go:74–78`); the signer environment is closed
   (`HOME/TMPDIR/TMP/TEMP` = owner path, `PATH=/usr/bin:/bin`, `LANG/LC_ALL=C`,
   `TZ=UTC`) with a 30-second command budget, 4 KiB stdout and 4 KiB stderr
   bounds (`signer_key_unix.go:126–156`).
2. **Candidate detached before authority**: the signed bytes are the
   canonical candidate re-derived by the signer from its held inputs
   (`signer_seal_unix.go:70–72`); the candidate carries no binding, signature,
   ordinal, or operational authority (`freeze.go:166–168`).
3. **Retention bounds**: envelope ≤ 64 KiB, signature ≤ 4 KiB, key ≤ 1 KiB.
4. **Identifier discipline**: the ceremony identifier is selected only at
   freeze and is permanently consumed thereafter (T40 precedent: neutral-40
   custody purged only under explicit approval; neutral-41 executed and
   sealed; neutral-43 retired with a source-free stop summary at
   `spike/t4013/t4013x-neutral43-authorized-query-stop.json`). Never reuse a
   consumed identifier.
5. **Expiry handoff**: `FinalAdmissionDeadlineUnixNano` is anchored at first
   verification and clipped by the outer deadline
   (`execution_inner_handoff.go:10`), carried in the authorization handoff
   (`execution_authorization_handoff_darwin.go:24–30,97`), and honored at the
   launch handoff (`execution_launcher_handoff.go:198`). Launch refuses when
   the deadline is after the outer deadline or already passed, and re-checks
   `binding.planSHA256` against the frozen plan bytes
   (`epoch_launch_darwin.go:110–129`). Expiry is terminal: "That path always
   fails; it is not an extended acceptance budget."
   (`epoch_teardown_darwin.go:35`). Behavioral pin:
   `TestExecutionFinalAdmissionDeadlineAnchorsAtFirstVerification`
   (`execution_inner_handoff_test.go:12`).
6. **Custody handoff recorded**: envelope, signature, allowlist, signer
   fingerprint provenance, and the exact invocation are handed to the
   independent reviewer together, with custody ownership and expiry named.

## 7. Open-bindings ledger

The 2026-09-21 record's six open clauses, mapped to state, evidence, and
closing owner as of 2026-10-09:

| # | binding (record clause) | state 2026-10-09 | what closes it / owner |
| --- | --- | --- | --- |
| 1 | Reviewed integration of this record | Artifact and record observable in integration history: seal commit `61d3c9d1` (parent `f0809ebb`) is an ancestor of `origin/main` `55a962e9` (verified; section 1). The clause is ticked again mechanically at freeze time (section 5 item 9) rather than by trusting this note. | T42.2o continuing step; reviewer re-proves ancestry |
| 2 | Exact-main preflight | Open; not attempted for T42 on this host. T40 precedent shape: clean-checkout, toolchain, memory, ports, and module checks against the exact committed-and-pushed main to be frozen (T40 also projected the frozen pressure minimum against available space — the T42 projection derives from the frozen V5 contract and is not predicted here). | T42.2o continuing step on exact main |
| 3 | Live host/tool/profile admission | Open, with code-level blockers named: `validateExecutionHost` refuses non-Darwin hosts (`freeze.go:509–510`) and the V4 inventory is Darwin-bound (`hdiutil`, `plan.go:1086,1097`). T42.H2 owns the port: integrated slices H2a (native process accounting), H2b (sealed direct-input custody), H2c (direct-tool custody and object binding), H2d (signer and namespace custody), H2e (shared Git/Go child-probe observation), H2f (session and descendant hard-death supervision), H2g (Git exec-path helper manifest and Go SDK location recipes); remaining per the H2g status row: "The Linux immutable-flag input-custody model, isolated pressure/allocation/restore adapters, new Linux-bound plan versions and aggregate physical/effective-cgroup resource admission remain H2 prerequisites before complete readiness, freeze or execution" (`docs/BACKLOG.md:3796–3800`; the same list recurs through the H2 ladder at `3416–3419`, `3481–3485`, `3597–3602`). The parent ticket's work plan reads "isolated pressure filesystem/allocation/restore semantics; then prospective Linux-bound plan construction, validation, admission, and readiness replay" — "a new version records the changed Linux execution semantics" (T42.H2, `docs/BACKLOG.md:3090–3091,3100–3102`). | T42.H2 remaining slices |
| 4 | Authenticated freeze/signature replay | Open. Prerequisites per BACKLOG rows: T42.2l (real author/executor, full private admission), T42.2m (signed launcher, custody closure), T42.2p (signed V4 pressure-interphase allowance, no carry-forward), renewed T42.2n (exact-tree acceptance). Then the V5 author/seal, freeze build, and signer seal per sections 3–4, replayed with the reviewer commands in 4.2. | T42.2o after 1–3 and the l/m/p/n chain |
| 5 | Custody/expiry handoff | Open. Procedure pinned in section 6; executed only when the freeze exists. | T42.2o execution-time step |
| 6 | Frozen-plan review | Procedure ready: this package supplies the envelope procedure (4), the absence checklist (5), the handoff checklist (6), and today-bindable identities (1–2). The review itself requires the freeze inputs (frozen plan digest, envelope, signature, signer fingerprint), which do not exist yet. | Independent reviewer after 3–5 |

Acceptance-contract correspondence (T42.2o AC): "independent signature/byte
replay" is section 4.2; "retained V1/V2 unchanged" is section 1 (fresh digests
plus the compiled-in pins and the retained-identity tests in 4.3); "exact
invocation" is sections 2 and 6.1; "custody/expiry handoff" is section 6; "no
ceremony execution, Epic closure, release or scale claim" is section 8.

## 8. Nonclaims

- No freeze envelope, signature, or authenticated freeze exists; no ceremony
  identifier is selected; no V5 plan artifact is authored or sealed by this
  package.
- The Linux replay (section 2) is a bounded reproducibility observation on the
  new host baseline. It is not a ceremony pass, an admission, a freeze, or a
  supersession of the Darwin-authored canonical artifacts.
- The review-time suite (section 4.3) is regression evidence at this branch
  tip, not a review verdict on any future frozen bytes.
- No ceremony execution, no Epic 42 closure, no release, and no scale/SLO
  claim follows from anything here. `GATE2-V2` remains `NOT_ESTABLISHED` and
  `DO_NOT_RELEASE` remains unchanged.
- T42.H2 applies verbatim: "This ticket authorizes preparation and
  implementation, not a ceremony launch, a fabricated past PASS, or a scale or
  release claim."
