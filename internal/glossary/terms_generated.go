// Code generated from glossary.json; DO NOT EDIT.

package glossary

const Digest = "sha256:354838d7094f74b1f6951f11485d6a915f26c07211a0abbc820263842084b330"

const (
	CapabilityCallerMapExactIdentity Capability = "caller-map-exact-identity"
	CapabilityChangeWorkbench        Capability = "change-workbench"
	CapabilityCodeNavigation         Capability = "code-navigation"
	CapabilityContractAtlas          Capability = "contract-atlas"
	CapabilityContractImpactReport   Capability = "contract-impact-report"
	CapabilityCoverageCertificate    Capability = "coverage-certificate"
	CapabilityHistory                Capability = "history"
	CapabilityServiceCatalogV2       Capability = "service-catalog-v2"
	CapabilityServiceRelationshipsV1 Capability = "service-relationships-v1"
	CapabilitySourceSearch           Capability = "source-search"
)

const (
	ModeAdd     Mode = "add"
	ModeMigrate Mode = "migrate"
	ModeModify  Mode = "modify"
	ModeRetire  Mode = "retire"
)

const (
	SurfaceAtlas                Surface = "atlas"
	SurfaceBlame                Surface = "blame"
	SurfaceCallerMap            Surface = "caller_map"
	SurfaceCommit               Surface = "commit"
	SurfaceFile                 Surface = "file"
	SurfaceHistory              Surface = "history"
	SurfaceImpact               Surface = "impact"
	SurfaceManual               Surface = "manual"
	SurfaceMCP                  Surface = "mcp"
	SurfaceRelationshipExplorer Surface = "relationship_explorer"
	SurfaceServiceDirectory     Surface = "service_directory"
	SurfaceWorkbench            Surface = "workbench"
)

const (
	TermAnalysisScopeAndGaps    TermID = "analysis_scope_and_gaps"
	TermCouldNotResolve         TermID = "could_not_resolve"
	TermCoverageCertificate     TermID = "coverage_certificate"
	TermExactStaticRelationship TermID = "exact_static_relationship"
	TermImplementationEvidence  TermID = "implementation_evidence"
	TermMatchingStaticEvidence  TermID = "matching_static_evidence"
	TermNameMatchNeedingReview  TermID = "name_match_needing_review"
	TermResolvedCaller          TermID = "resolved_caller"
	TermServiceCatalogAuthority TermID = "service_catalog_authority"
	TermSuccessCriterion        TermID = "success_criterion"
)

var Capabilities = []Capability{CapabilityCallerMapExactIdentity, CapabilityChangeWorkbench, CapabilityCodeNavigation, CapabilityContractAtlas, CapabilityContractImpactReport, CapabilityCoverageCertificate, CapabilityHistory, CapabilityServiceCatalogV2, CapabilityServiceRelationshipsV1, CapabilitySourceSearch}

