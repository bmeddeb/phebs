package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

// The T57.1 composition tests validate the composed topic inventory directly:
// what the two independently released planes contribute to one row, what each
// plane separately says about its own publication, coverage and quality state,
// and how the composed rows page. They deliberately bypass HTTP and MCP,
// because T57.2 owns exposure; nothing here pins a route.

const (
	topicInventoryKind   = "list_kafka_topics"
	topicDetailKind      = "find_kafka_topic_usage"
	topicEvidenceDetail  = "kafka-topic-evidence-detail-v1"
	topicUnresolvedShape = "kafka-topic-unresolved-detail-v1"
)

// topicAssertion builds one file's claim about one topic exactly as kafkago
// emits it: the subject is the file, the object is topic:<spelling>, the detail
// is the frozen evidence schema, and the supporting atoms are that file's
// sites. It also returns the per-atom resolutions a topic *detail* bundle
// needs, so one fixture can drive both the overview and the detail.
func topicAssertion(
	repo, runID, id, predicate, topic string,
	sites int,
	libraries ...string,
) (store.Assertion, []store.EvidenceResolution) {
	supporting := make([]string, 0, sites)
	resolutions := make([]store.EvidenceResolution, 0, sites)
	for index := range sites {
		atomID := "ea_" + id + "_" + strconv.Itoa(index)
		supporting = append(supporting, atomID)
		resolutions = append(resolutions, store.EvidenceResolution{
			Atom: store.EvidenceAtom{
				ID: atomID, SchemaVersion: "t14-test",
				BlobDigest: "sha256:" + strings.Repeat("c", 64),
				StartByte:  index, EndByte: index + 4,
			},
			Occurrences: []store.SnapshotEvidence{{
				ID: "occ_" + atomID, AtomID: atomID, Repo: repo,
				Commit: strings.Repeat("a", 40), Path: "producer/" + id + ".go",
				StartLine: 7 + index, EndLine: 7 + index,
				VisibilityScope: "repo:" + repo, RunID: runID,
			}},
		})
	}
	quoted := make([]string, len(libraries))
	for index, library := range libraries {
		quoted[index] = strconv.Quote(library)
	}
	assertion := store.Assertion{
		ID: id, Predicate: predicate, Subject: "producer/" + id + ".go",
		Object: "topic:" + topic, Tier: store.TierHeuristic, CodeRole: "production",
		Repo: repo, RunID: runID, Supporting: supporting,
		Detail: `{"schema":` + strconv.Quote(topicEvidenceDetail) +
			`,"libraries":[` + strings.Join(quoted, ",") +
			`],"import_paths":[],"shapes":["writer-publish"],"bindings":["literal"]}`,
	}
	return assertion, resolutions
}

// unresolvedAssertion builds one abstention row for the shared census, whose
// object prefix keeps it out of the inventory scan by construction.
func unresolvedAssertion(repo, runID, id, predicate, shape string, sites int) store.Assertion {
	supporting := make([]string, 0, sites)
	for index := range sites {
		supporting = append(supporting, "ea_"+id+"_"+strconv.Itoa(index))
	}
	return store.Assertion{
		ID: id, Predicate: predicate, Subject: "producer/" + id + ".go",
		Object: "unresolved:" + shape, Tier: store.TierUnresolved, CodeRole: "production",
		Repo: repo, RunID: runID, Supporting: supporting,
		Detail: `{"schema":` + strconv.Quote(topicUnresolvedShape) +
			`,"shape":` + strconv.Quote(shape) + `}`,
	}
}

type topicFixture struct {
	repo        string
	producerRun store.ExtractionRun
	consumerRun store.ExtractionRun
	assertions  []store.Assertion
	resolutions map[string]store.EvidenceResolution
}

func newTopicFixture(repo string) *topicFixture {
	return &topicFixture{
		repo:        repo,
		producerRun: proofRun(repo, "kafka-producer", "run-kafka-producer"),
		consumerRun: proofRun(repo, "kafka-consumer", "run-kafka-consumer"),
		resolutions: map[string]store.EvidenceResolution{},
	}
}

func (f *topicFixture) add(assertion store.Assertion, resolutions []store.EvidenceResolution) {
	f.assertions = append(f.assertions, assertion)
	runID := assertion.RunID
	for _, resolution := range resolutions {
		f.resolutions[proofEvidenceScope(f.repo, runID, resolution.Atom.ID)] = resolution
	}
}

func (f *topicFixture) store() *proofAPIStore {
	return &proofAPIStore{
		repos: []store.Repo{{Name: f.repo, IndexedCommitHash: f.producerRun.Commit}},
		runs: map[string]store.ExtractionRun{
			proofScope(f.repo, "kafka-producer"): f.producerRun,
			proofScope(f.repo, "kafka-consumer"): f.consumerRun,
		},
		assertions:  map[string][]store.Assertion{f.repo: f.assertions},
		resolutions: f.resolutions,
	}
}

// topicService builds the proof service the composition runs against. It is
// the same option set the HTTP boundary assembles, minus every transport.
func topicService(st *proofAPIStore, planes []string) *api.ProofService {
	return api.NewProofService(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st,
		KafkaReleasedPlanes:   planes,
		Principal:             func(context.Context) string { return "user:member" },
		AuthorizationProvider: "test-permissions-v1",
	})
}

func topicInventory(
	t *testing.T,
	service *api.ProofService,
) (*api.ProofBundleEnvelope, *api.TopicInventoryReport) {
	t.Helper()
	envelope, err := service.ListKafkaTopics(context.Background())
	if err != nil {
		t.Fatalf("ListKafkaTopics: %v", err)
	}
	report, err := service.BuildTopicInventory(context.Background(), envelope.ID, 0, "")
	if err != nil {
		t.Fatalf("BuildTopicInventory: %v", err)
	}
	return envelope, report
}

func planeState(report *api.TopicInventoryReport, plane string) api.TopicPlaneState {
	for _, state := range report.Planes {
		if state.Plane == plane {
			return state
		}
	}
	return api.TopicPlaneState{}
}

