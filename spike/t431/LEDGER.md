# T43.1 findings ledger

Severity: **B** blocker · **M** major · **m** minor. Every finding names the
charter rule it violates (§1 philosophy, §2 discipline, §3 language) and its
routing into Epic 43. Line references are against the audited tree
(`ux/t43.1-surface-audit`, base `4a0b3e4`).

## 1. Design-specificity verdict

Authored semantics, unauthored surface (~40% authored). The semantic layer —
client-side authority validators that refuse mismatched rows, the
`unavailable ≠ empty ≠ zero ≠ filtered ≠ scan-window` vocabulary, census copy
like "producer zeros are not meaningful" — is unmistakably this product. The
visual layer does not carry it: digests render as inline strings with no
copy/verify affordance anywhere; the charter's chip → drawer → receipt
gesture is unbuilt (no drawer exists in the codebase); caveats are permanent
server-text walls; nine chip dialects, seven truncation helpers, five focus
rings; and three coexisting aesthetics (rounded GitHub-adjacent git surfaces,
square brutalist evidence instruments, an editorial Workbench). The only
crafted visual identity is the brand mark and loader. Dark theme is the more
ownable look; light theme is where the product reads most generic.

## 2. Nielsen heuristics (Operate mode) — 23/40

| # | Heuristic | Score | Key issue |
|---|---|---|---|
| 1 | Visibility of system status | 3 | Streaming search meta/skeletons exemplary; route-level loading is a bare unlabeled 20px spinner (App.tsx:99–125,165); Settings claims "No API keys." mid-fetch |
| 2 | Match system/real world | 2 | "authorization-first visible-repository fallback" as user copy; raw enum `old_only_evidence` in a chip |
| 3 | User control & freedom | 2 | Atlas/CallerMap/Comparison filters, cursors, selection all non-URL; native `window.confirm` guards |
| 4 | Consistency & standards | 1 | Nine chip primitives; radii 0/4/5/7/8/10/999; six h1 scales; status colors drift per surface |
| 5 | Error prevention | 4 | Byte limits refused before send ("Nothing was sent or written"); field-number range checks; preview-digest fencing |
| 6 | Recognition over recall | 2 | Caller Map needs an arriving 4-tuple or dead-ends; Workbench resume asks for a pasted ULID |
| 7 | Flexibility & efficiency | 2 | Shortcuts only on Search; no density toggle; no global navigator |
| 8 | Aesthetic & minimalist | 2 | 8–9px eyebrows, inline digest blocks, permanent caveat paragraphs |
| 9 | Error recovery | 3 | 409 dual-retry excellent; raw `String(cause)` on Search/History/Blame; Repos error replaces the table |
| 10 | Help & documentation | 2 | SectionHelp glossary is excellent but wired to only two surfaces; search help is an external link |

## 3. Per-surface charter rubric

Trust = hierarchy of trust · Dens = density · States = state completeness ·
Kbd = keyboard · Mot = motion · Voice = copy. ok / F-ref / n-a.

| Surface | Trust | Dens | States | Kbd | Mot | Voice |
|---|---|---|---|---|---|---|
| App shell / header | F1, F5 | ok | F7 | ok | ok | ok |
| Search | F9, F26 | F13 | ok (model) | F21 | F24 | F30 |
| Repos | ok | ok | F8 | ok | ok | ok |
| Service directory + overview | ok (model) | F13 | ok (model) | ok | none | ok |
| Relationship explorer | ok | F14 | ok | F2 | none | F29 |
| File | ok | ok | ok | F20, F33 | none | ok |
| History | ok | ok | F31 | ok | none | ok |
| Blame | ok | ok | F4 | ok | none | ok |
| Commit | ok | F34 | F4 | ok | none | ok |
| Contract Atlas | F9, F11 | F14 | ok | F3 | none | ok |
| Caller Map | F9, F12 | F13/F14 | ok (model) | F3, F22 | none | ok |
| Caller comparison | ok | F14 | ok | F3 | none | F23 |
| Impact | F9, F10 | ok | ok | ok | none | F28 |
| Kafka topics | F16 | ok | ok (model copy) | ok | none | ok |
| Investigation | ok | ok | F17, F35 | ok | none | ok |
| Workbench why/what | ok | ok | ok | F18 | none | F27 |
| Workbench where/how | F9, F15 | F14 | ok | F19 | none | F29 |
| Settings | ok | ok | F6, F25 | ok | none | ok |
| Audit | ok | ok | ok | ok | none | ok |
| Analytics | ok | ok | F36 | F36 | none | ok |
| Login | ok | ok | ok | ok | ok | ok |

