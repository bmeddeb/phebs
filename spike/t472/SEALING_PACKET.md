# T47.2b run-g sealing packet

Prepared by T47.2q on 2026-10-11 for Ben Meddeb, the sole owner, reviewer and
sealer. It covers what the seal binds, the protocol §10 checklist status, and
the exact ceremony. Nothing here is sealed, drawn, labeled, published or
scored yet. Any refusal stops the round. A stopped round never re-rolls: no
other pulse is chosen and no sample is redrawn.

## What the seal binds

| Input | sha256 |
|---|---|
| `docs/CALLER_QUALITY_PROTOCOL.md` (includes the T47.2q abstention-frame drop) | `febd797b4d566a4324766d1b06dcf9e3a79f3d2ac0c0a7e19748b68e54f74868` |
| `bundle/preregistration.frames.run-g.bundle.json` (input to `seal.py seal`) | `27591d96be692b27e75d62ec8f7b17100a6e8b6c56ae6457b9ea9fe9dc27a86c` |
| `frames/frames.plan.run-g.json` (round plan) | `d712b3e01978d1a4f1b102a8c6742e0de9386ccac8b04b36bfdc87da43b9093f` |
| `frames/frames.plan.json` (template plan) | `0c60125b1cdf1ebd54a0aec726e744720d8e31078f2a90cee6b2f0b04452fa60` |
| `validation.parameters.json` (thresholds, 97 per stratum) | `3396a4b15358683e2a714ca1992a69d065169b96c20099d8d3b7d2de98a0b9fe` |
| `corpus.lock.json` | `8c5340427010c3ca86d2a038d88ff6d23ade76c04ba2e9bcd49d579237762168` |
| `universe/universe.json` | `46f6a02c92414845ccc460619b92d1c681b40e23a636d334cce668c9363f80d3` |
| `caller_map_bindings_20261011g.json` (candidate export and ledger) | `83979104b0be6081094d51250192a18d3c8d93876003ac85559602bd28f034d0` |
| `candidate_run_processing_20261011g.json` (processing outcomes) | `88d1b2d5ace6caa10985ee308059ef22e56dedc11078910c631bfd72114a6f68` |
| `artifact_bindings_20261011g.json` (binary, card, recipe, packet) | `09bffab86cac625d21ab25302ab146326c20e1da8ea9cc5e1604e256eeb077ec` |
| `source_frame_judgments_20261011.json` (your two confirmations) | `fc4ac53449a17ceeaf2741196cfef064c5766846d2545f8acc5c6e078d33a8ac` |

The frames bundle also binds every machinery digest, including the frozen
`label_protocol.py` and `harness.py`, plus the private candidate populations
by digest.

### Planned draw

| Frame | Population | Planned labels | Census strata |
|---|---|---|---|
| recall | 498,793 | 1,045 | 0 of 11 |
| attribution | 74 | 74 | 4 of 4 |
| precision | 231 | 206 | 3 of 4 |
| unresolved | 708 | 261 | 3 of 5 |
| abstention (withheld) | 344,034 | dropped, not drawn | — |

That is 1,586 planned draws before overlap between frames. The labeling sheets
will merge overlapping sites and hide which frame each site came from.

## Protocol §10 checklist

1. **Frozen corpus with license and provenance, plus the independent
   universe:** done (corpus lock, universe, source census).
2. **Exact frame counts and stratum sizes:** done (T47.2p round plan; T47.2q
   drops the unscored frame).
3. **Approval:** done. You approved the §3 thresholds and §8 sampling on
   2026-10-10, and you are the sole reviewer.
4. **Machinery, card, manifest, binary and input digests:** done (bundle and
   artifact bindings). The public randomness seed comes from the ceremony below.
5. **Corpus derivation checks:** done (T47.2b corrected derivation and
   build-cache classification).
6. **`phebs.grpc.caller.go` stays `experimental-dark`:** done (unchanged until
   T47.5).
7. **Label projection, ledgers and state census implemented and tested:** done.
   The independent expected-resolution and declaration-citation ledgers are made
   from source for the sampled sites after the draw.

After the seal, 21 named inputs stay open: the pulse and seed, your blind
labels, the two independent ledgers, the commitment payload fields, and the
five randomness-sequence bindings. Each is filled only by the step that
actually produces it.

## Ceremony

Run every command from the repository root.

1. **Review and say "seal".** Read the protocol, including both 2026-10-11
   amendments, and this packet. Once you say "seal", the protocol and every
   digest above are frozen. Any later change is a new protocol.

2. **Seal.** Pick an activation time at least 15 minutes ahead, in UTC ending in
   `Z`; the tool refuses anything under five minutes. Then run:

   ```bash
   python3 spike/t472/seal.py seal --bundle-in spike/t472/bundle/preregistration.frames.run-g.bundle.json --bundle-out spike/t472/bundle/preregistration.sealed.run-g.bundle.json --document-out spike/t472/seal.run-g.json --activation-at 2026-10-12T15:00:00Z
   ```

   This fills only the protocol-digest slot and writes the canonical seal
   document. Commit both files before activation.

3. **Publish before activation.** You do this yourself: it is an external
   publication. Create a gist containing exactly one file, the unedited
   `spike/t472/seal.run-g.json`:

   ```bash
   gh gist create spike/t472/seal.run-g.json
   ```

   The gist's initial revision must be created before the activation time.
   Never edit the gist afterwards; only its initial revision counts.

4. **Seed.** After the first pulse following the activation minute has been
   published (wait about two minutes), run:

   ```bash
   python3 spike/t472/seal.py seed --document spike/t472/seal.run-g.json --gist-api-url https://api.github.com/gists/GIST_ID --out spike/t472/seed.run-g.json
   ```

   Replace `GIST_ID` with the gist's ID. The tool:
   - verifies the gist's single file matches the document byte-for-byte;
   - checks the initial revision predates activation;
   - fetches the pulse by time and checks it against its canonical URI;
   - derives `seed_hex`.

   A late gist, a missing pulse or a disagreeing lookup stops the round. No
   other pulse is ever substituted.

5. **Draw.** Run:

   ```bash
   python3 spike/t472/seal.py draw --document spike/t472/seal.run-g.json --seed-record spike/t472/seed.run-g.json --population-dir /home/ben/phebs-rehearsals/t472-round-frames-20261011g-r2 --out /home/ben/phebs-rehearsals/t472-round-frames-20261011g-r2/sample.run-g.json
   ```

   The draw uses the frozen `harness.stratified_sample`. It refuses unless every
   population matches its sealed digest and the counts equal the sealed planned
   samples. The sample stays private because frame membership is a prediction.

6. **Blind labels (next ticket).** Mixed-frame label sheets showing source only,
   your labels, the independent ledgers, `freeze_labels`, the label commitment
   gist plus beacon receipt, then unsealing and scoring.

## Stop rules

- Any refusal from `seal.py` stops the round.
- Changed protocol, plan, population or tool bytes stop the round.
- A late gist or a non-first pulse stops the round.
- Disclosing predictions before the labels are committed stops the round.

A stopped round's remedy is a fresh, unseen round, never a re-roll.
