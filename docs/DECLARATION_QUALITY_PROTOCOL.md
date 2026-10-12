# Protobuf declaration quality protocol — T49.1b machine-oracle round

Status: **frozen for sealing** (2026-10-12). Ben set the §1 thresholds and is
the sole reviewer who labels the oracle sample. On his delegation Claude, as
lead, performs the §8 ceremony; the draw is fixed by the NIST beacon, not by
the sealer. The sealed bytes are the protocol,
and a different byte is a different protocol. It inherits the identity and
scorable surface frozen by
[PROTOBUF_DECLARATION_IDENTITY.md](./PROTOBUF_DECLARATION_IDENTITY.md) §5–§6
and does not restate them.

## 1. Decisions (Ben, 2026-10-12)

- Ben is the sole owner, reviewer and adjudicator. No independent-human
  validation is claimed. This supersedes the identity document's §9
  two-reviewer requirement for this round only.
- **Machine-oracle design.** An independent enumerator is a complete oracle
  for the whole population. Ben validates a seeded sample of oracle claims and
  adjudicates every oracle/candidate disagreement.
- Thresholds, applied to point estimates (§5): declaration agreement ≥ 99.5%,
  reference-state agreement ≥ 99.5%, oracle-sample correctness ≥ 99%, and
  every disagreement adjudicated, with none left `unsure`.

## 2. Frozen tuple

| Input | Value |
|---|---|
| Corpus | The T47.2 frozen corpus at its three admitted derived commits ([`corpus.lock.json`](../spike/t472/corpus.lock.json)) |
| Candidate | `proto-contract` extractor 4.0.0 at implementation `f0da8d18`. Its run-g current partitioned roots are exported privately by [`export_candidate.py`](../spike/t491/README.md) and bound by digest |
| Oracle | [`enumerate_declarations.py`](../spike/t491/README.md): a standalone lexer and reader written independently of protocompile and `protodecl`, using no phebs code or output |
| Universe | 6,081 declarations of the five §5 kinds and 5,197 type references, from all 131 `.proto` blobs. Enums, enum values, reserved ranges, extension ranges and options are out of scope and never misses |
| Strata | repository × role × predicate. A `vendor` path segment makes a declaration vendor; otherwise `tests` or `testdata` makes it test; everything else is production |

## 3. Oracle resolution rule

The oracle records each field type (or map key and value), each RPC request
and response, and each extension type and extendee. It resolves them using
only the file's own declarations:

- A scalar keyword is `intrinsic`.
- A leading `.` is fully qualified.
- Otherwise each enclosing scope is tried from the innermost outward, then the
  root, and the first scope level with any match decides.
- One match is `same_file`. Several declarations of that exact name are
  `unresolved` / `AMBIGUOUS_SAME_FILE_DECLARATION`.
- No match is `IMPORT_LINKING_UNAVAILABLE` when the file imports anything, and
  `DECLARATION_NOT_FOUND` otherwise.
- A request, response or extendee that names a non-message is
  `INVALID_DECLARATION_KIND`.

This is the oracle's own reading of §6, not phebs's implementation. Any
difference is a disagreement that Ben adjudicates.

## 4. Order of work and blindness

1. Seal (§8), then draw the oracle sample from the seed.
2. Ben labels every sampled oracle claim from source only, without seeing any
   phebs output: `yes`, `no` or `unsure`, with a rationale. The labels are
   frozen, digested and published as a single-file gist with its own beacon
   receipt, before any comparison runs.
3. Only then does [`oracle_compare.py`](../spike/t491/README.md) compare the
   private candidate export with the oracle, key by key. Declaration keys are
   repository, path, predicate and object. Reference keys are repository,
   path, object and slot, compared for every declaration on which both sides
   agree.
4. Ben adjudicates every disagreement from source as `oracle`, `candidate`,
   `neither` or `unsure`. Adjudications are frozen and digested, then scored
   once.

Pre-seal disclosure: while exporting the candidate, the agent saw its root
fact totals (522 + 5,444 + 115 = 6,081), which equal the oracle's declaration
total. No per-item comparison was made before the seal.

## 5. Measures

- **Declaration agreement:** |oracle ∩ candidate| / |oracle ∪ candidate|.
- **Reference-state agreement:** references with identical resolution and
  reason / references compared.
- **Oracle-sample correctness:** `yes` / (`yes` + `no`), with a Wilson 95%
  interval (`harness.wilson_interval`). `unsure` is excluded and always
  reported. An all-`unsure` or empty sample is `not_measured`.
- Reported, not thresholded: post-adjudication emission precision, declaration
  recall and gap-classification correctness. Non-emitted kinds are never
  misses.

The outcome is `pass` only if all three thresholded measures meet §1 and no
adjudication is `unsure`. Any `unsure` adjudication or missing measure makes
the round `not_measured`. Otherwise the outcome is `fail`.

## 6. Oracle sample

97 per stratum, or a complete census below that. Declarations are stratified
by predicate and references by resolution state, using the frozen
`harness.stratified_sample` under the sealed seed. The plan, with exact
counts, is recorded in the seal document.

## 7. Label machinery

Oracle-sample labels and adjudications use the declaration-specific row
`{unit_id, stratum, correct, rationale}`. The frozen t111 eight-field label
schema describes caller and registration truth and cannot express declaration
correctness. Wilson intervals and stratified sampling use the frozen pilot
harness byte-for-byte. Freezing, digesting and gist publication follow the
retained discipline.

## 8. Seal and randomness

[`seal_round.py`](../spike/t491/README.md) `seal` writes a canonical document
binding this protocol, the universe, the candidate export digest, every tool
digest, the thresholds, the sample plan and an activation time. Claude
publishes it unedited as a single-file gist whose initial revision predates activation.
`seed` verifies the gist, fetches the first NIST Beacon 2.0 pulse strictly
after the activation minute, checks it at its canonical URI, and derives the
seed. There is no fallback pulse. `draw` and `sheet` produce the sample and
its review material.

## 9. Stop rules

Any of the following stops the round:

- a seal or seed refusal, or a late gist;
- changed universe, export or tool bytes;
- a comparison run before the oracle-sample labels are published;
- an `unsure` adjudication;
- a missing label.

A stopped round's remedy is a fresh, unseen round, never a re-roll.

## 10. What this can and cannot produce

A sealed passing round is single-human-reviewed evidence that the
`proto-contract` extractor's emitted declarations and §6 reference states
agree with an independent reading of these three repositories, and is
evidence toward admitting that recipe. It establishes no canonical
descriptor/module identity, no cross-file linking, no other corpus, and no
release or status change by itself.
