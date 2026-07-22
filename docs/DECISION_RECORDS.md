# Pilot decision-record templates

*Draft artifact for pilot prerequisite item 10 (design phase). Templates
only: filling one in creates no decision. A decision exists when the acting
principal records the completed form in PLAN.md as a dated ADR entry with
every minimum field present — the fail-closed rule of
[PILOT_PREREQS.md](./PILOT_PREREQS.md) (omission of a minimum field is not
permitted, placeholders block). Epic 16 requires **both** records below,
each independently complete; neither implies the other.*

## Template A — validation gate decision

The record for the pilot internal validation's outcome under the sealed
[ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md). One record per
sealed round; a failed or underpowered round is recorded with the same
completeness as a pass.

```
stage:                pilot-internal-validation round <n>
stage_result:         <one sentence: what the round produced>
gate_status:          PENDING | ESTABLISHED | NOT_ESTABLISHED | INVALID | ABORTED
protocol:             ACCURACY_GOLD_PROTOCOL.md sha256:<digest of the SEALED version>
machinery:            label_protocol.py sha256:<digest>; harness.py sha256:<digest>
frozen_inputs:        RPC <identity>; S0 <commitment>; extractor <source commit, binary digest, toolchain digest>
universe_commitment:  sha256:<rows_commitment of the frozen eligible universe>
label_commitment:     sha256:<build_label_commitment digest>; receipts <gist revision, beacon pulse>
sample_design:        strata <list>; per-stratum n <list>; seed <beacon URI + 64-hex>
scores:               per §3 claim family: count / denominator / point / Wilson low–high / stratum table digest
thresholds:           each named T_* value as sealed at Gate 0, beside its measured result and verdict (pass | conditional | stop)
error_taxonomy:       sha256:<digest of the tally document>
unresolved_findings:  <list, or none>
timestamp:            <RFC3339 UTC>
acting_principal:     <name, role>
reviewers:            <names>; adjudicator <name>; signatures <references>
authorized_next_action: <exactly one, or none>
```

Rules already in force that this template does not soften: a stage success
never implies `ESTABLISHED`; an underpowered round records
`gate_status: INVALID` or `NOT_ESTABLISHED`, never a partial score; a
remediated failure needs a fresh unseen round; `ESTABLISHED` here satisfies
only the first of Epic 16's two prerequisites.

## Template B — pilot continuation decision

A human judgment by the named decision authority, made after Template A
exists with `gate_status: ESTABLISHED`, and after the partner's own review.
It is not derivable from scores.

```
decision:             CONTINUE | DO_NOT_CONTINUE | CONTINUE_WITH_CONDITIONS
depends_on:           Template A record <PLAN.md date/row reference> with gate_status ESTABLISHED
partner:              <design partner identity or agreed pseudonym>
partner_disposition:  <the partner's stated position, with reference>
scope_authorized:     <exactly what continues: e.g. Epic 16 implementation on a post-gate branch; anything absent stays frozen>
conditions:           <list, or none — each with an owner and a check>
pivot_boundary:       <what this decision does NOT authorize — at minimum: code-host writes, the broader platform pivot, any public accuracy claim>
unresolved_findings:  <list, or none>
timestamp:            <RFC3339 UTC>
acting_principal:     <name, role — the charter's named decision authority>
authorized_next_action: <exactly one, or none>
```

## How the two records unlock Epic 16

Epic 16's gate reads, verbatim from the closure ADR: "requires ESTABLISHED
plus pilot continuation." Mechanically: a PLAN.md ADR entry per Template A
with `gate_status: ESTABLISHED`, and a PLAN.md ADR entry per Template B with
`decision: CONTINUE` (or `CONTINUE_WITH_CONDITIONS` whose conditions are
met), both complete under the minimum-field rule. The BACKLOG Epic 16
heading is then updated citing both rows — in the same PR, per the ADR
convention. Any other path — partial rounds, trends, enthusiasm — does not
unlock it.
