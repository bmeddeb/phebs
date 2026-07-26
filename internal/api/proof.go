package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	proofBundleSchemaVersion = "proof-bundle-v1"
	proofQueryAssertionLimit = 5000
	proofQueryEvidenceLimit  = 20_000
	proofBuildAttempts       = 3
	proofCaveat              = "Provisional evidence only; no absence, compatibility, migration-complete, or decommissioning conclusion."
	compatibilityCaveat      = "Buf WIRE verdict applies only to the committed before/after input digests; consumer evidence is provisional and cannot establish absence, migration completeness, or decommissioning safety."
	compatibilityBodyLimit   = 72 << 20
)

var errProofQueryLimit = errors.New("proof query result exceeds its bounded limit")

// VisibilityContext records the authorization inputs under which the bundle
// was computed. Digests are deterministic and disclose no hidden repo names.
type VisibilityContext struct {
	Principal                  string `json:"principal"`
	AuthorizationProvider      string `json:"authorization_provider"`
	PermissionSnapshot         string `json:"permission_snapshot"`
	VisibleRepositorySetDigest string `json:"visible_repository_set_digest"`
}

// ProofQuery is the canonical question embedded in a proof bundle.
type ProofQuery struct {
	Kind         string   `json:"kind"`
	Operation    string   `json:"operation,omitempty"`
	Lineage      string   `json:"lineage,omitempty"`
	Message      string   `json:"message,omitempty"`
	FieldNumber  int      `json:"field_number,omitempty"`
	Topic        string   `json:"topic,omitempty"`
	BeforeDigest string   `json:"before_digest,omitempty"`
	AfterDigest  string   `json:"after_digest,omitempty"`
	Domains      []string `json:"domains"`
}

// BundleExtractor explicitly binds extractor versions to exact published
// runs rather than requiring consumers to infer them from coverage fields.
type BundleExtractor struct {
	Repository string `json:"repository"`
	Domain     string `json:"domain"`
	RunID      string `json:"run_id"`
	Extractor  string `json:"extractor"`
}

// BundleEvidence makes assertion atom references self-contained.
type BundleEvidence struct {
	Repository  string                   `json:"repository"`
	RunID       string                   `json:"run_id"`
	Atom        store.EvidenceAtom       `json:"atom"`
	Occurrences []store.SnapshotEvidence `json:"occurrences"`
}

// ProofBundle is canonical content. Its ID is sha256 over the JSON encoding
// of this value and therefore lives only in ProofBundleEnvelope.
type ProofBundle struct {
	SchemaVersion     string                      `json:"schema_version"`
	Query             ProofQuery                  `json:"query"`
	Assertions        []store.Assertion           `json:"assertions"`
	Evidence          []BundleEvidence            `json:"evidence"`
	UnresolvedCensus  *KafkaTopicCensus           `json:"unresolved_census,omitempty"`
	Coverage          extract.CoverageCertificate `json:"coverage"`
	ExtractorVersions []BundleExtractor           `json:"extractor_versions"`
	VisibilityContext VisibilityContext           `json:"visibility_context"`
	Compatibility     *compat.CompatibilityResult `json:"compatibility,omitempty"`
	Caveat            string                      `json:"caveat"`
}

// KafkaTopicCensus is the first-class abstention census a topic-usage bundle
// always carries (KD10): per-plane counts of supporting source sites on
// UNRESOLVED_KAFKA_* assertions by frozen shape class, with every class
// present even at zero, so completeness can never be implied by omission.
// Counts are topic-independent by construction — a non-literal topic cannot
// be matched to any literal — which is exactly the honesty the census
// communicates.
type KafkaTopicCensus struct {
	SchemaVersion string         `json:"schema_version"`
	Producer      map[string]int `json:"producer"`
	Consumer      map[string]int `json:"consumer"`
	// PublishedRuns is the total number of (repository, Kafka domain) runs.
	// The per-plane fields prevent a producer-only publication from making
	// consumer zeros appear measured (and vice versa).
	PublishedRuns         int `json:"published_runs"`
	ProducerPublishedRuns int `json:"producer_published_runs"`
	ConsumerPublishedRuns int `json:"consumer_published_runs"`
	// Truncated lists "plane:class" entries whose counts are lower bounds:
	// one repository exceeded the bounded per-plane query limit. All classes
	// in that plane are then marked conservatively. The census never fails
	// the whole surface on abstention volume, and it never presents a clipped
	// count as exact.
	Truncated []string `json:"truncated,omitempty"`
}

