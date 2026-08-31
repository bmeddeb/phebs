# phebs · design charter

**Status:** adopted design authority for every phebs product surface ·
**Owns:** presentation philosophy, design discipline, and design language ·
**Does not own:** evidence semantics, authority, authorization, claims, or
release posture — those belong to the domain contracts and the dated PLAN
decisions, and nothing in this charter may broaden them.

This charter exists because half of the product's success hangs on how its
honesty *feels*. phebs' differentiator is that every answer carries
provenance, explicit gaps, and refusal boundaries. Rendered carelessly, that
honesty reads as clutter; rendered well, it is the brand. The charter fixes
the philosophy, the process discipline, and the shared visual language so the
interface stays coherent while the program scales.

## 1. Philosophy — calm honesty

The interface has one job: **make trustworthy evidence feel effortless**.

- **Hierarchy of trust.** Every surface presents three altitudes. The
  *answer* (results, rows, counts) leads. Its *authority* (state, generation,
  scope, "as of" revision) is one gesture away. The full *audit* (digests,
  policies, receipts, citations) is one further gesture. Digest strings never
  lead a surface; absence of authority is never hidden behind one.
- **Honesty is polish, not tax.** A stale service, an abstention, a refusal,
  or an empty page receives the same design attention as a result. The
  product's best moments are its honest ones; they must feel deliberate,
  never apologetic.
- **The answer is bounded; say so calmly.** Truncation, admission caps,
  unavailable planes, and partial coverage are stated in one short line where
  they apply — not in a paragraph, and never omitted.
- **Nothing implied.** No currency implied by layout, no completeness implied
  by a full page, no relationship implied by adjacency. If the authority does
  not establish it, the interface does not suggest it.
- **Census exception** *(amended 2026-08-07, adjudicated in T43.1)*. On a
  census-shaped surface — where the claim boundary makes coverage itself the
  answer, as on the topic census — the census may lead. The exception is
  narrow: it applies only where the census is itself the claim the authority
  establishes; it never promotes digests or audit material to the leading
  altitude; and the surface states in one line that the census is the answer,
  so the ordering reads as deliberate rather than as preamble.

## 2. Design discipline

These process rules bind every UI ticket. They extend, and never replace,
the repository-wide merge bar in [BACKLOG.md](./BACKLOG.md).

- **Authority-boundary rule.** Every rendered datum must map to a named field
  of a receipt, contract, or authority response. Concept boards and generated
  mockups are reviewed against this rule *before* implementation; a concept
  field with no backing authority (an owner, an endpoint, a runtime claim) is
  removed at design time, and the removal is recorded in the ticket.
- **Fail-closed rendering.** A malformed, crossed-authority, or
  bound-exceeding response renders as a bounded error with a retry path —
  never as partial data. Response validators live beside the fetch and check
  echoed query identity, root authority, and count arithmetic.
- **State completeness.** Loading, empty, sparse-window, filtered-empty,
  partial, stale, error, retry, and unavailable are all designed states, all
  conveyed by text as well as color, and all announced to assistive
  technology. An empty page is never presented as an absence claim.
- **Deep-link discipline.** View state — scope, filters, cursors, selection,
  step — lives in the URL. Reload and browser back/forward reproduce the
  same authorized request; nothing of authority is retained client-side
  across navigation.
- **Per-ticket gates.** Every UI ticket ends demoable on the `make dev`
  neutral cohorts and passes: desktop and 390 px browser QA with no document
  overflow; keyboard-complete journeys with visible focus; a clean console;
  an accessibility audit pass; deterministic screenshot receipts over the
  neutral fixtures compared like any other retained artifact; UI-suite
  growth; and a recorded bundle-size and interaction-latency note.
- **Measurement honesty.** Walkthrough timings and task completion measured
  on neutral cohorts are *mechanics* evidence for design decisions. They are
  never user-validation, usability, or product-market claims; those remain
  behind a separately sealed evaluation, per the retained T39 boundary.
- **Charter review.** Every UI ticket is reviewed against this charter —
  hierarchy, density, states, keyboard, motion restraint, and copy — with
  findings recorded in the ticket like any code-review finding.

