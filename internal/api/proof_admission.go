package api

import (
	"encoding/hex"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/danielgtaylor/huma/v2"
)

func proofSourceExtractors(certificate *extract.CoverageCertificate, sources catalogSources, versions map[string]string) []BundleExtractor {
	result := certificateExtractors(certificate)
	if len(versions) == 0 {
		return result
	}
	added := false
	for key, source := range sources {
		if source.authority != nil && versions[key.domain] != "" && source.run.Extractor != "" {
			added = true
			result = append(result, BundleExtractor{Repository: key.repository, Domain: key.domain, RunID: source.run.RunID, Extractor: source.run.Extractor})
		}
	}
	if !added {
		return result
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Repository != result[j].Repository {
			return result[i].Repository < result[j].Repository
		}
		return result[i].Domain < result[j].Domain
	})
	return result
}

func releasedProofOnly(opts Options) bool {
	return opts.ProtobufFieldReferencesReleased || len(opts.KafkaReleasedPlanes) > 0
}

func admittedFieldIdentity(opts Options, lineage string) error {
	if opts.EvidenceExtractorVersions["scip-proto-field"] == "" {
		return nil
	}
	value, ok := strings.CutPrefix(lineage, "contract_scip_package_v1_")
	if !ok || len(value) != 64 || strings.ToLower(value) != value {
		return huma.Error400BadRequest("field identity requires the exact admitted SCIP package lineage; declaration lineage is a distinct family")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return huma.Error400BadRequest("invalid SCIP package lineage")
	}
	return nil
}

// All transports and direct proof/report calls converge here before visibility
// or source reads. A released recipe cannot inherit the provisional umbrella.
func admitProofQuery(opts Options, query ProofQuery) error {
	if query.Kind == "find_proto_field_references" || query.Kind == "find_field_references" && slices.Contains(query.Domains, "scip-proto-field") {
		if err := admittedFieldIdentity(opts, query.Lineage); err != nil {
			return err
		}
	}
	if !releasedProofOnly(opts) {
		return nil
	}
	allowed := map[string]bool{}
	for _, plane := range opts.KafkaReleasedPlanes {
		allowed["kafka-"+plane] = true
	}
	switch query.Kind {
	case "find_proto_field_references", "find_field_references":
		if !opts.ProtobufFieldReferencesReleased {
			return huma.Error503ServiceUnavailable("field references unavailable")
		}
		allowed = map[string]bool{"scip-proto-field": true}
	case "find_kafka_topic_usage":
		if len(opts.KafkaReleasedPlanes) == 0 {
			return huma.Error503ServiceUnavailable("Kafka topic usage unavailable")
		}
	case topicInventoryQueryKind:
		if !opts.KafkaTopicInventoryReleased || len(opts.KafkaReleasedPlanes) == 0 {
			return huma.Error503ServiceUnavailable("Kafka inventory unavailable")
		}
	default:
		return huma.Error503ServiceUnavailable("proof query unavailable for admitted recipes")
	}
	if len(query.Domains) == 0 {
		return huma.Error503ServiceUnavailable("proof query has no admitted domains")
	}
	for _, domain := range query.Domains {
		if !allowed[domain] {
			return huma.Error503ServiceUnavailable("proof domain unavailable")
		}
	}
	return nil
}

// A native source's extractor was checked by its existing bounded SQL fence;
// a legacy source's version came from the exact published-run certificate.
// Keep those witnesses in immutable bundles, including zero-row roots, so a
// retained provisional generation cannot be reinterpreted as stable admission.
func validateExpectedEvidenceVersions(opts Options, bundle *ProofBundle) error {
	if len(opts.EvidenceExtractorVersions) == 0 {
		return nil
	}
	versions := map[string]string{}
	for _, run := range bundle.ExtractorVersions {
		if expected := opts.EvidenceExtractorVersions[run.Domain]; expected != "" {
			if run.Extractor != expected {
				return huma.Error409Conflict("evidence recipe generation unavailable")
			}
			versions[run.Repository+"\x00"+run.RunID] = expected
		}
	}
	for _, root := range bundle.ExactAuthorities {
		if expected := opts.EvidenceExtractorVersions[root.Domain]; expected != "" {
			if versions[root.Repository+"\x00"+root.RunID] != expected {
				return huma.Error409Conflict("evidence recipe generation unavailable")
			}
		}
	}
	for _, assertion := range bundle.Assertions {
		if expected := opts.EvidenceExtractorVersions["scip-proto-field"]; assertion.Predicate == "REFERENCES_PROTO_FIELD" && expected != "" && versions[assertion.Repo+"\x00"+assertion.RunID] != expected {
			return huma.Error409Conflict("evidence recipe witness unavailable")
		}
	}
	for _, evidence := range bundle.Evidence {
		if expected := versions[evidence.Repository+"\x00"+evidence.RunID]; expected != "" && evidence.Atom.ExtractorVersion != expected {
			return huma.Error409Conflict("evidence citation recipe generation unavailable")
		}
	}
	return nil
}
