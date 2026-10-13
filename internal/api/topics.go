package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

// The Topics product composition contract (T57.1).
//
// The Kafka producer and consumer planes are released independently (T55.3 and
// T56.3), so the composed product may serve one, both, or neither, and it may
// never let an admitted plane's measurement make an unadmitted plane's zeros
// look measured. This file owns that composition and nothing else: it builds
// one immutable inventory bundle from a bounded prefix scan and presents it as
// a deterministic paged report with separate per-plane publication, coverage
// and quality state. Transport exposure is T57.2 and the measured promotion is
// T57.3.
//
// The inventory deliberately carries no evidence atoms. An overview row names a
// topic, not a source site, so its citations are exact assertion identities
// plus the pinned evidence commit; the per-site spans a click-through needs
// stay with the topic detail bundle (FindKafkaTopicUsage), which already
// resolves them for one spelling at a time. Keeping atoms out of the overview
// is what bounds it: composing the inventory costs one bounded prefix query
// per published plane and run, the same shape the unresolved census already
// pays, and no ResolveEvidence round trip at all.

const (
	// topicInventoryQueryKind is the immutable bundle question the composed
	// overview answers. It is distinct from find_kafka_topic_usage, which asks
	// about one known spelling.
	topicInventoryQueryKind = "list_kafka_topics"

	topicInventorySchemaVersion = "kafka-topic-inventory-v1"
	topicInventoryCursorSchema  = "phebs-topic-inventory-cursor-v1"

	topicInventoryDefaultPage = 50
	topicInventoryMaxPage     = 100

	// topicInventoryMaxTopics is the composed-row ceiling. Distinct spellings
	// beyond it are clipped deterministically (sorted order) and the inventory
	// reports itself incomplete rather than presenting a clipped list as the
	// whole corpus.
	topicInventoryMaxTopics = 2000

	// topicInventoryCitationsPerRow bounds one row's rendered citations. A hot
	// topic can occur in thousands of files, so the row retains the first few
	// exactly and counts the remainder; the accumulator never holds the whole
	// set, which is what keeps a composed inventory bounded independently of
	// how many files mention one spelling.
	topicInventoryCitationsPerRow = 8

	topicInventoryCursorBytesLimit = 4096

	topicInventoryCaveat = "Composed source-spelled topic inventory. A topic is a source spelling, never a broker, cluster, environment, or runtime identity, and a topic absent from this inventory is not proven absent from the system."
)

// topicPlane is one Kafka plane's frozen composition identity: the extraction
// domain and predicate the inventory reads, and the independently released
// quality card that qualifies what its counts mean.
type topicPlane struct {
	plane     string
	domain    string
	predicate string
	card      string
	protocol  string
}

// topicPlanes is the closed two-plane vocabulary, in the canonical order both
// the census and the released-plane admission use. Both planes are always
// reported: an unadmitted plane is named unavailable instead of omitted, so
// its zeros can never read as measured.
var topicPlanes = []topicPlane{
	{
		plane: "consumer", domain: "kafka-consumer", predicate: "CONSUMES_FROM_TOPIC",
		card: "phebs.kafka.consumer", protocol: "docs/KAFKA_CONSUMER_QUALITY_PROTOCOL.md",
	},
	{
		plane: "producer", domain: "kafka-producer", predicate: "PRODUCES_TO_TOPIC",
		card: "phebs.kafka.producer", protocol: "docs/KAFKA_PRODUCER_QUALITY_PROTOCOL.md",
	},
}

// topicQualityClaim and its two lists are the frozen per-plane quality state.
// Both Kafka cards are site-coverage results (T55.1 scored 762/762 producer
// sites, T56.1 scored 640/640 consumer sites): they establish that the
// extractor finds and classes the source sites, not that a literal spelling is
// a real topic in any broker. Only 4 of the 762 producer sites spelled a topic
// literally, so topic precision is unmeasured, and no card observes a cluster,
// an environment, or a running process. These are constants of the released
// cards, not per-request measurements, and a card that later measures more
// must change them here in the same change that releases it.
var (
	topicQualityClaim      = "site-coverage"
	topicQualityMeasured   = []string{"site-coverage"}
	topicQualityUnmeasured = []string{
		"broker-identity", "environment-identity", "runtime-activity",
		"topic-precision", "topology-completeness",
	}
)

