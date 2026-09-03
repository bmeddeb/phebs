package t421

import (
	"errors"
	"slices"
)

// IdentityDerivation is a row of the prospective authority derivation table.
// ChangedInputs groups phases with identical changed production inputs. The
// schema's fixed defaults are cold="initial" and every unlisted operational
// phase="none" (equality is required, not merely permitted). Expand before
// displaying the complete identity-by-transition derivation table.
type IdentityDerivation struct {
	Identity      string            `json:"identity"`
	Constructor   string            `json:"constructor"`
	Inputs        []string          `json:"inputs"`
	ChangedInputs map[string]string `json:"changes"`
}

// ExpandedChangedInputs returns cold through product_queries. A complete plan
// order may include the non-authority preflight/teardown endpoints; they are
// removed together, never represented as identity transitions.
func (row IdentityDerivation) ExpandedChangedInputs(phaseOrder []string) ([]string, error) {
	if len(phaseOrder) >= 2 && phaseOrder[0] == "preflight" && phaseOrder[len(phaseOrder)-1] == "teardown" {
		phaseOrder = phaseOrder[1 : len(phaseOrder)-1]
	}
	if len(phaseOrder) == 0 || phaseOrder[0] != "cold" {
		return nil, errors.New("identity derivation requires cold-first operational phase order")
	}
	expanded := make([]string, len(phaseOrder))
	seen := make(map[string]bool, len(phaseOrder))
	for index, phase := range phaseOrder {
		if phase == "" || seen[phase] {
			return nil, errors.New("identity derivation has duplicate or empty operational phase")
		}
		seen[phase], expanded[index] = true, "none"
	}
	expanded[0] = "initial"
	seen = make(map[string]bool)
	for when, inputs := range row.ChangedInputs {
		if inputs == "" || inputs == "none" || inputs == "initial" {
			return nil, errors.New("identity derivation contains a redundant or empty input change")
		}
		phases := []string{when}
		// This shared pattern is exact: it excludes the logical-only revision.
		if when == "physical_revision" {
			phases = []string{"physical_delta_b", "return_a"}
		}
		for _, phase := range phases {
			index := slices.Index(phaseOrder, phase)
			if index <= 0 || seen[phase] {
				return nil, errors.New("identity derivation changed phase is absent, cold, or repeated")
			}
			seen[phase], expanded[index] = true, inputs
		}
	}
	return expanded, nil
}