Systemic rows (5+ surfaces): F4, F11, F12, F13, F14, F32, F37, F38.

## 4. Findings

Routing: **HF** = pre-T43.2 hotfix candidate · otherwise the owning ticket.

| ID | Sev | Surface · location | Charter rule | Defect → fix | Route |
|---|---|---|---|---|---|
| F1 | B | App.tsx:93–135 | §2 fail-closed rendering; §1 "absence of authority is never hidden" | Capability-absent deep links (`#/contracts`, `#/callers`, `#/impact`, `#/topics`, `#/investigations`, `#/workbench`, `#/services`, `#/relationships`) silently render SearchPage under the original URL → per-prefix terminal "capability not available" page | HF |
| F2 | B | RelationshipExplorerPage.tsx:331,349,423 | §2 deep-link discipline ("makes a journey unusable") | `href="#R-01"` fragments hit the hash router and navigate to Search, destroying explorer state → scrollIntoView handlers, never bare fragments | HF |
| F3 | B | ContractAtlasPage.tsx:159–168; CallerMapPage.tsx:68–91; CallerComparisonPage.tsx:294–318 | §2 "view state lives in the URL" (MUST) | Filters, cursor stacks, page index, selection all in component state; reload/back resets the analysis → encode in hash query as ServiceDirectory/Explorer already do | T43.8 |
| F4 | B | Blame:47–51, Commit:59, CallerMap:952–961, WorkbenchEvidenceSteps:2395–2409, AnalysisScopePanel:560–570,930–940, Explorer:291–295, ExactCallerCitation:87–97, Impact:461 | §3 WCAG AA floor | `statusAmber #FFC043` as light-mode text ≈1.6:1 — the honest moments are near-invisible → add `statusAmberText` light-mode value; keep #FFC043 for dots/borders/dark | T43.2 |
| F5 | M | App.tsx:229 | §2 deep-link discipline | Hardcoded `#/investigations?id=04` fixture ID in product chrome → bare route | HF |
| F6 | M | SettingsPage.tsx:28–36,195–198 | §2 "empty page is never an absence claim" | "No API keys." rendered during fetch → loading state before empty branch | HF |
| F7 | M | App.tsx:99–125,165 | §2 state completeness | Route/gate loading is a bare unlabeled corner spinner → shared centered role="status" block (pattern exists at WorkbenchPage:305–323) | T43.3 |
| F8 | M | ReposPage.tsx:76–81 | §2 fail-closed ("bounded error with retry") | Any poll error replaces the whole table, no retry, self-heal unmentioned → keep last-good table + stale/error band | T43.3 |
| F9 | M | SearchPage:609–613; ContractAtlas:231,1034–1051; Impact:333–336; CallerMap:662–678; WorkbenchEvidenceSteps:681; AnalysisScopePanel:833–844 | §3 "digest strings never lead a surface" | Inline digest text leads six surfaces; none copyable/verifiable → digests behind the audit altitude as identity objects | T43.5 |
| F10 | M | AnalysisScopePanel.tsx:86 + placements | §1 "the answer leads" | Panel motto "Authority before rows," rendered above results on five surfaces → collapse to one-line authority chip row by default | T43.5 |
| F11 | M | Atlas:459–470; CallerMap:289–299; Comparison:485–495; Impact:362–364; Workbench; KafkaTopics:159–162 | §3 "caveats collapse, never disappear" | Permanent caveat walls; Kafka's summary is an ID not a claim → one shared ClaimBoundary (one line + establishes/does-not-establish disclosure) | T43.6 |
| F12 | M | ContractAtlas:677–679; CallerMap:948–950; checklist badges | §3 chip → drawer → receipt; "staleness is worn" | No drawer exists; stale/current chips inert, name no generation; age rarely relative+absolute → build the authority drawer once; every status chip opens it | T43.5 |
| F13 | M | systemic | §3 two densities | No density control anywhere; density achieved by shrinking type globally | T43.11 |
| F14 | M | WorkbenchEvidenceSteps:2353–2387,2629–2638; AnalysisScopePanel:1030–1039; Comparison:805; Explorer:618–622; CallerMap; ServiceDirectory:584–588 | §3 type scale | 8–9.5px persistent text endemic; six weights; six h1 scales → token type scale (11px floor), enforced via kit | T43.2 |
| F15 | M | Search:1007–1010; Impact:448–450,527–539; WorkbenchPage:799–804 | §3 AA floor | `tok.gutter` as text ≈2.6:1 light/3.5:1 dark → textTertiary minimum for legible text | T43.2 |
| F16 | M | KafkaTopicsPage.tsx:138–172 | §1 "the answer leads" — or the charter is wrong for census-shaped answers | Census renders before producers/consumers, deliberately (code comment) → adjudicated 2026-08-07: charter amended with the narrow census exception; page ordering stands; T43.6 adds the one-line "census is the answer" naming the exception requires | resolved (charter §1) + T43.6 |
| F17 | M | InvestigationPage.tsx:124–141 | §2 state completeness; §1 nothing implied | On refusal, tablist stays interactive while all tabs render the same RefusalView → disable/annotate tabs on refusal | HF |
| F18 | M | WorkbenchPage.tsx:724–814; WorkbenchEvidenceSteps:2281–2298 | §2 state completeness (journey) | Step rail carries no locked/done/available state; gates discovered only by landing on them → per-step state markers with reasons | T43.8 |
| F19 | M | WorkbenchEvidenceSteps.tsx:135–142,219–293 | §1 cognitive calm | Two stacked filter panels, two Apply buttons, both submit everything → one filter surface, one Apply | T43.8 |
| F20 | M | FilePage.tsx:527–536 | §3 keyboard-first ("every journey completable") | Symbol selection is posAtCoords mouse-only; definition/references unreachable by keyboard → CodeMirror keymap + panel shortcut | T43.9 |
| F21 | M | SearchPage.tsx:904,1120–1127 | §3 a11y baseline | j/k cursor visual-only (no aria-activedescendant); match-count live region re-announces per batch → roving tabindex + debounced live region | T43.9 |
| F22 | M | CallerMapPage.tsx:160–165 + page | §3 keyboard-first | 100-row pages, zero keyboard support; Next-page disables at cursor cap unexplained | T43.9 |
| F23 | M | CallerComparisonPage.tsx:947–968 | §3 closed status vocabulary; voice | Raw enum `old_only_evidence` in chip; unresolved red here, amber elsewhere → humanized labels + vocabulary tones | T43.3 |
| F24 | M | SearchPage.tsx:969–974,1070–1075; SectionHelp:189–190 | §3 durations are tokens | 320ms entry animation (over cap); 100ms popover (under floor) → motion tokens in theme.ts | T43.2/T43.12 |
| F25 | M | SettingsPage.tsx:240–247; AnalysisScopePanel:1006; CallerMap chips | §3 status vocabulary ("unavailable is blue") | Capacity-unavailable rendered red (false 2am alarm); unavailable amber elsewhere → single `statusToneFor(state)` in theme | T43.2/T43.10 |
| F26 | m | SearchPage.tsx:597 | §2 deep-link discipline | "Choose a service" routes to /services without repository → two-hop dead-end → route via /repos or carry context | T43.8 |
| F27 | m | WorkbenchPage.tsx:495–505,718 | §3 voice ("nothing in-product persuades") | clamp-54px marketing headline + permanent "No implicit writes" reassurance chip | T43.6 |
| F28 | M | ImpactPage.tsx:214–222,257–271 | §2 authority-boundary of input; Operate mode | Hand-authored JSON arrays in textareas for contract-change mode → per-file path+content rows (ProposalSourceEditor pattern exists) | T43.3 |
| F29 | M | Explorer:234; WorkbenchEvidenceSteps:2273–2276; "exact" everywhere | §3 voice ("sentences are short, states are named") | Contract-ese as user copy; "exact" as verbal tic diluting a load-bearing term → copy pass reserving "exact" for contrastive use | T43.6 |
| F30 | m | SearchPage.tsx:375,920–927 | §3 voice | `repo:zoekt` placeholder; syntax help is an external GitHub link → in-product help, neutral example | T43.6 |
| F31 | m | HistoryPage.tsx:97 | §2 "never an absence claim" | "No commits." bare → "No commits are visible for this path at {ref}." | T43.6 |
| F32 | M | ContractAtlas:1294–1300 vs CallerMap; AnalysisScopePanel | §3 closed vocabulary | unresolved red vs amber; unpublished neutral vs amber → semantic-status module | T43.2 |
| F33 | m | FilePage.tsx:208,355–361,414 | §3 a11y | Sticky offsets hardcoded to 52px header; aria-label diverges from visible label | T43.3 |
| F34 | m | CommitPage PatchView | §3 tables are the evidence surface | Flat div-per-line diff, no per-file grouping, no contentVisibility | T43R.2 (closed) |
| F35 | m | InvestigationPage.tsx:245 | §1 nothing implied | Permanent dead "What changed — '—'" quarter implies absent capability | T43.3 |
| F36 | m | AnalyticsPage.tsx:117 | §3 a11y ("text conveys everything color conveys") | Bar values hover-title only; zero-height = missing-data ambiguity | T43.10 |
| F37 | M | ui/src/pages/* | §2 charter review presupposes a kit | Nine chip components, three table systems, five focusRing clones, seven truncation helpers with different arithmetic, duplicated validateCitation → extract kit, delete locals | T43.3 |
| F38 | m | App shell | §2 deep-link discipline | `document.title` never set on any route | T43.8 |

## 5. Cognitive load

Workbench "What" exposes 20+ simultaneous controls with no in-step
disclosure (WorkbenchPage:948–1197). Caller Map 8 and Comparison 10
always-expanded filter controls, none persisted (F3), so the cost re-pays
per reload. Header nav: 10 ungrouped items; Services/Relationships absent
from nav while Topics is present — IA does not match the object model.
Checklist disposition defaults to "accepted" (the risky category is the
zero-effort path). Applied-filter state is invisible after Apply. The
AnalysisScopePanel chip row can surface eight chip species with no priority
order — a legend-less status telegraph.

## 6. Emotional journey

Peaks exist and are charter-true: RefusalView (contract text as headline,
calm identity rail) and Caller Map's "This is not evidence of zero callers"
have real spine; loading voice ("Reading exact catalog state…") makes
waiting feel like verification. The aggregate, though, is disclaimer
fatigue: the same non-claims repeat in 10px tertiary prose on nearly every
surface until they are wallpaper — the honesty has no visual form (F11/F12)
so it compensates with volume. The worst honest moment is silent (F1), and
light mode renders the amber honest moments near-invisible (F4) — the
emotional design inverts by theme.

## 7. Personas

**Alex (staff engineer, keyboard-first):** FilePage symbol nav mouse-only
(F20); Caller Map state evaporates on reload (F3) and has no keyboard
cursor (F22); selected Atlas operation unshareable (F3); no digest
copyable anywhere; no global navigator.
**Jordan (first-timer, deep link):** capability-absent link lands on Search
with no explanation (F1); 9px tertiary citation caveat with near-invisible
amber chip (F4); tier/lineage jargon carries no help outside two surfaces;
explorer row-ID click throws her to Search (F2).
**Sam (SRE, 2am):** lifecycle pressure only inside admin Settings, no
auto-refresh, no "as of" on the reading; capacity-unavailable renders red
(F25); audit log unfilterable; one flaky poll blanks the Repos table (F8);
refusal counts live at the deepest, smallest altitude
(AnalysisScopePanel:857–864). Live specimen: during this audit the T35.3
disk watermark refused indexing and the UI showed only red "Failed index"
with a truncated path — the cause was log-only.

## 8. Strengths and open questions

Strengths: view-layer authority validators that refuse mismatched rows;
the differentiated emptiness vocabulary (ServiceDirectory:296–313 is the
model); ServiceDirectory/Explorer URL discipline; Brand mark/loader;
ContractDependencyMap (diagram subordinate to table, mobile fallback);
SectionHelp's evidence/authority-boundary structure — under-deployed.

Open questions for the charter owner: (1) if ClaimBoundary existed
everywhere, could 80% of the disclaimer prose be deleted so the remainder
is finally read? (2) Kafka census-before-answer is a live, code-commented
conflict with §1 — adjudicate. (3) The square "instrument" dialect of
Atlas/Workbench is the more ownable identity than the rounded git surfaces
— promote it to the token layer, or keep three aesthetics?

## 9. Adjudications (2026-08-07)

Rulings by the charter owner on the three decisions this audit raised:

1. **Blockers — hotfix first.** F1, F2, F5, F6, F17 (the small functional
   breaks) land on `ux/t43.1f-blocker-hotfix`, stacked on this record. F3
   lands as the first act of T43.8 and F4 as the first act of T43.2.
2. **F16 — charter amended, page stands.** §1 gains the narrow census
   exception (census-shaped surfaces where coverage is the claim may lead;
   digests never promoted; ordering named in one line). Kafka Topics keeps
   its ordering; T43.6 adds the required one-line naming.
3. **Live round — resume after disk relief.** The operator frees ~15 GB;
   the dev instance restarts below the T35.3 watermark and the deferred
   populated-surface confirmation completes under this record. Question (2)
   above is thereby closed; (1) and (3) remain open for T43.6 and T43.2.