// kafkaAbstentionClasses is the T23.1-frozen shape-class vocabulary (KD6).
var kafkaAbstentionClasses = []string{
	"ambiguous-library-import", "call-expr", "invalid-topic-literal",
	"non-literal-expr", "selector-expr", "unresolved-ident",
}

// ProofBundleEnvelope is returned by both query creation and immutable reads.
type ProofBundleEnvelope struct {
	ID     string      `json:"id"`
	Bundle ProofBundle `json:"bundle"`
}

type assertionFilter struct {
	Domain    string
	Predicate string
	Object    string
	Lineage   string
}

// ProofService is the shared T14 query core used by both Huma and MCP. A nil
// result from NewProofService keeps every proof surface undiscoverable while
// the provisional extraction feature is disabled.
type ProofService struct {
	opts Options
}

// NewProofService constructs the shared query service only when every store
// boundary needed to build and persist a proof bundle is available.
func NewProofService(opts Options) *ProofService {
	if opts.Store == nil || opts.Evidence == nil || opts.ProofBundles == nil {
		return nil
	}
	return &ProofService{opts: opts}
}

// FindOperationConsumers builds the same immutable answer returned by the
// Huma endpoint. Operation is the canonical /service/method identity.
func (s *ProofService) FindOperationConsumers(ctx context.Context, operation string) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	if err := validateOperation(operation); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	// The bare operation string carries no protocol, so every registered
	// consumer domain is queried (T19.5). Domains without a published run
	// surface as honest no-run coverage rows, never as errors. Equal operation
	// spellings may exist across protocols; the certificate/run identity keeps
	// their assertions distinct and projections retain the domain.
	query := ProofQuery{
		Kind: "find_operation_consumers", Operation: operation,
		Domains: []string{"grpc-consumer", "thrift-consumer"},
	}
	return buildProofBundle(ctx, s.opts, query, []assertionFilter{
		{Domain: "grpc-consumer", Predicate: "CALLS_OPERATION", Object: operation},
		{Domain: "thrift-consumer", Predicate: "CALLS_OPERATION", Object: operation},
	}, nil)
}

// FindProtoFieldReferences builds the immutable answer for one canonical
// (lineage, message, field number) identity.
func (s *ProofService) FindProtoFieldReferences(ctx context.Context, lineage, message string, fieldNumber int) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	if err := validateFieldIdentity(lineage, message, fieldNumber); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{
		Kind: "find_proto_field_references", Lineage: lineage,
		Message: message, FieldNumber: fieldNumber, Domains: []string{"scip-proto-field"},
	}
	return createProofBundle(ctx, s.opts, query, assertionFilter{
		Domain: "scip-proto-field", Predicate: "REFERENCES_PROTO_FIELD",
		Object: message + "#" + strconv.Itoa(fieldNumber), Lineage: lineage,
	})
}

