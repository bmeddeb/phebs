# T47.2b review packet — sealing-input preparation

**Code/docs commit under review:** `13b28e70` on `hp/t47.2b-corrected-derivation`
(base `a9022b22`). This packet is the second commit and changes only this file.

**Scope.** Protocol §4 enumeration, §5–§6 frames, §3 scoring projection and
ledgers, §10 binding bundle. No Go production code. No sample drawn, no label
manufactured, no prediction disclosed, no score produced, nothing published,
nothing merged.

## What to review

1. `spike/t472/enumerate_universe.py` + committed `spike/t472/universe/` —
   source-only enumeration at the three exact derived commits; role rules are
   a port of `gocaller.classifyRole`; fail-closed reconciliation to
   `source.census.json` counts/paths and lock client mappings before any
   output is written. Three consecutive runs were byte-identical.
2. `spike/t472/frames.py` + `spike/t472/frames/` — recall/attribution
   populations, excluded-kinds ledger (11,867 non-client-aware tokens + 978
   bare imports = 12,845, reconciling 11,376 + 12,845 = 24,221), census
   flags at 97/stratum, provenance digests, pending candidate frames.
3. `spike/t472/caller_scoring.py` — exact numerator/denominator joins,
   invalidation (missing/surplus labels refuse; all-unsure refuses;
   zero-decided = unavailable, never 100%), eligible-unit ledger accepting
   only explicit outcome records and gating analyzed≥90 / partial+failed≤5 /
   excluded≤10 over the whole 7,685-source census.
4. `spike/t472/binding_bundle.py` + `spike/t472/bundle/` — every binding is
   a sha256 or a named null; malformed digests refuse; publication payloads
   are local skeletons with `published: false`.
5. `spike/t472/test_universe_frames.py` — 24 unittest cases (synthetic git
   repo end-to-end; join arithmetic; ledger refusal paths; bundle walk).
6. Docs: `docs/CALLER_QUALITY_PROTOCOL.md` §4 placeholder filled and §10
   items 2/7 updated; `spike/t472/README.md` preparation section; BACKLOG,
   ROADMAP, PLAN ADR rows.

## Gates run

- `python3 -m unittest spike.t472.test_universe_frames` — 24/24 pass.
- `make docs-check` — pass (incl. sealed T11.1 tree digest).
- `make verify-glossary` — pass.
- Trailing-whitespace scan of new files — clean.

## What Ben must still supply (genuine human inputs)

1. **Confirm the two `review_required` source judgments** in
   `frames/frames.plan.json`: (a) recall exclusions — bare method-name tokens
   in files referencing no committed generated client, and client imports
   alone, stay out of recall sampling; (b) attribution units use the whole
   constructor-backed interface span as their coordinate.
2. **Blind labels** for the drawn samples after the sealed seed exists
   (sole owner/reviewer; t111 schema; no manufactured labels).
3. **The sealing decisions** the protocol already reserves: NIST beacon pulse
   → seed, sealed proof-bundle envelope + complete candidate ledger
   identities, run-outcome receipt, protocol digest, external publication.
4. Independent code/security review of this packet (not waived).

## Steady-state cost

One-shot offline tools: one `git cat-file --batch` per repository, single-pass
in-memory JSONL for frames/scoring/bundle. No production request, sync,
startup, retry, publication, lock, cache, schema, memory/disk or child cost.

## Known limits

- Precision/abstention/unresolved frames stay pending until candidate
  identities seal; the abstention frame additionally needs phebs's complete
  candidate ledger, not derivable from proof-bundle assertions alone.
- The enumeration is candidate evidence inventory, not a true-call frame;
  `client_aware` is import/constructor provenance, not alias/dynamic-path
  resolution (that is exactly judgment 1 above).
- No quality result, seal, activation or release follows from this packet.
