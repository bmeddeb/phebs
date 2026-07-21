# GATE2-V2 Stage-2 P0 admission parser — independent review r1

**Verdict: ACCEPT.** This accepts an admission-only parser. It is not a P0
executor and does not authorize a live P0 run.

Reviewed candidate:

| file | sha256 |
| --- | --- |
| `stage2_prebuild.py` | `sha256:c06700e9d4b33faff45638684d2c9f5ec12cea7c442e45ae6bbd39f323945a7d` |
| `stage2_prebuild_test.py` | `sha256:6f06f99102988e1362fa8327d46bd2144e33970251ec32b23b72e01e674a0340` |

Independent review verified the isolated/no-site bootstrap, canonical JSON
parsing, exact four-fixture hydration and two offline extraction plans, phase
separation, pinned toolchain/environment envelope, fixed derived-root topology,
and boolean-typed scope that excludes enumeration, preparation, selection, and
disclosure. The parser has no `subprocess`, Git, network, hydration, extraction,
or filesystem-construction mechanism.

The authorization reserves independent namespaces: a copy-on-write source must
not overlap the derived root or ceremony; terminal/marker/evidence records must
not overlap any hydration or extraction capture path (including ancestor
overlap); fixed fact runs and state targets remain pairwise disjoint. The
implementation-review fields deliberately bind prior implementation acceptance,
not the future P0 authorization itself, avoiding a self-referential Git hash.

Validation was synthetic only: 9/9 isolated tests passed with
`PYTHONDONTWRITEBYTECODE=1 /usr/bin/python3 -I -S
spike/t111/labeling/gate2-v2/stage2_prebuild_test.py`; `git diff --check` was
clean. No P0 authorization, derived root, network request, hydration, fact
extraction, fact evidence, enumeration, selection, or disclosure occurred.

The next permitted implementation step is a separately reviewed executor that
verifies this admission before performing any one-shot action. Live P0 remains
blocked pending that executor and a distinct committed P0 authorization.
