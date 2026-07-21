# GATE2-V2 Stage-2 P0 executor — independent review r3 (request)

**Verdict: PENDING INDEPENDENT REVIEW.**

This is a review request, not an acceptance record and not authorization to
execute P0.  It supersedes neither the terminal P0-01 evidence nor the
historical r2 record.  The target is the R1–R5 successor implementation after
P0-01's terminal refusal.

## Candidate artifacts

- `spike/t111/labeling/gate2-v2/stage2_prebuild.py`: `sha256:bef0f3aea88f45de1ce3d9426e69ac5cfd7c36e777c7ed8838187a7435481b56`
- `spike/t111/labeling/gate2-v2/stage2_prebuild_test.py`: `sha256:b4373dac3e5504f9d0a58257bb2101359f62d211a55a81e10d13a88924d89a8e`
- `spike/t111/labeling/gate2-v2/stage2_prebuild_execute.py`: `sha256:c4404d1d9ca5187aa6a5c7fbb2c236172b188aeb1864c3e0fe1d7f475cea5c54`
- `spike/t111/labeling/gate2-v2/stage2_prebuild_execute_test.py`: `sha256:9ef3668edc98b1806c21b813aae2a633c358c89fc5fb1cd7a43ea9bf90c5e84e`
- `spike/t111/labeling/gate2-v2/stage2_enumerate.py`: `sha256:2684f5c917713ff320adef0fc0bdadbc7c3c2a660a62d3469e6528f6a3a01873`
- `spike/t111/labeling/gate2-v2/stage2_enumerate_test.py`: `sha256:81b46cc2cd26a0898c5d22448659261d35f447eeb7f166d05834c038e382ca88`

## Required independent checks

1. Confirm R2 maintenance independently: each of temporal, dapr, loki, and
   online-boutique is non-shallow; its prior-attempt old head and sealed
   Stage-1 head are commit objects; and the old head is an ancestor of the
   sealed head.
2. Verify admission binds those facts, and the executor repeats them before
   M0.  A shallow or incomplete source must refuse before creating a source
   ref, derived root, or consumption marker.
3. Verify each source recipe creates only `refs/gate2-v2/<fixture>` at the
   sealed head with zero-old-value CAS, bundles that ref, fetches that ref into
   the derived repository, and checks out the sealed head.  No review may
   treat `git bundle verify` as a completeness oracle.
4. Execute the sealed ref, bundle, init, fetch, and checkout argv classes
   against isolated real Git repositories.  Confirm the raw-OID bundle form
   fails and the sealed ref-bearing form supplies a checkoutable destination.
5. Confirm a command refusal or timeout records only a fixed-shape terminal
   diagnostic: bounded stderr digest and prefix, redacted URL userinfo,
   sensitive query/key/header values, and credential schemes; command step,
   nonzero exit, and truncation flag.  The prefix must remain safe when a URL
   userinfo field is cut off at the bound.  Completed terminals must carry
   `failure_diagnostic: null`.
6. Confirm post-M0 Git ref and checkout verification failures use that same
   diagnostic path.  Their stdout must drain concurrently and be bounded, so
   an unexpected ref expansion cannot deadlock the executor or consume
   unbounded memory.
7. Re-run the isolated parser/executor/enumerator suites under the bound
   isolated/no-site runtime.  Acceptance, if any, must bind the exact code and
   contemporaneous r6 enumeration-review digest, land in a new accepted
   implementation commit, and precede any successor authorization.

P0-01 remains terminal evidence at its preserved ceremony path.  This request
does not create an authorization, source ref, derived root, fact run,
enumeration output, selection, or disclosure.
