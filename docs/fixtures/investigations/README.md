# Synthetic Investigation fixtures

Eight envelope instances (MCP envelope v0.2 shape) modeling the states the
UI, API contracts, and authorization tests must handle. **Entirely
synthetic**: the `acme-payments` domain, all identifiers, and every count
are invented. No benchmark repository, fixture, coordinate, or label
material was used or referenced (GATE2-V2 disclosure rules).

| Fixture | State modeled |
|---|---|
| 01-complete-with-findings | full analysis, facts present, absence inapplicable |
| 02-complete-zero-findings | eligible absence with authoritative negative wording |
| 03-partial-failed-processing | outcome gates failed; incomplete-analysis wording |
| 04-unresolved-attribution | facts present; service/owner hops unresolved |
| 05-stale-pack-validation | suspended pack; validation inapplicable |
| 06-inaccessible-scope-refusal | minimal refusal envelope; no scope leakage |
| 07-non-comparable-revisions | comparison report, no deltas rendered |
| 08-truncated-result | non-final page blocks absence eligibility |

Invariants enforced at generation (see git history for the generator):
processing counts sum to eligible units; absence eligibility only with
complete analysis and result set; nonempty facts force inapplicability;
the refusal envelope omits scope/validation/provenance entirely.