func topicRow(report *api.TopicInventoryReport, topic string) (api.TopicInventoryRow, bool) {
	for _, row := range report.Topics {
		if row.Topic == topic {
			return row, true
		}
	}
	return api.TopicInventoryRow{}, false
}

// TestTopicInventoryComposesBothPlanes is the core composition validation: two
// independently published planes land in one row per spelling, a plane that
// found nothing for a spelling reports a measured zero rather than borrowing
// the other plane's evidence, and the overview carries the same unresolved
// census the detail does.
func TestTopicInventoryComposesBothPlanes(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	first, firstEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 2, "segmentio/kafka-go",
	)
	second, secondEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-b", "PRODUCES_TO_TOPIC", "orders-v1", 1, "confluent-kafka-go",
	)
	third, thirdEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-c", "PRODUCES_TO_TOPIC", "payments", 3, "segmentio/kafka-go",
	)
	consumed, consumedEvidence := topicAssertion(
		repo, fixture.consumerRun.ID, "consume-a", "CONSUMES_FROM_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
	)
	fixture.add(first, firstEvidence)
	fixture.add(second, secondEvidence)
	fixture.add(third, thirdEvidence)
	fixture.add(consumed, consumedEvidence)
	fixture.add(unresolvedAssertion(
		repo, fixture.producerRun.ID, "abstain", "UNRESOLVED_KAFKA_PRODUCER", "call-expr", 4,
	), nil)

	var queries []store.AssertionQuery
	st := fixture.store()
	st.onAssertions = func(query store.AssertionQuery) {
		queries = append(queries, query)
	}
	envelope, report := topicInventory(t, topicService(st, nil))

	if envelope.Bundle.Query.Kind != topicInventoryKind {
		t.Fatalf("bundle query kind = %q, want %q", envelope.Bundle.Query.Kind, topicInventoryKind)
	}
	if got := strings.Join(envelope.Bundle.Query.Domains, ","); got != "kafka-consumer,kafka-producer" {
		t.Fatalf("bundle domains = %q", got)
	}
	// The bounded-cost contract: the overview names exact assertion identities
	// and leaves per-site spans to the detail bundle, so it resolves no atoms.
	if len(envelope.Bundle.Assertions) != 0 || len(envelope.Bundle.Evidence) != 0 {
		t.Fatalf("inventory bundle must carry no assertions or evidence: %+v", envelope.Bundle)
	}
	for _, call := range st.calls {
		if strings.HasPrefix(call, "resolve:") {
			t.Fatalf("inventory resolved evidence (%s); it must not", call)
		}
	}
	// Two bounded prefix scans (one per published plane/run) plus the two the
	// shared census already pays — the same shape as one topic detail query.
	if len(queries) != 4 {
		t.Fatalf("assertion query count = %d, want 4: %+v", len(queries), queries)
	}
	inventoryQueries := 0
	for _, query := range queries {
		if query.ObjectPrefix != "topic:" {
			continue
		}
		inventoryQueries++
		if query.Object != "" || !query.AllowTruncate || query.Limit != 5000 {
			t.Fatalf("unbounded inventory query = %+v", query)
		}
	}
	if inventoryQueries != 2 {
		t.Fatalf("inventory prefix query count = %d, want 2", inventoryQueries)
	}

	if len(report.Topics) != 2 {
		t.Fatalf("composed topics = %+v", report.Topics)
	}
	if report.Topics[0].Topic != "orders-v1" || report.Topics[1].Topic != "payments" {
		t.Fatalf("topics are not sorted by spelling: %+v", report.Topics)
	}
	orders, _ := topicRow(report, "orders-v1")
	if orders.Producer.State != "served" || orders.Producer.Sites != 3 || orders.Producer.Files != 2 {
		t.Fatalf("orders-v1 producer plane = %+v", orders.Producer)
	}
	if orders.Consumer.State != "served" || orders.Consumer.Sites != 1 || orders.Consumer.Files != 1 {
		t.Fatalf("orders-v1 consumer plane = %+v", orders.Consumer)
	}
	payments, _ := topicRow(report, "payments")
	// A measured zero on an admitted plane, distinct from an unmeasured one.
	if payments.Consumer.State != "none" || payments.Consumer.Sites != 0 ||
		payments.Consumer.Files != 0 || len(payments.Consumer.Citations) != 0 {
		t.Fatalf("payments consumer plane = %+v", payments.Consumer)
	}
	if payments.Producer.Sites != 3 || payments.Producer.Files != 1 {
		t.Fatalf("payments producer plane = %+v", payments.Producer)
	}

	// Citations are exact, pinned, and ordered — never a mutable HEAD link.
	if len(orders.Producer.Citations) != 2 {
		t.Fatalf("orders-v1 producer citations = %+v", orders.Producer.Citations)
	}
	for _, citation := range orders.Producer.Citations {
		if citation.Repository != repo || citation.RunID != fixture.producerRun.ID ||
			citation.AssertionID == "" || citation.Path == "" ||
			citation.Commit != strings.Repeat("a", 40) || citation.Sites < 1 {
			t.Fatalf("citation is not an exact pinned identity: %+v", citation)
		}
	}

	producer := planeState(report, "producer")
	consumer := planeState(report, "consumer")
	if len(report.Planes) != 2 || report.Planes[0].Plane != "consumer" || report.Planes[1].Plane != "producer" {
		t.Fatalf("plane rows are not the closed vocabulary in canonical order: %+v", report.Planes)
	}
	for _, state := range []api.TopicPlaneState{producer, consumer} {
		if state.State != "served" || state.Released {
			t.Fatalf("experimental admission must serve both planes unreleased: %+v", state)
		}
		if state.PublishedRuns != 1 || state.Repositories != 1 {
			t.Fatalf("plane publication state = %+v", state)
		}
		if len(state.CoverageRows) != 1 || state.CoverageRows[0].State != "covered" {
			t.Fatalf("plane coverage state = %+v", state.CoverageRows)
		}
		if state.Quality == nil || state.Quality.Claim != "site-coverage" ||
			!slices.Contains(state.Quality.Unmeasured, "topic-precision") ||
			!slices.Contains(state.Quality.Unmeasured, "broker-identity") {
			t.Fatalf("plane quality state must name the frozen card and its non-claims: %+v", state.Quality)
		}
	}
	if producer.TopicCount != 2 || producer.SiteCount != 6 {
		t.Fatalf("producer plane totals = %+v", producer)
	}
	if consumer.TopicCount != 1 || consumer.SiteCount != 1 {
		t.Fatalf("consumer plane totals = %+v", consumer)
	}
	if producer.Quality.Card != "phebs.kafka.producer" || consumer.Quality.Card != "phebs.kafka.consumer" {
		t.Fatalf("each plane must cite its own card: %+v / %+v", producer.Quality, consumer.Quality)
	}
	if !report.Pagination.Complete || len(report.Pagination.NextCursor) != 0 {
		t.Fatalf("an unclipped single page must report itself complete: %+v", report.Pagination)
	}
	if report.Census == nil || report.Census.Producer["call-expr"] != 4 ||
		len(report.Census.UnavailablePlanes) != 0 {
		t.Fatalf("composed census = %+v", report.Census)
	}
	if report.BundleID != envelope.ID || report.Caveat == "" {
		t.Fatalf("report is not bound to its bundle: %+v", report)
	}
}

