# T11.1 Gate 2 benchmark protocol

The committed `sites*.jsonl`, `g34.*.jsonl`, and `g3h2.*.jsonl` files are
legacy v1 artifacts retained only as disclosed development history. The old
Gate 2 used an enriched case-control sample and cannot estimate
eligible-population recall; the old Gate 3/4 sample also does not establish the
ticket criteria. Do not cite those scores as gate results or migrate their
labels into a new holdout.

No Gate 2 pass is recorded by this document. A result remains
`NOT ESTABLISHED` until the protocol below has been completed, the canonical labels
have been publicly anchored, and the fail-closed scorer establishes every
threshold.

## Disclosed coordinates are a permanent census

All 700 legacy coordinates remain immutable audit history in
`burn-ledger.json`. Disclosure follows source content across commits; refreshing
a fixture revision does not make a previously seen occurrence blind again.
The preparation harness resolves disclosed source into the current locked
trees and puts every carried occurrence in `sites.census.jsonl`:

- unchanged content is carried at every current placement, including a rename
  or an identical copied blob;
- a uniquely translatable occurrence in modified content is carried to its
  current span; and
- when exact translation is not unique, the affected current path is included
  conservatively as census rather than admitted to dev or holdout.

Every census unit is labeled and counted exactly in the full-population
numerator and denominator. Census, dev, and holdout IDs must be disjoint. An
unresolved mapping, missing locked object, or census overlap fails closed; it
never restores eligibility for blind sampling.

## What Gate 2 measures

Gate 2 v4 measures two reference families independently:

- Client calls: an extractor-independent typed recall frame over Go call
  expressions whose statically resolved receiver implements a discovered gRPC
  client interface, plus an exhaustive probability-one fresh holdout containing
  every emitted `CALLS_OPERATION` fact. Previously disclosed facts remain in
  the permanent census. Package/type-load gaps fail closed.
- Server registrations: an extractor-independent lexical recall frame over
  call-shaped `Register<Name>Server(...)` sites and direct
  `server.RegisterService(...)` sites, plus a precision frame containing every
  emitted `IMPLEMENTS_SERVICE` fact.

Gate-eligible calls require an `exact` or `derived` fact with a canonical
`package.Service/Method` identity. Heuristic `/?.Service/Method` facts are
bound and counted as disclosed abstention debt but are excluded from precision,
recall alignment, role sampling, and future consumer edges.

For registration labels, `IMPLEMENTS_SERVICE` means application wiring pins a
concrete implementation. A generated `RegisterXServer` wrapper's internal
forwarding call is a hard negative, while a hand-written direct
`RegisterService(&Desc, impl)` wiring call is eligible.

A fifth frame samples emitted references across `production`, `test`, `mock`,
`generated`, and `vendor`. Expected roles are derived from source only with the
fixed precedence `vendor > mock/fake > generated > test > production`;
the test path signal is `_test.go` or an exact `tests`, `testing`, or `testdata`
segment. This taxonomy is supplied verbatim to reviewers.
extractor role output remains hidden until scoring. Every sampled reference
requires an exact role match.

| Frame | Fresh holdout | Development | Gate treatment |
|---|---:|---:|---|
| Client-call recall | up to 200 per fixture, stratified | up to 120 per fixture | finite-population bounds |
| Client-call precision | exhaustive (`pi = 1`) | 0 | exact enumeration |
| Registration recall | exhaustive (`pi = 1`) | 0 | exact enumeration |
| Registration precision | exhaustive (`pi = 1`) | 0 | exact enumeration |
| Source role | up to 20 per role | up to 10 per role | every selected role must match exactly |

The manifest binds the exact corpus lock, every fixed Gate 2 commit, declared
gitlink exclusions, Dapr's `unit` build tag, fact files, extractor
configuration, burn resolution, typed-call oracle, and sampling design. Any
undeclared, missing, or changed input fails closed.

## Exact local toolchain

Preparation and reconstruction must resolve the same producer toolchain. On
the benchmark machine, Go must be Go 1.26.5 at the following exact `GOROOT`,
and its `bin` directory must be first on `PATH`. `/usr/bin` follows it so the
producer-bound Apple Git is selected. Do not allow Go's toolchain downloader to
substitute another binary.