func frozenIdentityDerivations() []IdentityDerivation {
	physical := func(changed string) map[string]string {
		if changed == "none" {
			return map[string]string{}
		}
		return map[string]string{"physical_revision": changed}
	}
	logical := func(source, catalog string) map[string]string {
		if source == catalog {
			return map[string]string{"physical_revision": source, "logical_delta_b": catalog}
		}
		return map[string]string{
			"physical_delta_b": source,
			"logical_delta_b":  catalog,
			"return_a":         source + "+" + catalog,
		}
	}
	rows := []IdentityDerivation{
		{"physical_commit", "Git commit hash", []string{"tree", "parent", "author", "committer", "message"}, physical("tree,parent,message")},
		{"physical_tree", "Git tree hash", []string{"ordered path/mode/OID"}, physical("one source record; return=A tree")},
		{"source_generation_sha256", "repositoryindex.SourceManifestDigest", []string{"repository", "revisions", "policy", "owners"}, physical("revisions.commit,owners")},
		{"search_generation_sha256", "repositoryindex.SearchManifestDigest", []string{"repository", "revisions", "source", "topology", "shardRoot"}, physical("revisions.commit,source,shardRoot")},
		{"observation_generation_sha256", "observationpublication.InventoryGenerationDigestV2", []string{"repository", "source", "partitionRoot/policy", "observationPolicy", "pack"}, physical("source,partitionRoot")},
		{"candidate_generation_sha256", "candidate.GenerationDigest", []string{"repository", "commit", "unit", "policies", "typedSelection"}, physical("commit")},
		{"extraction_roots_sha256", "receiptSHA256", []string{"ordered extraction_roots[]; native derivations below"}, physical("source-bound roots")},
		{"extraction_roots[].generation_sha256", "extractionpublication.Runtime.Reconcile", []string{"repository", "ordered planDigests"}, physical("planDigests")},
		{"extraction_roots[].root_sha256", "candidate.BuildDomainResultRoot", []string{"plan", "results", "totals", "disposition"}, physical("plan,results")},
		{"extraction_roots[].candidate_generation_sha256", "candidate.GenerationDigest", []string{"containing authority.candidate_generation_sha256"}, physical("commit")},
		{"extraction_roots[].source_generation_sha256", "repositoryindex.SourceManifestDigest", []string{"containing authority.source_generation_sha256"}, physical("revisions.commit,owners")},
		{"extraction_roots[].observation_generation_sha256", "observationpublication.InventoryGenerationDigestV2", []string{"containing authority.observation_generation_sha256"}, physical("source,partitionRoot")},
		{"extraction_roots[].plan_sha256", "extractionpublication.BuildReservedPlan", []string{"sparseDomain", "source", "observation", "extractor", "policy", "reservations"}, physical("sparseDomain,source,observation")},
		{"extraction_roots[].schedule_sha256", "none in V2", []string{"empty; operational IDs in RecoveryPreparationResult"}, physical("none")},
		{"extraction_roots[].typed_scope_sha256", "candidate.BuildSparse", []string{"admitted path/OID/size; SparseTypedScopeDescriptor.ContentDigest"}, physical("admitted source record; return=A scope")},
		{"extraction_roots[].typed_scope_descriptor_content_sha256", "candidate.BuildSparse", []string{"same bytes as typed_scope_sha256"}, physical("admitted source record; return=A scope")},
		{"extraction_roots[].members", "extractionResultMembers", []string{"length-framed t421-extraction-result-members-v1 + ordered result IDs"}, physical("result IDs; empty set unchanged")},
		{"extraction_roots[].partition_results_sha256", "receiptSHA256", []string{"ordered native-result projections"}, physical("partition/expectation/result IDs; empty array unchanged")},
		{"extraction_roots[].partition_results[].partition_sha256", "candidate.BuildSparse", []string{"candidate", "domain/policy", "member/scope", "coordinates"}, physical("candidate,admitted source")},
		{"extraction_roots[].partition_results[].expectation_sha256", "candidate.BuildDomainResultPlanV2/V3", []string{"partition", "coordinates", "reservation"}, physical("partition")},
		{"extraction_roots[].partition_results[].result_digest_sha256", "candidate.BuildPartitionResult", []string{"plan/expectation", "coordinates", "content/totals/disposition"}, physical("plan/expectation")},
		{"extraction_roots[].partition_results[].result_identity_sha256", "candidate.PartitionResultIdentity", []string{"partitionDigest", "resultDigest"}, physical("partitionDigest,resultDigest")},
		{"catalog_root_sha256", "servicecatalogv3.Build", []string{"repository", "source", "catalog", "authority", "policy"}, logical("source.commit/census", "catalog")},
		{"catalog_activation_plan_sha256", "store.BeginServiceStateV3Activation", []string{"repository", "phase", "catalogRoot/revision", "search", "repairOrdinal"}, logical("catalogRoot/revision,search", "catalogRoot/revision")},
		{"catalog_activation_schedule_sha256", "store.GenerationScheduleDigest", []string{"repository", "plan", "stage", "resource", "items/chunks", "attempts/tokens"}, logical("plan", "plan")},
		{"catalog_activation_unit_sha256", "store.ClaimGenerationChunk", []string{"schedule", "offset", "attempt"}, logical("schedule", "schedule")},
		{"resolver_catalog_generation_sha256", "resolvercatalog.NewIdentity", []string{"repository", "commit", "unit", "candidate", "declarations (no RunID)", "packs", "policies"}, physical("commit,candidate,declarationPlan/root")},
		{"resolver_catalog_root_sha256", "resolvercatalog.Stage.Seal", []string{"semantic identity (no RunID)", "members; AuthorityDigest"}, physical("identity")},
		{"caller_generation_sha256", "callerexecute.GenerationIdentity", []string{"repository/commit/unit", "candidate/policy", "resolverGeneration/AuthorityDigest", "declarations", "policies"}, physical("commit,candidate,resolver")},
		{"caller_root_sha256", "callerpublication.BuildManifest", []string{"generation", "ordered pair identities/receipts"}, physical("generation,pairs")},
		{"relationship_generation_sha256", "relationshippublication.BuildV3", []string{"catalogRoot/revision", "stateSet/summary/revision", "componentRoots", "full upstream", "policy"}, logical("components,catalog/state", "catalog/state")},
		{"relationship_root_sha256", "relationshippublication.BuildV3", []string{"authority", "generation", "members", "totals", "policy"}, logical("authority,generation,members", "authority,generation,serviceMembers")},
		{"relationship_provenance_sha256", "downstreamauthority.Build", []string{"observation", "ordered domain plan/root/RunID"}, physical("observation,domainPlan/root")},
	}
	for index := range rows {
		if rows[index].Identity == "relationship_generation_sha256" || rows[index].Identity == "relationship_root_sha256" || rows[index].Identity == "relationship_provenance_sha256" {
			rows[index].ChangedInputs["archive_restore"] = "fresh upstream RunIDs; same provenance requires same IDs"
		}
	}
	return rows
}

// extractionResultMembers is the receipt inventory of native result identities,
// not the production root's differently framed ResultSetDigest. Each string is
// framed by the existing receipt identity builder, so the public projection can
// independently validate its record count, framed byte count and digest.
func extractionResultMembers(results []ExtractionPartitionResult) (SetIdentity, error) {
	builder := newIdentityBuilder("t421-extraction-result-members-v1")
	for _, result := range results {
		if err := builder.add(result.ResultIdentitySHA256); err != nil {
			return SetIdentity{}, err
		}
	}
	return builder.finish(), nil
}
