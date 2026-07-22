# GATE2-V2 Stage-2 enumeration failure review r3 — t111-gate2-v2-enum-fbd8474-01

**Verdict: FAILURE ROOT-CAUSED — one defect (frame rows not unique per
sampling site). The authorization is terminally consumed; no retry under it.
Fail-closed behavior correct; no output, staging, or coordinate disclosure
exists. `gate_status` remains PENDING.**

Reviewer: Claude, independent of implementation and launch (operator fired).

## Bindings

- Authorization: `t111-gate2-v2-enum-fbd8474-01`
  `sha256:ab0c53454382a7e75026d047f357767ce60c7f56d088473e8bd9163ad9627f2d`
- Terminal receipt `sha256:94fdaedf72989abe4a8843b59decf3cf82ac54cd88ebb5b6be5d14e9581c1083`;
  consumption marker `sha256:578d3960ab86b7347c318dc595ca75d6d7e743f8209242d9740180a2e1aabf6c`;
  exit 2, ABORTED, outputs null.
- Refusal: `frame has duplicate sampling-site coordinates`
  (`stage2_enumerate.py` sampling-site key `system:path:line:line`).

## Finding F5: frame rows are emitted per fact, not per sampling site

The census unit is the line-granular sampling site; the enumerator refuses
any frame whose rows collapse to a duplicate site key. One source line can
legitimately carry multiple extracted facts (the evidence model's
`fact_fingerprint` exists for exactly this multiplicity), so a per-fact row
emitter produces duplicate site keys on real corpora. The refusal is the
correct integrity boundary; the defect is upstream in frame-row
construction, which must aggregate fact multiplicity within one unique row
per site. Population and census arithmetic must count distinct sites.

## Remediation requirement binding any successor authorization (enum-02)

- **R7 — site-unique frame rows.** The frame-row producer emits exactly one
  row per sampling site, aggregating that site's facts; the enumerator's
  duplicate refusal is retained unchanged. Regression: a synthetic corpus
  with two facts on one line yields one site row and a population counting
  sites, not facts; a genuinely duplicated row still refuses.
- R1–R6 carry forward unchanged. Fresh authorization ID and digests; this
  ceremony directory is preserved evidence.

No enumeration, preparation, selection, or disclosure is authorized by this
record.