// FindKafkaTopicUsage builds the immutable topic-centered answer for one
// Kafka topic spelling: producer and consumer evidence rows plus the
// always-present unresolved census. The topic is a source spelling — the
// answer carries no cluster, environment, runtime, or completeness claim,
// and abstention volume is presented as the honest norm (KD10).
func (s *ProofService) FindKafkaTopicUsage(ctx context.Context, topic string) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	if err := validateKafkaTopic(topic); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{
		Kind: "find_kafka_topic_usage", Topic: topic,
		Domains: []string{"kafka-consumer", "kafka-producer"},
	}
	return buildProofBundleWithCensus(ctx, s.opts, query, []assertionFilter{
		{Domain: "kafka-producer", Predicate: "PRODUCES_TO_TOPIC", Object: "topic:" + topic},
		{Domain: "kafka-consumer", Predicate: "CONSUMES_FROM_TOPIC", Object: "topic:" + topic},
	}, nil, true)
}

// validateKafkaTopic applies the KD2 identity bounds — Kafka's own topic
// naming rules: 1..249 characters of [a-zA-Z0-9._-], excluding the
// reserved "." and "..".
func validateKafkaTopic(topic string) error {
	if len(topic) == 0 || len(topic) > 249 || topic == "." || topic == ".." {
		return errors.New("topic must be 1-249 characters and not a reserved name")
	}
	for _, r := range topic {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return errors.New("topic must contain only [a-zA-Z0-9._-]")
		}
	}
	return nil
}

// GetExtractionCoverage builds an assertion-free proof bundle over the
// requested domains. An empty slice selects every provisional domain.
func (s *ProofService) GetExtractionCoverage(ctx context.Context, domains []string) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	domains, err := canonicalProofDomains(domains)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{Kind: "get_extraction_coverage", Domains: domains}
	return createProofBundle(ctx, s.opts, query, assertionFilter{})
}

// CompatibilityAvailable reports whether the shared service can expose a
// genuine compatibility operation. Transports use it to avoid advertising a
// permanently failing placeholder when the Buf sandbox is unavailable.
func (s *ProofService) CompatibilityAvailable() bool {
	return s != nil && s.opts.Compatibility != nil
}

// CheckContractCompatibility runs the pinned Buf WIRE policy, maps findings
// to stable field identities, then enriches them with permission-filtered SCIP
// consumers and exact source evidence in the same immutable proof bundle.
func (s *ProofService) CheckContractCompatibility(ctx context.Context, request compat.Request) (*ProofBundleEnvelope, error) {
	if s == nil || s.opts.Compatibility == nil {
		return nil, huma.Error503ServiceUnavailable("contract compatibility unavailable")
	}
	result, err := s.opts.Compatibility.Check(ctx, request)
	if err != nil {
		switch {
		case errors.Is(err, compat.ErrInvalidInput):
			return nil, huma.Error400BadRequest(err.Error())
		case errors.Is(err, compat.ErrLimit):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		case errors.Is(err, compat.ErrUnavailable):
			return nil, huma.Error503ServiceUnavailable(err.Error())
		default:
			return nil, huma.Error422UnprocessableEntity("compatibility engine could not produce a verdict", err)
		}
	}
	filters := make([]assertionFilter, 0, len(result.AffectedFields))
	for _, field := range result.AffectedFields {
		if err := validateFieldIdentity(field.Lineage, field.Message, field.Number); err != nil {
			return nil, huma.Error500InternalServerError("compatibility engine returned an invalid field identity", err)
		}
		filters = append(filters, assertionFilter{
			Domain: "scip-proto-field", Predicate: "REFERENCES_PROTO_FIELD",
			Object: field.Message + "#" + strconv.Itoa(field.Number), Lineage: field.Lineage,
		})
	}
	query := ProofQuery{
		Kind: "check_contract_compatibility", Lineage: request.Lineage,
		BeforeDigest: result.Before.Digest, AfterDigest: result.After.Digest,
		Domains: []string{"scip-proto-field"},
	}
	return createProofBundleWithCompatibility(ctx, s.opts, query, filters, result)
}