## 3. Design language

### Identity and status

- **Status vocabulary is closed and colored once.** `current` green, `stale`
  amber, `conflict` red, `removed` neutral, `unavailable` blue — as semantic
  tokens, used identically on every surface, always paired with the state
  word. Color never carries meaning alone.
- **Identities are monospace.** Paths, service keys, revisions, digests, and
  operations render in the mono stack; prose and controls render in the UI
  stack. Counts use tabular numerals.
- **Staleness is worn, not confessed.** A non-current authority carries an
  "as of" chip naming its exact generation or revision; the chip opens the
  authority drawer. Age is stated as both relative and absolute time.

### Disclosure

- **Chip → drawer → receipt.** Authority appears as a compact chip on the
  answer; the chip opens a drawer with state, generations, scope, and
  citations; the drawer opens the full receipt with copyable digests and a
  verify affordance. Each altitude is complete at its own level.
- **Caveats collapse, never disappear.** Every surface with a claim boundary
  shows a one-line summary ("static source evidence within the displayed
  snapshots") with an expandable "establishes / does not establish" section
  carrying the exact contract language. The words are the contract's; the
  wall is not.
- **Citations are objects.** A citation renders as `path:line` with its span,
  opens in place with surrounding context, and exposes its immutable identity
  in the audit altitude. Expired or superseded citations fail closed with a
  refresh path.

### Typography

- **Interface text has an 11 px minimum.** A single named 10 px
  `evidenceMetadata` token may be used only for non-interactive,
  supplementary machine metadata that repeats or qualifies information
  already presented at 11 px or larger. It must never carry the sole status,
  warning, error, caveat, action, navigation label, field label, or authority
  claim. Metadata meets the normal-text contrast requirement and remains
  readable without clipping at 200% zoom and at the 390 px viewport.
- **The exception is closed.** No arbitrary 8 px, 9 px, 9.5 px, 10 px, or
  10.5 px values exist outside the named token. Font shorthand, font-size
  keywords, aliases, `calc()`, standalone relative values, and unresolved
  dynamic sizes are not alternate routes around the floor; a responsive
  `clamp()` must name an absolute minimum of at least 11 px. Controls and
  actionable text remain at least 11 px. If removing the small text would
  remove essential meaning, it is at least 11 px. Every legacy site is
  adjudicated; the exception does not automatically legitimize prior usage.

### Layout, density, and input

- **Tables are the evidence surface.** Diagrams, charts, and summaries are
  subordinate projections of a visible table and add no edge, transitivity,
  or completeness the table does not show.
- **Local scroll only.** Wide content scrolls inside its container; the
  document never scrolls horizontally at any supported width down to 390 px.
- **Two densities.** Comfortable is the default; dense is offered where
  operators scan (directories, explorers, operations). Density changes
  spacing and row height, never information.
- **Keyboard-first.** Every journey is completable from the keyboard; search
  and navigation have first-class shortcuts; focus is always visible; a
  global navigator is a goal of this program, not an afterthought.

### Motion

- **Motion communicates state transitions only** — appearance of a drawer,
  settlement of a result, a status change. Nothing moves to decorate.
- **Durations are tokens:** 120–200 ms for element transitions, at most
  300 ms for panels; standard easing tokens; `prefers-reduced-motion`
  replaces movement with opacity or immediate state.

### Voice

- Product copy uses the contracts' honest stopping language: "not an absence
  claim", "establishes mechanics only", "no runtime topology is implied".
- Sentences are short, states are named, and nothing in-product persuades:
  no marketing adjectives, no reassurance without authority.

### Accessibility baseline

- WCAG AA contrast in both themes; landmarks, labels, and live regions on
  every state change; stable accessible names; 390 px support; text conveys
  everything color conveys. This codifies existing practice as a floor, not
  a target.

## 4. Boundaries

This charter governs presentation. It grants no authority, establishes no
accuracy, completeness, scale, or release claim, and cannot relax a domain
contract, an admission bound, or a caveat's wording — it may only present
them better. Changes to this charter follow the same review discipline as
any source-of-truth document.