// TestTopicInventoryReleasedPlaneScoping pins that a released plane set admits
// only its own planes, and that every unadmitted plane is named unavailable at
// the plane row, at the census, and on every topic row — so its zeros can never
// read as measurements.
func TestTopicInventoryReleasedPlaneScoping(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	tests := []struct {
		name              string
		planes            []string
		servedState       string
		unavailable       []string
		released          bool
		wantUnavailableAt string
	}{
		{
			name: "producer alone", planes: []string{"producer"},
			servedState: "served", unavailable: []string{"consumer"},
			released: true, wantUnavailableAt: "consumer",
		},
		{
			name: "consumer alone", planes: []string{"consumer"},
			servedState: "served", unavailable: []string{"producer"},
			released: true, wantUnavailableAt: "producer",
		},
		{
			name: "both planes", planes: []string{"consumer", "producer"},
			servedState: "served", unavailable: nil, released: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTopicFixture(repo)
			produced, producedEvidence := topicAssertion(
				repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 2, "segmentio/kafka-go",
			)
			consumed, consumedEvidence := topicAssertion(
				repo, fixture.consumerRun.ID, "consume-a", "CONSUMES_FROM_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
			)
			fixture.add(produced, producedEvidence)
			fixture.add(consumed, consumedEvidence)
			envelope, report := topicInventory(t, topicService(fixture.store(), test.planes))

			for _, plane := range []string{"producer", "consumer"} {
				state := planeState(report, plane)
				wantState := test.servedState
				if slices.Contains(test.unavailable, plane) {
					wantState = "unavailable"
				}
				if state.State != wantState {
					t.Fatalf("plane %s state = %q, want %q", plane, state.State, wantState)
				}
				if state.Released != (test.released && wantState == "served") {
					t.Fatalf("plane %s released = %t: %+v", plane, state.Released, state)
				}
				// An unavailable plane has no quality result to report and must
				// not borrow the other plane's card.
				if (state.Quality != nil) != (wantState == "served") {
					t.Fatalf("plane %s quality presence = %+v", plane, state.Quality)
				}
				row, _ := topicRow(report, "orders-v1")
				rowPlane := row.Producer
				if plane == "consumer" {
					rowPlane = row.Consumer
				}
				if wantState == "unavailable" && rowPlane.State != "unavailable" {
					t.Fatalf("plane %s row state = %q, want unavailable", plane, rowPlane.State)
				}
			}
			if !slices.Equal(envelope.Bundle.UnresolvedCensus.UnavailablePlanes, test.unavailable) {
				t.Fatalf(
					"census unavailable planes = %+v, want %+v",
					envelope.Bundle.UnresolvedCensus.UnavailablePlanes, test.unavailable,
				)
			}
			// The unadmitted plane's domain must not enter the bundle query.
			if test.wantUnavailableAt != "" &&
				slices.Contains(envelope.Bundle.Query.Domains, "kafka-"+test.wantUnavailableAt) {
				t.Fatalf("bundle domains admit an unavailable plane: %+v", envelope.Bundle.Query.Domains)
			}
		})
	}
}