func registerProofAPI(api huma.API, opts Options) {
	service := NewProofService(opts)
	if service == nil {
		return
	}

	type operationIn struct {
		Operation string `query:"operation" required:"true" minLength:"1" maxLength:"1024" example:"/shop.Cart/Get"`
	}
	type proofOut struct{ Body ProofBundleEnvelope }
	huma.Get(api, "/api/find_operation_consumers", func(ctx context.Context, in *operationIn) (*proofOut, error) {
		envelope, err := service.FindOperationConsumers(ctx, in.Operation)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})

	type fieldIn struct {
		Lineage     string `query:"lineage" required:"true" minLength:"1" maxLength:"1024"`
		Message     string `query:"message" required:"true" minLength:"1" maxLength:"1024" example:"shop.Cart"`
		FieldNumber int    `query:"field_number" required:"true" minimum:"1" maximum:"536870911" example:"1"`
	}
	huma.Get(api, "/api/find_proto_field_references", func(ctx context.Context, in *fieldIn) (*proofOut, error) {
		envelope, err := service.FindProtoFieldReferences(ctx, in.Lineage, in.Message, in.FieldNumber)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})

	type topicIn struct {
		Topic string `query:"topic" required:"true" minLength:"1" maxLength:"249" example:"orders-v1" doc:"one Kafka topic spelling; the answer is source evidence only and never a completeness claim"`
	}
	huma.Get(api, "/api/find_kafka_topic_usage", func(ctx context.Context, in *topicIn) (*proofOut, error) {
		envelope, err := service.FindKafkaTopicUsage(ctx, in.Topic)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})

	type coverageIn struct {
		Domains string `query:"domains" doc:"comma-separated extractor domains; defaults to all provisional domains" example:"grpc-consumer,scip-proto-field"`
	}
	huma.Get(api, "/api/get_extraction_coverage", func(ctx context.Context, in *coverageIn) (*proofOut, error) {
		domains, err := proofDomains(in.Domains)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		envelope, err := service.GetExtractionCoverage(ctx, domains)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})
	if service.CompatibilityAvailable() {
		type compatibilityIn struct {
			Body compat.Request
		}
		huma.Register(api, huma.Operation{
			OperationID: "check-contract-compatibility",
			Method:      "POST", Path: "/api/check_contract_compatibility",
			Summary:      "Check protobuf wire compatibility and find affected consumers",
			MaxBodyBytes: compatibilityBodyLimit,
		}, func(ctx context.Context, in *compatibilityIn) (*proofOut, error) {
			envelope, err := service.CheckContractCompatibility(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			return &proofOut{Body: *envelope}, nil
		})
	}

	type bundleIn struct {
		ID string `path:"id" minLength:"67" maxLength:"67"`
	}
	huma.Get(api, "/api/proof_bundles/{id}", func(ctx context.Context, in *bundleIn) (*proofOut, error) {
		envelope, err := readProofBundle(ctx, opts, in.ID)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})
	registerContractImpactAPI(api, opts, service)
}

func createProofBundle(ctx context.Context, opts Options, query ProofQuery, filter assertionFilter) (*ProofBundleEnvelope, error) {
	filters := []assertionFilter{}
	if filter.Predicate != "" {
		filters = append(filters, filter)
	}
	return buildProofBundle(ctx, opts, query, filters, nil)
}

func createProofBundleWithCompatibility(ctx context.Context, opts Options, query ProofQuery, filters []assertionFilter, result *compat.CompatibilityResult) (*ProofBundleEnvelope, error) {
	if result == nil {
		return nil, huma.Error500InternalServerError("compatibility result is missing")
	}
	return buildProofBundle(ctx, opts, query, filters, result)
}

func buildProofBundle(ctx context.Context, opts Options, query ProofQuery, filters []assertionFilter, compatibility *compat.CompatibilityResult) (*ProofBundleEnvelope, error) {
	return buildProofBundleWithCensus(ctx, opts, query, filters, compatibility, false)
}

func buildProofBundleWithCensus(ctx context.Context, opts Options, query ProofQuery, filters []assertionFilter, compatibility *compat.CompatibilityResult, withCensus bool) (*ProofBundleEnvelope, error) {
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < proofBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(ctx, opts.Evidence, visible, query.Domains)
		if err != nil {
			return nil, huma.Error500InternalServerError("build extraction coverage", err)
		}
		assertions, evidence, err := collectProofEvidence(ctx, opts.Evidence, certificate, filters)
		if err != nil {
			if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
				continue
			}
			if errors.Is(err, store.ErrResultLimit) || errors.Is(err, errProofQueryLimit) {
				return nil, huma.Error422UnprocessableEntity("proof query exceeds bounded result limits; narrow the query")
			}
			return nil, huma.Error500InternalServerError("build proof evidence", err)
		}
		var census *KafkaTopicCensus
		if withCensus {
			census, err = collectKafkaTopicCensus(ctx, opts.Evidence, certificate)
			if err != nil {
				if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
					continue
				}
				if errors.Is(err, store.ErrResultLimit) {
					return nil, huma.Error422UnprocessableEntity("unresolved census exceeds bounded result limits")
				}
				return nil, huma.Error500InternalServerError("build unresolved census", err)
			}
		}
		confirmed, err := extract.BuildCoverageCertificate(ctx, opts.Evidence, visible, query.Domains)
		if err != nil {
			return nil, huma.Error500InternalServerError("confirm extraction coverage", err)
		}
		if confirmed.Digest != certificate.Digest {
			continue
		}
		caveat := proofCaveat
		if compatibility != nil {
			caveat = compatibilityCaveat
		}
		bundle := ProofBundle{
			SchemaVersion: proofBundleSchemaVersion, Query: query,
			Assertions: assertions, Evidence: evidence, UnresolvedCensus: census,
			Coverage:          *certificate,
			ExtractorVersions: certificateExtractors(certificate),
			VisibilityContext: visibilityContext(ctx, opts, certificate),
			Compatibility:     compatibility, Caveat: caveat,
		}
		content, err := json.Marshal(bundle)
		if err != nil {
			return nil, huma.Error500InternalServerError("canonicalize proof bundle", err)
		}
		repositories, runIDs := certificateScopes(certificate)
		record := store.ProofBundleRecord{
			ID: store.ComputeProofBundleID(string(content)), Content: string(content),
			Repositories: repositories, RunIDs: runIDs,
			RetainedAt: time.Now().UTC().Truncate(time.Second),
		}
		if err := opts.ProofBundles.PutProofBundle(ctx, record); err != nil {
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			return nil, huma.Error500InternalServerError("persist proof bundle", err)
		}
		return &ProofBundleEnvelope{ID: record.ID, Bundle: bundle}, nil
	}
	return nil, huma.Error409Conflict("evidence changed while building the proof bundle; retry")
}

