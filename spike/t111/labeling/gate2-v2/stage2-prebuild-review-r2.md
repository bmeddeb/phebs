# GATE2-V2 Stage-2 P0 admission parser — independent review r2

**Verdict: ACCEPT.** This supersedes r1 as the implementation binding only.
It does not authorize P0, derived-root construction, module hydration, fact
production, enumeration, Stage-2 preparation, selection, disclosure, or any
network activity.

Reviewed candidate:

| file | sha256 |
| --- | --- |
| `stage2_prebuild.py` | `sha256:51f70c84bf4596a6edd5f8f119d1260438d7aaafb5992641e95cb269f70b599c` |
| `stage2_prebuild_test.py` | `sha256:4915e6b5b4920444b08385e764500a8edbf52aa5f1ea4e6b6ebb13b8611d5510` |

Independent review verified that hydration can start from the sealed,
read-only Stage-0 source `t111` binary while the derived checkout is its
working directory; no cache or binary copy is permitted. Only the freshly
hydrated derived binary may extract facts. The admission contract now also
binds the sole allowed derived-lock transformation: four named `commit`
fields move from the Stage-0 fixture commits to the Stage-1 heads, with every
other parsed lock value and order preserved and fixed UTF-8/indent-2/LF bytes
(`sha256:d02cd5ef2baff3101fd72ac02eb57c14fee91593d1ca80c772584153eed9540b`).

The review re-checked the exact local clone/bundle/init/fetch/checkout plan,
fresh-cache and non-hardlink fact handoff, control-character refusal, and the
post-hydration/pre-extract core-only Git-config closure. The final fixture
config is ASCII/LF and permits only inert core values including
`repositoryformatversion=0` and `symlinks=false`; origin, alternates, hooks,
fsmonitor, bare, and log-all-ref updates are forbidden. Static P0 evidence and
terminal projections align with the enum-side exact M0/R0/T0 schemas.

Validation was synthetic only: 16/16 isolated tests passed with
`PYTHONDONTWRITEBYTECODE=1 /usr/bin/python3 -I -S
spike/t111/labeling/gate2-v2/stage2_prebuild_test.py`; `git diff --check` was
clean. No P0 authorization, derived root, network request, `t111` invocation,
hydration, fact extraction, evidence, enumeration, selection, or disclosure
occurred.

The next permitted implementation step is a separately reviewed P0 executor.
Live P0 remains blocked pending that executor and a distinct committed,
digest-named P0 authorization.