// TestTopicInventoryPaging validates the composed page boundaries and the
// cursor: a full walk returns every row exactly once, a cursor is bound to the
// inventory it was cut from, and a tampered or foreign cursor is refused
// rather than silently re-anchored.
func TestTopicInventoryPaging(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	want := make([]string, 0, 5)
	for index := range 5 {
		topic := "topic-" + strconv.Itoa(index)
		want = append(want, topic)
		assertion, evidence := topicAssertion(
			repo, fixture.producerRun.ID, "produce-"+strconv.Itoa(index),
			"PRODUCES_TO_TOPIC", topic, 1, "segmentio/kafka-go",
		)
		fixture.add(assertion, evidence)
	}
	service := topicService(fixture.store(), nil)
	ctx := context.Background()
	envelope, err := service.ListKafkaTopics(ctx)
	if err != nil {
		t.Fatalf("ListKafkaTopics: %v", err)
	}

	var walked []string
	cursor := ""
	for page := 0; page < 10; page++ {
		report, err := service.BuildTopicInventory(ctx, envelope.ID, 2, cursor)
		if err != nil {
			t.Fatalf("BuildTopicInventory page %d: %v", page, err)
		}
		if report.Pagination.PageSize != 2 || report.Pagination.Total != 5 ||
			!report.Pagination.Complete || report.Pagination.Order != "topic" {
			t.Fatalf("pagination block = %+v", report.Pagination)
		}
		for _, row := range report.Topics {
			walked = append(walked, row.Topic)
		}
		if report.Pagination.NextCursor == "" {
			if len(report.Topics) != 1 {
				t.Fatalf("final page returned %d rows, want 1", len(report.Topics))
			}
			break
		}
		if len(report.Topics) != 2 {
			t.Fatalf("page %d returned %d rows, want 2", page, len(report.Topics))
		}
		cursor = report.Pagination.NextCursor
	}
	if !slices.Equal(walked, want) {
		t.Fatalf("paged walk = %+v, want %+v", walked, want)
	}

	// A cursor is bound to its own inventory.
	first, err := service.BuildTopicInventory(ctx, envelope.ID, 2, "")
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	other := newTopicFixture(repo)
	assertion, evidence := topicAssertion(
		repo, other.producerRun.ID, "produce-solo", "PRODUCES_TO_TOPIC", "solo", 1, "segmentio/kafka-go",
	)
	other.add(assertion, evidence)
	otherService := topicService(other.store(), nil)
	otherEnvelope, err := otherService.ListKafkaTopics(ctx)
	if err != nil {
		t.Fatalf("other ListKafkaTopics: %v", err)
	}
	if otherEnvelope.ID == envelope.ID {
		t.Fatal("the second inventory must have its own content address")
	}
	if _, err := otherService.BuildTopicInventory(
		ctx, otherEnvelope.ID, 2, first.Pagination.NextCursor,
	); !statusIs(err, http.StatusBadRequest) {
		t.Fatalf("foreign cursor = %v, want 400", err)
	}

	// A tampered cursor fails its own checksum instead of being trusted.
	tampered := strings.TrimSuffix(first.Pagination.NextCursor, "A") + "B"
	if tampered == first.Pagination.NextCursor {
		tampered = first.Pagination.NextCursor + "A"
	}
	if _, err := service.BuildTopicInventory(ctx, envelope.ID, 2, tampered); !statusIs(err, http.StatusBadRequest) {
		t.Fatalf("tampered cursor = %v, want 400", err)
	}
	if _, err := service.BuildTopicInventory(ctx, envelope.ID, 2, "not-base64!!"); !statusIs(err, http.StatusBadRequest) {
		t.Fatalf("malformed cursor = %v, want 400", err)
	}
	if _, err := service.BuildTopicInventory(ctx, envelope.ID, 101, ""); !statusIs(err, http.StatusBadRequest) {
		t.Fatalf("page_size 101 = %v, want 400", err)
	}
	// Page size zero means the default, and the default fits every row here.
	report, err := service.BuildTopicInventory(ctx, envelope.ID, 0, "")
	if err != nil {
		t.Fatalf("default page: %v", err)
	}
	if report.Pagination.PageSize != 50 || len(report.Topics) != 5 || report.Pagination.NextCursor != "" {
		t.Fatalf("default page = %+v", report.Pagination)
	}
	// A content address that was never composed is a plain miss.
	if _, err := service.BuildTopicInventory(
		ctx, "sha256:"+strings.Repeat("0", 64), 2, "",
	); !statusIs(err, http.StatusNotFound) {
		t.Fatalf("unknown bundle = %v, want 404", err)
	}
}

// TestTopicInventoryRefusesAnotherKind keeps the inventory presentation from
// reading a bundle that asks a different question.
func TestTopicInventoryRefusesAnotherKind(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	produced, producedEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
	)
	fixture.add(produced, producedEvidence)
	st := fixture.store()
	handler := proofHandler(st, "user:member", nil)
	code, body, detail := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	if code != http.StatusOK {
		t.Fatalf("detail query = %d %s", code, body)
	}
	if detail.Bundle.Query.Kind != topicDetailKind {
		t.Fatalf("detail bundle kind = %q", detail.Bundle.Query.Kind)
	}
	// The released detail answer keeps its exact bytes: the composition annex
	// is omitempty and only a list_kafka_topics bundle sets it, so the T55.3
	// and T56.3 released surface is not re-shaped by this ticket.
	if strings.Contains(body, "topic_inventory") {
		t.Fatalf("the detail bundle gained the inventory annex: %s", body)
	}
	if detail.Bundle.TopicInventory != nil {
		t.Fatalf("detail bundle carries an inventory annex: %+v", detail.Bundle.TopicInventory)
	}
	service := topicService(st, nil)
	if _, err := service.BuildTopicInventory(
		context.Background(), detail.ID, 0, "",
	); !statusIs(err, http.StatusNotFound) {
		t.Fatalf("detail bundle as inventory = %v, want 404", err)
	}
}

// TestTopicInventoryAgreesWithDetail is the drift pin between the composed
// overview and the released per-spelling detail: both read the same two planes
// through the same predicates, so a topic's overview counts must equal the
// detail bundle's own evidence. If either path's domain or predicate literal
// drifts, this fails.
func TestTopicInventoryAgreesWithDetail(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	produced, producedEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 2, "segmentio/kafka-go",
	)
	consumed, consumedEvidence := topicAssertion(
		repo, fixture.consumerRun.ID, "consume-a", "CONSUMES_FROM_TOPIC", "orders-v1", 3, "segmentio/kafka-go",
	)
	fixture.add(produced, producedEvidence)
	fixture.add(consumed, consumedEvidence)
	st := fixture.store()

	_, report := topicInventory(t, topicService(st, nil))
	row, found := topicRow(report, "orders-v1")
	if !found {
		t.Fatalf("overview has no orders-v1 row: %+v", report.Topics)
	}

	handler := proofHandler(st, "user:member", nil)
	code, body, detail := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	if code != http.StatusOK {
		t.Fatalf("detail query = %d %s", code, body)
	}
	producerSites, consumerSites := 0, 0
	for _, assertion := range detail.Bundle.Assertions {
		switch assertion.Predicate {
		case "PRODUCES_TO_TOPIC":
			producerSites += len(assertion.Supporting)
		case "CONSUMES_FROM_TOPIC":
			consumerSites += len(assertion.Supporting)
		default:
			t.Fatalf("detail returned an unexpected predicate %q", assertion.Predicate)
		}
	}
	if row.Producer.Sites != producerSites || row.Consumer.Sites != consumerSites {
		t.Fatalf(
			"overview (%d/%d) disagrees with detail (%d/%d)",
			row.Producer.Sites, row.Consumer.Sites, producerSites, consumerSites,
		)
	}
	if row.Producer.Sites != 2 || row.Consumer.Sites != 3 {
		t.Fatalf("the fixture itself changed meaning: %+v", row)
	}
}