func collectProofEvidence(ctx context.Context, source store.EvidenceStore, certificate *extract.CoverageCertificate, filters []assertionFilter) ([]store.Assertion, []BundleEvidence, error) {
	assertions := make([]store.Assertion, 0)
	domains := make(map[string]struct{}, len(certificate.Domains))
	for _, domain := range certificate.Domains {
		domains[domain] = struct{}{}
	}
	for _, filter := range filters {
		if filter.Domain == "" || filter.Predicate == "" {
			return nil, nil, errors.New("proof assertion filter requires a domain and predicate")
		}
		if _, ok := domains[filter.Domain]; !ok {
			return nil, nil, errors.New("proof assertion filter domain is absent from coverage")
		}
		for _, repository := range certificate.Repositories {
			run, ok := certificateRun(repository, filter.Domain)
			if !ok || run.Status != "published" {
				continue
			}
			rows, err := source.ListAssertions(ctx, store.AssertionQuery{
				Predicate: filter.Predicate, Object: filter.Object, Lineage: filter.Lineage,
				Repo: repository.Repository, RunID: run.RunID, Limit: proofQueryAssertionLimit,
			})
			if err != nil {
				return nil, nil, err
			}
			for _, assertion := range rows {
				if assertion.Repo != repository.Repository || assertion.RunID != run.RunID ||
					assertion.Predicate != filter.Predicate || assertion.Object != filter.Object ||
					filter.Lineage != "" && assertion.Lineage != filter.Lineage {
					return nil, nil, errors.New("proof query returned an inconsistent assertion")
				}
			}
			assertions = append(assertions, rows...)
			if len(assertions) > proofQueryAssertionLimit {
				return nil, nil, fmt.Errorf("%w: more than %d assertions", errProofQueryLimit, proofQueryAssertionLimit)
			}
		}
	}
	assertions = deterministicAssertions(assertions)

	type evidenceKey struct{ repo, runID, atomID string }
	keys := make(map[evidenceKey]struct{})
	for _, assertion := range assertions {
		for _, atomID := range append(append([]string(nil), assertion.Supporting...), assertion.Contradicting...) {
			keys[evidenceKey{repo: assertion.Repo, runID: assertion.RunID, atomID: atomID}] = struct{}{}
			if len(keys) > proofQueryEvidenceLimit {
				return nil, nil, fmt.Errorf("%w: more than %d evidence references", errProofQueryLimit, proofQueryEvidenceLimit)
			}
		}
	}
	ordered := make([]evidenceKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].repo != ordered[j].repo {
			return ordered[i].repo < ordered[j].repo
		}
		if ordered[i].runID != ordered[j].runID {
			return ordered[i].runID < ordered[j].runID
		}
		return ordered[i].atomID < ordered[j].atomID
	})
	evidence := make([]BundleEvidence, 0, len(ordered))
	for _, key := range ordered {
		resolved, err := source.ResolveEvidence(ctx, key.repo, key.runID, key.atomID)
		if err != nil {
			return nil, nil, err
		}
		if resolved == nil || resolved.Atom.ID != key.atomID {
			return nil, nil, errors.New("proof evidence resolution is inconsistent")
		}
		occurrences := append([]store.SnapshotEvidence(nil), resolved.Occurrences...)
		sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].ID < occurrences[j].ID })
		for _, occurrence := range occurrences {
			if occurrence.Repo != key.repo || occurrence.RunID != key.runID || occurrence.AtomID != key.atomID {
				return nil, nil, errors.New("proof evidence occurrence scope is inconsistent")
			}
		}
		evidence = append(evidence, BundleEvidence{
			Repository: key.repo, RunID: key.runID, Atom: resolved.Atom, Occurrences: occurrences,
		})
	}
	return assertions, evidence, nil
}