// TopicPlaneQuality states what the plane's released card measured and, always,
// what it did not. It is present only for an admitted plane: an unavailable
// plane has no quality result to report and must not borrow one.
type TopicPlaneQuality struct {
	Card       string   `json:"card"`
	Claim      string   `json:"claim"`
	Protocol   string   `json:"protocol"`
	Measured   []string `json:"measured"`
	Unmeasured []string `json:"unmeasured"`
}

// TopicPlaneState is one plane's separate publication, coverage and quality
// state inside a composed answer. CoverageRows carries the per-repository
// publication state (covered, stale, failed, processing, unsupported) exactly
// as Contract Impact reports it, so a composed inventory never flattens one
// repository's failed extraction into another's covered count.
type TopicPlaneState struct {
	Plane    string `json:"plane"`
	Domain   string `json:"domain"`
	Released bool   `json:"released"`
	// State is served for an admitted plane and unavailable for a plane no
	// admitted recipe publishes.
	State         string             `json:"state"`
	Quality       *TopicPlaneQuality `json:"quality,omitempty"`
	PublishedRuns int                `json:"published_runs"`
	Repositories  int                `json:"repositories"`
	// TopicCount and SiteCount are this plane's own totals over its bounded
	// scan, so they can exceed the composed row count when the composed
	// ceiling clipped. Truncated names which bound was hit.
	TopicCount   int                 `json:"topic_count"`
	SiteCount    int                 `json:"site_count"`
	CoverageRows []ImpactCoverageRow `json:"coverage_rows"`
}

// TopicCitation is one exact, immutable pointer to a source file's claim about
// a topic. Commit is the pinned evidence commit of the run the composition
// actually read, not a mutable repository HEAD link, so the citation survives
// later indexing. The composition refuses to emit a row it cannot pin.
type TopicCitation struct {
	Repository  string `json:"repository"`
	Commit      string `json:"commit"`
	RunID       string `json:"run_id"`
	AssertionID string `json:"assertion_id"`
	Path        string `json:"path"`
	Sites       int    `json:"sites"`
}

// TopicRowPlane is one plane's contribution to one inventory row. State
// separates a measured zero from an unmeasured one: none means the plane is
// admitted and found no site for this spelling, while unavailable means the
// plane is not admitted at all and its zero is not a measurement.
type TopicRowPlane struct {
	State string `json:"state"` // served | none | unavailable
	Sites int    `json:"sites"`
	Files int    `json:"files"`
	// Citations is a bounded prefix of the row's exact assertion identities,
	// retained in the store's stable scan order — repository, then path, then
	// assertion ID, which is also the order they are rendered in. Sites and
	// Files stay exact totals, and OmittedCitations counts what the bound left
	// out, so a clipped row never reads as a complete one. Per-site spans and
	// the client libraries behind them stay with the topic detail bundle, which
	// resolves one spelling at a time.
	Citations        []TopicCitation `json:"citations,omitempty"`
	OmittedCitations int             `json:"omitted_citations,omitempty"`
}

// TopicInventoryRow is one composed source spelling and both planes' evidence
// for it.
type TopicInventoryRow struct {
	Topic    string        `json:"topic"`
	Producer TopicRowPlane `json:"producer"`
	Consumer TopicRowPlane `json:"consumer"`
}

// TopicInventoryContent is the composed annex an inventory bundle carries. It
// is content-addressed inside the bundle, so one immutable ID names the exact
// inventory, the exact plane states and the exact coverage the page was cut
// from.
type TopicInventoryContent struct {
	SchemaVersion string              `json:"schema_version"`
	Planes        []TopicPlaneState   `json:"planes"`
	Topics        []TopicInventoryRow `json:"topics"`
	// TopicCount counts the retained rows. Complete is false when a plane's
	// bounded scan clipped or the composed-row ceiling clipped, and Truncated
	// names which; the count is then a lower bound, never a corpus total.
	TopicCount int      `json:"topic_count"`
	Complete   bool     `json:"complete"`
	Truncated  []string `json:"truncated,omitempty"`
}