// TestTopicInventoryBounds validates both ceilings: a clipped plane scan and a
// clipped composed row list each report themselves incomplete and name what
// clipped, instead of presenting a lower bound as a corpus total.
func TestTopicInventoryBounds(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	t.Run("clipped plane scan", func(t *testing.T) {
		fixture := newTopicFixture(repo)
		for index := range 5001 {
			topic := "topic-" + strconv.Itoa(index%10)
			assertion, _ := topicAssertion(
				repo, fixture.producerRun.ID, "produce-"+strconv.Itoa(index),
				"PRODUCES_TO_TOPIC", topic, 1, "segmentio/kafka-go",
			)
			fixture.add(assertion, nil)
		}
		_, report := topicInventory(t, topicService(fixture.store(), nil))
		if report.Pagination.Complete {
			t.Fatal("a clipped scan must not report itself complete")
		}
		if !slices.Contains(report.Truncated, "producer") {
			t.Fatalf("truncation does not name the producer plane: %+v", report.Truncated)
		}
		producer := planeState(report, "producer")
		if producer.SiteCount != 5000 {
			t.Fatalf("clipped producer site count = %d, want the 5000 bound", producer.SiteCount)
		}
	})
	t.Run("clipped composed rows", func(t *testing.T) {
		fixture := newTopicFixture(repo)
		for index := range 2001 {
			topic := fmt.Sprintf("topic-%05d", index)
			assertion, _ := topicAssertion(
				repo, fixture.producerRun.ID, "produce-"+strconv.Itoa(index),
				"PRODUCES_TO_TOPIC", topic, 1, "segmentio/kafka-go",
			)
			fixture.add(assertion, nil)
		}
		_, report := topicInventory(t, topicService(fixture.store(), nil))
		if report.Pagination.Complete || report.Pagination.Total != 2000 {
			t.Fatalf("composed ceiling = %+v", report.Pagination)
		}
		if !slices.Contains(report.Truncated, "composed") {
			t.Fatalf("truncation does not name the composed ceiling: %+v", report.Truncated)
		}
		// The retained rows are the deterministic sorted prefix.
		if report.Topics[0].Topic != "topic-00000" {
			t.Fatalf("clipped inventory is not the sorted prefix: %+v", report.Topics[0])
		}
	})
}

// TestTopicInventoryRowBounds pins that one hot topic names its first few
// citations exactly and counts the remainder, rather than either dropping them
// silently or holding an unbounded row. The file and site totals stay exact
// even though the rendered citation list is bounded.
func TestTopicInventoryRowBounds(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	for index := range 12 {
		assertion, _ := topicAssertion(
			repo, fixture.producerRun.ID, "produce-"+strconv.Itoa(index),
			"PRODUCES_TO_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
		)
		fixture.add(assertion, nil)
	}
	_, report := topicInventory(t, topicService(fixture.store(), nil))
	row, found := topicRow(report, "orders-v1")
	if !found {
		t.Fatalf("no orders-v1 row: %+v", report.Topics)
	}
	if len(row.Producer.Citations) != 8 || row.Producer.OmittedCitations != 4 {
		t.Fatalf("citation bound = %d cited, %d omitted",
			len(row.Producer.Citations), row.Producer.OmittedCitations)
	}
	if row.Producer.Files != 12 || row.Producer.Sites != 12 {
		t.Fatalf("row totals must stay exact even when the render is bounded: %+v", row.Producer)
	}
	// The retained prefix is the store's stable scan order, so the citations
	// are ordered by path and the omitted ones are the tail, not an arbitrary
	// subset.
	for index := 1; index < len(row.Producer.Citations); index++ {
		if row.Producer.Citations[index-1].Path > row.Producer.Citations[index].Path {
			t.Fatalf("citations are not in scan order: %+v", row.Producer.Citations)
		}
	}
	if row.Producer.Citations[0].Path != "producer/produce-0.go" {
		t.Fatalf("retained prefix does not start at the first scanned file: %+v",
			row.Producer.Citations[0])
	}
}

// TestTopicInventoryIgnoresAssertionDetail pins that the overview reads no
// per-assertion detail. That is what keeps it off the atom-resolution path: a
// row whose detail the detail-bundle would need to parse still composes here,
// because the overview claims only counts and exact assertion identities.
func TestTopicInventoryIgnoresAssertionDetail(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	assertion, _ := topicAssertion(
		repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 2, "segmentio/kafka-go",
	)
	assertion.Detail = "not-json-at-all"
	fixture.add(assertion, nil)
	_, report := topicInventory(t, topicService(fixture.store(), nil))
	row, found := topicRow(report, "orders-v1")
	if !found || row.Producer.Sites != 2 || row.Producer.Files != 1 {
		t.Fatalf("a detail the overview does not read changed the row: %+v", row)
	}
}

// TestTopicInventoryRefusesInconsistentRows pins that a row the composition
// cannot honestly label fails the whole inventory instead of being skipped: a
// silent skip would understate a plane's coverage.
func TestTopicInventoryRefusesInconsistentRows(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	tests := []struct {
		name   string
		mutate func(*store.Assertion)
	}{
		{"no supporting site", func(assertion *store.Assertion) { assertion.Supporting = nil }},
		{"contradicted", func(assertion *store.Assertion) { assertion.Contradicting = []string{"ea_x"} }},
		{"invalid topic spelling", func(assertion *store.Assertion) { assertion.Object = "topic:bad spelling!" }},
		{"reserved topic spelling", func(assertion *store.Assertion) { assertion.Object = "topic:.." }},
		{"empty topic spelling", func(assertion *store.Assertion) { assertion.Object = "topic:" }},
		{"foreign repository", func(assertion *store.Assertion) { assertion.Repo = "github.com/other/repo" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTopicFixture(repo)
			assertion, _ := topicAssertion(
				repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
			)
			test.mutate(&assertion)
			fixture.add(assertion, nil)
			_, err := topicService(fixture.store(), nil).ListKafkaTopics(context.Background())
			if err == nil {
				t.Fatal("an inconsistent row composed without error")
			}
			if !statusIs(err, http.StatusInternalServerError) {
				t.Fatalf("status = %v, want 500", err)
			}
		})
	}
}

