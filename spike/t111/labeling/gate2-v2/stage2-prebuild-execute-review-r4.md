# GATE2-V2 Stage-2 P0 executor — independent review r4 (request)

**Verdict: PENDING INDEPENDENT REVIEW.**

This is a review request, not an acceptance record and not authorization to
execute P0. P0-01 and P0-02 are terminal and their preserved ceremony
directories are outside this review's scope. The target is the R6 successor
implementation required by failure review r2
(`sha256:b8ba1386b532999d861544981646410b39a1d54a2e1748699830d3e7856cdab2`).

## Candidate artifacts

- `spike/t111/labeling/gate2-v2/stage2_prebuild.py`: `sha256:bef0f3aea88f45de1ce3d9426e69ac5cfd7c36e777c7ed8838187a7435481b56`
- `spike/t111/labeling/gate2-v2/stage2_prebuild_test.py`: `sha256:b4373dac3e5504f9d0a58257bb2101359f62d211a55a81e10d13a88924d89a8e`
- `spike/t111/labeling/gate2-v2/stage2_prebuild_execute.py`: `sha256:382fef28c7bffc0f045df6b04e455818580cff3321783918f2672fd7979ee49e`
- `spike/t111/labeling/gate2-v2/stage2_prebuild_execute_test.py`: `sha256:348ea0851113f6a1df856ac60def3308b733f9b3d77fe376212133dd0dc76ae4`
- `spike/t111/labeling/gate2-v2/stage2_enumerate.py`: `sha256:926190eee4e0b6d30d97b468fa625ea405ea98b11a7d4f8f20ea2f5cb19c1a91`
- `spike/t111/labeling/gate2-v2/stage2_enumerate_test.py`: `sha256:50f5169a42f6180c056a9bf97d7c139d69dc37b2fad6b76a743f14ec6cfa7c0c`
- `spike/t111/labeling/gate2-v2/stage2-enumerate-review-r7.md`: `sha256:e4a512c926c53ea58decdf0e50464bfb9e12c27a60c65ca28725c089003b13dd`
- `spike/t111/module_cache.go`: `sha256:efb4548efd8f263e35cf5f34bd642278606e82ac8cf12d01e22caabe3abc393c`
- `spike/t111/module_cache_test.go`: `sha256:fae2fd4550722253aaf28ab01cf88f61a7b504b926623ec203b9297429c83e74`

Current ignored harness inputs, rebuilt offline from clean source commit
`8f105581f2a231b4be9ac28fb7c238bcf11a37cd` with the bound Go toolchain:

- `spike/t111/.module-cache/manifest.json`: `sha256:3e30ea0069b5773af8154ebca8e5576dcc0e55094b808d1c72c97316a169c1b3`
- `spike/t111/.module-cache/bin/t111`: `sha256:7d6db7fca68981e05758ca41b0b0ae109d935ace71ef228d52b5184928e93f65`
- `spike/t111/.module-cache/bin/typedcalloracle`: `sha256:43f4a0328ac5f5cd8e62236163552871c514d7a60eb1f161db9016575b46c9cc`

## Chosen R6 design and required independent checks

1. Confirm narrow hydration is retained. `hydrateHarnessModule` and both
   pre/post-build calls to `verifyHarnessDependencyClosure` must obtain the
   same exact two target patterns from one source. Graph-wide `go mod verify`
   must not remain on the build path.
2. Independently verify the reader's fail-closed envelope: offline package
   resolution, sealed-cache containment, direct h1 checks of every selected
   external source tree and cached `go.mod` against committed `go.sum`, local
   snapshot/commit binding, and identical pre/post-build closure digests.
3. Run the real-Go regression. Its graph must contain a non-selected older
   module version; narrow hydration plus offline bound build must pass without
   that older source, while an actually missing selected closure member must
   refuse. Confirm the test fails at graph-wide verification on the pre-R6
   implementation and passes on these bytes.
4. Confirm the r4 executor review predicate and r7 verifier transitively bind
   exact committed `module_cache.go` and `module_cache_test.go` bytes before
   M0/E1 derived-state access. Accepted r3/r6 records must remain immutable
   history and cannot bless this implementation.
5. Verify R1-R5 carry forward unchanged where R6 does not touch them:
   non-shallow/ancestry admission, ref-bearing bundle recipe, bounded sanitized
   terminal-v2 diagnostics, real-Git sealed argv tests, and atomic custody.
6. Verify the refreshed ignored manifest names source commit `8f10558...`,
   binds the candidate producer source and exact Go 1.26.5 identity, and that
   its t111/oracle digests match the files above. No acceptance should treat an
   ignored artifact as committed Git state; a later P0-03 promotion must bind
   its then-current exact digest in the canonical authorization.
7. Re-run the Go producer suite and the isolated parser/executor/enumerator
   suites under the bound toolchains. Acceptance, if any, must rewrite r7 then
   r4 with exact sealed binding lines and land in a new accepted implementation
   commit before any P0-03 authorization is promoted.

This request creates no authorization, source ref, derived root, fact run,
enumeration output, preparation, selection, or disclosure. `gate_status`
remains `PENDING`; independent review is the only next action.
