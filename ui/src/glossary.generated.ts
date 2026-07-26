// Code generated from internal/glossary/glossary.json; DO NOT EDIT.
export const glossarySchemaVersion = "change-workbench-glossary-v1" as const
export const glossaryDigest = "sha256:ce13b715607141f2833c8ade57b8aa552d89b68475c5884257972a7defcb3274" as const

export const glossaryCapabilities = [
  "caller-map-exact-identity",
  "change-workbench",
  "code-navigation",
  "contract-atlas",
  "contract-impact-report",
  "coverage-certificate",
  "history",
  "source-search"
] as const
export type GlossaryCapability = typeof glossaryCapabilities[number]
export type GlossaryMode = 'add' | 'migrate' | 'modify' | 'retire'
export type GlossarySurface = 'atlas' | 'caller_map' | 'impact' | 'manual' | 'mcp' | 'workbench'

export type GlossaryTerm = Readonly<{
  id: string
  label: string
  shortHelp: string
  expandedHelp: string
  evidenceBoundary: string
  authorityBoundary: string
  modes: readonly GlossaryMode[]
  surfaces: readonly GlossarySurface[]
  wireAliases: readonly string[]
  availability: Readonly<{
    requiresAll: readonly GlossaryCapability[]
    requiresAny: readonly GlossaryCapability[]
    unavailableHelp: string
  }>
}>