// TestTopicInventoryRespectsRepositoryVisibility pins that the composition
// inherits the proof path's authorization rather than widening it: a repository
// the caller cannot see contributes no topic, no citation and no count, its
// name never reaches the persisted bytes, and losing visibility withdraws an
// already-composed page.
func TestTopicInventoryRespectsRepositoryVisibility(t *testing.T) {
	const allowed = "github.com/allowed/kafka"
	const denied = "github.com/denied/kafka"
	allowedRun := proofRun(allowed, "kafka-producer", "run-allowed-producer")
	deniedRun := proofRun(denied, "kafka-producer", "run-denied-producer")
	allowedAssertion, _ := topicAssertion(
		allowed, allowedRun.ID, "produce-allowed", "PRODUCES_TO_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
	)
	deniedAssertion, _ := topicAssertion(
		denied, deniedRun.ID, "produce-denied", "PRODUCES_TO_TOPIC", "secret-topic", 1, "segmentio/kafka-go",
	)
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: allowed, IndexedCommitHash: allowedRun.Commit},
			{Name: denied, IndexedCommitHash: deniedRun.Commit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(allowed, "kafka-producer"): allowedRun,
			proofScope(denied, "kafka-producer"):  deniedRun,
		},
		assertions: map[string][]store.Assertion{
			allowed: {allowedAssertion},
			denied:  {deniedAssertion},
		},
	}
	visible := map[string]bool{allowed: true}
	service := api.NewProofService(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st,
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool { return visible[repo.Name] }
		},
		Principal:             func(context.Context) string { return "user:member" },
		AuthorizationProvider: "test-permissions-v1",
	})

	envelope, report := topicInventory(t, service)
	if len(report.Topics) != 1 || report.Topics[0].Topic != "orders-v1" {
		t.Fatalf("inventory leaked an invisible repository: %+v", report.Topics)
	}
	if got := planeState(report, "producer"); got.Repositories != 1 || got.PublishedRuns != 1 {
		t.Fatalf("plane publication counts include an invisible repository: %+v", got)
	}
	// The persisted canonical bytes are what a later page re-reads, so the
	// denied repository must not appear in them at all.
	record, ok := st.bundles[envelope.ID]
	if !ok {
		t.Fatalf("the inventory bundle was not persisted: %s", envelope.ID)
	}
	if strings.Contains(record.Content, denied) || strings.Contains(record.Content, "secret-topic") {
		t.Fatalf("persisted inventory discloses an invisible repository: %s", record.Content)
	}
	for _, row := range record.Repositories {
		if row != allowed {
			t.Fatalf("bundle scope names an invisible repository: %+v", record.Repositories)
		}
	}

	// Losing visibility withdraws the page even though the bundle still exists.
	visible = map[string]bool{}
	if _, err := service.BuildTopicInventory(
		context.Background(), envelope.ID, 0, "",
	); !statusIs(err, http.StatusNotFound) {
		t.Fatalf("reading an inventory after losing visibility = %v, want 404", err)
	}
}

// TestTopicInventoryNamesUnpublishedPlanes pins the distinction that keeps an
// unrun plane honest: a plane the experimental switch admits but nothing has
// published is unpublished, not served, and its contribution to a row is not a
// measured zero. Before this the row reported none, which reads as "we looked
// and found nothing" when nothing looked at all.
func TestTopicInventoryNamesUnpublishedPlanes(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	producerRun := proofRun(repo, "kafka-producer", "run-kafka-producer")
	produced, _ := topicAssertion(
		repo, producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 2, "segmentio/kafka-go",
	)
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: producerRun.Commit}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, "kafka-producer"): producerRun,
		},
		assertions: map[string][]store.Assertion{repo: {produced}},
	}
	_, report := topicInventory(t, topicService(st, nil))

	producer := planeState(report, "producer")
	consumer := planeState(report, "consumer")
	if producer.State != "served" || producer.PublishedRuns != 1 || producer.Repositories != 1 {
		t.Fatalf("producer plane = %+v", producer)
	}
	if consumer.State != "unpublished" || consumer.PublishedRuns != 0 || consumer.Repositories != 0 {
		t.Fatalf("consumer plane = %+v", consumer)
	}
	if consumer.Released {
		t.Fatalf("an experimental admission must not claim a release: %+v", consumer)
	}
	// The card describes what the released recipe measured independently, so an
	// admitted plane keeps it even before this deployment published anything.
	if consumer.Quality == nil || consumer.Quality.Card != "phebs.kafka.consumer" {
		t.Fatalf("an admitted plane lost its frozen card: %+v", consumer.Quality)
	}
	row, found := topicRow(report, "orders-v1")
	if !found {
		t.Fatalf("no orders-v1 row: %+v", report.Topics)
	}
	if row.Consumer.State != "unpublished" || row.Consumer.Sites != 0 || row.Consumer.Files != 0 {
		t.Fatalf("an unpublished plane must not report a measured zero: %+v", row.Consumer)
	}
	if row.Producer.State != "served" || row.Producer.Sites != 2 {
		t.Fatalf("producer row plane = %+v", row.Producer)
	}
}