// collectKafkaTopicCensus counts supporting source sites per plane and frozen
// shape class over every visible repository's published run, inside the same
// coverage-digest sandwich as the evidence collection. Every class key is
// present even at zero. One bounded prefix query per published plane/run
// replaces the former six exact-object round trips; returned rows remain
// authorization-scoped by the certificate's exact repository and run.
func collectKafkaTopicCensus(ctx context.Context, source store.EvidenceStore, certificate *extract.CoverageCertificate) (*KafkaTopicCensus, error) {
	census := &KafkaTopicCensus{
		SchemaVersion: "kafka-topic-census-v1",
		Producer:      make(map[string]int, len(kafkaAbstentionClasses)),
		Consumer:      make(map[string]int, len(kafkaAbstentionClasses)),
	}
	planes := []struct {
		name      string
		domain    string
		predicate string
		counts    map[string]int
		published *int
	}{
		{"producer", "kafka-producer", "UNRESOLVED_KAFKA_PRODUCER", census.Producer, &census.ProducerPublishedRuns},
		{"consumer", "kafka-consumer", "UNRESOLVED_KAFKA_CONSUMER", census.Consumer, &census.ConsumerPublishedRuns},
	}
	for _, plane := range planes {
		for _, class := range kafkaAbstentionClasses {
			plane.counts[class] = 0
		}
		for _, repository := range certificate.Repositories {
			run, ok := certificateRun(repository, plane.domain)
			if !ok || run.Status != "published" {
				continue
			}
			(*plane.published)++
			census.PublishedRuns++
			rows, err := source.ListAssertions(ctx, store.AssertionQuery{
				Predicate: plane.predicate, ObjectPrefix: "unresolved:",
				Repo: repository.Repository, RunID: run.RunID,
				Limit: proofQueryAssertionLimit, AllowTruncate: true,
			})
			if err != nil {
				return nil, err
			}
			truncated := len(rows) > proofQueryAssertionLimit
			if truncated {
				rows = rows[:proofQueryAssertionLimit]
			}
			for _, assertion := range rows {
				class, found := strings.CutPrefix(assertion.Object, "unresolved:")
				if assertion.Repo != repository.Repository || assertion.RunID != run.RunID ||
					assertion.Predicate != plane.predicate || !found ||
					!slices.Contains(kafkaAbstentionClasses, class) ||
					len(assertion.Supporting) == 0 || len(assertion.Contradicting) != 0 {
					return nil, errors.New("census query returned an inconsistent assertion")
				}
				plane.counts[class] += len(assertion.Supporting)
			}
			if truncated {
				for _, class := range kafkaAbstentionClasses {
					census.Truncated = append(census.Truncated, plane.name+":"+class)
				}
			}
		}
	}
	sort.Strings(census.Truncated)
	return census, nil
}

