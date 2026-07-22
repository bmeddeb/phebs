# GATE2-V2 Stage-2 enumeration verifier — independent review r8 (request)

**Verdict: PENDING INDEPENDENT REVIEW.**

This is a review request, not an acceptance record and not authorization for
enumeration. Enumeration attempt 01 is terminal and must not be retried. R7
changes the frame-row producer, so enum-02 requires fresh implementation
authority instead of inheriting r7.

## Candidate artifacts

- `spike/t111/labeling/gate2-v2/stage2_enumerate.py`: `sha256:c624913debea72fed95b0acfd3836db6cd2d1480a89d49d3b9f2495ab4c3e25a`
- `spike/t111/labeling/gate2-v2/stage2_enumerate_test.py`: `sha256:251edde88f96170f6a8e99f39f2c2de893ad98a7ebbc112aabfcef32bb396188`
- `spike/t111/labeling/gate2-v2/stage2-enumeration-failure-review-r3.md`: `sha256:653d19e4f042b87a3199594fc850648a0db1e82487c349b5ca9b3ed93aebf0f9`

## Required independent checks

1. Confirm the completed P0-03 evidence chain remains pinned to the historical
   r7 review at its accepted implementation commit. Confirm only the live
   enumeration binding advances to r8, so a later review is never substituted
   into the sealed P0 evidence ancestry.
2. Confirm each external frame is projected to exactly one row per
   `system:path:line` sampling site. Distinct producer identities on the same
   line must yield one deterministic row whose multiplicity, sorted member
   identities, and complete-members digest retain the aggregation evidence.
   Population and membership arithmetic must therefore count distinct sites.
3. Confirm `stage2_inputs` and its exact duplicate-coordinate refusal are
   byte-identical to commit `9ed830236860a2643d08d08dd1ca141f69d8c79c`.
   Malformed coordinates still reach that boundary, and an identical or
   repeated-identity producer row must remain duplicated so it refuses with
   `frame has duplicate sampling-site coordinates`.
4. Re-run the isolated/no-site suite. The synthetic two-fact/one-line case
   must produce one row and population one; a genuinely duplicated emitted
   row must still refuse. Confirm all 48 tests pass without ceremony access,
   enumeration, network activity, or coordinate disclosure.
5. Confirm R1–R6 carry forward byte-for-byte wherever untouched, including
   the accepted parser (`sha256:bef0f3ae…`), executor
   (`sha256:382fef28…`), closure reader (`sha256:efb4548e…`), and sealed
   derived `label_prep.py` inventory source (`sha256:53c83c4e…`).

Any future acceptance must bind these exact candidate bytes in a later
accepted implementation commit. Only after that may a separately reviewed
enum-02 worksheet be promoted under a fresh authorization ID and state
namespace. Until then, `gate_status` remains `PENDING`; there is no
enumeration, preparation, selection, disclosure, or ceremony authority.