// TestTopicInventoryWithNothingPublished pins that an inventory composed while
// neither Kafka domain has published is visibly empty rather than silently
// reading as a measured absence of topics.
func TestTopicInventoryWithNothingPublished(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	unrelated := proofRun(repo, "grpc-consumer", "run-unrelated")
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: unrelated.Commit}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, "grpc-consumer"): unrelated,
		},
		assertions: map[string][]store.Assertion{repo: {}},
	}
	_, report := topicInventory(t, topicService(st, nil))

	if len(report.Topics) != 0 || report.Pagination.Total != 0 {
		t.Fatalf("an inventory with nothing published returned rows: %+v", report.Topics)
	}
	for _, plane := range report.Planes {
		if plane.State != "unpublished" {
			t.Fatalf("plane %s = %q, want unpublished", plane.Plane, plane.State)
		}
		if plane.TopicCount != 0 || plane.SiteCount != 0 || plane.PublishedRuns != 0 {
			t.Fatalf("plane %s counts something it never read: %+v", plane.Plane, plane)
		}
	}
	// Complete describes clipping only; the plane states carry the fact that
	// nothing was published, and the caveat still forbids reading an empty
	// inventory as proven absence.
	if !report.Pagination.Complete || report.Caveat == "" || report.Census == nil {
		t.Fatalf("empty inventory lost its honesty signals: %+v", report.Pagination)
	}
}

// statusIs reads the HTTP status a composition refusal carries. The transport
// is T57.2's, but the status is part of the contract these tests pin.
func statusIs(err error, status int) bool {
	var coded huma.StatusError
	return errors.As(err, &coded) && coded.GetStatus() == status
}

func getInventory(
	t *testing.T,
	handler http.Handler,
	target string,
) (int, string, api.TopicInventoryReport) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	var report api.TopicInventoryReport
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
			t.Fatalf("decoding %s: %v", target, err)
		}
	}
	return recorder.Code, recorder.Body.String(), report
}

func versionBody(t *testing.T, handler http.Handler) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	return recorder.Body.String()
}

// topicHTTPFixture is one repository with both planes published and two
// spellings, shared by the T57.2 transport tests.
func topicHTTPFixture(t *testing.T) *proofAPIStore {
	t.Helper()
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	first, firstEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-a", "PRODUCES_TO_TOPIC", "orders-v1", 2, "segmentio/kafka-go",
	)
	second, secondEvidence := topicAssertion(
		repo, fixture.producerRun.ID, "produce-b", "PRODUCES_TO_TOPIC", "payments", 1, "segmentio/kafka-go",
	)
	consumed, consumedEvidence := topicAssertion(
		repo, fixture.consumerRun.ID, "consume-a", "CONSUMES_FROM_TOPIC", "orders-v1", 1, "segmentio/kafka-go",
	)
	fixture.add(first, firstEvidence)
	fixture.add(second, secondEvidence)
	fixture.add(consumed, consumedEvidence)
	return fixture.store()
}

// TestTopicInventoryHTTPExposure pins T57.2's first slice: the experimental
// proof surface serves the composed inventory and its paged presentation, the
// report survives a JSON round trip with its per-plane state intact, and the
// new capability is advertised to an authenticated caller only.
func TestTopicInventoryHTTPExposure(t *testing.T) {
	st := topicHTTPFixture(t)
	handler := proofHandler(st, "user:member", nil)

	code, body, envelope := getProof(t, handler, "/api/list_kafka_topics")
	if code != http.StatusOK {
		t.Fatalf("list_kafka_topics = %d %s", code, body)
	}
	if envelope.Bundle.Query.Kind != topicInventoryKind || envelope.Bundle.TopicInventory == nil {
		t.Fatalf("inventory bundle = %+v", envelope.Bundle.Query)
	}
	// The bundle read route still serves it, so a client can re-fetch the exact
	// content address it was given.
	if code, _, reread := getProof(t, handler, "/api/proof_bundles/"+envelope.ID); code != http.StatusOK ||
		reread.ID != envelope.ID || reread.Bundle.TopicInventory == nil {
		t.Fatalf("bundle re-read = %d", code)
	}

	code, body, report := getInventory(t, handler, "/api/topic_inventory/"+envelope.ID)
	if code != http.StatusOK {
		t.Fatalf("topic_inventory = %d %s", code, body)
	}
	if report.BundleID != envelope.ID || report.SchemaVersion == "" || report.Caveat == "" {
		t.Fatalf("report is not bound to its bundle: %+v", report)
	}
	if len(report.Topics) != 2 || report.Topics[0].Topic != "orders-v1" || report.Topics[1].Topic != "payments" {
		t.Fatalf("composed rows did not survive the wire: %+v", report.Topics)
	}
	if len(report.Planes) != 2 {
		t.Fatalf("plane rows did not survive the wire: %+v", report.Planes)
	}
	for _, plane := range report.Planes {
		if plane.Quality == nil || plane.Quality.Claim != "site-coverage" ||
			len(plane.Quality.Unmeasured) == 0 {
			t.Fatalf("plane %s lost its quality state on the wire: %+v", plane.Plane, plane.Quality)
		}
	}
	if report.Census == nil || report.Pagination.Total != 2 || !report.Pagination.Complete {
		t.Fatalf("report lost its census or pagination: %+v", report.Pagination)
	}

	if got := versionBody(t, handler); !strings.Contains(got, "kafka-topic-inventory") {
		t.Fatalf("an authenticated experimental caller is not told the inventory exists: %s", got)
	}
	// Dark and anonymous callers learn nothing about it.
	if got := versionBody(t, api.New(api.Options{Version: "test", Store: st})); strings.Contains(got, "kafka-topic-inventory") {
		t.Fatalf("a dark server advertised the inventory: %s", got)
	}
	if got := versionBody(t, proofHandler(st, "", nil)); strings.Contains(got, "kafka-topic-inventory") {
		t.Fatalf("an anonymous caller was advertised the inventory: %s", got)
	}
}