```sh
export GOROOT=/opt/homebrew/Cellar/go/1.26.5/libexec
export PATH="$GOROOT/bin:/usr/bin:/bin"
export GOTOOLCHAIN=local

test "$(go env GOROOT)" = "$GOROOT"
go version        # go version go1.26.5 darwin/arm64
/usr/bin/git --version
```

The harness verifies the executable versions and SHA-256 digests recorded by
the fact producer, so a different binary reporting the same version is not
interchangeable. Use a Python version that supports the harness without
changing the tool lookup above; on this machine the commands below use
`/opt/homebrew/bin/python3` explicitly.

## 1. Commit inputs before randomness

Run the coordinate-free preflight once:

```sh
/opt/homebrew/bin/python3 spike/t111/label_prep.py commit-inputs \
  > /private/tmp/t111-gate2-input-commitment.json
```

`commit-inputs` emits population counts, census counts, provenance, the
seed-independent power analysis, and an `input_binding`. It emits neither
labeler coordinates nor hidden extractor outcomes. It also creates the local
commit-set attempt claim. Do not request randomness unless
`power_analysis.attainable` is `true`; that is a design-capacity result, not a
Gate 2 pass.

The `t111-gate2-input-commitment-v3` frame strata intentionally omit realized
development allocation fields. Its development-site ceiling is derived only
from the fixed quotas and fixed post-holdout frame capacities, so the exact
commitment bytes reconstruct identically for every later public seed.

The fixed client-call precision quota is an exhaustive-population sentinel of
1,000,000 sites per fixture. It exceeds every committed fresh precision
population, so every such site has holdout inclusion probability 1 and no
precision-frame site remains for development allocation. The exact population,
sample size, inclusion probability, and sampling configuration are all bound
into the input commitment. Preflight rejects either client-call or registration
precision if any fresh stratum is not exhaustively held out, so the sentinel
cannot silently degrade into sampling.

The Gate 2 v4 protocol commitment's `committed_at` is a scheduled activation
time at least 30 minutes in the future. Before that activation, publish the
exact commitment document as a new public GitHub Gist and verify its initial
revision:

```sh
gh gist create --public /private/tmp/t111-gate2-input-commitment.json

/opt/homebrew/bin/python3 spike/t111/label_prep.py github-receipt \
  --document /private/tmp/t111-gate2-input-commitment.json \
  --api-url https://api.github.com/gists/<gist-id> \
  --output /private/tmp/t111-gate2-input-github-receipt.json
```

The verifier binds GitHub's server `created_at`, the initial 40-hex revision,
and the exact one-file document bytes. Do not edit or delete the Gist.

The lead time makes the ordering independently visible:

1. generate the exact input commitment with its future activation;
2. publish its initial GitHub revision before activation; and
3. after activation, use the first subsequent one-minute NIST Beacon pulse.

The local O_EXCL attempt claim prevents accidental reuse but is not the public
precommitment. If the commitment is not publicly anchored before activation,
or the first eligible pulse is missed, abandon the attempt rather than select a
later pulse.

## 2. Supply the v3 audit seed

The preferred receipt uses the first successful NIST Beacon 2.0 pulse strictly
after the scheduled `input_committed_at`. After that exact minute is available,
create and freeze the receipt directly from the public APIs:

```sh
/opt/homebrew/bin/python3 spike/t111/label_prep.py nist-seed \
  --commitment /private/tmp/t111-gate2-input-commitment.json \
  --github-receipt /private/tmp/t111-gate2-input-github-receipt.json \
  --output /private/tmp/t111-gate2-audit-seed.json
```

`nist-seed` first re-verifies the exact initial GitHub Gist revision. It then
queries only the precommitted POSIX-millisecond NIST time endpoint, requires
the response timestamp to equal the first 60-second boundary strictly after
activation, and requires the immutable chain/pulse endpoint to return the same
complete pulse. It never consults `pulse/last` and never substitutes a nearby
or later pulse. A missing exact pulse fails the attempt closed. The output is
created once with mode `0600` and is never replaced.

The resulting structure is:

