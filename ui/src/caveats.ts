// Client-side claim-boundary wording (T43.6). Every string here is either a
// mirror of an owning Go contract constant (named beside it) or adjudicated
// charter language. caveats.test.ts pins each string byte-for-byte so wording
// cannot drift silently; server-delivered caveat fields are always rendered
// verbatim in preference to anything here.

// Charter §1 census exception (adjudicated 2026-08-07, T43.1 §9): a
// census-shaped surface names its ordering in one line.
export const CENSUS_LEADS_LINE =
  'The census leads deliberately: unresolved coverage is the claim this ' +
  'page establishes, and the producer and consumer rows below are evidence ' +
  'within it.'

// Mirror of internal/api/relationships.go relationshipCaveat. Fallback only:
// wherever a ServiceRelationshipPage is in scope, its server-delivered
// `caveat` renders instead of this constant.
export const RELATIONSHIP_CAVEAT_MIRROR =
  'Static source evidence only; no runtime topology, completeness, ' +
  'migration-complete, decommissioning-safety, or release claim.'

// Presentation addenda (no Go owner): honest extra sentences a surface adds
// AFTER the contract text. Centralized here so wording is governed and
// pinned, and cannot masquerade as contract language.
export const EXPLORER_DIAGRAM_ADDENDUM =
  'The table is authoritative for this exact page; the optional diagram ' +
  'adds no edge or transitivity.'
export const CALLER_ROWS_UNAVAILABLE_ADDENDUM =
  'This is not zero callers, and no partial rows are shown.'
export const COMPARISON_UNAVAILABLE_ADDENDUM =
  'No partial rows are classified, and this is not evidence of zero callers.'