func certificateRun(repository extract.CertificateRepository, domain string) (extract.CertificateRun, bool) {
	for _, run := range repository.Runs {
		if run.Domain == domain {
			return run, true
		}
	}
	return extract.CertificateRun{}, false
}

func certificateExtractors(certificate *extract.CoverageCertificate) []BundleExtractor {
	result := make([]BundleExtractor, 0)
	for _, repository := range certificate.Repositories {
		for _, run := range repository.Runs {
			if run.Status == "published" {
				result = append(result, BundleExtractor{
					Repository: repository.Repository, Domain: run.Domain,
					RunID: run.RunID, Extractor: run.Extractor,
				})
			}
		}
	}
	return result
}

func certificateScopes(certificate *extract.CoverageCertificate) ([]string, []string) {
	repositories := make([]string, 0, len(certificate.Repositories))
	runSet := make(map[string]struct{})
	for _, repository := range certificate.Repositories {
		repositories = append(repositories, repository.Repository)
		for _, run := range repository.Runs {
			if run.Status == "published" {
				runSet[run.RunID] = struct{}{}
			}
		}
	}
	runIDs := make([]string, 0, len(runSet))
	for runID := range runSet {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	return repositories, runIDs
}

func visibilityContext(ctx context.Context, opts Options, certificate *extract.CoverageCertificate) VisibilityContext {
	repositories, _ := certificateScopes(certificate)
	visibleDigest := digestJSON(repositories)
	principal := "anonymous"
	if opts.Principal != nil {
		if resolved := strings.TrimSpace(opts.Principal(ctx)); resolved != "" {
			principal = resolved
		}
	}
	provider := opts.AuthorizationProvider
	if provider == "" {
		provider = "unfiltered-v1"
	}
	snapshot := digestJSON(struct {
		Principal string `json:"principal"`
		Provider  string `json:"provider"`
		Visible   string `json:"visible"`
	}{principal, provider, visibleDigest})
	return VisibilityContext{
		Principal: principal, AuthorizationProvider: provider,
		PermissionSnapshot: snapshot, VisibleRepositorySetDigest: visibleDigest,
	}
}

func digestJSON(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readProofBundle(ctx context.Context, opts Options, id string) (*ProofBundleEnvelope, error) {
	var activeAfter *time.Time
	if opts.ProofBundleRetention > 0 {
		cutoff := time.Now().UTC().Add(-opts.ProofBundleRetention)
		activeAfter = &cutoff
	}
	record, err := opts.ProofBundles.GetProofBundle(ctx, id, activeAfter)
	if errors.Is(err, store.ErrNotFound) {
		return nil, huma.Error404NotFound("proof bundle not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("read proof bundle", err)
	}
	if record == nil || record.ID != id || store.ComputeProofBundleID(record.Content) != id {
		return nil, huma.Error500InternalServerError("stored proof bundle identity is inconsistent")
	}
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	visibleNames := make(map[string]struct{}, len(visible))
	for _, repo := range visible {
		visibleNames[repo.Name] = struct{}{}
	}
	for _, name := range record.Repositories {
		if _, ok := visibleNames[name]; !ok {
			return nil, huma.Error404NotFound("proof bundle not found")
		}
	}
	var bundle ProofBundle
	if err := json.Unmarshal([]byte(record.Content), &bundle); err != nil {
		return nil, huma.Error500InternalServerError("decode proof bundle", err)
	}
	canonical, err := json.Marshal(bundle)
	if err != nil || string(canonical) != record.Content || bundle.SchemaVersion != proofBundleSchemaVersion {
		return nil, huma.Error500InternalServerError("stored proof bundle is inconsistent")
	}
	repositories, runIDs := certificateScopes(&bundle.Coverage)
	if !equalStrings(repositories, record.Repositories) || !equalStrings(runIDs, record.RunIDs) {
		return nil, huma.Error500InternalServerError("stored proof bundle scope is inconsistent")
	}
	return &ProofBundleEnvelope{ID: record.ID, Bundle: bundle}, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validateOperation(operation string) error {
	if !utf8.ValidString(operation) || len(operation) > 1024 || strings.TrimSpace(operation) != operation ||
		!strings.HasPrefix(operation, "/") || strings.Count(operation, "/") != 2 {
		return errors.New("operation must be canonical /fully.qualified.Service/Method")
	}
	parts := strings.Split(strings.TrimPrefix(operation, "/"), "/")
	if len(parts) != 2 || !validQueryIdentity(parts[0]) || !validQueryIdentity(parts[1]) {
		return errors.New("operation must be canonical /fully.qualified.Service/Method")
	}
	return nil
}

func validateFieldIdentity(lineage, message string, number int) error {
	if !validQueryIdentity(lineage) || !validQueryIdentity(message) || number < 1 || number > 536_870_911 ||
		number >= 19_000 && number <= 19_999 {
		return errors.New("field identity requires bounded lineage, message, and a valid protobuf field number")
	}
	return nil
}

func validQueryIdentity(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == '/' || r == '#' {
			return false
		}
	}
	return true
}

func proofDomains(raw string) ([]string, error) {
	if raw == "" {
		return canonicalProofDomains(nil)
	}
	return canonicalProofDomains(strings.Split(raw, ","))
}

func canonicalProofDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return []string{
			"grpc-consumer", "kafka-consumer", "kafka-producer",
			"proto-contract", "scip-proto-field",
			"thrift-consumer", "thrift-contract",
		}, nil
	}
	parts := append([]string(nil), domains...)
	if len(parts) == 0 || len(parts) > 64 {
		return nil, errors.New("domains must contain from 1 through 64 values")
	}
	seen := make(map[string]struct{}, len(parts))
	for _, domain := range parts {
		if domain == "" || len(domain) > 128 || strings.TrimSpace(domain) != domain || !validDomain(domain) {
			return nil, errors.New("domains must be comma-separated extractor tokens")
		}
		if _, duplicate := seen[domain]; duplicate {
			return nil, errors.New("domains must not contain duplicates")
		}
		seen[domain] = struct{}{}
	}
	sort.Strings(parts)
	return parts, nil
}

func validDomain(value string) bool {
	for _, r := range value {
		if r == '-' || r == '.' || r == '_' || r >= '0' && r <= '9' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return value != ""
}