```json
{
  "schema": "t111-gate2-audit-seed-v4",
  "input_binding": "sha256:<digest from commit-inputs>",
  "input_committed_at": "<scheduled activation from commit-inputs>",
  "commitment_reference": "https://api.github.com/gists/<gist-id>",
  "commitment_published_at": "<RFC3339 time before activation>",
  "commitment_document_sha256": "sha256:<exact commitment document digest>",
  "commitment_github_receipt": {
    "schema": "t111-github-initial-gist-receipt-v1",
    "api_url": "https://api.github.com/gists/<gist-id>",
    "revision": "<initial 40-hex revision>",
    "created_at": "<GitHub server created_at>",
    "document_sha256": "sha256:<exact commitment document digest>"
  },
  "source": "public_randomness_beacon",
  "source_reference": "https://beacon.nist.gov/beacon/2.0/chain/<chain>/pulse/<pulse>",
  "source_output_hex": "<NIST outputValue in lowercase>",
  "source_payload": {"pulse": {"uri": "<canonical pulse URI>", "...": "..."}},
  "source_payload_sha256": "sha256:<canonical source_payload digest>",
  "recorded_at": "<the pulse timeStamp>",
  "seed_hex": "<canonical derivation below>"
}
```

In the real JSON, `source_payload.pulse` is the complete pulse object, not the
abbreviated object shown above. The validator checks its version, 60-second
period, success status, canonical URI and matching chain/pulse indices, output,
certificate, signature, precommitment, previous-pulse link, timestamp, and
canonical payload digest. Official HTTPS authenticity and the NIST certificate
chain remain manual audit checks.

`seed_hex` is the lowercase SHA-256 of canonical JSON for this ordered list:

```text
["t111-gate2-audit-seed-derive-v2",
 input_binding, input_committed_at,
 commitment_reference, commitment_published_at,
 commitment_document_sha256,
 commitment_github_receipt.revision,
 source, source_reference, source_output_hex,
 source_payload_sha256, recorded_at]
```

The fixed `seed 111`, a local self-issued seed, an input-derived seed, a receipt
for different inputs, a pulse other than the first eligible NIST pulse, and a
custom sealed-mode `--seed` are rejected.

## 3. Prepare the immutable sample

Generate the bundle once into a destination that has never existed:

```sh
/opt/homebrew/bin/python3 spike/t111/label_prep.py prepare \
  --audit-seed-file /path/to/audit-seed.json \
  --output-dir spike/t111/labeling/g2-v4
```

Gate mode forbids `--force` and post-seed `--dry-run`. Census, dev, holdout,
hidden key, seed receipt, attempt receipt, the exact input burn-ledger snapshot,
hashes, and manifest are assembled in a private sibling staging directory and
published by one atomic rename. A
missing, extra, symlinked, or digest-changed bundle entry is rejected. Never
publish `key.jsonl` or extractor outcomes to reviewers.

## 4. Independent source-only review

Materialize source context, then create the two reviewer kits. The export
command appends the exposed coordinates to burn-ledger v2 before publishing
either kit:

```sh
/opt/homebrew/bin/python3 spike/t111/label_prep.py materialize-context \
  --artifact-dir spike/t111/labeling/g2-v4

/opt/homebrew/bin/python3 spike/t111/label_prep.py export-labeler-kits \
  --artifact-dir spike/t111/labeling/g2-v4 \
  --reviewers reviewer-a,reviewer-b \
  --output-dir /private/path/t111-reviewer-kits
```

Kits contain coordinate context only and exclude
`key.jsonl`, fact files, extractor output, frame membership, expected outcomes,
and prior labels.

Before review begins, partition all required census and sampled coordinates
between two independent reviewers. The assignment is deterministic and
digest-bound, with a 10% overlap cohort assigned to both reviewers; the
remaining coordinates are disjoint. Neither reviewer sees
the other's decisions before submitting a complete signed result.

Each selected coordinate receives both family decisions and a source-derived
role:

```json
{
  "site_id": "g2s_...",
  "invocation": "yes",
  "operation": "package.Service/Method",
  "registration": "no",
  "service": null,
  "expected_code_role": "production",
  "rationale": "review rationale",
  "evidence": "source evidence"
}
```

`invocation` and `registration` are `yes`, `no`, or `unsure`. A `yes` requires
a canonical fully qualified operation or service; other decisions require the
corresponding identity to be `null`. Any disagreement in the overlap cohort,
any `unsure`, and any schema-invalid decision goes to a third reviewer for
source-only adjudication.