// TopicInventoryPagination is the house pagination block: a stable order, the
// page bound, what this page returned, and an opaque cursor for the next one.
type TopicInventoryPagination struct {
	Order      string `json:"order"`
	PageSize   int    `json:"page_size"`
	Returned   int    `json:"returned"`
	Total      int    `json:"total"`
	Complete   bool   `json:"complete"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// TopicInventoryReport is a deterministic paged presentation of one immutable,
// reauthorized inventory bundle. Like ContractImpactReport it is not persisted
// separately: BundleID remains the content address.
type TopicInventoryReport struct {
	SchemaVersion string                      `json:"schema_version"`
	BundleID      string                      `json:"bundle_id"`
	Planes        []TopicPlaneState           `json:"planes"`
	Topics        []TopicInventoryRow         `json:"topics"`
	Pagination    TopicInventoryPagination    `json:"pagination"`
	Census        *KafkaTopicCensus           `json:"unresolved_census,omitempty"`
	Coverage      extract.CoverageCertificate `json:"coverage"`
	// Truncated names what clipped, using the same convention as the census:
	// a plane name for a clipped bounded scan, "composed" for the composed-row
	// ceiling. A non-empty list means every count here is a lower bound.
	Truncated []string `json:"truncated,omitempty"`
	Caveat    string   `json:"caveat"`
}

// topicInventoryCursor binds a page position to the exact bundle it was cut
// from. The bundle read is already reauthorized and byte-verified, so the
// cursor needs no separate principal binding; it does need to refuse reuse
// against another inventory, which the checksum and the ID comparison do.
type topicInventoryCursor struct {
	Schema   string `json:"schema"`
	BundleID string `json:"bundle_id"`
	Offset   int    `json:"offset"`
	Checksum string `json:"checksum"`
}

// admittedTopicPlanes applies the released-plane admission the topic detail
// path already uses: with released planes present, only those are admitted and
// every other plane is reported unavailable; with none, the experimental
// switch admits both. The returned domains are sorted for the bundle query.
func admittedTopicPlanes(opts Options) (admitted []topicPlane, domains []string, released bool) {
	releasedSet := opts.KafkaReleasedPlanes
	released = len(releasedSet) > 0
	for _, plane := range topicPlanes {
		if !released || slices.Contains(releasedSet, plane.plane) {
			admitted = append(admitted, plane)
			domains = append(domains, plane.domain)
		}
	}
	sort.Strings(domains)
	return admitted, domains, released
}

// ListKafkaTopics composes the immutable topic inventory bundle over the
// admitted Kafka planes. It answers "which source-spelled topics exist in
// scope, and what does each plane separately say about them", which the
// per-spelling detail query cannot: that one requires the caller to already
// know the spelling.
func (s *ProofService) ListKafkaTopics(ctx context.Context) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	admitted, domains, released := admittedTopicPlanes(s.opts)
	if len(admitted) == 0 {
		return nil, huma.Error404NotFound("topic inventory unavailable")
	}
	query := ProofQuery{Kind: topicInventoryQueryKind, Domains: domains}
	return buildAndPersistProofBundle(ctx, s.opts, func() (*ProofBundle, error) {
		return buildTopicInventoryBundle(ctx, s.opts, query, admitted, released)
	})
}

// buildTopicInventoryBundle evaluates the composition inside the same bounded
// coverage-digest sandwich and retry as every other proof bundle, so a
// concurrent extraction or authorization change conflicts instead of producing
// an inventory that does not match the coverage it cites.
func buildTopicInventoryBundle(
	ctx context.Context,
	opts Options,
	query ProofQuery,
	admitted []topicPlane,
	released bool,
) (*ProofBundle, error) {
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < proofBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(ctx, opts.Evidence, visible, query.Domains)
		if err != nil {
			return nil, huma.Error500InternalServerError("build extraction coverage", err)
		}
		sources, err := selectExactSources(ctx, opts.Evidence, certificate, visible, query.Domains)
		if err != nil {
			if isProofRetryable(err) {
				continue
			}
			return nil, huma.Error500InternalServerError("select topic inventory sources", err)
		}
		inventory, err := collectTopicInventory(ctx, opts.Evidence, certificate, sources, admitted, released)
		if err != nil {
			if isProofRetryable(err) {
				continue
			}
			return nil, huma.Error500InternalServerError("compose topic inventory", err)
		}
		census, err := collectKafkaTopicCensus(ctx, opts.Evidence, certificate, sources)
		if err != nil {
			if isProofRetryable(err) {
				continue
			}
			if errors.Is(err, store.ErrResultLimit) {
				return nil, huma.Error422UnprocessableEntity("unresolved census exceeds bounded result limits")
			}
			return nil, huma.Error500InternalServerError("build unresolved census", err)
		}
		markUnavailableCensusPlanes(census, opts, admitted)
		confirmed, err := extract.BuildCoverageCertificate(ctx, opts.Evidence, visible, query.Domains)
		if err != nil {
			return nil, huma.Error500InternalServerError("confirm extraction coverage", err)
		}
		if confirmed.Digest != certificate.Digest {
			continue
		}
		reselected, err := selectExactSources(ctx, opts.Evidence, confirmed, visible, query.Domains)
		if err != nil || !slices.Equal(reselected.authorities(), sources.authorities()) {
			continue // a partitioned root moved while the bundle was built
		}
		confirmedVisible, err := visibleRepositories(ctx, opts)
		if err != nil {
			return nil, err
		}
		if !sameVisibleEvidenceScopes(visible, confirmedVisible) {
			return nil, huma.Error409Conflict(
				"repository authorization or indexed scope changed while building the proof bundle; retry",
			)
		}
		bundle := ProofBundle{
			SchemaVersion: proofBundleSchemaVersion, Query: query,
			// The inventory carries no assertions and no evidence atoms: its
			// composed rows are the content, and their citations name exact
			// assertion identities rather than resolved occurrences.
			Assertions:        []store.Assertion{},
			Evidence:          []BundleEvidence{},
			UnresolvedCensus:  census,
			TopicInventory:    inventory,
			ExactAuthorities:  nonEmptyAuthorities(sources),
			Coverage:          *certificate,
			ExtractorVersions: certificateExtractors(certificate),
			VisibilityContext: visibilityContext(ctx, opts, certificate),
			Caveat:            topicInventoryCaveat,
			visibilityReceipt: cloneVisibleEvidenceScopes(confirmedVisible),
		}
		return &bundle, nil
	}
	return nil, huma.Error409Conflict("evidence changed while building the proof bundle; retry")
}

// markUnavailableCensusPlanes names every plane the composition did not admit
// on the shared census, so an unreleased plane's abstention zeros stay visibly
// unmeasured in the composed answer exactly as they do in the detail answer.
func markUnavailableCensusPlanes(census *KafkaTopicCensus, opts Options, admitted []topicPlane) {
	if census == nil || len(opts.KafkaReleasedPlanes) == 0 {
		return
	}
	for _, plane := range topicPlanes {
		if slices.ContainsFunc(admitted, func(value topicPlane) bool { return value.plane == plane.plane }) {
			continue
		}
		census.UnavailablePlanes = append(census.UnavailablePlanes, plane.plane)
	}
	sort.Strings(census.UnavailablePlanes)
}

// collectTopicInventory performs one bounded prefix query per published plane
// and repository, then composes the distinct spellings both planes report. The
// scan shape is the unresolved census's: one query, AllowTruncate, and an
// explicit lower-bound marker instead of an unbounded loop or a hard failure.
func collectTopicInventory(
	ctx context.Context,
	source store.EvidenceStore,
	certificate *extract.CoverageCertificate,
	sources catalogSources,
	admitted []topicPlane,
	released bool,
) (*TopicInventoryContent, error) {
	inventory := &TopicInventoryContent{
		SchemaVersion: topicInventorySchemaVersion,
		Planes:        make([]TopicPlaneState, 0, len(topicPlanes)),
		Topics:        []TopicInventoryRow{},
		Complete:      true,
	}
	// byTopic accumulates per spelling before the sorted composition, keyed by
	// topic then plane so both planes land in one row.
	byTopic := map[string]map[string]*topicRowAccumulator{}
	admittedPlanes := make(map[string]topicPlane, len(admitted))
	for _, plane := range admitted {
		admittedPlanes[plane.plane] = plane
	}
	for _, plane := range topicPlanes {
		selected, ok := admittedPlanes[plane.plane]
		state := TopicPlaneState{
			Plane: plane.plane, Domain: plane.domain,
			State: "unavailable", Released: released && ok,
			CoverageRows: []ImpactCoverageRow{},
		}
		if !ok {
			inventory.Planes = append(inventory.Planes, state)
			continue
		}
		state.State = "served"
		state.Quality = &TopicPlaneQuality{
			Card: selected.card, Claim: topicQualityClaim, Protocol: selected.protocol,
			Measured:   slices.Clone(topicQualityMeasured),
			Unmeasured: slices.Clone(topicQualityUnmeasured),
		}
		perPlane := map[string]*topicRowAccumulator{}
		for _, repository := range certificate.Repositories {
			run, hasRun := certificateRun(repository, plane.domain)
			if hasRun {
				rowState, reason := impactCoverageState(repository, run)
				state.CoverageRows = append(state.CoverageRows, ImpactCoverageRow{
					Repository: repository.Repository, Domain: run.Domain, State: rowState, Reason: reason,
					IndexedCommit: repository.IndexedCommit, EvidenceCommit: run.Commit,
					RunID: run.RunID, Extractor: run.Extractor,
					Protocols: slices.Clone(run.Protocols), Failures: slices.Clone(run.Failures),
					AssertionCount: run.AssertionCount, UnresolvedCount: run.UnresolvedCount,
					CandidateFiles: run.CandidateFileCount, ReadFiles: run.ReadFileCount,
					SourceScopeDigest: run.SourceScopeDigest,
				})
			}
			reader, runID, hasSource := proofSource(source, sources, repository, plane.domain)
			if !hasSource {
				continue
			}
			// A citation is only immutable if its commit is pinned, so the
			// commit comes from the exact run proofSource selected and never
			// from the certificate's own entry: for a partitioned source that
			// entry need not describe the fenced current root T55.3 reads.
			commit := selectedRunCommit(sources, repository, plane.domain, runID)
			if commit == "" {
				return nil, errors.New("topic inventory cannot pin an evidence commit for a published run")
			}
			state.PublishedRuns++
			state.Repositories++
			rows, err := reader.ListAssertions(ctx, store.AssertionQuery{
				Predicate: plane.predicate, ObjectPrefix: "topic:",
				Repo: repository.Repository, RunID: runID,
				Limit: proofQueryAssertionLimit, AllowTruncate: true,
			})
			if err != nil {
				return nil, err
			}
			if len(rows) > proofQueryAssertionLimit {
				rows = rows[:proofQueryAssertionLimit]
				inventory.Complete = false
				inventory.Truncated = append(inventory.Truncated, plane.plane)
			}
			for _, assertion := range rows {
				topic, found := strings.CutPrefix(assertion.Object, "topic:")
				if assertion.Repo != repository.Repository || assertion.RunID != runID ||
					assertion.Predicate != plane.predicate || !found || !validKafkaTopicSpelling(topic) ||
					len(assertion.Supporting) == 0 || len(assertion.Contradicting) != 0 {
					return nil, errors.New("topic inventory query returned an inconsistent assertion")
				}
				accumulator := perPlane[topic]
				if accumulator == nil {
					accumulator = &topicRowAccumulator{}
					perPlane[topic] = accumulator
				}
				accumulator.add(assertion, commit)
				state.SiteCount += len(assertion.Supporting)
			}
		}
		state.TopicCount = len(perPlane)
		for topic, accumulator := range perPlane {
			byPlane, seen := byTopic[topic]
			if !seen {
				byPlane = map[string]*topicRowAccumulator{}
				byTopic[topic] = byPlane
			}
			byPlane[plane.plane] = accumulator
		}
		inventory.Planes = append(inventory.Planes, state)
	}
	inventory.Topics, inventory.TopicCount = composeTopicRows(
		byTopic, admittedPlanes, &inventory.Complete, &inventory.Truncated,
	)
	// One marker per cause: several repositories can clip the same plane's
	// scan, and the marker names the plane, not the repository.
	sort.Strings(inventory.Truncated)
	inventory.Truncated = slices.Compact(inventory.Truncated)
	return inventory, nil
}

// selectedRunCommit returns the evidence commit of the exact run proofSource
// selected for one repository and domain, or an empty string when that
// selection cannot be re-identified. It never decides admission — proofSource
// does — so the two cannot drift: the run ID must match the one already
// selected, and a mismatch yields no commit rather than a plausible one.
func selectedRunCommit(
	sources catalogSources,
	repository extract.CertificateRepository,
	domain, runID string,
) string {
	if sources != nil {
		selected, ok := sources.lookup(repository.Repository, domain)
		if ok && selected.run.RunID == runID {
			return selected.run.Commit
		}
		return ""
	}
	if run, ok := certificateRun(repository, domain); ok && run.RunID == runID {
		return run.Commit
	}
	return ""
}

// topicRowAccumulator aggregates one plane's assertions for one spelling. Each
// assertion is one file's claim, so files counts assertions and sites sums
// their supporting atoms. Citations are retained only up to the render bound;
// the remainder is counted, never held.
type topicRowAccumulator struct {
	sites            int
	files            int
	citations        []TopicCitation
	omittedCitations int
}

func (accumulator *topicRowAccumulator) add(assertion store.Assertion, commit string) {
	accumulator.sites += len(assertion.Supporting)
	accumulator.files++
	if len(accumulator.citations) >= topicInventoryCitationsPerRow {
		accumulator.omittedCitations++
		return
	}
	accumulator.citations = append(accumulator.citations, TopicCitation{
		Repository: assertion.Repo, Commit: commit, RunID: assertion.RunID,
		AssertionID: assertion.ID, Path: assertion.Subject,
		Sites: len(assertion.Supporting),
	})
}

// composeTopicRows renders the accumulated per-plane evidence as one sorted
// row per spelling, with an explicit state for a plane that is admitted but
// found nothing and for a plane that is not admitted at all.
func composeTopicRows(
	rows map[string]map[string]*topicRowAccumulator,
	admitted map[string]topicPlane,
	complete *bool,
	truncated *[]string,
) ([]TopicInventoryRow, int) {
	topics := make([]string, 0, len(rows))
	for topic := range rows {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	if len(topics) > topicInventoryMaxTopics {
		topics = topics[:topicInventoryMaxTopics]
		*complete = false
		*truncated = append(*truncated, "composed")
	}
	composed := make([]TopicInventoryRow, 0, len(topics))
	for _, topic := range topics {
		row := TopicInventoryRow{Topic: topic}
		row.Producer = renderTopicRowPlane(rows[topic]["producer"], admitted["producer"])
		row.Consumer = renderTopicRowPlane(rows[topic]["consumer"], admitted["consumer"])
		composed = append(composed, row)
	}
	return composed, len(composed)
}

func renderTopicRowPlane(accumulator *topicRowAccumulator, plane topicPlane) TopicRowPlane {
	if plane.plane == "" {
		return TopicRowPlane{State: "unavailable"}
	}
	if accumulator == nil {
		// The plane is admitted and reported no site for this spelling: a
		// measured zero, distinct from an unadmitted plane's unmeasured one.
		// State carries that distinction, so the row needs no empty slices.
		return TopicRowPlane{State: "none"}
	}
	// The retained citations are already in the store's stable scan order, so
	// this sort only pins the rendered order against any future change in that
	// scan; it never reorders a bounded prefix into a different set.
	citations := slices.Clone(accumulator.citations)
	sort.Slice(citations, func(i, j int) bool {
		if citations[i].Repository != citations[j].Repository {
			return citations[i].Repository < citations[j].Repository
		}
		if citations[i].Path != citations[j].Path {
			return citations[i].Path < citations[j].Path
		}
		return citations[i].AssertionID < citations[j].AssertionID
	})
	return TopicRowPlane{
		State: "served", Sites: accumulator.sites, Files: accumulator.files,
		Citations: citations, OmittedCitations: accumulator.omittedCitations,
	}
}

// validKafkaTopicSpelling applies the same KD2 identity bounds the topic detail
// path enforces, so a composed row can never carry a spelling the detail query
// would refuse.
func validKafkaTopicSpelling(topic string) bool {
	return validateKafkaTopic(topic) == nil
}

// BuildTopicInventory presents one page of an immutable inventory bundle. The
// bundle read reauthorizes every repository it cites and re-verifies its
// canonical bytes, so a page can outlive the request that composed it without
// outliving the caller's access to it.
func (s *ProofService) BuildTopicInventory(
	ctx context.Context,
	bundleID string,
	pageSize int,
	cursor string,
) (*TopicInventoryReport, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	if pageSize == 0 {
		pageSize = topicInventoryDefaultPage
	}
	if pageSize < 1 || pageSize > topicInventoryMaxPage {
		return nil, huma.Error400BadRequest("page_size must be from 1 through 100")
	}
	envelope, err := readProofBundle(ctx, s.opts, bundleID)
	if err != nil {
		return nil, err
	}
	if envelope.Bundle.Query.Kind != topicInventoryQueryKind {
		return nil, huma.Error404NotFound("proof bundle is not a topic inventory")
	}
	inventory := envelope.Bundle.TopicInventory
	if inventory == nil || inventory.SchemaVersion != topicInventorySchemaVersion {
		return nil, huma.Error500InternalServerError("stored topic inventory is inconsistent")
	}
	offset, err := decodeTopicInventoryCursor(cursor, envelope.ID)
	if err != nil {
		return nil, err
	}
	if offset > len(inventory.Topics) {
		return nil, huma.Error409Conflict("topic inventory cursor is no longer valid")
	}
	end := min(offset+pageSize, len(inventory.Topics))
	page := slices.Clone(inventory.Topics[offset:end])
	pagination := TopicInventoryPagination{
		Order: "topic", PageSize: pageSize, Returned: len(page),
		Total: inventory.TopicCount, Complete: inventory.Complete,
	}
	if end < len(inventory.Topics) {
		pagination.NextCursor, err = encodeTopicInventoryCursor(envelope.ID, end)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode topic inventory cursor", err)
		}
	}
	return &TopicInventoryReport{
		SchemaVersion: topicInventorySchemaVersion, BundleID: envelope.ID,
		Planes: inventory.Planes, Topics: page, Pagination: pagination,
		Census: envelope.Bundle.UnresolvedCensus, Coverage: envelope.Bundle.Coverage,
		Truncated: slices.Clone(inventory.Truncated),
		Caveat:    envelope.Bundle.Caveat,
	}, nil
}

func decodeTopicInventoryCursor(encoded, bundleID string) (int, error) {
	if encoded == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > topicInventoryCursorBytesLimit || !json.Valid(raw) {
		return 0, huma.Error400BadRequest("topic inventory cursor is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var cursor topicInventoryCursor
	if err := decoder.Decode(&cursor); err != nil {
		return 0, huma.Error400BadRequest("topic inventory cursor is invalid")
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if cursor.Schema != topicInventoryCursorSchema || checksum == "" || checksum != digestJSON(cursor) {
		return 0, huma.Error400BadRequest("topic inventory cursor is invalid")
	}
	if cursor.BundleID != bundleID {
		return 0, huma.Error400BadRequest("topic inventory cursor does not match this inventory")
	}
	if cursor.Offset < 0 {
		return 0, huma.Error400BadRequest("topic inventory cursor is invalid")
	}
	return cursor.Offset, nil
}

func encodeTopicInventoryCursor(bundleID string, offset int) (string, error) {
	cursor := topicInventoryCursor{
		Schema: topicInventoryCursorSchema, BundleID: bundleID, Offset: offset,
	}
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
