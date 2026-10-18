Method: dual-agent (A: `t442a_design_a` · B: `t442a_detector_b`).

The Appearance card fits Phebs' restrained Settings UI. Its code specimen,
plain-language failure message and nearby recovery action keep the task clear.
Cognitive load is low: four choices, no extra decision step, and no checklist
failures. The retained source and explicit recovery make the failure
understandable without exposing diagnostics.

| Heuristic | Score |
|---|---:|
| System status | 3 |
| Familiar language | 4 |
| User control | 3 |
| Consistency | 4 |
| Error prevention | 3 |
| Recognition | 4 |
| Efficiency | 3 |
| Minimalism | 4 |
| Error recovery | 3 |
| Help | 3 |
| **Total** | **34/40** |

Strengths: the specimen stays truthful on failure; the status and action sit
together; desktop and mobile layouts retain clear reading order and keyboard
focus.

One nonblocking P2 concern remains: full-page reload clears unfinished Settings
state, including an uncopied new API token. The action is explicitly labeled
**Reload page**, and the guide explains the consequence. A future
`$impeccable clarify` pass could put a copy-first reminder beside it when relevant.

Operator and keyboard journeys pass. Screen-reader announcements are represented
by the live-region markup; actual screen-reader speech was not tested. The
existing 30px palette selector remains a minor mobile comfort observation.

The detector found zero issues. Injection succeeded in three fresh headless
contexts and reported no anti-patterns; no visible browser overlay is claimed.
One Chrome cleanup check raised `EPERM` after root exit; the original refusal is
retained, and a read-only follow-up proved all owned groups absent without a
rerun or kill.

Questions skipped: one priority issue, already documented; no blocking design
decision remains.
