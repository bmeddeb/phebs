package t323

import (
	"fmt"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

var smallRevisions = []struct {
	name    string
	date    string
	message string
}{
	{"r0-baseline", "2026-08-04T23:30:00Z", "fixture: establish neutral service authority"},
	{"r1-rename", "2026-08-04T23:31:00Z", "fixture: rename billing placement without changing identity"},
	{"r2-split", "2026-08-04T23:32:00Z", "fixture: split orders service"},
	{"r3-merge-partial", "2026-08-04T23:33:00Z", "fixture: merge fulfillment with partial publication"},
	{"r4-removal", "2026-08-04T23:34:00Z", "fixture: remove payments placement and retain tombstone"},
}

func SmallSnapshots() ([]Snapshot, error) {
	snapshots := make([]Snapshot, 0, len(smallRevisions))
	for index, revision := range smallRevisions {
		catalog := catalogFor(index, revision.name)
		files, err := filesFor(index, catalog)
		if err != nil {
			return nil, err
		}
		snapshot := Snapshot{
			Revision: revision.name,
			Date:     revision.date,
			Message:  revision.message,
			Files:    files,
			Catalog:  catalog,
			Oracle:   oracleFor(index, revision.name, catalog),
		}
		if err := ValidateSnapshot(snapshot); err != nil {
			return nil, fmt.Errorf("%s: %w", revision.name, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func catalogFor(stage int, revision string) Catalog {
	records := []AuthorityRecord{
		accepted("operator:risk", "operator", "svc.risk", "Risk Console", "restricted",
			placement("services/risk", "primary"), placement("shared/trace/trace.go", "shared")),
		conflicting("operator:conflict", "operator", "svc.conflicted", "Conflicted Service",
			"services/conflict-a"),
		conflicting("build:conflict", "build", "svc.conflicted", "Conflicted Service",
			"services/conflict-b"),
		proposal("build:proposal", "svc.proposed", "Proposed Service", "services/proposed"),
	}

	switch stage {
	case 0:
		records = append(records,
			acceptedCommerce("operator:orders", "svc.orders", "Orders", "services/orders"),
			acceptedCommerce("operator:billing", "svc.billing", "Billing", "services/billing"),
			accepted("operator:shipping", "operator", "svc.shipping", "Shipping", "reader",
				placement("services/shipping", "primary"), placement("shared/trace/trace.go", "shared")),
		)
	case 1:
		records = append(records,
			acceptedCommerce("operator:orders", "svc.orders", "Orders", "services/orders"),
			acceptedCommerce("operator:billing", "svc.billing", "Payments", "services/payments"),
			accepted("operator:shipping", "operator", "svc.shipping", "Shipping", "reader",
				placement("services/shipping", "primary"), placement("shared/trace/trace.go", "shared")),
		)
	case 2:
		records = append(records,
			tombstone("operator:orders-tombstone", "svc.orders", "Orders", "svc.orders-api", "svc.orders-worker"),
			acceptedCommerce("operator:orders-api", "svc.orders-api", "Orders API", "services/orders-api"),
			accepted("operator:orders-worker", "operator", "svc.orders-worker", "Orders Worker", "reader",
				placement("services/orders-worker", "primary"), placement("shared/trace/trace.go", "shared")),
			acceptedCommerce("operator:billing", "svc.billing", "Payments", "services/payments"),
			accepted("operator:shipping", "operator", "svc.shipping", "Shipping", "reader",
				placement("services/shipping", "primary"), placement("shared/trace/trace.go", "shared")),
		)
	case 3:
		records = append(records,
			tombstone("operator:orders-tombstone", "svc.orders", "Orders", "svc.orders-api", "svc.orders-worker"),
			tombstone("operator:orders-worker-tombstone", "svc.orders-worker", "Orders Worker", "svc.fulfillment"),
			tombstone("operator:shipping-tombstone", "svc.shipping", "Shipping", "svc.fulfillment"),
			acceptedCommerce("operator:orders-api", "svc.orders-api", "Orders API", "services/orders-api"),
			acceptedCommerce("operator:billing", "svc.billing", "Payments", "services/payments"),
			accepted("operator:fulfillment", "operator", "svc.fulfillment", "Fulfillment", "reader",
				placement("services/fulfillment", "primary"), placement("shared/trace/trace.go", "shared")),
		)
	case 4:
		records = append(records,
			tombstone("operator:orders-tombstone", "svc.orders", "Orders", "svc.orders-api", "svc.orders-worker"),
			tombstone("operator:orders-worker-tombstone", "svc.orders-worker", "Orders Worker", "svc.fulfillment"),
			tombstone("operator:shipping-tombstone", "svc.shipping", "Shipping", "svc.fulfillment"),
			tombstone("operator:billing-tombstone", "svc.billing", "Payments"),
			acceptedCommerce("operator:orders-api", "svc.orders-api", "Orders API", "services/orders-api"),
			accepted("operator:fulfillment", "operator", "svc.fulfillment", "Fulfillment", "reader",
				placement("services/fulfillment", "primary"), placement("shared/trace/trace.go", "shared")),
		)
	default:
		panic("unknown small-corpus stage")
	}

	slices.SortFunc(records, func(left, right AuthorityRecord) int {
		return strings.Compare(left.AuthorityID, right.AuthorityID)
	})
	return Catalog{
		Schema: CatalogSchema, Revision: revision, Records: records,
		UnownedPaths: []string{
			"services/conflict-a/main.go", "services/conflict-b/main.go",
			"services/proposed/main.go", "tools/unowned.go",
		},
		Invalid: []InvalidAuthority{{
			CaseID: "unsafe-parent-path", ExpectedReason: "unsafe_path",
			Payload: []byte(`{"authority_id":"operator:malformed","service_key":"svc.malformed","placements":[{"path":"../escape","role":"primary"}]}`),
		}},
	}
}

func acceptedCommerce(authorityID, serviceKey, displayName, primaryPath string) AuthorityRecord {
	return accepted(authorityID, "operator", serviceKey, displayName, "reader",
		placement(primaryPath, "primary"),
		placement("api/commerce.proto", "supporting"),
		placement("gen/commercev1/commerce_grpc.pb.go", "generated"),
		placement("shared/trace/trace.go", "shared"),
		placement("typed/commerce/index.scip", "typed"),
	)
}

func accepted(authorityID, kind, serviceKey, displayName, visibility string, placements ...Placement) AuthorityRecord {
	return AuthorityRecord{
		AuthorityID: authorityID, Kind: kind, ServiceKey: serviceKey,
		DisplayName: displayName, Disposition: "accepted", Visibility: visibility,
		Placements: placements,
	}
}

func conflicting(authorityID, kind, serviceKey, displayName, primaryPath string) AuthorityRecord {
	return AuthorityRecord{
		AuthorityID: authorityID, Kind: kind, ServiceKey: serviceKey,
		DisplayName: displayName, Disposition: "conflicting", Visibility: "none",
		Placements: []Placement{placement(primaryPath, "primary")},
	}
}

func proposal(authorityID, serviceKey, displayName, primaryPath string) AuthorityRecord {
	return AuthorityRecord{
		AuthorityID: authorityID, Kind: "build", ServiceKey: serviceKey,
		DisplayName: displayName, Disposition: "proposal", Visibility: "none",
		Placements: []Placement{placement(primaryPath, "primary")},
	}
}

func tombstone(authorityID, serviceKey, displayName string, successors ...string) AuthorityRecord {
	return AuthorityRecord{
		AuthorityID: authorityID, Kind: "operator", ServiceKey: serviceKey,
		DisplayName: displayName, Disposition: "tombstone", Visibility: "none",
		Placements: []Placement{}, Successors: successors,
	}
}

func placement(path, role string) Placement {
	return Placement{Path: path, Role: role}
}

func filesFor(stage int, catalog Catalog) (map[string][]byte, error) {
	files := map[string][]byte{
		"go.mod": []byte("module " + RepositoryName + "\n\ngo 1.26\n"),
		"api/commerce.proto": []byte(`syntax = "proto3";

package neutral.commerce.v1;
option go_package = "example.invalid/t323-neutral-services/gen/commercev1;commercev1";

// T323_CENTRAL_CONTRACT
service Orders {
  rpc Create(CreateRequest) returns (CreateResponse);
}

message CreateRequest { string customer_id = 1; }
message CreateResponse { string order_id = 1; }
`),
		"gen/commercev1/commerce_grpc.pb.go": []byte(`// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// T323_GENERATED_CLIENT
package commercev1

type OrdersClient interface { Create(any, any) (any, error) }
type OrdersServer interface{}
func NewOrdersClient(any) OrdersClient { return client{} }
type client struct{}
func (client) Create(_ any, request any) (any, error) { return request, nil }
func RegisterOrdersServer(any, OrdersServer) {}
`),
		"shared/trace/trace.go":       []byte("package trace\n\nconst SharedNeedle = \"T323_SHARED_TRACE\"\n"),
		"tools/unowned.go":            []byte("package tools\n\nconst UnownedNeedle = \"T323_UNOWNED_TOOL\"\n"),
		"services/risk/private.go":    []byte("package risk\n\nconst PrivateNeedle = \"T323_RISK_PRIVATE\"\n"),
		"services/conflict-a/main.go": []byte("package conflicta\n\nconst Needle = \"T323_CONFLICT_A\"\n"),
		"services/conflict-b/main.go": []byte("package conflictb\n\nconst Needle = \"T323_CONFLICT_B\"\n"),
		"services/proposed/main.go":   []byte("package proposed\n\nconst Needle = \"T323_PROPOSAL_ONLY\"\n"),
	}

	switch stage {
	case 0:
		files["services/orders/service.go"] = ordersProvider("T323_ORDERS_PROVIDER")
		files["services/orders/kafka.go"] = topicProducer("T323_ORDERS_TOPIC_PRODUCER")
		files["services/billing/client.go"] = rpcCaller("billing", "T323_BILLING_CALLER")
		files["services/shipping/consumer.go"] = topicConsumer("shipping", "T323_SHIPPING_TOPIC_CONSUMER")
	case 1:
		files["services/orders/service.go"] = ordersProvider("T323_ORDERS_PROVIDER")
		files["services/orders/kafka.go"] = topicProducer("T323_ORDERS_TOPIC_PRODUCER")
		files["services/payments/client.go"] = rpcCaller("payments", "T323_PAYMENTS_CALLER")
		files["services/shipping/consumer.go"] = topicConsumer("shipping", "T323_SHIPPING_TOPIC_CONSUMER")
	case 2:
		files["services/orders-api/service.go"] = ordersProvider("T323_ORDERS_API_PROVIDER")
		files["services/orders-worker/kafka.go"] = topicProducer("T323_ORDERS_WORKER_PRODUCER")
		files["services/payments/client.go"] = rpcCaller("payments", "T323_PAYMENTS_CALLER")
		files["services/shipping/consumer.go"] = topicConsumer("shipping", "T323_SHIPPING_TOPIC_CONSUMER")
	case 3:
		files["services/orders-api/service.go"] = ordersProvider("T323_ORDERS_API_PROVIDER")
		files["services/orders-api/kafka.go"] = topicProducer("T323_ORDERS_API_PRODUCER")
		files["services/payments/client.go"] = rpcCaller("payments", "T323_PAYMENTS_CALLER")
		files["services/fulfillment/consumer.go"] = topicConsumer("fulfillment", "T323_FULFILLMENT_CONSUMER")
	case 4:
		files["services/orders-api/service.go"] = ordersProvider("T323_ORDERS_API_PROVIDER")
		files["services/orders-api/kafka.go"] = topicProducer("T323_ORDERS_API_PRODUCER")
		files["services/fulfillment/consumer.go"] = topicConsumer("fulfillment", "T323_FULFILLMENT_CONSUMER")
	}

	typedDocuments := []string{"gen/commercev1/commerce_grpc.pb.go"}
	for filePath := range files {
		if strings.HasSuffix(filePath, "/client.go") {
			typedDocuments = append(typedDocuments, filePath)
		}
	}
	slices.Sort(typedDocuments)
	index := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo:             &scip.ToolInfo{Name: "phebs-t323-neutral-author", Version: "1"},
			ProjectRoot:          "file:///fixture/t323-neutral-services",
			TextDocumentEncoding: scip.TextEncoding_UTF8,
		},
	}
	for _, documentPath := range typedDocuments {
		index.Documents = append(index.Documents, &scip.Document{
			RelativePath: documentPath, PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		})
	}
	encodedIndex, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		return nil, err
	}
	files["typed/commerce/index.scip"] = encodedIndex
	catalogBytes, err := MarshalCanonical(catalog)
	if err != nil {
		return nil, err
	}
	files[".phebs/service-authority.json"] = catalogBytes
	return files, nil
}

func ordersProvider(needle string) []byte {
	return []byte(fmt.Sprintf(`package orders

import commercev1 "example.invalid/t323-neutral-services/gen/commercev1"

const ProviderNeedle = %q
type Server struct{}
func Register(registrar any) { commercev1.RegisterOrdersServer(registrar, Server{}) }
`, needle))
}

func rpcCaller(packageName, needle string) []byte {
	return []byte(fmt.Sprintf(`package %s

import commercev1 "example.invalid/t323-neutral-services/gen/commercev1"

const CallerNeedle = %q
func Create(ctx any, client commercev1.OrdersClient, request any) (any, error) {
	return client.Create(ctx, request)
}
`, packageName, needle))
}

func topicProducer(needle string) []byte {
	return []byte(fmt.Sprintf(`package orders

import "github.com/IBM/sarama"

const TopicProducerNeedle = %q
func Publish(producer sarama.SyncProducer) {
	_, _, _ = producer.SendMessage(&sarama.ProducerMessage{Topic: "orders.created.v1"})
}
`, needle))
}

func topicConsumer(packageName, needle string) []byte {
	return []byte(fmt.Sprintf(`package %s

import (
	"context"
	"github.com/IBM/sarama"
)

const TopicConsumerNeedle = %q
func Consume(ctx context.Context, group sarama.ConsumerGroup, handler sarama.ConsumerGroupHandler) error {
	return group.Consume(ctx, []string{"orders.created.v1"}, handler)
}
`, packageName, needle))
}

func oracleFor(stage int, revision string, catalog Catalog) Oracle {
	oracle := Oracle{
		Schema: OracleSchema, Revision: revision,
		Memberships:  membershipsFrom(catalog),
		UnownedPaths: slices.Clone(catalog.UnownedPaths),
		Ambiguities: []Ambiguity{{
			ServiceKey: "svc.conflicted", AuthorityIDs: []string{"build:conflict", "operator:conflict"},
			Paths: []string{"services/conflict-a", "services/conflict-b"}, State: "conflict",
		}},
	}
	oracle.Queries = append(oracle.Queries,
		query("all-shared", "admin", "all-code", "", "T323_SHARED_TRACE", "exact", "shared/trace/trace.go"),
		query("all-contract", "admin", "all-code", "", "T323_CENTRAL_CONTRACT", "exact", "api/commerce.proto"),
		query("all-unowned", "admin", "all-code", "", "T323_UNOWNED_TOOL", "exact", "tools/unowned.go"),
		query("risk-reader", "reader", "service", "svc.risk", "T323_RISK_PRIVATE", "not_found"),
	)
	if stage < 4 {
		oracle.Queries = append(oracle.Queries,
			query("risk-admin", "admin", "service", "svc.risk", "T323_RISK_PRIVATE", "exact", "services/risk/private.go"),
		)
	}

	switch stage {
	case 0:
		oracle.Queries = append(oracle.Queries,
			query("orders-provider", "reader", "service", "svc.orders", "T323_ORDERS_PROVIDER", "exact", "services/orders/service.go"),
			query("billing-caller", "reader", "service", "svc.billing", "T323_BILLING_CALLER", "exact", "services/billing/client.go"),
			query("orders-shared", "reader", "service", "svc.orders", "T323_SHARED_TRACE", "exact", "shared/trace/trace.go"),
			query("billing-shared", "reader", "service", "svc.billing", "T323_SHARED_TRACE", "exact", "shared/trace/trace.go"),
		)
		oracle.Relationships = relationships("svc.orders", "services/orders/service.go", "svc.billing", "services/billing/client.go", "svc.orders", "services/orders/kafka.go", "svc.shipping", "services/shipping/consumer.go")
		oracle.States = currentStates(revision, "svc.orders", "svc.billing", "svc.shipping", "svc.risk")
	case 1:
		oracle.Queries = append(oracle.Queries,
			query("orders-provider", "reader", "service", "svc.orders", "T323_ORDERS_PROVIDER", "exact", "services/orders/service.go"),
			query("payments-caller", "reader", "service", "svc.billing", "T323_PAYMENTS_CALLER", "exact", "services/payments/client.go"),
		)
		oracle.Relationships = relationships("svc.orders", "services/orders/service.go", "svc.billing", "services/payments/client.go", "svc.orders", "services/orders/kafka.go", "svc.shipping", "services/shipping/consumer.go")
		oracle.States = currentStates(revision, "svc.orders", "svc.billing", "svc.shipping", "svc.risk")
	case 2:
		oracle.Queries = append(oracle.Queries,
			query("orders-api-provider", "reader", "service", "svc.orders-api", "T323_ORDERS_API_PROVIDER", "exact", "services/orders-api/service.go"),
			query("orders-worker-producer", "reader", "service", "svc.orders-worker", "T323_ORDERS_WORKER_PRODUCER", "exact", "services/orders-worker/kafka.go"),
			query("payments-caller", "reader", "service", "svc.billing", "T323_PAYMENTS_CALLER", "exact", "services/payments/client.go"),
		)
		oracle.Relationships = relationships("svc.orders-api", "services/orders-api/service.go", "svc.billing", "services/payments/client.go", "svc.orders-worker", "services/orders-worker/kafka.go", "svc.shipping", "services/shipping/consumer.go")
		oracle.States = append(currentStates(revision, "svc.orders-api", "svc.orders-worker", "svc.billing", "svc.shipping", "svc.risk"), removedState(revision, "svc.orders"))
		oracle.Tombstones = []Tombstone{{ServiceKey: "svc.orders", Reason: "split", Successors: []string{"svc.orders-api", "svc.orders-worker"}}}
	case 3:
		oracle.Queries = append(oracle.Queries,
			query("orders-api-provider", "reader", "service", "svc.orders-api", "T323_ORDERS_API_PROVIDER", "exact", "services/orders-api/service.go"),
			query("payments-stale", "reader", "service", "svc.billing", "T323_PAYMENTS_CALLER", "stale", "services/payments/client.go"),
			query("fulfillment-consumer", "reader", "service", "svc.fulfillment", "T323_FULFILLMENT_CONSUMER", "exact", "services/fulfillment/consumer.go"),
		)
		oracle.Relationships = relationships("svc.orders-api", "services/orders-api/service.go", "svc.billing", "services/payments/client.go", "svc.orders-api", "services/orders-api/kafka.go", "svc.fulfillment", "services/fulfillment/consumer.go")
		oracle.States = []ServiceState{
			currentState(revision, "svc.orders-api"),
			{ServiceKey: "svc.billing", DesiredRevision: revision, ActiveRevision: "r2-split", Catalog: "current", Search: "stale", Relationships: "stale"},
			{ServiceKey: "svc.fulfillment", DesiredRevision: revision, ActiveRevision: revision, Catalog: "current", Search: "current", Relationships: "partial"},
			currentState(revision, "svc.risk"),
			removedState(revision, "svc.orders"), removedState(revision, "svc.orders-worker"), removedState(revision, "svc.shipping"),
		}
		oracle.Tombstones = []Tombstone{
			{ServiceKey: "svc.orders", Reason: "split", Successors: []string{"svc.orders-api", "svc.orders-worker"}},
			{ServiceKey: "svc.orders-worker", Reason: "merge", Successors: []string{"svc.fulfillment"}},
			{ServiceKey: "svc.shipping", Reason: "merge", Successors: []string{"svc.fulfillment"}},
		}
	case 4:
		oracle.Queries = append(oracle.Queries,
			query("orders-api-provider", "reader", "service", "svc.orders-api", "T323_ORDERS_API_PROVIDER", "exact", "services/orders-api/service.go"),
			query("fulfillment-consumer", "reader", "service", "svc.fulfillment", "T323_FULFILLMENT_CONSUMER", "exact", "services/fulfillment/consumer.go"),
			query("removed-payments", "reader", "service", "svc.billing", "T323_PAYMENTS_CALLER", "not_found"),
			query("risk-unavailable", "admin", "service", "svc.risk", "T323_RISK_PRIVATE", "unavailable"),
		)
		oracle.Relationships = []RelationshipExpectation{
			{Kind: "rpc", Identity: "/neutral.commerce.v1.Orders/Create", Provider: "svc.orders-api", Consumer: "svc.billing", EvidencePaths: []string{"services/orders-api/service.go"}, State: "unresolved", Reason: "consumer_removed"},
			{Kind: "topic", Identity: "orders.created.v1", Provider: "svc.orders-api", Consumer: "svc.fulfillment", EvidencePaths: []string{"services/orders-api/kafka.go", "services/fulfillment/consumer.go"}, State: "resolved"},
		}
		oracle.States = []ServiceState{
			currentState(revision, "svc.orders-api"), currentState(revision, "svc.fulfillment"),
			{ServiceKey: "svc.risk", DesiredRevision: revision, Catalog: "current", Search: "unavailable", Relationships: "unavailable"},
			removedState(revision, "svc.orders"), removedState(revision, "svc.orders-worker"), removedState(revision, "svc.shipping"), removedState(revision, "svc.billing"),
		}
		oracle.Tombstones = []Tombstone{
			{ServiceKey: "svc.orders", Reason: "split", Successors: []string{"svc.orders-api", "svc.orders-worker"}},
			{ServiceKey: "svc.orders-worker", Reason: "merge", Successors: []string{"svc.fulfillment"}},
			{ServiceKey: "svc.shipping", Reason: "merge", Successors: []string{"svc.fulfillment"}},
			{ServiceKey: "svc.billing", Reason: "removed", Successors: []string{}},
		}
	}
	oracle.States = append(oracle.States,
		ServiceState{ServiceKey: "svc.conflicted", DesiredRevision: revision, Catalog: "conflict", Search: "unavailable", Relationships: "unavailable"},
		ServiceState{ServiceKey: "svc.proposed", DesiredRevision: revision, Catalog: "unavailable", Search: "unavailable", Relationships: "unavailable"},
	)
	slices.SortFunc(oracle.Queries, func(left, right QueryExpectation) int { return strings.Compare(left.ID, right.ID) })
	slices.SortFunc(oracle.States, func(left, right ServiceState) int { return strings.Compare(left.ServiceKey, right.ServiceKey) })
	return oracle
}

func membershipsFrom(catalog Catalog) []Membership {
	var memberships []Membership
	for _, record := range catalog.Records {
		if record.Disposition != "accepted" {
			continue
		}
		for _, placement := range record.Placements {
			memberships = append(memberships, Membership{
				ServiceKey: record.ServiceKey, Path: placement.Path, Role: placement.Role,
			})
		}
	}
	slices.SortFunc(memberships, func(left, right Membership) int {
		return strings.Compare(membershipKey(left.ServiceKey, left.Path, left.Role), membershipKey(right.ServiceKey, right.Path, right.Role))
	})
	return memberships
}

func query(id, principal, scope, serviceKey, expression, state string, paths ...string) QueryExpectation {
	return QueryExpectation{
		ID: id, Principal: principal, Scope: scope, ServiceKey: serviceKey,
		Expression: expression, State: state, Paths: paths,
	}
}

func relationships(rpcProvider, rpcProviderPath, rpcConsumer, rpcConsumerPath, topicProvider, topicProviderPath, topicConsumer, topicConsumerPath string) []RelationshipExpectation {
	return []RelationshipExpectation{
		{Kind: "rpc", Identity: "/neutral.commerce.v1.Orders/Create", Provider: rpcProvider, Consumer: rpcConsumer, EvidencePaths: []string{rpcProviderPath, rpcConsumerPath}, State: "resolved"},
		{Kind: "topic", Identity: "orders.created.v1", Provider: topicProvider, Consumer: topicConsumer, EvidencePaths: []string{topicProviderPath, topicConsumerPath}, State: "resolved"},
	}
}

func currentStates(revision string, serviceKeys ...string) []ServiceState {
	states := make([]ServiceState, 0, len(serviceKeys))
	for _, serviceKey := range serviceKeys {
		states = append(states, currentState(revision, serviceKey))
	}
	return states
}

func currentState(revision, serviceKey string) ServiceState {
	return ServiceState{
		ServiceKey: serviceKey, DesiredRevision: revision, ActiveRevision: revision,
		Catalog: "current", Search: "current", Relationships: "current",
	}
}

func removedState(revision, serviceKey string) ServiceState {
	return ServiceState{
		ServiceKey: serviceKey, DesiredRevision: revision,
		Catalog: "removed", Search: "removed", Relationships: "removed",
	}
}