export const glossaryTerms = [
  {
    "id": "analysis_scope_and_gaps",
    "label": "Analysis scope \u0026 gaps",
    "shortHelp": "Shows what phebs examined, what evidence was available, and what remained unsupported or unresolved.",
    "expandedHelp": "This summary binds visible repositories and revisions to evidence domains, freshness, failures, inventory boundaries, unresolved counts, and unsupported planes. It qualifies the adjacent result and is not a completeness score.",
    "evidenceBoundary": "It summarizes recorded processing and inventory state; it does not prove that unobserved callers, resources, or runtime uses do not exist.",
    "authorityBoundary": "Only the requesting principal's authorized repository universe contributes rows, counts, or capability state.",
    "modes": [
      "add",
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "caller_map",
      "impact",
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [
      "coverage",
      "coverage_rows"
    ],
    "availability": {
      "requiresAll": [],
      "requiresAny": [
        "contract-atlas",
        "contract-impact-report",
        "coverage-certificate"
      ],
      "unavailableHelp": "Analysis scope \u0026 gaps is unavailable because no supporting contract or coverage capability is enabled."
    }
  },
  {
    "id": "could_not_resolve",
    "label": "Could not resolve",
    "shortHelp": "A relevant source construct was observed, but the bounded resolver deliberately did not assign it to one identity.",
    "expandedHelp": "This is an extractor abstention, not a confirmed caller and not a processing failure. The reason and cited source remain available for review when the evidence pack recorded them.",
    "evidenceBoundary": "The row proves an observed construct and a refusal reason only; it makes no claim about the construct's runtime target.",
    "authorityBoundary": "The label is derived from authorized published evidence and cannot be upgraded by presentation code.",
    "modes": [
      "add",
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "caller_map",
      "impact",
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [
      "unresolved_candidates"
    ],
    "availability": {
      "requiresAll": [],
      "requiresAny": [
        "caller-map-exact-identity",
        "contract-impact-report"
      ],
      "unavailableHelp": "Resolver abstentions are unavailable because no supporting caller or impact capability is enabled."
    }
  },
  {
    "id": "coverage_certificate",
    "label": "Coverage certificate",
    "shortHelp": "The deterministic audit receipt behind Analysis scope \u0026 gaps.",
    "expandedHelp": "The certificate records the authorized repository universe, indexed revisions, published extraction runs, freshness, failures, counts, protocols, and inventory boundaries under one content digest.",
    "evidenceBoundary": "It proves change detection over recorded extraction state, not extraction correctness, business completeness, or runtime absence.",
    "authorityBoundary": "Invisible repositories are structurally unreachable to the builder and never appear in certificate bytes or counts.",
    "modes": [
      "add",
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "atlas",
      "impact",
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [
      "coverage-certificate-v1"
    ],
    "availability": {
      "requiresAll": [
        "coverage-certificate"
      ],
      "requiresAny": [],
      "unavailableHelp": "The coverage certificate is unavailable because extraction coverage is not enabled for this surface."
    }
  },
  {
    "id": "implementation_evidence",
    "label": "Implementation evidence",
    "shortHelp": "Cited source or history that may inform how the change is implemented.",
    "expandedHelp": "Search matches, definitions, references, tests, mocks, documentation, blame, commits, and diffs retain immutable repository, revision, path, and span provenance plus the rule that selected them.",
    "evidenceBoundary": "Similarity or proximity is not a correctness ranking and does not authorize an edit.",
    "authorityBoundary": "The developer reviews and decides whether evidence is relevant; phebs does not turn it into an instruction.",
    "modes": [
      "add",
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [],
    "availability": {
      "requiresAll": [],
      "requiresAny": [
        "code-navigation",
        "history",
        "source-search"
      ],
      "unavailableHelp": "Implementation evidence is unavailable because search, code navigation, and history capabilities are not available."
    }
  },
  {
    "id": "matching_static_evidence",
    "label": "Matching static evidence",
    "shortHelp": "A source occurrence whose extracted object matches the question.",
    "expandedHelp": "The occurrence keeps its immutable citation and extraction tier. A matching operation object may not be joined to one declaration lineage, generated client, logical service, deployable, or runtime use.",
    "evidenceBoundary": "This is source-level matching evidence, not a proven service roster or a resolved caller for one exact declaration.",
    "authorityBoundary": "Presentation code may qualify or group the row but cannot promote its evidence tier or lineage.",
    "modes": [
      "add",
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "atlas",
      "caller_map",
      "impact",
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [
      "known_consumers"
    ],
    "availability": {
      "requiresAll": [],
      "requiresAny": [
        "contract-atlas",
        "contract-impact-report"
      ],
      "unavailableHelp": "Matching static evidence is unavailable because contract evidence is not enabled."
    }
  },
  {
    "id": "name_match_needing_review",
    "label": "Name match needing review",
    "shortHelp": "An operation-name match that is not proven to belong to the selected declaration.",
    "expandedHelp": "The source citation and candidate operation remain reviewable, but missing or ambiguous generated-client and declaration provenance prevents exact caller attribution.",
    "evidenceBoundary": "A shared method name is not contract identity and cannot establish blast radius for one declaration.",
    "authorityBoundary": "Only a validated exact-identity join may promote the row to Resolved caller.",
    "modes": [
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "caller_map",
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [
      "unresolved_name_match"
    ],
    "availability": {
      "requiresAll": [
        "caller-map-exact-identity"
      ],
      "requiresAny": [],
      "unavailableHelp": "Name-match review is unavailable until the exact-identity Caller Map capability is enabled."
    }
  },
  {
    "id": "resolved_caller",
    "label": "Resolved caller",
    "shortHelp": "A source call occurrence joined through generated-client provenance to the exact selected declaration lineage.",
    "expandedHelp": "The row retains the call-site citation, generated symbol, wire operation, declaration lineage, and any separate unit attribution. Missing or ambiguous attribution never removes the source occurrence.",
    "evidenceBoundary": "Static resolution does not prove runtime execution, traffic, ownership, or migration completion.",
    "authorityBoundary": "Only the exact-identity Caller Map service may emit this label; legacy matching evidence cannot be renamed into it.",
    "modes": [
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "caller_map",
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [
      "resolved_caller"
    ],
    "availability": {
      "requiresAll": [
        "caller-map-exact-identity"
      ],
      "requiresAny": [],
      "unavailableHelp": "Resolved callers are unavailable until declaration-proven caller identity is enabled; matching static evidence remains separate."
    }
  },
  {
    "id": "success_criterion",
    "label": "Success criterion",
    "shortHelp": "A human-authored condition used to judge whether the ticket achieved its intended outcome.",
    "expandedHelp": "Phebs may attach cited evidence and analysis gaps to the criterion, but it cannot invent the business condition or declare it satisfied.",
    "evidenceBoundary": "Code and contract evidence can inform review but cannot establish a business outcome by itself.",
    "authorityBoundary": "Only an explicit authorized human revision records or changes a success criterion.",
    "modes": [
      "add",
      "migrate",
      "modify",
      "retire"
    ],
    "surfaces": [
      "manual",
      "mcp",
      "workbench"
    ],
    "wireAliases": [],
    "availability": {
      "requiresAll": [
        "change-workbench"
      ],
      "requiresAny": [],
      "unavailableHelp": "Structured success criteria are unavailable until Change Workbench is enabled."
    }
  }
] as const satisfies readonly GlossaryTerm[]
export type GlossaryTermId = typeof glossaryTerms[number]['id']