After adjudication, assemble one complete label file and an adjudication JSON
object with the exact counters `overlap_sites`, `disagreements`, `adjudicated`,
and `unresolved` (`unresolved` must be zero). The supplied adjudication command
validates both reviewer populations against the assignment and requires the
third reviewer's exact resolution set:

```sh
/opt/homebrew/bin/python3 spike/t111/label_prep.py adjudicate-labels \
  --assignment-manifest /private/path/t111-reviewer-kits/assignment-manifest.json \
  --assignment-digest /private/path/t111-reviewer-kits/assignment-manifest.sha256 \
  --reviewer-a-labels /private/path/reviewer-a-labels.jsonl \
  --reviewer-b-labels /private/path/reviewer-b-labels.jsonl \
  --adjudicator-labels /private/path/adjudicator-labels.jsonl \
  --output-dir /private/path/t111-adjudicated-labels
```

Freeze both the canonical labels and their hash-only commitment:

```sh
/opt/homebrew/bin/python3 spike/t111/label_prep.py freeze-labels \
  --artifact-dir spike/t111/labeling/g2-v4 \
  --labels /private/path/t111-adjudicated-labels/adjudicated-labels.jsonl \
  --assignment-manifest /private/path/t111-reviewer-kits/assignment-manifest.json \
  --assignment-digest /private/path/t111-reviewer-kits/assignment-manifest.sha256 \
  --adjudication /private/path/t111-adjudicated-labels/adjudication.json \
  --output-dir /private/path/t111-frozen-labels
```

Publish only `labels.commitment.json`—never labels or coordinates—as a second
new public Gist. Run `github-receipt` again to create the label receipt. Do not
amend labels after seeing any dev or holdout score.

## 5. Score once, fail closed

Only after the second GitHub anchor may the frozen canonical labels be scored:

First rerun the exact fact-producer toolchain attestation in the same shell that
will perform the score. This command reads only the sealed manifest and the
local Go/Git executables; it does not open coordinates, labels, facts, or the
hidden key:

```sh
/opt/homebrew/bin/python3 spike/t111/score.py \
  --artifact-dir spike/t111/labeling/g2-v4 \
  --preflight-toolchain
```

Do not score unless this preflight passes. The scorer repeats the guard before
opening `key.jsonl`, so an environment drift cannot disclose hidden outcomes
before it is rejected. The preflight does not relax the one-score rule: an
actual holdout-score invocation remains terminal even when it fails closed.

```sh
/opt/homebrew/bin/python3 spike/t111/score.py \
  /private/path/t111-frozen-labels/labels.frozen.jsonl holdout \
  --artifact-dir spike/t111/labeling/g2-v4 \
  --label-commitment /private/path/t111-frozen-labels/labels.commitment.json \
  --label-receipt /private/path/t111-label-github-receipt.json \
  --assignment-manifest /private/path/t111-reviewer-kits/assignment-manifest.json
```

The scorer re-enumerates every frame from the pinned Git objects and verified
facts, reconstructs census and random selection from the v4 receipt, and checks
all artifact and label bindings. Client and registration precision/recall are
one Bonferroni simultaneous-confidence family. A Gate 2 pass requires, at 95%
joint confidence:

- at least 98% overall precision;
- at least 90% eligible-population recall;
- at least 90% precision for every fixture; and
- complete, exact classification of the five-role cohort.

These are full-population estimates: permanent-census outcomes and exhaustive
fresh precision outcomes enter exactly. Only non-exhaustive fresh holdout
populations—the recall frames in the fixed design—are expanded with
finite-population hypergeometric bounds. Missing inputs, provenance drift,
unresolved labels, review-protocol violations, or a failed threshold produce
`NOT ESTABLISHED`.

## Downstream scope

Even if Gate 2 passes, its unblocking effect is deliberately narrow. It
unblocks T13.1 and the T13.3 repository/build-target coverage wedge: Go/gRPC
implementation and consumer resolution, evidence-backed code roles, and
coverage reporting at that scope. It does not establish SCIP proto-field
references or canonical field lineage. T13.2 remains gated until its separate
field-reference evidence requirement is satisfied.