// TestTopicInventoryRoutesStayDarkUnderReleasedPlanes is the pin that protects
// the T55.3/T56.3 release evidence: a binary admitted only by released Kafka
// planes still serves exactly find_kafka_topic_usage and bundle reads, and
// never the composed inventory, which has no measured Topics claim of its own
// until T57.3.
func TestTopicInventoryRoutesStayDarkUnderReleasedPlanes(t *testing.T) {
	for _, planes := range [][]string{{"producer"}, {"consumer"}, {"consumer", "producer"}} {
		t.Run(strings.Join(planes, "+"), func(t *testing.T) {
			st := topicHTTPFixture(t)
			handler := api.New(api.Options{
				Version: "test", Store: st, Evidence: st, ProofBundles: st,
				KafkaReleasedPlanes:   planes,
				Visible:               func(context.Context) func(store.Repo) bool { return nil },
				Principal:             func(context.Context) string { return "user:member" },
				AuthorizationProvider: "test-permissions-v1",
			})
			// The released detail surface still works, so this is not a
			// vacuous pass over a broken handler.
			if code, body, _ := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1"); code != http.StatusOK {
				t.Fatalf("released topic usage = %d %s", code, body)
			}
			code, body, _ := getProof(t, handler, "/api/list_kafka_topics")
			if code != http.StatusNotFound {
				t.Fatalf("list_kafka_topics answered %d under a released admission: %s", code, body)
			}
			// Even a bundle ID composed elsewhere is not readable here: the
			// presentation route is not registered at all.
			if code, _, _ := getInventory(t, handler, "/api/topic_inventory/pb_"+strings.Repeat("0", 64)); code != http.StatusNotFound {
				t.Fatalf("topic_inventory answered %d under a released admission", code)
			}
			if got := versionBody(t, handler); strings.Contains(got, "kafka-topic-inventory") {
				t.Fatalf("a released plane advertised the inventory: %s", got)
			}
			if got := versionBody(t, handler); !strings.Contains(got, "kafka-topic-usage") {
				t.Fatalf("a released plane lost its own capability: %s", got)
			}
		})
	}
}

// TestTopicInventoryHTTPPagingAndValidation pins the transport's paging and its
// input bounds: a cursor round-trips through the query string, the page bound
// is enforced by the route schema, and an unknown content address is a plain
// miss rather than an error about inventory internals.
func TestTopicInventoryHTTPPagingAndValidation(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	fixture := newTopicFixture(repo)
	for index := range 5 {
		assertion, evidence := topicAssertion(
			repo, fixture.producerRun.ID, "produce-"+strconv.Itoa(index),
			"PRODUCES_TO_TOPIC", "topic-"+strconv.Itoa(index), 1, "segmentio/kafka-go",
		)
		fixture.add(assertion, evidence)
	}
	handler := proofHandler(fixture.store(), "user:member", nil)
	code, body, envelope := getProof(t, handler, "/api/list_kafka_topics")
	if code != http.StatusOK {
		t.Fatalf("list_kafka_topics = %d %s", code, body)
	}

	base := "/api/topic_inventory/" + envelope.ID
	var walked []string
	cursor := ""
	for page := 0; page < 10; page++ {
		target := base + "?page_size=2"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		code, body, report := getInventory(t, handler, target)
		if code != http.StatusOK {
			t.Fatalf("page %d = %d %s", page, code, body)
		}
		for _, row := range report.Topics {
			walked = append(walked, row.Topic)
		}
		if report.Pagination.NextCursor == "" {
			break
		}
		cursor = report.Pagination.NextCursor
	}
	want := []string{"topic-0", "topic-1", "topic-2", "topic-3", "topic-4"}
	if !slices.Equal(walked, want) {
		t.Fatalf("HTTP paged walk = %+v, want %+v", walked, want)
	}

	// The route schema rejects an out-of-bound page before the composition runs.
	if code, _, _ := getInventory(t, handler, base+"?page_size=101"); code != http.StatusUnprocessableEntity &&
		code != http.StatusBadRequest {
		t.Fatalf("page_size=101 = %d, want a 4xx refusal", code)
	}
	// A cursor is bound to its own inventory, over the wire as well.
	_, _, first := getInventory(t, handler, base+"?page_size=2")
	other := newTopicFixture(repo)
	assertion, evidence := topicAssertion(
		repo, other.producerRun.ID, "produce-solo", "PRODUCES_TO_TOPIC", "solo", 1, "segmentio/kafka-go",
	)
	other.add(assertion, evidence)
	otherHandler := proofHandler(other.store(), "user:member", nil)
	_, otherBody, otherEnvelope := getProof(t, otherHandler, "/api/list_kafka_topics")
	if otherEnvelope.ID == envelope.ID {
		t.Fatalf("the second inventory reused a content address: %s", otherBody)
	}
	if code, _, _ := getInventory(
		t, otherHandler,
		"/api/topic_inventory/"+otherEnvelope.ID+"?page_size=2&cursor="+first.Pagination.NextCursor,
	); code != http.StatusBadRequest {
		t.Fatalf("a foreign cursor over HTTP = %d, want 400", code)
	}
	if code, _, _ := getInventory(t, handler, "/api/topic_inventory/pb_"+strings.Repeat("0", 64)); code != http.StatusNotFound {
		t.Fatalf("an unknown content address = %d, want 404", code)
	}
}

// TestTopicInventoryHTTPVisibilityFence pins that authorization is re-checked
// on every page rather than only when the inventory was composed: a caller who
// loses access to a cited repository gets a plain miss, not the rows.
func TestTopicInventoryHTTPVisibilityFence(t *testing.T) {
	st := topicHTTPFixture(t)
	const repo = "github.com/allowed/kafka"
	visible := map[string]bool{repo: true}
	handler := proofHandler(st, "user:member", &visible)

	code, body, envelope := getProof(t, handler, "/api/list_kafka_topics")
	if code != http.StatusOK {
		t.Fatalf("list_kafka_topics = %d %s", code, body)
	}
	if code, _, report := getInventory(t, handler, "/api/topic_inventory/"+envelope.ID); code != http.StatusOK ||
		len(report.Topics) != 2 {
		t.Fatalf("visible read = %d %+v", code, report.Topics)
	}

	delete(visible, repo)
	if code, body, _ := getProof(t, handler, "/api/list_kafka_topics"); code != http.StatusOK {
		t.Fatalf("composing with nothing visible = %d %s", code, body)
	}
	if code, _, _ := getInventory(t, handler, "/api/topic_inventory/"+envelope.ID); code != http.StatusNotFound {
		t.Fatalf("reading a cited-but-no-longer-visible inventory = %d, want 404", code)
	}
}