var Terms = []Term{
	{
		ID:                TermAnalysisScopeAndGaps,
		Label:             "Analysis scope & gaps",
		ShortHelp:         "Shows what phebs examined, what evidence was available, and what remained unsupported or unresolved.",
		ExpandedHelp:      "This summary binds visible repositories and revisions to evidence domains, freshness, failures, inventory boundaries, unresolved counts, and unsupported planes. It qualifies the adjacent result and is not a completeness score.",
		EvidenceBoundary:  "It summarizes recorded processing and inventory state; it does not prove that unobserved callers, resources, or runtime uses do not exist.",
		AuthorityBoundary: "Only the requesting principal's authorized repository universe contributes rows, counts, or capability state.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"caller_map", "impact", "manual", "mcp", "workbench"},
		WireAliases:       []string{"coverage", "coverage_rows"},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{},
			RequiresAny:     []Capability{"contract-atlas", "contract-impact-report", "coverage-certificate"},
			UnavailableHelp: "Analysis scope & gaps is unavailable because no supporting contract or coverage capability is enabled.",
		},
	},
	{
		ID:                TermCouldNotResolve,
		Label:             "Could not resolve",
		ShortHelp:         "A relevant source construct was observed, but the bounded resolver deliberately did not assign it to one identity.",
		ExpandedHelp:      "This is an extractor abstention, not a confirmed caller and not a processing failure. The reason and cited source remain available for review when the evidence pack recorded them.",
		EvidenceBoundary:  "The row proves an observed construct and a refusal reason only; it makes no claim about the construct's runtime target.",
		AuthorityBoundary: "The label is derived from authorized published evidence and cannot be upgraded by presentation code.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"caller_map", "impact", "manual", "mcp", "workbench"},
		WireAliases:       []string{"unresolved_candidates"},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{},
			RequiresAny:     []Capability{"caller-map-exact-identity", "contract-impact-report"},
			UnavailableHelp: "Resolver abstentions are unavailable because no supporting caller or impact capability is enabled.",
		},
	},
	{
		ID:                TermCoverageCertificate,
		Label:             "Coverage certificate",
		ShortHelp:         "The deterministic audit receipt behind Analysis scope & gaps.",
		ExpandedHelp:      "The certificate records the authorized repository universe, indexed revisions, published extraction runs, freshness, failures, counts, protocols, and inventory boundaries under one content digest.",
		EvidenceBoundary:  "It proves change detection over recorded extraction state, not extraction correctness, business completeness, or runtime absence.",
		AuthorityBoundary: "Invisible repositories are structurally unreachable to the builder and never appear in certificate bytes or counts.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"atlas", "impact", "manual", "mcp", "workbench"},
		WireAliases:       []string{"coverage-certificate-v1", "coverage-certificate-v2", "coverage-certificate-v3"},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{"coverage-certificate"},
			RequiresAny:     []Capability{},
			UnavailableHelp: "The coverage certificate is unavailable because extraction coverage is not enabled for this surface.",
		},
	},
	{
		ID:                TermExactStaticRelationship,
		Label:             "Exact static relationship",
		ShortHelp:         "A published RPC or Kafka source relationship bound to one exact service incarnation and generation.",
		ExpandedHelp:      "Each row retains its repository, selected service key, incarnation, service generation, relationship root, evidence kind and plane, lookup key, attribution class, and immutable source citation. Missing or unavailable roots remain explicit gaps.",
		EvidenceBoundary:  "Static source evidence does not prove runtime execution, traffic, ownership, or completeness; an empty result is exact only when every authorized root is complete or empty.",
		AuthorityBoundary: "Rows come only from authorized exact-current relationship roots and are revalidated before cited source bytes load; presentation cannot promote ambiguous, shared, unowned, failed, or unavailable evidence into an exact runtime edge.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"manual", "mcp", "relationship_explorer"},
		WireAliases:       []string{},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{"service-relationships-v1"},
			RequiresAny:     []Capability{},
			UnavailableHelp: "Exact static relationships are unavailable because the service relationship capability is not enabled for this surface.",
		},
	},
	{
		ID:                TermImplementationEvidence,
		Label:             "Implementation evidence",
		ShortHelp:         "Cited source or history that may inform how the change is implemented.",
		ExpandedHelp:      "Search matches, definitions, references, tests, mocks, documentation, blame, commits, and diffs retain immutable repository, revision, path, and span provenance plus the rule that selected them.",
		EvidenceBoundary:  "Similarity or proximity is not a correctness ranking and does not authorize an edit.",
		AuthorityBoundary: "The developer reviews and decides whether evidence is relevant; phebs does not turn it into an instruction.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"blame", "commit", "file", "history", "manual", "mcp", "workbench"},
		WireAliases:       []string{},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{},
			RequiresAny:     []Capability{"code-navigation", "history", "source-search"},
			UnavailableHelp: "Implementation evidence is unavailable because search, code navigation, and history capabilities are not available.",
		},
	},
	{
		ID:                TermMatchingStaticEvidence,
		Label:             "Matching static evidence",
		ShortHelp:         "A source occurrence whose extracted object matches the question.",
		ExpandedHelp:      "The occurrence keeps its immutable citation and extraction tier. A matching operation object may not be joined to one declaration lineage, generated client, logical service, deployable, or runtime use.",
		EvidenceBoundary:  "This is source-level matching evidence, not a proven service roster or a resolved caller for one exact declaration.",
		AuthorityBoundary: "Presentation code may qualify or group the row but cannot promote its evidence tier or lineage.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"atlas", "caller_map", "impact", "manual", "mcp", "workbench"},
		WireAliases:       []string{"known_consumers"},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{},
			RequiresAny:     []Capability{"contract-atlas", "contract-impact-report"},
			UnavailableHelp: "Matching static evidence is unavailable because contract evidence is not enabled.",
		},
	},
	{
		ID:                TermNameMatchNeedingReview,
		Label:             "Name match needing review",
		ShortHelp:         "An operation-name match that is not proven to belong to the selected declaration.",
		ExpandedHelp:      "The source citation and candidate operation remain reviewable, but missing or ambiguous generated-client and declaration provenance prevents exact caller attribution.",
		EvidenceBoundary:  "A shared method name is not contract identity and cannot establish blast radius for one declaration.",
		AuthorityBoundary: "Only a validated exact-identity join may promote the row to Resolved caller.",
		Modes:             []Mode{"migrate", "modify", "retire"},
		Surfaces:          []Surface{"caller_map", "manual", "mcp", "workbench"},
		WireAliases:       []string{"unresolved_name_match"},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{"caller-map-exact-identity"},
			RequiresAny:     []Capability{},
			UnavailableHelp: "Name-match review is unavailable until the exact-identity Caller Map capability is enabled.",
		},
	},
	{
		ID:                TermResolvedCaller,
		Label:             "Resolved caller",
		ShortHelp:         "A source call occurrence joined through generated-client provenance to the exact selected declaration lineage.",
		ExpandedHelp:      "The row retains the call-site citation, generated symbol, wire operation, declaration lineage, and any separate unit attribution. Missing or ambiguous attribution never removes the source occurrence.",
		EvidenceBoundary:  "Static resolution does not prove runtime execution, traffic, ownership, or migration completion.",
		AuthorityBoundary: "Only the exact-identity Caller Map service may emit this label; legacy matching evidence cannot be renamed into it.",
		Modes:             []Mode{"migrate", "modify", "retire"},
		Surfaces:          []Surface{"caller_map", "manual", "mcp", "workbench"},
		WireAliases:       []string{"resolved_caller"},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{"caller-map-exact-identity"},
			RequiresAny:     []Capability{},
			UnavailableHelp: "Resolved callers are unavailable until declaration-proven caller identity is enabled; matching static evidence remains separate.",
		},
	},
	{
		ID:                TermServiceCatalogAuthority,
		Label:             "Service catalog authority",
		ShortHelp:         "The reviewed repository metadata that defines one service identity and its lifecycle state.",
		ExpandedHelp:      "The directory binds each service key to its repository, authority source, desired and active generations, incarnation, disposition, membership roles, and retained tombstone or successor state.",
		EvidenceBoundary:  "Catalog acceptance and source-path attribution do not prove ownership, deployment, runtime traffic, or relationship completeness.",
		AuthorityBoundary: "Only immutable accepted catalog and committed service state for repositories visible to the requesting principal may supply this label; presentation cannot promote desired, stale, conflict, unavailable, or removed state into current authority.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"manual", "mcp", "service_directory"},
		WireAliases:       []string{},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{"service-catalog-v2"},
			RequiresAny:     []Capability{},
			UnavailableHelp: "Service catalog authority is unavailable because the service catalog capability is not enabled for this surface.",
		},
	},
	{
		ID:                TermSuccessCriterion,
		Label:             "Success criterion",
		ShortHelp:         "A human-authored condition used to judge whether the ticket achieved its intended outcome.",
		ExpandedHelp:      "Phebs may attach cited evidence and analysis gaps to the criterion, but it cannot invent the business condition or declare it satisfied.",
		EvidenceBoundary:  "Code and contract evidence can inform review but cannot establish a business outcome by itself.",
		AuthorityBoundary: "Only an explicit authorized human revision records or changes a success criterion.",
		Modes:             []Mode{"add", "migrate", "modify", "retire"},
		Surfaces:          []Surface{"manual", "mcp", "workbench"},
		WireAliases:       []string{},
		Availability: CapabilityPredicate{
			RequiresAll:     []Capability{"change-workbench"},
			RequiresAny:     []Capability{},
			UnavailableHelp: "Structured success criteria are unavailable until Change Workbench is enabled.",
		},
	},
}

var termsByID = func() map[TermID]Term {
	result := make(map[TermID]Term, len(Terms))
	for _, term := range Terms {
		result[term.ID] = term
	}
	return result
}()

// Lookup returns one generated glossary term by stable identity.
func Lookup(id TermID) (Term, bool) {
	term, ok := termsByID[id]
	return term, ok
}
